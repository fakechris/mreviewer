package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/database"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/llm"
	"github.com/mreviewer/mreviewer/internal/logging"
	"github.com/mreviewer/mreviewer/internal/rules"
)

func main() {
	os.Exit(run())
}

func run() int {
	logger := logging.NewLogger(slog.LevelInfo)

	cfg, err := config.Load("config.yaml")
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		return 1
	}
	if err := validateWorkerConfig(cfg); err != nil {
		logger.Error("invalid worker configuration", "error", err)
		return 1
	}

	db, err := database.Open(cfg.MySQLDSN)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		return 1
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	gitlabClient, err := gitlab.NewClient(cfg.GitLabBaseURL, cfg.GitLabToken)
	if err != nil {
		logger.Error("failed to configure gitlab client", "error", err)
		return 1
	}

	defaultRoute, configuredFallbackRoute, providerConfigs, err := providerConfigsFromConfig(cfg)
	if err != nil {
		logger.Error("failed to resolve llm route configuration", "error", err)
		return 1
	}

	rulesLoader := rules.NewLoader(gitlabClient, rules.PlatformDefaults{
		Instructions:        "Platform defaults: prioritize correctness, security, and least-privilege behavior.",
		ConfidenceThreshold: 0.72,
		SeverityThreshold:   "medium",
		IncludePaths:        []string{"src/**"},
		ExcludePaths:        []string{"vendor/**"},
		GateMode:            "threads_resolved",
		ProviderRoute:       defaultRoute,
	})
	gitLabLimiter := gitlab.NewInMemoryRateLimiter(gitlab.RateLimitConfig{Requests: 5, Window: time.Second}, time.Now, nil)
	gitLabLimiter.SetLimit("global", gitlab.RateLimitConfig{Requests: 5, Window: time.Second})
	gitlabClient, err = gitlab.NewClient(cfg.GitLabBaseURL, cfg.GitLabToken, gitlab.WithRateLimiter(gitLabLimiter))
	if err != nil {
		logger.Error("failed to configure gitlab client with rate limiting", "error", err)
		return 1
	}
	llmLimiter := llm.NewInMemoryRateLimiter(llm.RateLimitConfig{Requests: 2, Window: time.Second}, time.Now, nil)
	for route, providerCfg := range providerConfigs {
		llmLimiter.SetLimit(route, llm.RateLimitConfig{Requests: 2, Window: time.Second})
		providerCfg.RateLimiter = llmLimiter
		providerConfigs[route] = providerCfg
	}
	providerRegistry, err := llm.BuildProviderRegistryFromRouteConfigs(logger, defaultRoute, configuredFallbackRoute, providerConfigs)
	if err != nil {
		logger.Error("failed to configure llm provider registry", "error", err)
		return 1
	}
	if cfg.RedisAddr == "" {
		logger.Warn("redis unavailable; optional coordination disabled", "mode", "degraded_fallback")
	} else {
		logger.Info("redis coordination configured", "redis_addr", cfg.RedisAddr)
	}
	// The processor uses the registry for policy-driven provider selection.
	// At runtime, ProcessRun calls registry.ResolveWithFallback(effectivePolicy.ProviderRoute)
	// to pick the provider for each run, instead of always using a static default.
	processor := llm.NewProcessor(logger, db, gitlabClient, rulesLoader, nil, llm.NewDBAuditLogger(db)).WithRegistry(providerRegistry)
	runtimeDeps := newRuntimeDepsWithWriteback(logger, db, processor, gitlabClient)
	worker := runtimeDeps.Scheduler
	logger.Info("worker starting", "platform_default_route", defaultRoute, "fallback_route", configuredFallbackRoute, "registry_routes", providerRegistry.Routes())
	if err := worker.Run(ctx); err != nil {
		logger.Error("worker stopped with error", "error", err)
		return 1
	}

	logger.Info("worker shutdown complete")
	return 0
}

func validateWorkerConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("worker: configuration is required")
	}
	if strings.TrimSpace(cfg.GitLabToken) == "" {
		return fmt.Errorf("worker: GITLAB_TOKEN is required")
	}
	return nil
}

func providerConfigsFromConfig(cfg *config.Config) (string, string, map[string]llm.ProviderConfig, error) {
	if cfg == nil {
		return "", "", nil, fmt.Errorf("worker: configuration is required")
	}
	routes := make(map[string]llm.ProviderConfig)
	if len(cfg.LLM.Routes) > 0 {
		defaultRoute := strings.TrimSpace(cfg.LLM.DefaultRoute)
		if defaultRoute == "" {
			return "", "", nil, fmt.Errorf("worker: llm.default_route is required when llm.routes is configured")
		}
		for routeName, route := range cfg.LLM.Routes {
			trimmed := strings.TrimSpace(routeName)
			if trimmed == "" {
				return "", "", nil, fmt.Errorf("worker: llm route name cannot be empty")
			}
			providerKind := strings.TrimSpace(route.Provider)
			if providerKind == "" {
				return "", "", nil, fmt.Errorf("worker: llm.routes.%s.provider is required", trimmed)
			}
			routes[trimmed] = llm.ProviderConfig{
				Kind:                providerKind,
				BaseURL:             strings.TrimSpace(route.BaseURL),
				APIKey:              strings.TrimSpace(route.APIKey),
				Model:               strings.TrimSpace(route.Model),
				RouteName:           trimmed,
				OutputMode:          strings.TrimSpace(route.OutputMode),
				MaxTokens:           route.MaxTokens,
				MaxCompletionTokens: route.MaxCompletionTokens,
				ReasoningEffort:     strings.TrimSpace(route.ReasoningEffort),
				Temperature:         route.Temperature,
			}
		}
		return defaultRoute, strings.TrimSpace(cfg.LLM.FallbackRoute), routes, nil
	}

	const legacyDefaultRoute = "default"
	const legacyFallbackRoute = "secondary"
	legacy := llm.ProviderConfig{
		Kind:       llm.ProviderKindMiniMax,
		BaseURL:    strings.TrimSpace(cfg.AnthropicBaseURL),
		APIKey:     strings.TrimSpace(cfg.AnthropicAPIKey),
		Model:      strings.TrimSpace(cfg.AnthropicModel),
		MaxTokens:  4096,
		OutputMode: "tool_call",
	}
	defaultProvider := legacy
	defaultProvider.RouteName = legacyDefaultRoute
	secondaryProvider := legacy
	secondaryProvider.RouteName = legacyFallbackRoute
	routes[legacyDefaultRoute] = defaultProvider
	routes[legacyFallbackRoute] = secondaryProvider
	return legacyDefaultRoute, legacyFallbackRoute, routes, nil
}
