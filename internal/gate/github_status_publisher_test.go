package gate_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/reviewstatus"
)

const githubMigrationsDir = "../../migrations"

func gateTestLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func setupGateDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, githubMigrationsDir)
	return sqlDB
}

type fakeGitHubStatusClient struct {
	requests []gate.GitHubCommitStatusRequest
}

func (f *fakeGitHubStatusClient) SetCommitStatus(_ context.Context, req gate.GitHubCommitStatusRequest) error {
	f.requests = append(f.requests, req)
	return nil
}

func TestGitHubStatusPublisherPublishesCommitStatus(t *testing.T) {
	sqlDB := setupGateDB(t)
	res, err := sqlDB.Exec("INSERT INTO gitlab_instances (url, name) VALUES ('https://github.com', 'GitHub')")
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	instanceID, _ := res.LastInsertId()
	res, err = sqlDB.Exec(`INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled)
		VALUES (?, ?, ?, TRUE)`, instanceID, 987, "acme/service")
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	projectID, _ := res.LastInsertId()
	res, err = sqlDB.Exec(`INSERT INTO merge_requests (project_id, mr_iid, title, state, target_branch, source_branch, head_sha, web_url)
		VALUES (?, ?, ?, 'opened', 'main', 'feature/auth', ?, ?)`, projectID, 24, "Improve auth checks", "head-sha", "https://github.com/acme/service/pull/24")
	if err != nil {
		t.Fatalf("insert merge request: %v", err)
	}
	mergeRequestID, _ := res.LastInsertId()

	client := &fakeGitHubStatusClient{}
	publisher := gate.NewGitHubStatusPublisher(client, db.New(sqlDB))
	err = publisher.PublishStatus(context.Background(), gate.Result{
		RunID:          1,
		ProjectID:      projectID,
		MergeRequestID: mergeRequestID,
		HeadSHA:        "head-sha",
		State:          "running",
		Stage:          reviewstatus.StageRunningPacks,
	})
	if err != nil {
		t.Fatalf("PublishStatus: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(client.requests))
	}
	req := client.requests[0]
	if req.Owner != "acme" || req.Repo != "service" {
		t.Fatalf("owner/repo = %s/%s, want acme/service", req.Owner, req.Repo)
	}
	if req.State != "pending" {
		t.Fatalf("state = %q, want pending", req.State)
	}
	if req.Description != "AI review is running specialist reviewers" {
		t.Fatalf("description = %q, want running packs description", req.Description)
	}
	if req.TargetURL != "https://github.com/acme/service/pull/24" {
		t.Fatalf("target url = %q, want PR URL", req.TargetURL)
	}
}
