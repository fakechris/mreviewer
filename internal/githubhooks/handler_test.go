package githubhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/reviewrun"
)

const (
	migrationsDir      = "../../migrations"
	testWebhookSecret  = "github-webhook-test-secret"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)
	return sqlDB
}

func githubWebhookPayload(action string, merged bool, headSHA string) string {
	mergedJSON := "false"
	if merged {
		mergedJSON = "true"
	}
	return `{
		"action": "` + action + `",
		"repository": {
			"id": 987,
			"full_name": "acme/service",
			"html_url": "https://github.com/acme/service"
		},
		"pull_request": {
			"number": 24,
			"title": "Improve auth checks",
			"body": "body",
			"html_url": "https://github.com/acme/service/pull/24",
			"merged": ` + mergedJSON + `,
			"state": "open",
			"user": {"login": "octocat"},
			"head": {"ref": "feature/auth", "sha": "` + headSHA + `"},
			"base": {"ref": "main", "sha": "base-sha"}
		}
	}`
}

func githubSignature(body string) string {
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postGitHubWebhook(handler http.Handler, body string, delivery string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-Hub-Signature-256", githubSignature(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestGitHubWebhookOpenedCreatesPendingRun(t *testing.T) {
	sqlDB := setupTestDB(t)
	handler := NewHandler(testLogger(), sqlDB, testWebhookSecret, reviewrun.NewService(testLogger(), sqlDB))

	rec := postGitHubWebhook(handler, githubWebhookPayload("opened", false, "head-sha-open"), "gh-open-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	queries := db.New(sqlDB)
	event, err := queries.GetHookEventByDeliveryKey(context.Background(), "gh-open-1")
	if err != nil {
		t.Fatalf("GetHookEventByDeliveryKey: %v", err)
	}
	if event.HookSource != "github" {
		t.Fatalf("hook source = %q, want github", event.HookSource)
	}
	if event.Action != "open" {
		t.Fatalf("action = %q, want open", event.Action)
	}

	instance, err := queries.GetGitlabInstanceByURL(context.Background(), "https://github.com")
	if err != nil {
		t.Fatalf("GetGitlabInstanceByURL: %v", err)
	}
	project, err := queries.GetProjectByGitlabID(context.Background(), db.GetProjectByGitlabIDParams{
		GitlabInstanceID: instance.ID,
		GitlabProjectID:  987,
	})
	if err != nil {
		t.Fatalf("GetProjectByGitlabID: %v", err)
	}
	if project.PathWithNamespace != "acme/service" {
		t.Fatalf("project path = %q, want acme/service", project.PathWithNamespace)
	}
	mr, err := queries.GetMergeRequestByProjectMR(context.Background(), db.GetMergeRequestByProjectMRParams{
		ProjectID: project.ID,
		MrIid:     24,
	})
	if err != nil {
		t.Fatalf("GetMergeRequestByProjectMR: %v", err)
	}
	runs, err := queries.ListReviewRunsByMR(context.Background(), mr.ID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("review run count = %d, want 1", len(runs))
	}
	if runs[0].Status != "pending" {
		t.Fatalf("run status = %q, want pending", runs[0].Status)
	}
	if runs[0].HeadSha != "head-sha-open" {
		t.Fatalf("run head sha = %q, want head-sha-open", runs[0].HeadSha)
	}
}

func TestGitHubWebhookMergedCancelsActiveRuns(t *testing.T) {
	sqlDB := setupTestDB(t)
	handler := NewHandler(testLogger(), sqlDB, testWebhookSecret, reviewrun.NewService(testLogger(), sqlDB))

	openPayload := githubWebhookPayload("opened", false, "head-sha-open")
	if rec := postGitHubWebhook(handler, openPayload, "gh-open-2"); rec.Code != http.StatusOK {
		t.Fatalf("open status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	mergedPayload := githubWebhookPayload("closed", true, "head-sha-merged")
	rec := postGitHubWebhook(handler, mergedPayload, "gh-merged-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("merged status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	queries := db.New(sqlDB)
	instance, err := queries.GetGitlabInstanceByURL(context.Background(), "https://github.com")
	if err != nil {
		t.Fatalf("GetGitlabInstanceByURL: %v", err)
	}
	project, err := queries.GetProjectByGitlabID(context.Background(), db.GetProjectByGitlabIDParams{
		GitlabInstanceID: instance.ID,
		GitlabProjectID:  987,
	})
	if err != nil {
		t.Fatalf("GetProjectByGitlabID: %v", err)
	}
	mr, err := queries.GetMergeRequestByProjectMR(context.Background(), db.GetMergeRequestByProjectMRParams{
		ProjectID: project.ID,
		MrIid:     24,
	})
	if err != nil {
		t.Fatalf("GetMergeRequestByProjectMR: %v", err)
	}
	if mr.State != "merged" {
		t.Fatalf("merge request state = %q, want merged", mr.State)
	}
	runs, err := queries.ListReviewRunsByMR(context.Background(), mr.ID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("review run count = %d, want 1", len(runs))
	}
	if runs[0].Status != "cancelled" {
		t.Fatalf("run status = %q, want cancelled", runs[0].Status)
	}
}
