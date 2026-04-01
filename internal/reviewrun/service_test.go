package reviewrun

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/hooks"
)

const migrationsDir = "../../migrations"

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)
	return sqlDB
}

func TestServiceCreatesPendingRunFromNormalizedEvent(t *testing.T) {
	sqlDB := setupTestDB(t)
	svc := NewService(testLogger(), sqlDB)

	err := svc.ProcessEvent(context.Background(), hooks.NormalizedEvent{
		GitLabInstanceURL: "https://gitlab.example.com",
		ProjectID:         42,
		ProjectPath:       "test/repo",
		MRIID:             7,
		Action:            "open",
		HeadSHA:           "head-sha-abc123",
		TriggerType:       "mr_open",
		EventType:         "merge_request",
		IdempotencyKey:    "reviewrun-test-1",
		Title:             "Add feature X",
		SourceBranch:      "feature-x",
		TargetBranch:      "main",
		Author:            "testuser",
		WebURL:            "https://gitlab.example.com/test/repo/-/merge_requests/7",
		State:             "opened",
	}, 0)
	if err != nil {
		t.Fatalf("ProcessEvent: %v", err)
	}

	instance, err := db.New(sqlDB).GetGitlabInstanceByURL(context.Background(), "https://gitlab.example.com")
	if err != nil {
		t.Fatalf("GetGitlabInstanceByURL: %v", err)
	}
	project, err := db.New(sqlDB).GetProjectByGitlabID(context.Background(), db.GetProjectByGitlabIDParams{
		GitlabInstanceID: instance.ID,
		GitlabProjectID:  42,
	})
	if err != nil {
		t.Fatalf("GetProjectByGitlabID: %v", err)
	}
	mr, err := db.New(sqlDB).GetMergeRequestByProjectMR(context.Background(), db.GetMergeRequestByProjectMRParams{
		ProjectID: project.ID,
		MrIid:     7,
	})
	if err != nil {
		t.Fatalf("GetMergeRequestByProjectMR: %v", err)
	}
	run, err := db.New(sqlDB).GetReviewRunByIdempotencyKey(context.Background(), "reviewrun-test-1")
	if err != nil {
		t.Fatalf("GetReviewRunByIdempotencyKey: %v", err)
	}
	if run.MergeRequestID != mr.ID {
		t.Fatalf("run merge_request_id = %d, want %d", run.MergeRequestID, mr.ID)
	}
	if run.Status != "pending" {
		t.Fatalf("run status = %q, want pending", run.Status)
	}
}
