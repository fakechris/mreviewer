package main

import (
	"strings"
	"testing"

	"github.com/mreviewer/mreviewer/internal/config"
)

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
