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
	t.Run("anthropic-compatible", func(t *testing.T) {
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
		"openai",
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
		"missing-route",
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
