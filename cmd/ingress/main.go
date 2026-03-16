// Command ingress is the HTTP API entry point for the MR review system.
// It loads configuration, connects to MySQL, sets up structured logging,
// exposes a health endpoint on the configured port, and shuts down
// gracefully on SIGTERM/SIGINT.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/database"
	"github.com/mreviewer/mreviewer/internal/hooks"
	apphttp "github.com/mreviewer/mreviewer/internal/http"
	"github.com/mreviewer/mreviewer/internal/logging"
	"github.com/mreviewer/mreviewer/internal/server"
)

func main() {
	os.Exit(run())
}

func run() int {
	// Structured JSON logger.
	logger := logging.NewLogger(slog.LevelInfo)

	// Load configuration.
	cfg, err := config.Load("config.yaml")
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		return 1
	}

	logger.Info("configuration loaded",
		"app_env", cfg.AppEnv,
		"port", cfg.Port,
	)

	// Open MySQL connection.
	db, err := database.Open(cfg.MySQLDSN)
	if err != nil {
		logger.Error("failed to open database", "error", err)
		return 1
	}
	defer db.Close()

	logger.Info("database connection pool initialized")

	// Build HTTP routes.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", apphttp.NewHealthHandler(logger, db))

	// Webhook ingress handler.
	webhookHandler := hooks.NewHandler(logger, db, cfg.GitLabWebhookSecret)
	mux.Handle("POST /webhook", webhookHandler)

	// Wrap with request-id middleware.
	handler := apphttp.RequestIDMiddleware(logger, mux)

	// Prepare signal-based context for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Start server.
	srv := server.New(cfg.Port, handler, logger)
	if err := srv.Start(ctx); err != nil {
		logger.Error("server error", "error", err)
		return 1
	}

	logger.Info("shutdown complete")
	return 0
}
