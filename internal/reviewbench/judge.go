package reviewbench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Judge decides whether a finding describes a labelled comment, and rates
// the Chinese naturalness of a finding. A nil Judge makes the scorer fall
// back to the line-window rule alone.
type Judge interface {
	SameIssue(ctx context.Context, finding Finding, comment Comment) (bool, string, error)
	Naturalness(ctx context.Context, finding Finding) (NaturalnessScore, error)
}

type NaturalnessScore struct {
	Score              int    `json:"score"` // 1-5
	CodeTermsPreserved bool   `json:"code_terms_preserved"`
	Reason             string `json:"reason,omitempty"`
}

// OpenAIJudge calls an OpenAI-compatible chat endpoint with temperature 0.
type OpenAIJudge struct {
	baseURL    string
	apiKey     string
	model      string
	extraBody  map[string]any
	httpClient *http.Client
}

// WithExtraBody merges vendor fields into every judge request, e.g.
// {"thinking": {"type": "disabled"}} so a DeepSeek thinking model answers in
// content instead of spending the token budget on reasoning.
func (j *OpenAIJudge) WithExtraBody(extra map[string]any) *OpenAIJudge {
	j.extraBody = extra
	return j
}

func NewOpenAIJudge(baseURL, apiKey, model string) *OpenAIJudge {
	return &OpenAIJudge{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:     strings.TrimSpace(apiKey),
		model:      strings.TrimSpace(model),
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

func (j *OpenAIJudge) Model() string { return j.model }

const sameIssuePrompt = `You are the judge of a code review benchmark. You get one finding produced by an automated reviewer and one reference comment written or verified by an expert for the same pull request. Both point at the same file and nearby lines.

Decide whether the finding reports the SAME underlying issue as the reference comment.

Rules:
- Same issue means the same defect, risk or improvement at that location. Different wording, language, severity or level of detail does not matter.
- A finding that only mentions the line generically (style, formatting, "consider adding tests") is NOT the same as a reference comment about a concrete defect, and vice versa.
- When unsure, answer false.

Respond with JSON only: {"same": true|false, "reason": "one sentence"}`

const naturalnessPrompt = `你是中文代码评审文本的评审员。给你一条自动评审工具输出的 finding（标题与正文）。请评估：
1. 自然度：是否像中文工程师在评审里会写的话，而不是翻译腔。1 分生硬难懂，3 分能看懂但明显翻译腔，5 分地道流畅。
2. 代码术语是否保持原样：变量名、函数名、类型名、API 名、错误码等必须保留英文原文，不应被翻译成中文。

只输出 JSON：{"score": 1-5, "code_terms_preserved": true|false, "reason": "一句话"}`

type sameIssueResponse struct {
	Same   bool   `json:"same"`
	Reason string `json:"reason"`
}

func (j *OpenAIJudge) SameIssue(ctx context.Context, finding Finding, comment Comment) (bool, string, error) {
	user := fmt.Sprintf("Finding (path %s, lines %d-%d, severity %s):\nTitle: %s\n%s\n\nReference comment (path %s, lines %d-%d, category %s):\n%s",
		finding.Path, finding.StartLine, finding.EndLine, finding.Severity, finding.Title, truncate(finding.Body, 2000),
		comment.Path, comment.FromLine, comment.ToLine, comment.Category, truncate(comment.Note, 2000))
	content, err := j.chat(ctx, sameIssuePrompt, user, 400)
	if err != nil {
		return false, "", err
	}
	var parsed sameIssueResponse
	if err := json.Unmarshal([]byte(extractJSON(content)), &parsed); err != nil {
		return false, "", fmt.Errorf("reviewbench: judge returned non-JSON: %s", truncate(content, 200))
	}
	return parsed.Same, parsed.Reason, nil
}

func (j *OpenAIJudge) Naturalness(ctx context.Context, finding Finding) (NaturalnessScore, error) {
	user := "标题：" + finding.Title + "\n正文：\n" + truncate(finding.Body, 2000)
	content, err := j.chat(ctx, naturalnessPrompt, user, 400)
	if err != nil {
		return NaturalnessScore{}, err
	}
	var parsed NaturalnessScore
	if err := json.Unmarshal([]byte(extractJSON(content)), &parsed); err != nil {
		return NaturalnessScore{}, fmt.Errorf("reviewbench: naturalness judge returned non-JSON: %s", truncate(content, 200))
	}
	if parsed.Score < 1 || parsed.Score > 5 {
		return NaturalnessScore{}, fmt.Errorf("reviewbench: naturalness score %d out of range", parsed.Score)
	}
	return parsed, nil
}

func (j *OpenAIJudge) chat(ctx context.Context, system, user string, maxTokens int) (string, error) {
	payload := map[string]any{
		"model":       j.model,
		"temperature": 0.0,
		"max_tokens":  maxTokens,
		"messages": []map[string]any{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	for k, v := range j.extraBody {
		payload[k] = v
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if j.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+j.apiKey)
	}
	resp, err := j.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("reviewbench: judge status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var parsed struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("reviewbench: judge response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("reviewbench: judge returned no choices")
	}
	content := messageText(parsed.Choices[0].Message.Content)
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("reviewbench: judge returned empty content (finish_reason=%s); a thinking model may need thinking disabled or a larger budget", parsed.Choices[0].FinishReason)
	}
	return content, nil
}

// messageText accepts both the string and the content-parts shapes of an
// OpenAI-compatible message.
func messageText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// extractJSON strips a markdown fence or surrounding prose around a JSON object.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return s[i : j+1]
		}
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
