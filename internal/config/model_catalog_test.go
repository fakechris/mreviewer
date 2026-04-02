package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mreviewer/mreviewer/internal/llm"
)

func TestConfigParsesModelCatalogAndReviewBindings(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	content := `models:
  minimax_reasoning:
    provider: minimax
    base_url: https://api.minimaxi.com/anthropic
    api_key: ${MINIMAX_API_KEY}
    model: MiniMax-M2.7-highspeed
    output_mode: tool_call
    max_tokens: 4096
  fireworks_kimi:
    provider: fireworks_router
    base_url: https://api.fireworks.ai/inference
    api_key: ${FIREWORKS_API_KEY}
    model: accounts/fireworks/routers/kimi-k2p5-turbo
    output_mode: tool_call
model_chains:
  reasoning_primary:
    primary: minimax_reasoning
    fallbacks:
      - fireworks_kimi
review:
  model_chain: reasoning_primary
  advisor_chain: reasoning_primary
  packs:
    - security
    - database
  compare_reviewers:
    - github:gemini
`
	t.Setenv("MINIMAX_API_KEY", "minimax-secret")
	t.Setenv("FIREWORKS_API_KEY", "fireworks-secret")
	if err := os.WriteFile(yamlPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writing yaml: %v", err)
	}

	cfg, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.Models) != 2 {
		t.Fatalf("Models = %d, want 2", len(cfg.Models))
	}
	if cfg.Models["minimax_reasoning"].APIKey != "minimax-secret" {
		t.Fatalf("minimax model api_key = %q, want env-expanded secret", cfg.Models["minimax_reasoning"].APIKey)
	}
	if cfg.ModelChains["reasoning_primary"].Primary != "minimax_reasoning" {
		t.Fatalf("chain primary = %q, want minimax_reasoning", cfg.ModelChains["reasoning_primary"].Primary)
	}
	if len(cfg.ModelChains["reasoning_primary"].Fallbacks) != 1 || cfg.ModelChains["reasoning_primary"].Fallbacks[0] != "fireworks_kimi" {
		t.Fatalf("chain fallbacks = %#v", cfg.ModelChains["reasoning_primary"].Fallbacks)
	}
	if cfg.Review.ModelChain != "reasoning_primary" {
		t.Fatalf("review.model_chain = %q, want reasoning_primary", cfg.Review.ModelChain)
	}
	if cfg.Review.AdvisorChain != "reasoning_primary" {
		t.Fatalf("review.advisor_chain = %q, want reasoning_primary", cfg.Review.AdvisorChain)
	}
	if len(cfg.Review.Packs) != 2 || cfg.Review.Packs[0] != "security" || cfg.Review.Packs[1] != "database" {
		t.Fatalf("review.packs = %#v", cfg.Review.Packs)
	}
}

func TestResolveModelChainBuildsProviderConfigsFromCatalog(t *testing.T) {
	cfg := &Config{
		Models: map[string]ModelConfig{
			"openai_primary": {
				Provider:            llm.ProviderKindOpenAI,
				BaseURL:             "https://api.openai.com/v1",
				APIKey:              "openai-secret",
				Model:               "gpt-5.4",
				OutputMode:          "json_schema",
				MaxCompletionTokens: 12000,
				ReasoningEffort:     "medium",
			},
			"fireworks_backup": {
				Provider:   llm.ProviderKindFireworksRouter,
				BaseURL:    "https://api.fireworks.ai/inference",
				APIKey:     "fireworks-secret",
				Model:      "accounts/fireworks/routers/kimi-k2p5-turbo",
				OutputMode: "tool_call",
				MaxTokens:  4096,
			},
		},
		ModelChains: map[string]ModelChainConfig{
			"review_primary": {
				Primary:   "openai_primary",
				Fallbacks: []string{"fireworks_backup"},
			},
		},
	}

	primary, fallbacks, providers, err := ResolveModelChain(cfg, "review_primary")
	if err != nil {
		t.Fatalf("ResolveModelChain() error: %v", err)
	}
	if primary != "openai_primary" {
		t.Fatalf("primary = %q, want openai_primary", primary)
	}
	if len(fallbacks) != 1 || fallbacks[0] != "fireworks_backup" {
		t.Fatalf("fallbacks = %#v", fallbacks)
	}
	if len(providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(providers))
	}
	if providers["openai_primary"].RouteName != "openai_primary" {
		t.Fatalf("provider route name = %q, want openai_primary", providers["openai_primary"].RouteName)
	}
	if providers["fireworks_backup"].Kind != llm.ProviderKindFireworksRouter {
		t.Fatalf("fallback provider kind = %q", providers["fireworks_backup"].Kind)
	}
}

func TestResolveModelChainRejectsUnknownFallbackModel(t *testing.T) {
	cfg := &Config{
		Models: map[string]ModelConfig{
			"openai_primary": {
				Provider: llm.ProviderKindOpenAI,
				BaseURL:  "https://api.openai.com/v1",
				APIKey:   "openai-secret",
				Model:    "gpt-5.4",
			},
		},
		ModelChains: map[string]ModelChainConfig{
			"review_primary": {
				Primary:   "openai_primary",
				Fallbacks: []string{"missing_model"},
			},
		},
	}

	_, _, _, err := ResolveModelChain(cfg, "review_primary")
	if err == nil {
		t.Fatal("ResolveModelChain() error = nil, want missing fallback failure")
	}
}
