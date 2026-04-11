package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/llm"
)

const (
	structuredOutputProbeModeTool   = "tool"
	structuredOutputProbeModeNative = "native"
)

type structuredOutputProbeOptions struct {
	configPath string
	route      string
	mode       string
	runs       int
	prompt     string
}

type structuredOutputProbeOutput struct {
	Route          string                           `json:"route"`
	Provider       string                           `json:"provider"`
	WireAPI        string                           `json:"wire_api"`
	Endpoint       string                           `json:"endpoint"`
	Mode           string                           `json:"mode"`
	RequestedModel string                           `json:"requested_model"`
	Runs           int                              `json:"runs"`
	HTTPOKCount    int                              `json:"http_ok_count"`
	ParsedOKCount  int                              `json:"parsed_ok_count"`
	SchemaOKCount  int                              `json:"schema_ok_count"`
	ObservedModels []string                         `json:"observed_models"`
	Results        []structuredOutputProbeRunResult `json:"results"`
}

type structuredOutputProbeRunResult struct {
	Run              int            `json:"run"`
	HTTPStatus       int            `json:"http_status"`
	TransportOK      bool           `json:"transport_ok"`
	Model            string         `json:"model,omitempty"`
	FinishReason     string         `json:"finish_reason,omitempty"`
	ParsedOK         bool           `json:"parsed_ok"`
	SchemaOK         bool           `json:"schema_ok"`
	ParseError       string         `json:"parse_error,omitempty"`
	SchemaErrors     []string       `json:"schema_errors,omitempty"`
	StructuredOutput map[string]any `json:"structured_output,omitempty"`
	TextPreview      string         `json:"text_preview,omitempty"`
	ReasoningPreview string         `json:"reasoning_preview,omitempty"`
	RawError         string         `json:"raw_error,omitempty"`
	ResponseBody     map[string]any `json:"response_body,omitempty"`
}

type structuredOutputProbeHTTPResponse struct {
	status int
	body   []byte
}

type openAIProbeResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content          json.RawMessage `json:"content"`
			ReasoningContent string          `json:"reasoning_content"`
			ToolCalls        []struct {
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

type anthropicProbeResponse struct {
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type     string          `json:"type"`
		Name     string          `json:"name,omitempty"`
		Text     string          `json:"text,omitempty"`
		Thinking string          `json:"thinking,omitempty"`
		Input    json.RawMessage `json:"input,omitempty"`
	} `json:"content"`
}

func runStructuredOutputProbeCommand(args []string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	opts, err := parseStructuredOutputProbeOptions(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	cfg, err := config.Load(opts.configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load config failed: %v\n", err)
		return 1
	}
	providers, err := config.BuildProviderConfigs(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build providers failed: %v\n", err)
		return 1
	}
	routeCfg, ok := providers[strings.TrimSpace(opts.route)]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "route %q is not configured\n", strings.TrimSpace(opts.route))
		return 1
	}
	if strings.TrimSpace(routeCfg.APIKey) == "" {
		_, _ = fmt.Fprintf(stderr, "route %q is missing api_key after config/env expansion\n", strings.TrimSpace(opts.route))
		return 1
	}

	wireAPI, err := structuredOutputProbeWireAPI(routeCfg.Kind)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "route %q: %v\n", strings.TrimSpace(opts.route), err)
		return 1
	}
	if wireAPI == "anthropic" && opts.mode == structuredOutputProbeModeNative {
		_, _ = fmt.Fprintln(stderr, "native mode is only supported for OpenAI-compatible routes")
		return 2
	}

	endpoint := structuredOutputProbeEndpoint(routeCfg.BaseURL, wireAPI)
	httpClient := &http.Client{Timeout: 90 * time.Second}
	output := structuredOutputProbeOutput{
		Route:          strings.TrimSpace(opts.route),
		Provider:       strings.TrimSpace(routeCfg.Kind),
		WireAPI:        wireAPI,
		Endpoint:       endpoint,
		Mode:           opts.mode,
		RequestedModel: strings.TrimSpace(routeCfg.Model),
		Runs:           opts.runs,
	}

	for i := 1; i <= opts.runs; i++ {
		payload, err := structuredOutputProbePayload(routeCfg, wireAPI, opts.mode, opts.prompt)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "build payload failed: %v\n", err)
			return 1
		}
		resp, err := structuredOutputProbeCall(httpClient, endpoint, wireAPI, routeCfg.APIKey, payload)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "probe request failed: %v\n", err)
			return 1
		}
		runResult := structuredOutputProbeSummarizeRun(i, wireAPI, opts.mode, resp)
		output.Results = append(output.Results, runResult)
		if runResult.TransportOK {
			output.HTTPOKCount++
		}
		if runResult.ParsedOK {
			output.ParsedOKCount++
		}
		if runResult.SchemaOK {
			output.SchemaOKCount++
		}
		if runResult.Model != "" && !containsString(output.ObservedModels, runResult.Model) {
			output.ObservedModels = append(output.ObservedModels, runResult.Model)
		}
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(output); err != nil {
		_, _ = fmt.Fprintf(stderr, "encode output failed: %v\n", err)
		return 1
	}
	return 0
}

func parseStructuredOutputProbeOptions(args []string, stderr io.Writer) (structuredOutputProbeOptions, error) {
	cleanedArgs, _, err := extractCommonCLIFlags(args)
	if err != nil {
		return structuredOutputProbeOptions{}, err
	}
	fs := flag.NewFlagSet("mreviewer structured-output-probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagSetUsage(fs, `
Usage: mreviewer structured-output-probe --route <configured-route> [options]

Run a live structured-output probe against one configured provider route.
This command uses a fixed minimal schema:
  {"verdict":"pass|fail","score":<number>}

Modes:
  tool    Claude Code style synthetic StructuredOutput tool contract
  native  Provider-native response_format json_schema contract (OpenAI-compatible only)

Examples:
  mreviewer structured-output-probe --route zhipuai_default --mode tool --runs 10
  mreviewer structured-output-probe --route zhipuai_default --mode native --runs 5
  mreviewer structured-output-probe --route minimax_default --mode tool --runs 10
`)
	opts := structuredOutputProbeOptions{
		configPath: defaultPersonalConfigPath,
		mode:       structuredOutputProbeModeTool,
		runs:       1,
		prompt:     "Return verdict=pass and score=0.93.",
	}
	fs.StringVar(&opts.configPath, "config", defaultPersonalConfigPath, "Path to config file")
	fs.StringVar(&opts.route, "route", "", "Configured model route to probe")
	fs.StringVar(&opts.mode, "mode", structuredOutputProbeModeTool, "Probe mode: tool|native")
	fs.IntVar(&opts.runs, "runs", 1, "Number of probe requests to issue")
	fs.StringVar(&opts.prompt, "prompt", opts.prompt, "Prompt to send during the probe")
	if err := fs.Parse(cleanedArgs); err != nil {
		return structuredOutputProbeOptions{}, err
	}
	if extra := fs.Args(); len(extra) > 0 {
		return structuredOutputProbeOptions{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(extra, ", "))
	}
	if strings.TrimSpace(opts.route) == "" {
		return structuredOutputProbeOptions{}, fmt.Errorf("--route is required")
	}
	opts.mode = strings.ToLower(strings.TrimSpace(opts.mode))
	if opts.mode != structuredOutputProbeModeTool && opts.mode != structuredOutputProbeModeNative {
		return structuredOutputProbeOptions{}, fmt.Errorf("--mode must be tool or native")
	}
	if opts.runs <= 0 {
		return structuredOutputProbeOptions{}, fmt.Errorf("--runs must be greater than 0")
	}
	return opts, nil
}

func structuredOutputProbeWireAPI(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case llm.ProviderKindMiniMax, llm.ProviderKindAnthropicCompatible, llm.ProviderKindAnthropic, llm.ProviderKindArkAnthropic, llm.ProviderKindFireworksRouter:
		return "anthropic", nil
	case llm.ProviderKindOpenAI, llm.ProviderKindZhipuAI, llm.ProviderKindArkOpenAI:
		return "openai", nil
	default:
		return "", fmt.Errorf("unsupported provider kind %q", kind)
	}
}

func structuredOutputProbeEndpoint(baseURL, wireAPI string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch wireAPI {
	case "anthropic":
		if strings.HasSuffix(baseURL, "/v1/messages") {
			return baseURL
		}
		return baseURL + "/v1/messages"
	default:
		if strings.HasSuffix(baseURL, "/chat/completions") {
			return baseURL
		}
		return baseURL + "/chat/completions"
	}
}

func structuredOutputProbePayload(cfg llm.ProviderConfig, wireAPI, mode, prompt string) (map[string]any, error) {
	switch wireAPI {
	case "openai":
		if mode == structuredOutputProbeModeNative {
			return structuredOutputProbeOpenAINativePayload(cfg.Model, prompt), nil
		}
		return structuredOutputProbeOpenAIToolPayload(cfg.Model, prompt), nil
	case "anthropic":
		if mode != structuredOutputProbeModeTool {
			return nil, fmt.Errorf("native mode is not supported for anthropic routes")
		}
		return structuredOutputProbeAnthropicToolPayload(cfg.Model, prompt), nil
	default:
		return nil, fmt.Errorf("unsupported wire api %q", wireAPI)
	}
}

func structuredOutputProbeOpenAIToolPayload(model, prompt string) map[string]any {
	return map[string]any{
		"model":       model,
		"temperature": 0.1,
		"messages": []map[string]any{
			{"role": "system", "content": "You must call the StructuredOutput tool exactly once at the end. Do not answer in plain text."},
			{"role": "user", "content": prompt},
		},
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "StructuredOutput",
				"description": "Use this tool to return your final response in the requested structured format. You MUST call this tool exactly once at the end of your response.",
				"parameters":  structuredOutputProbeSchema(),
			},
		}},
		"tool_choice": "auto",
	}
}

func structuredOutputProbeOpenAINativePayload(model, prompt string) map[string]any {
	return map[string]any{
		"model":       model,
		"temperature": 0.1,
		"messages": []map[string]any{
			{"role": "system", "content": "Return only valid JSON matching the supplied schema."},
			{"role": "user", "content": prompt},
		},
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "structured_output",
				"strict": true,
				"schema": structuredOutputProbeSchema(),
			},
		},
	}
}

func structuredOutputProbeAnthropicToolPayload(model, prompt string) map[string]any {
	return map[string]any{
		"model":       model,
		"max_tokens":  256,
		"temperature": 0.1,
		"system":      "You must call the StructuredOutput tool exactly once at the end. Do not answer in plain text.",
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "text",
				"text": prompt,
			}},
		}},
		"tools": []map[string]any{{
			"name":         "StructuredOutput",
			"description":  "Use this tool to return your final response in the requested structured format. You MUST call this tool exactly once at the end of your response.",
			"input_schema": structuredOutputProbeSchema(),
		}},
		"tool_choice": map[string]any{"type": "auto"},
	}
}

func structuredOutputProbeSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"verdict": map[string]any{"type": "string", "enum": []string{"pass", "fail"}},
			"score":   map[string]any{"type": "number"},
		},
		"required":             []string{"verdict", "score"},
		"additionalProperties": false,
	}
}

func structuredOutputProbeCall(httpClient *http.Client, endpoint, wireAPI, apiKey string, payload map[string]any) (structuredOutputProbeHTTPResponse, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return structuredOutputProbeHTTPResponse{}, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return structuredOutputProbeHTTPResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	switch wireAPI {
	case "anthropic":
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return structuredOutputProbeHTTPResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return structuredOutputProbeHTTPResponse{}, err
	}
	return structuredOutputProbeHTTPResponse{status: resp.StatusCode, body: body}, nil
}

func structuredOutputProbeSummarizeRun(run int, wireAPI, mode string, resp structuredOutputProbeHTTPResponse) structuredOutputProbeRunResult {
	result := structuredOutputProbeRunResult{
		Run:         run,
		HTTPStatus:  resp.status,
		TransportOK: resp.status >= 200 && resp.status < 300,
	}
	if !result.TransportOK {
		result.RawError = truncatePreview(string(resp.body), 500)
		return result
	}

	switch wireAPI {
	case "anthropic":
		structuredOutputProbeSummarizeAnthropicRun(&result, resp.body)
	default:
		if mode == structuredOutputProbeModeNative {
			structuredOutputProbeSummarizeOpenAINativeRun(&result, resp.body)
		} else {
			structuredOutputProbeSummarizeOpenAIToolRun(&result, resp.body)
		}
	}
	return result
}

func structuredOutputProbeSummarizeOpenAIToolRun(result *structuredOutputProbeRunResult, body []byte) {
	var response openAIProbeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		result.ParseError = err.Error()
		result.RawError = truncatePreview(string(body), 500)
		return
	}
	if len(response.Choices) == 0 {
		result.ParseError = "missing choices"
		return
	}
	choice := response.Choices[0]
	result.Model = strings.TrimSpace(response.Model)
	result.FinishReason = strings.TrimSpace(choice.FinishReason)
	result.TextPreview = truncatePreview(openAIMessageText(choice.Message.Content), 240)
	result.ReasoningPreview = truncatePreview(choice.Message.ReasoningContent, 240)
	for _, toolCall := range choice.Message.ToolCalls {
		if strings.TrimSpace(toolCall.Function.Name) != "StructuredOutput" {
			continue
		}
		var structured map[string]any
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &structured); err != nil {
			result.ParseError = err.Error()
			return
		}
		result.StructuredOutput = structured
		result.ParsedOK = true
		result.SchemaErrors = validateStructuredOutputProbeSchema(structured)
		result.SchemaOK = len(result.SchemaErrors) == 0
		return
	}
	result.ParseError = "no StructuredOutput tool call found"
}

func structuredOutputProbeSummarizeOpenAINativeRun(result *structuredOutputProbeRunResult, body []byte) {
	var response openAIProbeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		result.ParseError = err.Error()
		result.RawError = truncatePreview(string(body), 500)
		return
	}
	if len(response.Choices) == 0 {
		result.ParseError = "missing choices"
		return
	}
	choice := response.Choices[0]
	result.Model = strings.TrimSpace(response.Model)
	result.FinishReason = strings.TrimSpace(choice.FinishReason)
	fullText := openAIMessageText(choice.Message.Content)
	result.TextPreview = truncatePreview(fullText, 240)
	result.ReasoningPreview = truncatePreview(choice.Message.ReasoningContent, 240)
	clean := stripCodeFence(fullText)
	var structured map[string]any
	if err := json.Unmarshal([]byte(clean), &structured); err != nil {
		result.ParseError = err.Error()
		return
	}
	result.StructuredOutput = structured
	result.ParsedOK = true
	result.SchemaErrors = validateStructuredOutputProbeSchema(structured)
	result.SchemaOK = len(result.SchemaErrors) == 0
}

func structuredOutputProbeSummarizeAnthropicRun(result *structuredOutputProbeRunResult, body []byte) {
	var response anthropicProbeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		result.ParseError = err.Error()
		result.RawError = truncatePreview(string(body), 500)
		return
	}
	result.Model = strings.TrimSpace(response.Model)
	result.FinishReason = strings.TrimSpace(response.StopReason)
	for _, block := range response.Content {
		switch strings.TrimSpace(block.Type) {
		case "text":
			if result.TextPreview == "" {
				result.TextPreview = truncatePreview(block.Text, 240)
			}
		case "thinking":
			if result.ReasoningPreview == "" {
				result.ReasoningPreview = truncatePreview(block.Thinking, 240)
			}
		case "tool_use":
			if strings.TrimSpace(block.Name) != "StructuredOutput" {
				continue
			}
			var structured map[string]any
			if err := json.Unmarshal(block.Input, &structured); err != nil {
				result.ParseError = err.Error()
				return
			}
			result.StructuredOutput = structured
			result.ParsedOK = true
			result.SchemaErrors = validateStructuredOutputProbeSchema(structured)
			result.SchemaOK = len(result.SchemaErrors) == 0
		}
	}
	if !result.ParsedOK && result.ParseError == "" {
		result.ParseError = "no StructuredOutput tool_use block found"
	}
}

func validateStructuredOutputProbeSchema(value map[string]any) []string {
	var errs []string
	if len(value) != 2 {
		for key := range value {
			if key != "verdict" && key != "score" {
				errs = append(errs, fmt.Sprintf("$.%s: additional property not allowed", key))
			}
		}
	}
	verdict, ok := value["verdict"]
	if !ok {
		errs = append(errs, "$.verdict: missing required property")
	} else if verdictString, ok := verdict.(string); !ok {
		errs = append(errs, "$.verdict: expected string")
	} else if verdictString != "pass" && verdictString != "fail" {
		errs = append(errs, "$.verdict: expected one of pass, fail")
	}
	score, ok := value["score"]
	if !ok {
		errs = append(errs, "$.score: missing required property")
	} else if _, ok := score.(float64); !ok {
		errs = append(errs, "$.score: expected number")
	}
	return errs
}

func stripCodeFence(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") && strings.HasSuffix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```JSON")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(text, "```")
	}
	return strings.TrimSpace(text)
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

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func truncatePreview(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 1 {
		return value[:limit]
	}
	return value[:limit-1] + "…"
}
