package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/gate"
)

type errStatusPublisher struct{ err error }

func (p errStatusPublisher) PublishStatus(context.Context, gate.Result) error { return p.err }

func TestValidateWorkerConfigRequiresGitLabToken(t *testing.T) {
	err := validateWorkerConfig(&config.Config{GitLabBaseURL: "https://gitlab.example.com"})
	if err == nil {
		t.Fatal("expected missing token validation error")
	}
	if !strings.Contains(err.Error(), "GITLAB_TOKEN") {
		t.Fatalf("error = %q, want GITLAB_TOKEN mention", err.Error())
	}
}

func TestValidateWorkerConfigAllowsConfiguredToken(t *testing.T) {
	err := validateWorkerConfig(&config.Config{GitLabBaseURL: "https://gitlab.example.com", GitLabToken: "secret-token"})
	if err != nil {
		t.Fatalf("validateWorkerConfig: %v", err)
	}
}

func TestProviderConfigsFromLegacyAnthropicSettings(t *testing.T) {
	cfg := &config.Config{
		AnthropicBaseURL: "https://api.minimaxi.com/anthropic",
		AnthropicAPIKey:  "secret",
		AnthropicModel:   "MiniMax-M2.7",
	}

	defaultRoute, fallbackRoute, routes, err := providerConfigsFromConfig(cfg)
	if err != nil {
		t.Fatalf("providerConfigsFromConfig: %v", err)
	}
	if defaultRoute != "default" {
		t.Fatalf("defaultRoute = %q, want default", defaultRoute)
	}
	if fallbackRoute != "secondary" {
		t.Fatalf("fallbackRoute = %q, want secondary", fallbackRoute)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(routes))
	}
	if routes["default"].Kind != "minimax" {
		t.Fatalf("default kind = %q, want minimax", routes["default"].Kind)
	}
}

func TestProviderConfigsFromLLMRoutes(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			DefaultRoute:  "minimax",
			FallbackRoute: "openai",
			Routes: map[string]config.LLMRouteConfig{
				"minimax": {
					Provider:    "minimax",
					BaseURL:     "https://api.minimaxi.com/anthropic",
					APIKey:      "minimax-secret",
					Model:       "MiniMax-M2.7",
					OutputMode:  "tool_call",
					MaxTokens:   4096,
					Temperature: 0.2,
				},
				"openai": {
					Provider:            "openai",
					BaseURL:             "https://api.openai.com/v1",
					APIKey:              "openai-secret",
					Model:               "gpt-5.4",
					OutputMode:          "json_schema",
					MaxCompletionTokens: 12000,
					ReasoningEffort:     "medium",
					Temperature:         0.2,
				},
			},
		},
	}

	defaultRoute, fallbackRoute, routes, err := providerConfigsFromConfig(cfg)
	if err != nil {
		t.Fatalf("providerConfigsFromConfig: %v", err)
	}
	if defaultRoute != "minimax" {
		t.Fatalf("defaultRoute = %q, want minimax", defaultRoute)
	}
	if fallbackRoute != "openai" {
		t.Fatalf("fallbackRoute = %q, want openai", fallbackRoute)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(routes))
	}
	if routes["openai"].Kind != "openai" {
		t.Fatalf("openai kind = %q, want openai", routes["openai"].Kind)
	}
	if routes["minimax"].RouteName != "minimax" {
		t.Fatalf("minimax route name = %q, want minimax", routes["minimax"].RouteName)
	}
	if routes["openai"].OutputMode != "json_schema" {
		t.Fatalf("openai output_mode = %q, want json_schema", routes["openai"].OutputMode)
	}
	if routes["openai"].MaxCompletionTokens != 12000 {
		t.Fatalf("openai max_completion_tokens = %d, want 12000", routes["openai"].MaxCompletionTokens)
	}
	if routes["openai"].ReasoningEffort != "medium" {
		t.Fatalf("openai reasoning_effort = %q, want medium", routes["openai"].ReasoningEffort)
	}
}

func TestProviderConfigsFromLLMRoutesRequiresProviderKind(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{
			DefaultRoute: "openai",
			Routes: map[string]config.LLMRouteConfig{
				"openai": {
					BaseURL: "https://api.openai.com/v1",
					APIKey:  "openai-secret",
					Model:   "gpt-5.4",
				},
			},
		},
	}

	_, _, _, err := providerConfigsFromConfig(cfg)
	if err == nil {
		t.Fatal("expected missing provider kind error")
	}
	if !strings.Contains(err.Error(), "llm.routes.openai.provider is required") {
		t.Fatalf("error = %q, want provider required message", err.Error())
	}
}

func TestProviderConfigsFromSingleProviderQuickStartMiniMax(t *testing.T) {
	cfg := &config.Config{
		LLMProvider: "minimax",
		LLMBaseURL:  "https://api.minimaxi.com/anthropic",
		LLMAPIKey:   "minimax-secret",
		LLMModel:    "MiniMax-M2.7-highspeed",
	}

	defaultRoute, fallbackRoute, routes, err := providerConfigsFromConfig(cfg)
	if err != nil {
		t.Fatalf("providerConfigsFromConfig: %v", err)
	}
	if defaultRoute != "default" {
		t.Fatalf("defaultRoute = %q, want default", defaultRoute)
	}
	if fallbackRoute != "secondary" {
		t.Fatalf("fallbackRoute = %q, want secondary", fallbackRoute)
	}
	if got := routes["default"].Kind; got != "minimax" {
		t.Fatalf("default kind = %q, want minimax", got)
	}
}

func TestProviderConfigsFromSingleProviderQuickStartAnthropic(t *testing.T) {
	cfg := &config.Config{
		LLMProvider: "anthropic",
		LLMBaseURL:  "https://api.anthropic.com",
		LLMAPIKey:   "anthropic-secret",
		LLMModel:    "claude-sonnet-4-6",
	}

	defaultRoute, fallbackRoute, routes, err := providerConfigsFromConfig(cfg)
	if err != nil {
		t.Fatalf("providerConfigsFromConfig: %v", err)
	}
	if defaultRoute != "default" {
		t.Fatalf("defaultRoute = %q, want default", defaultRoute)
	}
	if fallbackRoute != "secondary" {
		t.Fatalf("fallbackRoute = %q, want secondary", fallbackRoute)
	}
	if got := routes["default"].Kind; got != "anthropic" {
		t.Fatalf("default kind = %q, want anthropic", got)
	}
}

func TestProviderConfigsFromSingleProviderQuickStartOpenAI(t *testing.T) {
	cfg := &config.Config{
		LLMProvider: "openai",
		LLMBaseURL:  "https://api.openai.com/v1",
		LLMAPIKey:   "openai-secret",
		LLMModel:    "gpt-5.4",
	}

	defaultRoute, fallbackRoute, routes, err := providerConfigsFromConfig(cfg)
	if err != nil {
		t.Fatalf("providerConfigsFromConfig: %v", err)
	}
	if defaultRoute != "default" {
		t.Fatalf("defaultRoute = %q, want default", defaultRoute)
	}
	if fallbackRoute != "secondary" {
		t.Fatalf("fallbackRoute = %q, want secondary", fallbackRoute)
	}
	if got := routes["default"].Kind; got != "openai" {
		t.Fatalf("default kind = %q, want openai", got)
	}
	if got := routes["default"].MaxTokens; got != 4096 {
		t.Fatalf("default max_tokens = %d, want 4096", got)
	}
	if got := routes["default"].MaxCompletionTokens; got != 12000 {
		t.Fatalf("default max_completion_tokens = %d, want 12000", got)
	}
	if got := routes["default"].OutputMode; got != "json_schema" {
		t.Fatalf("default output_mode = %q, want json_schema", got)
	}
}

func TestShouldLogHeartbeatStop(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "context canceled",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "wrapped context canceled",
			err:  errors.Join(errors.New("db shutdown"), context.Canceled),
			want: false,
		},
		{
			name: "unexpected error",
			err:  errors.New("boom"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldLogHeartbeatStop(tt.err); got != tt.want {
				t.Fatalf("shouldLogHeartbeatStop(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestNewReviewRunProcessorRequiresDependencies(t *testing.T) {
	cfg := &config.Config{}

	if _, err := newReviewRunProcessor(nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("newReviewRunProcessor(nil, ...) error = nil, want non-nil")
	}
	if _, err := newReviewRunProcessor(cfg, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("newReviewRunProcessor missing db error = nil, want non-nil")
	}
}

func TestWorkerStatusPublisherJoinsPublisherErrors(t *testing.T) {
	publisher := &workerStatusPublisher{
		publishers: []gate.StatusPublisher{
			errStatusPublisher{err: errors.New("gitlab failed")},
			errStatusPublisher{err: errors.New("github failed")},
		},
	}

	err := publisher.PublishStatus(context.Background(), gate.Result{})
	if err == nil {
		t.Fatal("PublishStatus error = nil, want combined error")
	}
	if !strings.Contains(err.Error(), "gitlab failed") {
		t.Fatalf("combined error = %q, want gitlab failure", err.Error())
	}
	if !strings.Contains(err.Error(), "github failed") {
		t.Fatalf("combined error = %q, want github failure", err.Error())
	}
}
