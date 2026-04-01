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
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/llm"
	"github.com/mreviewer/mreviewer/internal/logging"
	githubplatform "github.com/mreviewer/mreviewer/internal/platform/github"
	"github.com/mreviewer/mreviewer/internal/scheduler"
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

	sqlDB, err := database.Open(cfg.MySQLDSN)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		return 1
	}
	defer sqlDB.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	defaultRoute, configuredFallbackRoute, providerConfigs, err := providerConfigsFromConfig(cfg)
	if err != nil {
		logger.Error("failed to resolve llm route configuration", "error", err)
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
	var (
		gitlabClient        *gitlab.Client
		gitlabProcessor     scheduler.Processor
		gitHubProcessor     scheduler.Processor
		gitHubPublishClient githubplatform.PublishClient
		statusPublisher     gate.StatusPublisher = gate.NoopStatusPublisher{}
	)
	gitlabProcessor, gitlabClient, err = newGitLabEngineProcessor(sqlDB, cfg, defaultRoute, providerRegistry.ResolveWithFallback)
	if err != nil {
		logger.Error("failed to configure gitlab runtime processor", "error", err)
		return 1
	}
	if gitlabClient != nil {
		statusPublisher = gate.NewGitLabStatusPublisher(gitlabClient, db.New(sqlDB))
	}
	gitHubProcessor, gitHubClient, err := newGitHubEngineProcessor(sqlDB, cfg, defaultRoute, providerRegistry.ResolveWithFallback)
	if err != nil {
		logger.Error("failed to configure github runtime processor", "error", err)
		return 1
	}
	if gitHubClient != nil {
		gitHubPublishClient = gitHubClient
		githubStatusPublisher := gate.NewGitHubStatusPublisher(gitHubStatusClientAdapter{client: gitHubClient}, db.New(sqlDB))
		statusPublisher = newPlatformStatusPublisher(sqlDB, statusPublisher, githubStatusPublisher)
	}
	processor := newPlatformDispatchProcessor(sqlDB, gitlabProcessor, gitHubProcessor)
	if processor == nil {
		logger.Error("invalid worker configuration", "error", fmt.Errorf("worker: at least one complete GitLab or GitHub configuration is required"))
		return 1
	}
	runtimeDeps := newRuntimeDepsWithPlatformClientsAndGatePublishers(logger, sqlDB, processor, gitlabClient, gitHubPublishClient, statusPublisher, gate.NoopCIGatePublisher{})
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
	gitlabReady := strings.TrimSpace(cfg.GitLabBaseURL) != "" && strings.TrimSpace(cfg.GitLabToken) != ""
	githubReady := strings.TrimSpace(cfg.GitHubBaseURL) != "" && strings.TrimSpace(cfg.GitHubToken) != ""
	if !gitlabReady && !githubReady {
		return fmt.Errorf("worker: either GITLAB_BASE_URL/GITLAB_TOKEN or GITHUB_BASE_URL/GITHUB_TOKEN is required")
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

type gitHubStatusClientAdapter struct {
	client *githubplatform.Client
}

func (a gitHubStatusClientAdapter) SetCommitStatus(ctx context.Context, req gate.GitHubCommitStatusRequest) error {
	if a.client == nil {
		return fmt.Errorf("worker: github status client is required")
	}
	return a.client.SetCommitStatus(ctx, githubplatform.CommitStatusRequest{
		Owner:       req.Owner,
		Repo:        req.Repo,
		SHA:         req.SHA,
		State:       req.State,
		Context:     req.Context,
		Description: req.Description,
		TargetURL:   req.TargetURL,
	})
}
