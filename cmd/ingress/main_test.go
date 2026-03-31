package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/database"
)

func TestNewMuxRegistersGitHubWebhookRoute(t *testing.T) {
	sqlDB, dialect, err := database.OpenWithDialect("sqlite://file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("OpenWithDialect: %v", err)
	}
	defer sqlDB.Close()

	handler, err := newMux(slog.New(slog.NewJSONHandler(io.Discard, nil)), &config.Config{
		GitLabWebhookSecret: "gitlab-secret",
		GitHubWebhookSecret: "github-secret",
	}, sqlDB, dialect)
	if err != nil {
		t.Fatalf("newMux: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/github/webhook", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("POST /github/webhook status = %d, want non-404", rec.Code)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil).WithContext(context.Background())
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)
	if healthRec.Code == http.StatusNotFound {
		t.Fatalf("GET /health status = %d, want non-404", healthRec.Code)
	}
}
