package gitlab

import (
	"context"
	"database/sql"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/reviewcomment"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	legacywriter "github.com/mreviewer/mreviewer/internal/writer"
)

const runtimeWritebackMigrationsDir = "../../../migrations"

type fakeRuntimeDiscussionClient struct {
	discussions []reviewcomment.CreateDiscussionRequest
	notes       []reviewcomment.CreateNoteRequest
	resolves    []reviewcomment.ResolveDiscussionRequest
}

func (f *fakeRuntimeDiscussionClient) CreateDiscussion(_ context.Context, req reviewcomment.CreateDiscussionRequest) (reviewcomment.Discussion, error) {
	f.discussions = append(f.discussions, req)
	return reviewcomment.Discussion{ID: "discussion"}, nil
}

func (f *fakeRuntimeDiscussionClient) CreateNote(_ context.Context, req reviewcomment.CreateNoteRequest) (reviewcomment.Discussion, error) {
	f.notes = append(f.notes, req)
	return reviewcomment.Discussion{ID: "note"}, nil
}

func (f *fakeRuntimeDiscussionClient) ResolveDiscussion(_ context.Context, req reviewcomment.ResolveDiscussionRequest) error {
	f.resolves = append(f.resolves, req)
	return nil
}

func TestRuntimeWritebackWriteBundlePublishesCanonicalBundle(t *testing.T) {
	client := &fakeRuntimeDiscussionClient{}
	writeback := NewRuntimeWriteback(client, nil)
	bundle := core.ReviewBundle{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitLab,
			URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
			Repository:   "group/repo",
			ProjectID:    77,
			ChangeNumber: 23,
		},
		PublishCandidates: []core.PublishCandidate{
			{Kind: "summary", Body: "judge summary"},
			{
				Kind:     "finding",
				Title:    "Unsafe query",
				Body:     "User input flows into SQL.",
				Severity: "high",
				Location: core.CanonicalLocation{
					Path:      "internal/db/query.go",
					Side:      core.DiffSideNew,
					StartLine: 44,
					EndLine:   44,
				},
			},
		},
	}

	if err := writeback.WriteBundle(context.Background(), db.ReviewRun{ID: 55, Status: "completed"}, bundle); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	if len(client.notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(client.notes))
	}
	if len(client.discussions) != 1 {
		t.Fatalf("discussions = %d, want 1", len(client.discussions))
	}
}

func TestRuntimeWritebackWriteBundleResolvesSupersededPersistedDiscussion(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, runtimeWritebackMigrationsDir)

	instanceID := insertRuntimeWritebackInstance(t, sqlDB)
	projectID := insertRuntimeWritebackProject(t, sqlDB, instanceID)
	mrID := insertRuntimeWritebackMR(t, sqlDB, projectID, 7, "sha-new")
	currentRunID := insertRuntimeWritebackRun(t, sqlDB, projectID, mrID, "requested_changes", "runtime-supersede", "sha-new")
	previousRunID := insertRuntimeWritebackRun(t, sqlDB, projectID, mrID, "completed", "runtime-supersede-old", "sha-old")
	insertRuntimeWritebackVersion(t, sqlDB, mrID, "base", "start", "sha-new", "patch")

	oldFindingID := insertRuntimeWritebackFinding(t, sqlDB, previousRunID, mrID, "superseded", sql.NullInt64{})
	newFindingID := insertRuntimeWritebackFinding(t, sqlDB, currentRunID, mrID, "active", sql.NullInt64{})
	if _, err := sqlDB.Exec(`UPDATE review_findings SET matched_finding_id = ? WHERE id = ?`, newFindingID, oldFindingID); err != nil {
		t.Fatalf("update matched finding: %v", err)
	}
	oldDiscussionID := insertRuntimeWritebackDiscussion(t, sqlDB, oldFindingID, mrID, "disc-old")

	client := &fakeRuntimeDiscussionClient{}
	writeback := NewRuntimeWriteback(client, legacywriter.NewSQLStore(sqlDB))

	if err := writeback.WriteBundle(ctx, db.ReviewRun{ID: currentRunID, MergeRequestID: mrID, Status: "requested_changes"}, core.ReviewBundle{}); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	if len(client.discussions) != 1 {
		t.Fatalf("discussions = %d, want 1", len(client.discussions))
	}
	if len(client.notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(client.notes))
	}
	if len(client.resolves) != 1 {
		t.Fatalf("resolve requests = %d, want 1", len(client.resolves))
	}
	queries := db.New(sqlDB)
	actions, err := queries.ListCommentActionsByRun(ctx, currentRunID)
	if err != nil {
		t.Fatalf("ListCommentActionsByRun: %v", err)
	}
	if len(actions) != 3 {
		t.Fatalf("comment action count = %d, want 3", len(actions))
	}
	discussion, err := queries.GetGitlabDiscussion(ctx, oldDiscussionID)
	if err != nil {
		t.Fatalf("GetGitlabDiscussion: %v", err)
	}
	if !discussion.Resolved {
		t.Fatalf("discussion resolved = %v, want true", discussion.Resolved)
	}
	if !discussion.SupersededByDiscussionID.Valid {
		t.Fatalf("superseded_by_discussion_id = %+v, want valid", discussion.SupersededByDiscussionID)
	}
}

func insertRuntimeWritebackInstance(t *testing.T, sqlDB *sql.DB) int64 {
	t.Helper()
	res, err := sqlDB.Exec(`INSERT INTO gitlab_instances (url, name) VALUES ('https://gitlab.example.com', 'test')`)
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("instance last insert id: %v", err)
	}
	return id
}

func insertRuntimeWritebackProject(t *testing.T, sqlDB *sql.DB, instanceID int64) int64 {
	t.Helper()
	res, err := sqlDB.Exec(`INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled)
		VALUES (?, ?, ?, TRUE)`, instanceID, 77, "group/repo")
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project last insert id: %v", err)
	}
	return id
}

func insertRuntimeWritebackMR(t *testing.T, sqlDB *sql.DB, projectID, iid int64, headSHA string) int64 {
	t.Helper()
	res, err := sqlDB.Exec(`INSERT INTO merge_requests (project_id, mr_iid, title, state, target_branch, source_branch, head_sha)
		VALUES (?, ?, ?, 'opened', 'main', 'feature', ?)`, projectID, iid, "Runtime writeback MR", headSHA)
	if err != nil {
		t.Fatalf("insert merge request: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("merge request last insert id: %v", err)
	}
	return id
}

func insertRuntimeWritebackRun(t *testing.T, sqlDB *sql.DB, projectID, mrID int64, status, key, headSHA string) int64 {
	t.Helper()
	res, err := sqlDB.Exec(`INSERT INTO review_runs (project_id, merge_request_id, status, trigger_type, idempotency_key, head_sha, max_retries)
		VALUES (?, ?, ?, 'manual', ?, ?, 3)`, projectID, mrID, status, key, headSHA)
	if err != nil {
		t.Fatalf("insert review run: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("review run last insert id: %v", err)
	}
	return id
}

func insertRuntimeWritebackVersion(t *testing.T, sqlDB *sql.DB, mrID int64, baseSHA, startSHA, headSHA, patchSHA string) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO mr_versions (merge_request_id, gitlab_version_id, base_sha, start_sha, head_sha, patch_id_sha)
		VALUES (?, ?, ?, ?, ?, ?)`, mrID, 1, baseSHA, startSHA, headSHA, patchSHA); err != nil {
		t.Fatalf("insert mr version: %v", err)
	}
}

func insertRuntimeWritebackFinding(t *testing.T, sqlDB *sql.DB, runID, mrID int64, state string, matchedFindingID sql.NullInt64) int64 {
	t.Helper()
	res, err := sqlDB.Exec(`INSERT INTO review_findings (review_run_id, merge_request_id, category, severity, confidence, title, body_markdown, path, anchor_kind, new_line, anchor_snippet, anchor_fingerprint, semantic_fingerprint, matched_finding_id, state)
		VALUES (?, ?, 'bug', 'high', 0.95, 'Runtime writeback issue', 'body', 'src/main.go', 'new_line', 42, 'snippet', ?, ?, ?, ?)`,
		runID, mrID, "anchor-runtime", "semantic-runtime", matchedFindingID, state)
	if err != nil {
		t.Fatalf("insert finding: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("finding last insert id: %v", err)
	}
	return id
}

func insertRuntimeWritebackDiscussion(t *testing.T, sqlDB *sql.DB, findingID, mrID int64, gitlabDiscussionID string) int64 {
	t.Helper()
	res, err := sqlDB.Exec(`INSERT INTO gitlab_discussions (review_finding_id, merge_request_id, gitlab_discussion_id, discussion_type, resolved)
		VALUES (?, ?, ?, 'diff', FALSE)`, findingID, mrID, gitlabDiscussionID)
	if err != nil {
		t.Fatalf("insert gitlab discussion: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("discussion last insert id: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE review_findings SET gitlab_discussion_id = ? WHERE id = ?`, gitlabDiscussionID, findingID); err != nil {
		t.Fatalf("update finding discussion id: %v", err)
	}
	return id
}
