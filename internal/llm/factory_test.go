package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
)

func TestNewProviderFromConfigSupportsKnownKinds(t *testing.T) {
	t.Run("minimax", func(t *testing.T) {
		provider, err := NewProviderFromConfig(ProviderConfig{
			Kind:      "minimax",
			BaseURL:   "https://api.minimaxi.com/anthropic",
			APIKey:    "secret",
			Model:     "MiniMax-M2.7",
			RouteName: "minimax",
		})
		if err != nil {
			t.Fatalf("NewProviderFromConfig: %v", err)
		}
		if provider == nil {
			t.Fatal("provider is nil")
		}
	})

	t.Run("anthropic_compatible", func(t *testing.T) {
		provider, err := NewProviderFromConfig(ProviderConfig{
			Kind:      "anthropic_compatible",
			BaseURL:   "https://api.minimaxi.com/anthropic",
			APIKey:    "secret",
			Model:     "MiniMax-M2.7",
			RouteName: "legacy",
		})
		if err != nil {
			t.Fatalf("NewProviderFromConfig: %v", err)
		}
		if provider == nil {
			t.Fatal("provider is nil")
		}
	})

	t.Run("anthropic", func(t *testing.T) {
		provider, err := NewProviderFromConfig(ProviderConfig{
			Kind:      "anthropic",
			BaseURL:   "https://api.anthropic.com",
			APIKey:    "secret",
			Model:     "claude-opus-4-1",
			RouteName: "opus",
		})
		if err != nil {
			t.Fatalf("NewProviderFromConfig: %v", err)
		}
		if provider == nil {
			t.Fatal("provider is nil")
		}
	})

	t.Run("openai", func(t *testing.T) {
		provider, err := NewProviderFromConfig(ProviderConfig{
			Kind:      "openai",
			BaseURL:   "https://api.openai.com/v1",
			APIKey:    "secret",
			Model:     "gpt-4.1-mini",
			RouteName: "openai",
		})
		if err != nil {
			t.Fatalf("NewProviderFromConfig: %v", err)
		}
		if provider == nil {
			t.Fatal("provider is nil")
		}
	})

	t.Run("fireworks_router", func(t *testing.T) {
		provider, err := NewProviderFromConfig(ProviderConfig{
			Kind:      "fireworks_router",
			BaseURL:   "https://api.fireworks.ai/inference",
			APIKey:    "fw_test_key",
			Model:     "accounts/fireworks/routers/kimi-k2p5-turbo",
			RouteName: "fireworks",
		})
		if err != nil {
			t.Fatalf("NewProviderFromConfig: %v", err)
		}
		if provider == nil {
			t.Fatal("provider is nil")
		}
	})
}

func TestNewProviderFromConfigRejectsUnknownKind(t *testing.T) {
	_, err := NewProviderFromConfig(ProviderConfig{
		Kind:      "mystery",
		BaseURL:   "https://example.com",
		APIKey:    "secret",
		Model:     "model",
		RouteName: "mystery",
	})
	if err == nil {
		t.Fatal("expected unknown provider kind error")
	}
}

func TestBuildProviderRegistryFromRouteConfigs(t *testing.T) {
	registry, err := BuildProviderRegistryFromRouteConfigs(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"default",
		[]string{"openai", "opus"},
		map[string]ProviderConfig{
			"default": {
				Kind:      "minimax",
				BaseURL:   "https://api.minimaxi.com/anthropic",
				APIKey:    "secret",
				Model:     "MiniMax-M2.7",
				RouteName: "default",
			},
			"openai": {
				Kind:      "openai",
				BaseURL:   "https://api.openai.com/v1",
				APIKey:    "secret",
				Model:     "gpt-4.1-mini",
				RouteName: "openai",
			},
			"opus": {
				Kind:      "anthropic",
				BaseURL:   "https://api.anthropic.com",
				APIKey:    "secret",
				Model:     "claude-opus-4-1",
				RouteName: "opus",
			},
		},
	)
	if err != nil {
		t.Fatalf("BuildProviderRegistryFromRouteConfigs: %v", err)
	}
	if registry == nil {
		t.Fatal("registry is nil")
	}

	routes := registry.Routes()
	if len(routes) != 3 {
		t.Fatalf("routes = %#v, want 3 routes", routes)
	}

	if _, route := registry.Resolve("opus"); route != "opus" {
		t.Fatalf("Resolve(opus) route = %q, want opus", route)
	}
}

func TestBuildProviderRegistryFromRouteConfigsRejectsUnknownFallback(t *testing.T) {
	_, err := BuildProviderRegistryFromRouteConfigs(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"default",
		[]string{"missing-route"},
		map[string]ProviderConfig{
			"default": {
				Kind:      "minimax",
				BaseURL:   "https://api.minimaxi.com/anthropic",
				APIKey:    "secret",
				Model:     "MiniMax-M2.7",
				RouteName: "default",
			},
		},
	)
	if err == nil {
		t.Fatal("expected missing fallback route error")
	}
}

func TestOpenAIProviderUsesToolCallRequestShape(t *testing.T) {
	transport := &captureTransport{responseBody: `{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"submit_review","arguments":"{\"schema_version\":\"1.0\",\"review_run_id\":\"123\",\"summary\":\"ok\",\"findings\":[]}"}}]}}],"usage":{"completion_tokens":21}}`}
	provider, err := NewProviderFromConfig(ProviderConfig{
		Kind:       "openai",
		BaseURL:    "https://api.openai.com/v1",
		APIKey:     "secret-token",
		Model:      "gpt-4.1-mini",
		RouteName:  "openai",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatalf("NewProviderFromConfig: %v", err)
	}

	if _, err := provider.Review(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"}); err != nil {
		t.Fatalf("Review: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(transport.body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["model"] != "gpt-4.1-mini" {
		t.Fatalf("model = %#v", payload["model"])
	}
	if _, ok := payload["tools"]; !ok {
		t.Fatal("missing tools payload")
	}
	toolChoice, ok := payload["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %#v", payload["tool_choice"])
	}
	if toolChoice["type"] != "function" {
		t.Fatalf("tool_choice.type = %#v, want function", toolChoice["type"])
	}
}

func TestOpenAIProviderUsesJSONSchemaRequestShape(t *testing.T) {
	transport := &captureTransport{responseBody: `{"choices":[{"message":{"content":"{\"schema_version\":\"1.0\",\"review_run_id\":\"123\",\"summary\":\"ok\",\"findings\":[]}"}}],"usage":{"completion_tokens":21}}`}
	provider, err := NewProviderFromConfig(ProviderConfig{
		Kind:                "openai",
		BaseURL:             "https://api.openai.com/v1",
		APIKey:              "secret-token",
		Model:               "gpt-5.4",
		RouteName:           "openai-gpt-5-4",
		OutputMode:          "json_schema",
		MaxCompletionTokens: 12000,
		ReasoningEffort:     "medium",
		HTTPClient:          &http.Client{Transport: transport},
		Now:                 func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatalf("NewProviderFromConfig: %v", err)
	}

	if _, err := provider.Review(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"}); err != nil {
		t.Fatalf("Review: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(transport.body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["model"] != "gpt-5.4" {
		t.Fatalf("model = %#v", payload["model"])
	}
	if payload["max_completion_tokens"] != float64(12000) {
		t.Fatalf("max_completion_tokens = %#v, want 12000", payload["max_completion_tokens"])
	}
	if payload["reasoning_effort"] != "medium" {
		t.Fatalf("reasoning_effort = %#v, want medium", payload["reasoning_effort"])
	}
	if _, ok := payload["tools"]; ok {
		t.Fatal("tools should not be sent in json_schema mode")
	}
	responseFormat, ok := payload["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format = %#v, want object", payload["response_format"])
	}
	if responseFormat["type"] != "json_schema" {
		t.Fatalf("response_format.type = %#v, want json_schema", responseFormat["type"])
	}
	jsonSchema, ok := responseFormat["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("response_format.json_schema = %#v, want object", responseFormat["json_schema"])
	}
	schema, ok := jsonSchema["schema"].(map[string]any)
	if !ok {
		t.Fatalf("response_format.json_schema.schema = %#v, want object", jsonSchema["schema"])
	}
	rootRequired := stringSet(schema["required"])
	for _, key := range []string{"schema_version", "review_run_id", "status", "summary", "summary_note", "blind_spots", "findings"} {
		if _, ok := rootRequired[key]; !ok {
			t.Fatalf("root required missing %q: %#v", key, schema["required"])
		}
	}
	rootProps, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema.properties = %#v, want object", schema["properties"])
	}
	summaryNote, ok := rootProps["summary_note"].(map[string]any)
	if !ok {
		t.Fatalf("summary_note schema = %#v, want object", rootProps["summary_note"])
	}
	if !schemaTypeContains(summaryNote["type"], "null") {
		t.Fatalf("summary_note type = %#v, want nullable", summaryNote["type"])
	}
	findings, ok := rootProps["findings"].(map[string]any)
	if !ok {
		t.Fatalf("findings schema = %#v, want object", rootProps["findings"])
	}
	items, ok := findings["items"].(map[string]any)
	if !ok {
		t.Fatalf("findings.items = %#v, want object", findings["items"])
	}
	itemRequired := stringSet(items["required"])
	for _, key := range []string{"category", "severity", "confidence", "title", "body_markdown", "path", "anchor_kind", "old_line", "new_line"} {
		if _, ok := itemRequired[key]; !ok {
			t.Fatalf("finding required missing %q: %#v", key, items["required"])
		}
	}
	itemProps, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatalf("findings.items.properties = %#v, want object", items["properties"])
	}
	if !schemaTypeContains(itemProps["old_line"].(map[string]any)["type"], "null") {
		t.Fatalf("old_line type = %#v, want nullable", itemProps["old_line"].(map[string]any)["type"])
	}
}

func TestValidateReviewResultStrictJSONAllowsNullOptionalFields(t *testing.T) {
	raw := `{
		"schema_version":"1.0",
		"review_run_id":"123",
		"summary":"ok",
		"status":"completed",
		"summary_note":null,
		"blind_spots":null,
		"findings":[{
			"category":"bug-risk",
			"severity":"medium",
			"confidence":0.7,
			"title":"Issue",
			"body_markdown":"Body",
			"path":"main.go",
			"anchor_kind":"line",
			"old_line":null,
			"new_line":1,
			"range_start_kind":null,
			"range_start_old_line":null,
			"range_start_new_line":null,
			"range_end_kind":null,
			"range_end_old_line":null,
			"range_end_new_line":null,
			"anchor_snippet":null,
			"evidence":null,
			"suggested_patch":null,
			"canonical_key":null,
			"symbol":null,
			"trigger_condition":null,
			"impact":null,
			"introduced_by_this_change":false,
			"blind_spots":null,
			"no_finding_reason":null
		}]
	}`

	if err := validateReviewResultStrictJSON(raw); err != nil {
		t.Fatalf("validateReviewResultStrictJSON: %v", err)
	}
}

func TestValidateReviewResultStrictJSONRejectsWrongOptionalFieldType(t *testing.T) {
	raw := `{
		"schema_version":"1.0",
		"review_run_id":"123",
		"summary":"ok",
		"status":"completed",
		"summary_note":null,
		"blind_spots":null,
		"findings":[{
			"category":"bug-risk",
			"severity":"medium",
			"confidence":0.7,
			"title":"Issue",
			"body_markdown":"Body",
			"path":"main.go",
			"anchor_kind":"line",
			"old_line":"not-a-number",
			"new_line":1,
			"range_start_kind":null,
			"range_start_old_line":null,
			"range_start_new_line":null,
			"range_end_kind":null,
			"range_end_old_line":null,
			"range_end_new_line":null,
			"anchor_snippet":null,
			"evidence":null,
			"suggested_patch":null,
			"canonical_key":null,
			"symbol":null,
			"trigger_condition":null,
			"impact":null,
			"introduced_by_this_change":false,
			"blind_spots":null,
			"no_finding_reason":null
		}]
	}`

	err := validateReviewResultStrictJSON(raw)
	if err == nil {
		t.Fatal("expected strict validation error")
	}
	if !strings.Contains(err.Error(), "$.findings[0].old_line must be integer") {
		t.Fatalf("error = %v, want old_line integer validation failure", err)
	}
}

func TestOpenAIProviderMissingToolCallReturnsParserError(t *testing.T) {
	transport := &captureTransport{responseBody: `{"choices":[{"message":{"content":"{\"schema_version\":\"1.0\",\"review_run_id\":\"123\",\"summary\":\"ok\",\"findings\":[]}"}}],"usage":{"completion_tokens":21}}`}
	provider, err := NewProviderFromConfig(ProviderConfig{
		Kind:       "openai",
		BaseURL:    "https://api.openai.com/v1",
		APIKey:     "secret-token",
		Model:      "gpt-4.1-mini",
		RouteName:  "openai",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatalf("NewProviderFromConfig: %v", err)
	}

	_, err = provider.Review(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"})
	if err == nil {
		t.Fatal("expected missing tool_call parser error")
	}
	if !isParserError(err) {
		t.Fatalf("error = %v, want parser_error classification", err)
	}
	var parseErr *providerParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("expected providerParseError, got %T", err)
	}
	if !strings.Contains(parseErr.rawResponse, `"findings":[]`) {
		t.Fatalf("rawResponse = %q, want captured assistant content", parseErr.rawResponse)
	}
}

func TestOpenAIProviderJSONSchemaMissingContentReturnsRawResponse(t *testing.T) {
	transport := &captureTransport{responseBody: `{"choices":[{"message":{"content":null}}],"usage":{"completion_tokens":21}}`}
	provider, err := NewProviderFromConfig(ProviderConfig{
		Kind:       "openai",
		BaseURL:    "https://api.openai.com/v1",
		APIKey:     "secret-token",
		Model:      "gpt-5.4",
		RouteName:  "openai-gpt-5-4",
		OutputMode: "json_schema",
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return time.Unix(100, 0) },
	})
	if err != nil {
		t.Fatalf("NewProviderFromConfig: %v", err)
	}

	_, err = provider.Review(context.Background(), ctxpkg.ReviewRequest{SchemaVersion: "1.0", ReviewRunID: "123"})
	if err == nil {
		t.Fatal("expected missing structured content parser error")
	}
	var parseErr *providerParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("expected providerParseError, got %T", err)
	}
	if !strings.Contains(parseErr.rawResponse, `"content":null`) {
		t.Fatalf("rawResponse = %q, want original response body context", parseErr.rawResponse)
	}
}

func stringSet(value any) map[string]struct{} {
	out := make(map[string]struct{})
	for _, item := range anyToStringSlice(value) {
		out[item] = struct{}{}
	}
	return out
}

func schemaTypeContains(value any, want string) bool {
	switch typed := value.(type) {
	case string:
		return typed == want
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && text == want {
				return true
			}
		}
	case []string:
		for _, item := range typed {
			if item == want {
				return true
			}
		}
	}
	return false
}
