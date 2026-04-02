package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/database"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/llm"
	"github.com/mreviewer/mreviewer/internal/logging"
	platformgithub "github.com/mreviewer/mreviewer/internal/platform/github"
	"github.com/mreviewer/mreviewer/internal/reviewrun"
	"github.com/mreviewer/mreviewer/internal/rules"
)

const defaultHeartbeatInterval = 15 * time.Second

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

	sqlDB, dialect, err := database.OpenWithDialect(cfg.DSN())
	if err != nil {
		logger.Error("failed to open database", "error", err)
		return 1
	}
	defer sqlDB.Close()
	logger.Info("database connected", "dialect", dialect)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	var gitlabClient *gitlab.Client
	if strings.TrimSpace(cfg.GitLabToken) != "" {
		gitlabClient, err = gitlab.NewClient(cfg.GitLabBaseURL, cfg.GitLabToken)
		if err != nil {
			logger.Error("failed to configure gitlab client", "error", err)
			return 1
		}
	}

	defaultRoute, configuredFallbackRoutes, providerConfigs, err := providerConfigsFromConfig(cfg)
	if err != nil {
		logger.Error("failed to resolve llm route configuration", "error", err)
		return 1
	}

	repositoryRulesClient := newWorkerRepositoryRulesClient(gitlabClient)
	var githubClient *platformgithub.Client
	if strings.TrimSpace(cfg.GitHubToken) != "" {
		githubClient, err = platformgithub.NewClient(cfg.GitHubBaseURL, cfg.GitHubToken)
		if err != nil {
			logger.Error("failed to configure github client", "error", err)
			return 1
		}
		repositoryRulesClient.github = githubClient
	}

	rulesLoader := rules.NewLoader(repositoryRulesClient, rules.PlatformDefaults{
		Instructions:        "Platform defaults: prioritize correctness, security, and least-privilege behavior.",
		ConfidenceThreshold: 0.72,
		SeverityThreshold:   "medium",
		IncludePaths:        []string{"src/**"},
		ExcludePaths:        []string{"vendor/**"},
		GateMode:            "threads_resolved",
		ProviderRoute:       defaultRoute,
	})
	if gitlabClient != nil {
		gitLabLimiter := gitlab.NewInMemoryRateLimiter(gitlab.RateLimitConfig{Requests: 5, Window: time.Second}, time.Now, nil)
		gitLabLimiter.SetLimit("global", gitlab.RateLimitConfig{Requests: 5, Window: time.Second})
		gitlabClient, err = gitlab.NewClient(cfg.GitLabBaseURL, cfg.GitLabToken, gitlab.WithRateLimiter(gitLabLimiter))
		if err != nil {
			logger.Error("failed to configure gitlab client with rate limiting", "error", err)
			return 1
		}
		repositoryRulesClient.gitlab = gitlabClient
	}
	llmLimiter := llm.NewInMemoryRateLimiter(llm.RateLimitConfig{Requests: 2, Window: time.Second}, time.Now, nil)
	for route, providerCfg := range providerConfigs {
		llmLimiter.SetLimit(route, llm.RateLimitConfig{Requests: 2, Window: time.Second})
		providerCfg.RateLimiter = llmLimiter
		providerConfigs[route] = providerCfg
	}
	providerRegistry, err := llm.BuildProviderRegistryFromRouteConfigs(logger, defaultRoute, configuredFallbackRoutes, providerConfigs)
	if err != nil {
		logger.Error("failed to configure llm provider registry", "error", err)
		return 1
	}
	if cfg.RedisAddr == "" {
		logger.Warn("redis unavailable; optional coordination disabled", "mode", "degraded_fallback")
	} else {
		logger.Info("redis coordination configured", "redis_addr", cfg.RedisAddr)
	}
	newStore := database.StoreFactory(dialect)
	processor, err := newReviewRunProcessor(cfg, sqlDB, gitlabClient, githubClient, rulesLoader, providerRegistry)
	if err != nil {
		logger.Error("failed to configure review run processor", "error", err)
		return 1
	}
	runService := reviewrun.NewService(nil, processor)
	statusPublisher := newWorkerStatusPublisher(gitlabClient, githubClient, newStore(sqlDB))
	runtimeDeps := newRuntimeDepsWithPlatformWritebacksAndGatePublishers(logger, sqlDB, runService, gitlabClient, githubClient, statusPublisher, gate.NoopCIGatePublisher{}, newStore)
	worker := runtimeDeps.Scheduler
	if runtimeDeps.Heartbeat != nil {
		go func() {
			if err := runtimeDeps.Heartbeat.Run(ctx, defaultHeartbeatInterval, runtimeDeps.HeartbeatIdentity); shouldLogHeartbeatStop(err) {
				logger.Error("worker heartbeat stopped", "error", err)
			}
		}()
	}
	logger.Info("worker starting", "platform_default_route", defaultRoute, "fallback_routes", configuredFallbackRoutes, "registry_routes", providerRegistry.Routes())
	if err := worker.Run(ctx); err != nil {
		logger.Error("worker stopped with error", "error", err)
		return 1
	}

	logger.Info("worker shutdown complete")
	return 0
}

func shouldLogHeartbeatStop(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled)
}

func validateWorkerConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("worker: configuration is required")
	}
	if strings.TrimSpace(cfg.GitLabToken) == "" && strings.TrimSpace(cfg.GitHubToken) == "" {
		return fmt.Errorf("worker: at least one of GITLAB_TOKEN or GITHUB_TOKEN is required")
	}
	return nil
}

type workerRepositoryRulesClient struct {
	gitlab *gitlab.Client
	github *platformgithub.Client
}

func newWorkerRepositoryRulesClient(gitlabClient *gitlab.Client) *workerRepositoryRulesClient {
	return &workerRepositoryRulesClient{gitlab: gitlabClient}
}

func (c *workerRepositoryRulesClient) GetRepositoryFile(ctx context.Context, projectID int64, filePath, ref string) (string, error) {
	if c == nil || c.gitlab == nil {
		return "", rules.ErrNoRepositoryReader
	}
	return c.gitlab.GetRepositoryFile(ctx, projectID, filePath, ref)
}

func (c *workerRepositoryRulesClient) GetRepositoryFileByRepositoryRef(ctx context.Context, repositoryRef, filePath, ref string) (string, error) {
	if c == nil || c.github == nil {
		return "", rules.ErrNoRepositoryReader
	}
	return c.github.GetRepositoryFileByRepositoryRef(ctx, repositoryRef, filePath, ref)
}

type workerStatusPublisher struct {
	publishers []gate.StatusPublisher
}

func newWorkerStatusPublisher(gitlabClient *gitlab.Client, githubClient *platformgithub.Client, store gate.StatusStore) gate.StatusPublisher {
	publisher := &workerStatusPublisher{}
	if gitlabClient != nil {
		publisher.publishers = append(publisher.publishers, gate.NewGitLabStatusPublisher(gitlabClient, store))
	}
	if githubClient != nil {
		publisher.publishers = append(publisher.publishers, gate.NewGitHubStatusPublisher(githubStatusClient{client: githubClient}, store))
	}
	if len(publisher.publishers) == 0 {
		return gate.NoopStatusPublisher{}
	}
	return publisher
}

func (p *workerStatusPublisher) PublishStatus(ctx context.Context, result gate.Result) error {
	if p == nil {
		return nil
	}
	var errs []error
	for _, publisher := range p.publishers {
		if publisher == nil {
			continue
		}
		if err := publisher.PublishStatus(ctx, result); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type githubStatusClient struct {
	client *platformgithub.Client
}

func (c githubStatusClient) SetCommitStatus(ctx context.Context, req gate.GitHubCommitStatusRequest) error {
	if c.client == nil {
		return nil
	}
	return c.client.SetCommitStatus(ctx, platformgithub.CommitStatusRequest{
		Repository:  req.Repository,
		SHA:         req.SHA,
		State:       req.State,
		Context:     req.Context,
		Description: req.Description,
		TargetURL:   req.TargetURL,
	})
}

func providerConfigsFromConfig(cfg *config.Config) (string, []string, map[string]llm.ProviderConfig, error) {
	if cfg == nil {
		return "", nil, nil, fmt.Errorf("worker: configuration is required")
	}
	return config.ResolveReviewCatalog(cfg)
}
