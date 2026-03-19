package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
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

	// Platform default provider route name. This is used as both the
	// rules.PlatformDefaults.ProviderRoute (the default when no project
	// or group policy overrides it) and the registry's default route key.
	// At runtime, rules.Load produces EffectivePolicy.ProviderRoute which
	// the Processor uses to resolve the correct provider from the registry.
	const platformDefaultRoute = "default"
	const fallbackRoute = "secondary"

	rulesLoader := rules.NewLoader(gitlabClient, rules.PlatformDefaults{
		Instructions:        "Platform defaults: prioritize correctness, security, and least-privilege behavior.",
		ConfidenceThreshold: 0.72,
		SeverityThreshold:   "medium",
		IncludePaths:        []string{"src/**"},
		ExcludePaths:        []string{"vendor/**"},
		GateMode:            "threads_resolved",
		ProviderRoute:       platformDefaultRoute,
	})
	gitLabLimiter := gitlab.NewInMemoryRateLimiter(gitlab.RateLimitConfig{Requests: 5, Window: time.Second}, time.Now, nil)
	gitLabLimiter.SetLimit("global", gitlab.RateLimitConfig{Requests: 5, Window: time.Second})
	gitlabClient, err = gitlab.NewClient(cfg.GitLabBaseURL, cfg.GitLabToken, gitlab.WithRateLimiter(gitLabLimiter))
	if err != nil {
		logger.Error("failed to configure gitlab client with rate limiting", "error", err)
		return 1
	}
	llmLimiter := llm.NewInMemoryRateLimiter(llm.RateLimitConfig{Requests: 2, Window: time.Second}, time.Now, nil)
	llmLimiter.SetLimit(platformDefaultRoute, llm.RateLimitConfig{Requests: 2, Window: time.Second})
	llmLimiter.SetLimit(fallbackRoute, llm.RateLimitConfig{Requests: 2, Window: time.Second})
	defaultProvider, err := llm.NewMiniMaxProvider(llm.ProviderConfig{
		BaseURL:     cfg.AnthropicBaseURL,
		APIKey:      cfg.AnthropicAPIKey,
		Model:       cfg.AnthropicModel,
		RouteName:   platformDefaultRoute,
		MaxTokens:   4096,
		RateLimiter: llmLimiter,
	})
	if err != nil {
		logger.Error("failed to configure default llm provider", "error", err)
		return 1
	}
	secondaryProvider, err := llm.NewMiniMaxProvider(llm.ProviderConfig{
		BaseURL:   cfg.AnthropicBaseURL,
		APIKey:    cfg.AnthropicAPIKey,
		Model:     cfg.AnthropicModel,
		RouteName: fallbackRoute,
		MaxTokens: 4096,
	})
	if err != nil {
		logger.Error("failed to configure secondary llm provider", "error", err)
		return 1
	}
	// Build provider registry: maps route names to provider instances.
	// The Processor resolves EffectivePolicy.ProviderRoute at runtime
	// (from rules.Load) to select the correct provider for each review
	// run. When a project/group policy sets a custom ProviderRoute, the
	// registry resolves it; unknown routes fall back to platformDefaultRoute.
	providerRegistry := llm.NewProviderRegistry(logger, platformDefaultRoute, defaultProvider)
	providerRegistry.Register(fallbackRoute, secondaryProvider)
	providerRegistry.SetFallbackRoute(fallbackRoute)
	if cfg.RedisAddr == "" {
		logger.Warn("redis unavailable; optional coordination disabled", "mode", "degraded_fallback")
	} else {
		logger.Info("redis coordination configured", "redis_addr", cfg.RedisAddr)
	}
	// The processor uses the registry for policy-driven provider selection.
	// At runtime, ProcessRun calls registry.ResolveWithFallback(effectivePolicy.ProviderRoute)
	// to pick the provider for each run, instead of always using a static default.
	processor := llm.NewProcessor(logger, db, gitlabClient, rulesLoader, nil, llm.NewDBAuditLogger(db)).WithRegistry(providerRegistry)
	runtimeDeps := newRuntimeDeps(logger, db, processor)
	worker := runtimeDeps.Scheduler
	logger.Info("worker starting", "platform_default_route", platformDefaultRoute, "fallback_route", fallbackRoute, "registry_routes", providerRegistry.Routes())
	if err := worker.Run(ctx); err != nil {
		logger.Error("worker stopped with error", "error", err)
		return 1
	}

	logger.Info("worker shutdown complete")
	return 0
}
