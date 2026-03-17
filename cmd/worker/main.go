package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/database"
	"github.com/mreviewer/mreviewer/internal/logging"
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

	db, err := database.Open(cfg.MySQLDSN)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		return 1
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	worker := scheduler.NewService(logger, db, scheduler.NewNoopProcessor(logger))
	logger.Info("worker starting")
	if err := worker.Run(ctx); err != nil {
		logger.Error("worker stopped with error", "error", err)
		return 1
	}

	logger.Info("worker shutdown complete")
	return 0
}
