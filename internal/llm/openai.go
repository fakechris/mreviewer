package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/scheduler"
)

const (
	openAIOutputModeToolCall   = "tool_call"
	openAIOutputModeJSONSchema = "json_schema"
)

type OpenAIProvider struct {
	baseURL         string
	apiKey          string
	model           string
	maxTokens       int64
	maxCompletion   int64
	temperature     float64
	systemPrompt    string
	routeName       string
	outputMode      string
	reasoningEffort string
	rateLimiter     RateLimiter
	now             func() time.Time
	sleep           func(context.Context, time.Duration) error
	timeoutRetries  int
	httpClient      *http.Client
	compat          *OpenAICompatMode
}

type openAIChatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []struct {
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
}

func NewOpenAIProvider(cfg ProviderConfig) (*OpenAIProvider, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("llm: base URL is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("llm: model is required")
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultMaxTokens
	}
	if strings.TrimSpace(cfg.SystemPrompt) == "" {
		cfg.SystemPrompt = defaultSystemPrompt
	}
	if cfg.TimeoutRetries <= 0 {
		cfg.TimeoutRetries = defaultTimeoutRetry
	}
	if cfg.Temperature <= 0 {
		cfg.Temperature = defaultTemperature
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	outputMode := strings.ToLower(strings.TrimSpace(cfg.OutputMode))
	if outputMode == "" {
		outputMode = openAIOutputModeToolCall
	}
	switch outputMode {
	case openAIOutputModeToolCall, openAIOutputModeJSONSchema:
	default:
		return nil, fmt.Errorf("llm: unsupported openai output mode %q", cfg.OutputMode)
	}
	routeName := strings.TrimSpace(cfg.RouteName)
	if routeName == "" {
		routeName = strings.TrimSpace(cfg.Model)
	}
	return &OpenAIProvider{
		baseURL:         strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:          cfg.APIKey,
		model:           cfg.Model,
		maxTokens:       cfg.MaxTokens,
		maxCompletion:   cfg.MaxCompletionTokens,
		temperature:     cfg.Temperature,
		systemPrompt:    cfg.SystemPrompt,
		routeName:       routeName,
		outputMode:      outputMode,
		reasoningEffort: strings.TrimSpace(cfg.ReasoningEffort),
		rateLimiter:     cfg.RateLimiter,
		now:             cfg.Now,
		sleep:           cfg.Sleep,
		timeoutRetries:  cfg.TimeoutRetries,
		httpClient:      cfg.HTTPClient,
		compat:          cfg.CompatMode,
	}, nil
}

func (p *OpenAIProvider) RequestPayload(request ctxpkg.ReviewRequest) map[string]any {
	return p.RequestPayloadWithSystemPrompt(request, p.systemPrompt)
}

func (p *OpenAIProvider) RequestPayloadWithSystemPrompt(request ctxpkg.ReviewRequest, systemPrompt string) map[string]any {
	return p.requestPayloadWithUserContent(systemPrompt, mustJSON(request))
}

func (p *OpenAIProvider) requestPayloadWithUserContent(systemPrompt string, userContent string) map[string]any {
	systemRole := "developer"
	if p.compat != nil && p.compat.UseSystemRole {
		systemRole = "system"
	}
	payload := map[string]any{
		"model":       p.model,
		"temperature": p.temperature,
		"messages": []map[string]any{
			{"role": systemRole, "content": systemPrompt},
			{"role": "user", "content": userContent},
		},
	}
	if p.compat != nil && p.compat.UseMaxTokens {
		if p.maxCompletion > 0 {
			payload["max_tokens"] = p.maxCompletion
		} else {
			payload["max_tokens"] = p.maxTokens
		}
	} else if p.maxCompletion > 0 {
		payload["max_completion_tokens"] = p.maxCompletion
	} else {
		payload["max_tokens"] = p.maxTokens
	}
	if p.reasoningEffort != "" && (p.compat == nil || !p.compat.DropReasoningEffort) {
		payload["reasoning_effort"] = p.reasoningEffort
	}
	strict := p.compat == nil || !p.compat.DropStrictSchema
	switch p.outputMode {
	case openAIOutputModeJSONSchema:
		jsonSchema := map[string]any{
			"name":   reviewSubmitToolName,
			"schema": reviewResultSchemaOpenAIStrict(),
		}
		if strict {
			jsonSchema["strict"] = true
		}
		payload["response_format"] = map[string]any{
			"type":        "json_schema",
			"json_schema": jsonSchema,
		}
	default:
		if p.compat == nil || !p.compat.DropParallelToolCalls {
			payload["parallel_tool_calls"] = false
		}
		fnDef := map[string]any{
			"name":        reviewSubmitToolName,
			"description": "Emit the final merge request review result as structured JSON.",
			"parameters":  reviewResultSchema(),
		}
		if strict {
			fnDef["strict"] = true
		}
		payload["tools"] = []map[string]any{{
			"type":     "function",
			"function": fnDef,
		}}
		payload["tool_choice"] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": reviewSubmitToolName,
			},
		}
	}
	return payload
}

func (p *OpenAIProvider) Review(ctx context.Context, request ctxpkg.ReviewRequest) (ProviderResponse, error) {
	return p.ReviewWithSystemPrompt(ctx, request, p.systemPrompt)
}

func (p *OpenAIProvider) ReviewWithSystemPrompt(ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (ProviderResponse, error) {
	if p.rateLimiter != nil {
		if err := p.rateLimiter.Wait(ctx, strings.TrimSpace(p.routeName)); err != nil {
			return ProviderResponse{}, err
		}
	}
	var lastErr error
	maxAttempts := p.timeoutRetries
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	payload := p.RequestPayloadWithSystemPrompt(request, systemPrompt)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		started := p.now()
		raw, tokens, err := p.call(ctx, payload)
		if err != nil {
			lastErr = err
			var structuredMiss *structuredOutputMissError
			if errors.As(err, &structuredMiss) {
				return ProviderResponse{}, scheduler.NewTerminalError(parserErrorCode, &providerParseError{
					cause:       structuredMiss.cause,
					rawResponse: structuredMiss.rawResponse,
					latency:     p.now().Sub(started),
					tokens:      tokens,
					model:       p.routeName,
				})
			}
			if !isTimeoutError(err) || attempt == maxAttempts-1 {
				return ProviderResponse{}, err
			}
			if sleepErr := p.sleep(ctx, time.Duration(attempt+1)*50*time.Millisecond); sleepErr != nil {
				return ProviderResponse{}, sleepErr
			}
			continue
		}
		stage := "direct"
		if validationErr := validateReviewResultStrictJSON(raw); validationErr != nil {
			repairedRaw, repairTokens, repairErr := p.call(ctx, p.requestPayloadWithUserContent(systemPrompt, buildReviewRepairPayload(request, raw, validationErr)))
			tokens += repairTokens
			if repairErr != nil {
				return ProviderResponse{}, scheduler.NewTerminalError(parserErrorCode, &providerParseError{
					cause:       repairErr,
					rawResponse: raw,
					latency:     p.now().Sub(started),
					tokens:      tokens,
					model:       p.routeName,
				})
			}
			if repairValidationErr := validateReviewResultStrictJSON(repairedRaw); repairValidationErr != nil {
				return ProviderResponse{}, scheduler.NewTerminalError(parserErrorCode, &providerParseError{
					cause:       fmt.Errorf("llm: strict validation failed after repair: %w", repairValidationErr),
					rawResponse: repairedRaw,
					latency:     p.now().Sub(started),
					tokens:      tokens,
					model:       p.routeName,
				})
			}
			raw = repairedRaw
			stage = "repair_retry"
		}
		result, parseStage, parseErr := ParseReviewResult(raw)
		if parseErr != nil {
			return ProviderResponse{}, scheduler.NewTerminalError(parserErrorCode, &providerParseError{
				cause:       parseErr,
				rawResponse: raw,
				latency:     p.now().Sub(started),
				tokens:      tokens,
				model:       p.routeName,
			})
		}
		if stage == "direct" {
			stage = parseStage
		}
		return ProviderResponse{
			Result:          result,
			RawText:         raw,
			Latency:         p.now().Sub(started),
			Tokens:          tokens,
			FallbackStage:   stage,
			Model:           p.routeName,
			ResponsePayload: map[string]any{"text": raw, "fallback_stage": stage},
		}, nil
	}
	return ProviderResponse{}, lastErr
}

func (p *OpenAIProvider) call(ctx context.Context, payload map[string]any) (string, int64, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("openai: status %d: %s", resp.StatusCode, truncateText(string(body), 400))
	}
	var parsed openAIChatCompletionResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", 0, fmt.Errorf("openai: parse response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", parsed.Usage.CompletionTokens, &structuredOutputMissError{
			cause:       fmt.Errorf("llm: missing structured response"),
			rawResponse: truncateText(string(body), 400),
		}
	}
	message := parsed.Choices[0].Message
	if p.outputMode == openAIOutputModeJSONSchema {
		content := strings.TrimSpace(openAIMessageText(message.Content))
		if content == "" {
			return "", parsed.Usage.CompletionTokens, &structuredOutputMissError{
				cause:       fmt.Errorf("llm: missing structured JSON content"),
				rawResponse: truncateText(string(body), 400),
			}
		}
		return content, parsed.Usage.CompletionTokens, nil
	}
	for _, toolCall := range message.ToolCalls {
		if toolCall.Type != "function" {
			continue
		}
		if strings.TrimSpace(toolCall.Function.Name) != reviewSubmitToolName {
			continue
		}
		return strings.TrimSpace(toolCall.Function.Arguments), parsed.Usage.CompletionTokens, nil
	}
	return "", parsed.Usage.CompletionTokens, &structuredOutputMissError{
		cause:       fmt.Errorf("llm: missing tool_use block %q", reviewSubmitToolName),
		rawResponse: strings.TrimSpace(openAIMessageText(message.Content)),
	}
}

func openAIMessageText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var asString string
	if err := json.Unmarshal(trimmed, &asString); err == nil {
		return asString
	}
	var parts []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	}
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return string(trimmed)
	}
	var builder strings.Builder
	for _, part := range parts {
		switch {
		case strings.TrimSpace(part.Text) != "":
			builder.WriteString(part.Text)
		case strings.TrimSpace(part.Refusal) != "":
			builder.WriteString(part.Refusal)
		}
	}
	return builder.String()
}

// DeepSeekCompatMode returns an OpenAICompatMode suitable for DeepSeek models.
// DeepSeek uses "system" role (not "developer"), does not support strict schema
// validation, parallel_tool_calls, or reasoning_effort.
func DeepSeekCompatMode() *OpenAICompatMode {
	return &OpenAICompatMode{
		UseSystemRole:         true,
		DropParallelToolCalls: true,
		DropStrictSchema:      true,
		DropReasoningEffort:   true,
		UseMaxTokens:          true,
	}
}
