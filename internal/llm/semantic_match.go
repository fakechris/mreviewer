package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
)

// SemanticMatcher compares two code review findings using an LLM to determine
// if they describe the same underlying issue, even when fingerprint-based
// matching fails (e.g. different models using different terminology).
type SemanticMatcher interface {
	// IsSameFinding returns true if the two findings describe the same issue.
	// On error, returns false (conservative: treat as different findings).
	IsSameFinding(ctx context.Context, a SemanticFindingSummary, b SemanticFindingSummary) (bool, error)
}

// SemanticFindingSummary is a lightweight representation of a finding for
// semantic comparison. Only the fields relevant for judging equivalence.
type SemanticFindingSummary struct {
	Path         string `json:"path"`
	Category     string `json:"category"`
	Title        string `json:"title"`
	BodyMarkdown string `json:"body_markdown"`
	CanonicalKey string `json:"canonical_key,omitempty"`
	Symbol       string `json:"symbol,omitempty"`
	Severity     string `json:"severity"`
}

func summaryFromNormalized(f normalizedFinding) SemanticFindingSummary {
	return SemanticFindingSummary{
		Path:         f.Path,
		Category:     f.Category,
		Title:        f.Title,
		BodyMarkdown: f.BodyMarkdown,
		CanonicalKey: f.CanonicalKey,
		Symbol:       f.Symbol,
		Severity:     f.Severity,
	}
}

func summaryFromDBFinding(f db.ReviewFinding) SemanticFindingSummary {
	body := ""
	if f.BodyMarkdown.Valid {
		body = f.BodyMarkdown.String
	}
	return SemanticFindingSummary{
		Path:         f.Path,
		Category:     f.Category,
		Title:        f.Title,
		BodyMarkdown: body,
		CanonicalKey: f.CanonicalKey,
		Symbol:       "",
		Severity:     f.Severity,
	}
}

// semanticMatchResponse is the expected JSON response from the LLM.
type semanticMatchResponse struct {
	Same   bool   `json:"same"`
	Reason string `json:"reason"`
}

// LLMSemanticMatcher uses a lightweight LLM call to compare findings.
type LLMSemanticMatcher struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewLLMSemanticMatcher creates a matcher that calls an OpenAI-compatible endpoint.
func NewLLMSemanticMatcher(baseURL, apiKey, model string) *LLMSemanticMatcher {
	return &LLMSemanticMatcher{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{},
	}
}

const semanticMatchPrompt = `You are a code review deduplication judge. Two code review findings from different AI models are provided. Determine if they describe the SAME underlying issue in the code.

Rules:
- "Same issue" means they point to the same bug, vulnerability, or code smell in the same code location.
- Different wording or severity assessment does NOT make them different issues.
- Different files or fundamentally different problems make them different issues.
- When uncertain, answer false (conservative: treat as different).

Respond with JSON only: {"same": true/false, "reason": "one sentence explanation"}`

func (m *LLMSemanticMatcher) IsSameFinding(ctx context.Context, a, b SemanticFindingSummary) (bool, error) {
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	userContent := fmt.Sprintf("Finding A:\n%s\n\nFinding B:\n%s", aJSON, bJSON)

	payload := map[string]any{
		"model":       m.model,
		"temperature": 0.0,
		"max_tokens":  128,
		"messages": []map[string]any{
			{"role": "system", "content": semanticMatchPrompt},
			{"role": "user", "content": userContent},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("semantic_match: status %d: %s", resp.StatusCode, truncateText(string(body), 200))
	}

	var parsed openAIChatCompletionResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false, fmt.Errorf("semantic_match: parse response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return false, fmt.Errorf("semantic_match: no choices in response")
	}
	content := strings.TrimSpace(openAIMessageText(parsed.Choices[0].Message.Content))
	if content == "" {
		return false, fmt.Errorf("semantic_match: empty response content")
	}

	var result semanticMatchResponse
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return false, fmt.Errorf("semantic_match: parse result JSON: %w (raw: %s)", err, truncateText(content, 200))
	}
	return result.Same, nil
}

// NoopSemanticMatcher always returns false (no semantic matching).
// Used when LLM-based dedup is not configured.
type NoopSemanticMatcher struct{}

func (NoopSemanticMatcher) IsSameFinding(context.Context, SemanticFindingSummary, SemanticFindingSummary) (bool, error) {
	return false, nil
}
