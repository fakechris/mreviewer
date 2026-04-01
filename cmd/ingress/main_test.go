package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/logging"
)

func TestNewMuxRegistersGitLabAndGitHubWebhookRoutes(t *testing.T) {
	logger := logging.NewLogger(0)
	cfg := &config.Config{
		GitLabWebhookSecret: "gitlab-secret",
		GitHubWebhookSecret: "github-secret",
	}

	mux := newMux(logger, nil, cfg)

	for _, tc := range []struct {
		path string
	}{
		{path: "/webhook"},
		{path: "/github/webhook"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound {
			t.Fatalf("%s returned 404, want registered route", tc.path)
		}
	}
}
