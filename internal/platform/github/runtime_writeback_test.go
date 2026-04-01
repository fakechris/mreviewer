package github

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

const runtimeMigrationsDir = "../../../migrations"

func runtimeTestLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func setupRuntimeDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, runtimeMigrationsDir)
	return sqlDB
}

type fakePublishClient struct {
	issueComments  []IssueCommentRequest
	reviewComments []ReviewCommentRequest
}

func (f *fakePublishClient) CreateIssueComment(_ context.Context, req IssueCommentRequest) error {
	f.issueComments = append(f.issueComments, req)
	return nil
}

func (f *fakePublishClient) CreateReviewComment(_ context.Context, req ReviewCommentRequest) error {
	f.reviewComments = append(f.reviewComments, req)
	return nil
}

func TestRuntimeWritebackPublishesBundleForGitHubPullRequest(t *testing.T) {
	sqlDB := setupRuntimeDB(t)
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

	client := &fakePublishClient{}
	writeback := NewRuntimeWriteback(sqlDB, client)
	err = writeback.WriteBundle(context.Background(), db.ReviewRun{
		ID:             1,
		ProjectID:      projectID,
		MergeRequestID: mergeRequestID,
	}, reviewcore.ReviewBundle{
		Target: reviewcore.ReviewTarget{Platform: reviewcore.PlatformGitHub, Repository: "acme/service", Number: 24},
		PublishCandidates: []reviewcore.PublishCandidate{
			{Type: "summary", Body: "summary"},
			{Type: "finding", Body: "finding body", Location: &reviewcore.CanonicalLocation{Path: "auth/check.go", Line: 41, Side: reviewcore.LocationSideNew}},
		},
	})
	if err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	if len(client.issueComments) != 1 {
		t.Fatalf("issue comment count = %d, want 1", len(client.issueComments))
	}
	if len(client.reviewComments) != 1 {
		t.Fatalf("review comment count = %d, want 1", len(client.reviewComments))
	}
	if client.issueComments[0].Owner != "acme" || client.issueComments[0].Repo != "service" {
		t.Fatalf("owner/repo = %s/%s, want acme/service", client.issueComments[0].Owner, client.issueComments[0].Repo)
	}
	if client.reviewComments[0].Number != 24 {
		t.Fatalf("PR number = %d, want 24", client.reviewComments[0].Number)
	}
}
