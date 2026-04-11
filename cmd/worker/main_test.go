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

func TestProviderConfigsFromModelChainConfig(t *testing.T) {
	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"minimax_reasoning": {
				Provider:   "minimax",
				BaseURL:    "https://api.minimaxi.com/anthropic",
				APIKey:     "secret",
				Model:      "MiniMax-M2.7",
				OutputMode: "tool_call",
				MaxTokens:  4096,
			},
			"openai_backup": {
				Provider:            "openai",
				BaseURL:             "https://api.openai.com/v1",
				APIKey:              "openai-secret",
				Model:               "gpt-5.4",
				OutputMode:          "tool_call",
				MaxCompletionTokens: 12000,
				ReasoningEffort:     "medium",
			},
		},
		ModelChains: map[string]config.ModelChainConfig{
			"review_primary": {
				Primary:   "minimax_reasoning",
				Fallbacks: []string{"openai_backup"},
			},
		},
		Review: config.ReviewConfig{ModelChain: "review_primary"},
	}

	defaultRoute, fallbackRoutes, routes, err := providerConfigsFromConfig(cfg)
	if err != nil {
		t.Fatalf("providerConfigsFromConfig: %v", err)
	}
	if defaultRoute != "minimax_reasoning" {
		t.Fatalf("defaultRoute = %q, want minimax_reasoning", defaultRoute)
	}
	if len(fallbackRoutes) != 1 || fallbackRoutes[0] != "openai_backup" {
		t.Fatalf("fallbackRoutes = %#v, want [openai_backup]", fallbackRoutes)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(routes))
	}
	if routes["minimax_reasoning"].Kind != "minimax" {
		t.Fatalf("primary kind = %q, want minimax", routes["minimax_reasoning"].Kind)
	}
	if routes["openai_backup"].Kind != "openai" {
		t.Fatalf("fallback kind = %q, want openai", routes["openai_backup"].Kind)
	}
	if routes["openai_backup"].OutputMode != "tool_call" {
		t.Fatalf("openai output_mode = %q, want tool_call", routes["openai_backup"].OutputMode)
	}
	if routes["openai_backup"].MaxCompletionTokens != 12000 {
		t.Fatalf("openai max_completion_tokens = %d, want 12000", routes["openai_backup"].MaxCompletionTokens)
	}
	if routes["openai_backup"].ReasoningEffort != "medium" {
		t.Fatalf("openai reasoning_effort = %q, want medium", routes["openai_backup"].ReasoningEffort)
	}
}

func TestProviderConfigsFromModelChainRequiresProviderKind(t *testing.T) {
	cfg := &config.Config{
		Models: map[string]config.ModelConfig{
			"openai_primary": {
				BaseURL: "https://api.openai.com/v1",
				APIKey:  "openai-secret",
				Model:   "gpt-5.4",
			},
		},
		ModelChains: map[string]config.ModelChainConfig{
			"review_primary": {Primary: "openai_primary"},
		},
		Review: config.ReviewConfig{ModelChain: "review_primary"},
	}

	_, _, _, err := providerConfigsFromConfig(cfg)
	if err == nil {
		t.Fatal("expected missing provider kind error")
	}
	if !strings.Contains(err.Error(), "models.openai_primary.provider is required") {
		t.Fatalf("error = %q, want provider required message", err.Error())
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
