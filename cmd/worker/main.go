package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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
	rulesLoader := rules.NewLoader(gitlabClient, rules.PlatformDefaults{
		Instructions:        "Platform defaults: prioritize correctness, security, and least-privilege behavior.",
		ConfidenceThreshold: 0.72,
		SeverityThreshold:   "medium",
		IncludePaths:        []string{"src/**"},
		ExcludePaths:        []string{"vendor/**"},
		GateMode:            "threads_resolved",
		ProviderRoute:       "default",
	})
	provider, err := llm.NewMiniMaxProvider(llm.ProviderConfig{
		BaseURL:   cfg.AnthropicBaseURL,
		APIKey:    cfg.AnthropicAPIKey,
		Model:     cfg.AnthropicModel,
		MaxTokens: 4096,
	})
	if err != nil {
		logger.Error("failed to configure llm provider", "error", err)
		return 1
	}
	processor := llm.NewProcessor(logger, db, gitlabClient, rulesLoader, provider, llm.NewDBAuditLogger(db))
	runtimeDeps := newRuntimeDeps(logger, db, processor)
	worker := runtimeDeps.Scheduler
	logger.Info("worker starting")
	if err := worker.Run(ctx); err != nil {
		logger.Error("worker stopped with error", "error", err)
		return 1
	}

	logger.Info("worker shutdown complete")
	return 0
}
