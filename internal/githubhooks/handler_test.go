package githubhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/runs"
)

const githubMigrationsDir = "../../migrations"
const githubWebhookSecret = "github-webhook-secret" //nolint:gosec

func githubTestLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func githubSetupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, githubMigrationsDir)
	return sqlDB
}

func newGitHubTestHandler(sqlDB *sql.DB) *Handler {
	return NewHandler(githubTestLogger(), sqlDB, githubWebhookSecret, runs.NewService(githubTestLogger(), sqlDB))
}

func githubPullRequestPayload(action string, merged bool, headSHA string) string {
	return fmt.Sprintf(`{
		"action": %q,
		"number": 17,
		"repository": {
			"id": 101,
			"full_name": "acme/repo",
			"html_url": "https://github.com/acme/repo"
		},
		"pull_request": {
			"id": 501,
			"number": 17,
			"title": "Add GitHub webhook review",
			"body": "body",
			"state": "open",
			"draft": false,
			"html_url": "https://github.com/acme/repo/pull/17",
			"merged": %t,
			"user": {"login": "octocat"},
			"base": {"ref": "main", "sha": "base-sha", "repo": {"html_url": "https://github.com/acme/repo"}},
			"head": {"ref": "feature", "sha": %q, "repo": {"html_url": "https://github.com/acme/repo"}}
		}
	}`, action, merged, headSHA)
}

func githubSignature(body string) string {
	mac := hmac.New(sha256.New, []byte(githubWebhookSecret))
	_, _ = mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postGitHubWebhook(handler http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestGitHubWebhookCreatesPendingRun(t *testing.T) {
	sqlDB := githubSetupTestDB(t)
	handler := newGitHubTestHandler(sqlDB)
	payload := githubPullRequestPayload("opened", false, "github-head-sha")
	rec := postGitHubWebhook(handler, payload, map[string]string{
		"X-GitHub-Event":     "pull_request",
		"X-GitHub-Delivery":  "delivery-1",
		"X-Hub-Signature-256": githubSignature(payload),
		"Content-Type":       "application/json",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	store := db.New(sqlDB)
	ctx := context.Background()
	hookEvent, err := store.GetHookEventByDeliveryKey(ctx, "delivery-1")
	if err != nil {
		t.Fatalf("GetHookEventByDeliveryKey: %v", err)
	}
	if hookEvent.EventType != "pull_request" {
		t.Fatalf("event_type = %q, want pull_request", hookEvent.EventType)
	}

	instance, err := store.GetGitlabInstanceByURL(ctx, "https://github.com")
	if err != nil {
		t.Fatalf("GetGitlabInstanceByURL: %v", err)
	}
	project, err := store.GetProjectByGitlabID(ctx, db.GetProjectByGitlabIDParams{
		GitlabInstanceID: instance.ID,
		GitlabProjectID:  101,
	})
	if err != nil {
		t.Fatalf("GetProjectByGitlabID: %v", err)
	}
	if project.PathWithNamespace != "acme/repo" {
		t.Fatalf("project path = %q, want acme/repo", project.PathWithNamespace)
	}
	mr, err := store.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{
		ProjectID: project.ID,
		MrIid:     17,
	})
	if err != nil {
		t.Fatalf("GetMergeRequestByProjectMR: %v", err)
	}
	runs, err := store.ListReviewRunsByMR(ctx, mr.ID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("review runs = %d, want 1", len(runs))
	}
	var scope map[string]any
	if err := json.Unmarshal(runs[0].ScopeJson, &scope); err != nil {
		t.Fatalf("Unmarshal(scope_json): %v", err)
	}
	if got := scope["platform"]; got != "github" {
		t.Fatalf("scope_json platform = %#v, want github", got)
	}
}

func TestGitHubWebhookRejectsInvalidSignature(t *testing.T) {
	sqlDB := githubSetupTestDB(t)
	handler := newGitHubTestHandler(sqlDB)
	payload := githubPullRequestPayload("opened", false, "github-head-sha")
	rec := postGitHubWebhook(handler, payload, map[string]string{
		"X-GitHub-Event":     "pull_request",
		"X-GitHub-Delivery":  "delivery-invalid",
		"X-Hub-Signature-256": "sha256=deadbeef",
		"Content-Type":       "application/json",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
