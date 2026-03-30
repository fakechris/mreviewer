package sqlitedb_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/sqlitedb"
	_ "modernc.org/sqlite"
)

const migrationsDir = "../../../migrations_sqlite"

// setupDB opens an in-memory SQLite database and applies migrations.
func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	// Enable foreign keys and WAL.
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := conn.Exec(pragma); err != nil {
			t.Fatalf("pragma %q: %v", pragma, err)
		}
	}

	// Read and execute migration SQL.
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(migrationsDir, e.Name()))
		if err != nil {
			t.Fatalf("read migration %s: %v", e.Name(), err)
		}
		// Only execute the Up portion (before +goose Down).
		sqlStr := string(data)
		if idx := indexOf(sqlStr, "-- +goose Down"); idx > 0 {
			sqlStr = sqlStr[:idx]
		}
		// Strip the +goose Up marker.
		sqlStr = stripPrefix(sqlStr, "-- +goose Up")
		if _, err := conn.Exec(sqlStr); err != nil {
			t.Fatalf("exec migration %s: %v", e.Name(), err)
		}
	}

	return conn
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func stripPrefix(s, prefix string) string {
	if idx := indexOf(s, prefix); idx >= 0 {
		return s[:idx] + s[idx+len(prefix):]
	}
	return s
}

func newQueries(t *testing.T) *sqlitedb.Queries {
	t.Helper()
	return sqlitedb.New(setupDB(t))
}

// --- Tests ---

func TestUpsertGitlabInstance(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()

	// Insert
	res, err := q.UpsertGitlabInstance(ctx, db.UpsertGitlabInstanceParams{Url: "https://gitlab.example.com", Name: "Example"})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// Upsert updates name
	_, err = q.UpsertGitlabInstance(ctx, db.UpsertGitlabInstanceParams{Url: "https://gitlab.example.com", Name: "Updated"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	inst, err := q.GetGitlabInstance(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if inst.Name != "Updated" {
		t.Fatalf("want name Updated, got %s", inst.Name)
	}

	// GetByURL
	inst2, err := q.GetGitlabInstanceByURL(ctx, "https://gitlab.example.com")
	if err != nil {
		t.Fatalf("get by url: %v", err)
	}
	if inst2.ID != id {
		t.Fatalf("want id %d, got %d", id, inst2.ID)
	}
}

func TestUpsertProject(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()

	// Prerequisite: instance
	res, err := q.InsertGitlabInstance(ctx, db.InsertGitlabInstanceParams{Url: "https://gl.test", Name: "test"})
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	instID, _ := res.LastInsertId()

	// Insert project
	res, err = q.UpsertProject(ctx, db.UpsertProjectParams{
		GitlabInstanceID: instID, GitlabProjectID: 42, PathWithNamespace: "org/repo", Enabled: true,
	})
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	projID, _ := res.LastInsertId()

	// Upsert updates path
	_, err = q.UpsertProject(ctx, db.UpsertProjectParams{
		GitlabInstanceID: instID, GitlabProjectID: 42, PathWithNamespace: "org/renamed-repo", Enabled: true,
	})
	if err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	proj, err := q.GetProject(ctx, projID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if proj.PathWithNamespace != "org/renamed-repo" {
		t.Fatalf("want renamed path, got %s", proj.PathWithNamespace)
	}
}

func TestUpsertMergeRequest(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	instID, projID := seedProject(t, q, ctx)
	_ = instID

	// Insert MR
	res, err := q.UpsertMergeRequest(ctx, db.UpsertMergeRequestParams{
		ProjectID: projID, MrIid: 1, Title: "Initial",
		SourceBranch: "feat", TargetBranch: "main",
		Author: "dev", State: "opened", IsDraft: false,
		HeadSha: "abc123", WebUrl: "https://gl.test/mr/1",
	})
	if err != nil {
		t.Fatalf("insert MR: %v", err)
	}
	mrID, _ := res.LastInsertId()

	// Upsert updates title
	_, err = q.UpsertMergeRequest(ctx, db.UpsertMergeRequestParams{
		ProjectID: projID, MrIid: 1, Title: "Updated Title",
		SourceBranch: "feat", TargetBranch: "main",
		Author: "dev", State: "opened", IsDraft: false,
		HeadSha: "def456", WebUrl: "https://gl.test/mr/1",
	})
	if err != nil {
		t.Fatalf("upsert MR: %v", err)
	}

	mr, err := q.GetMergeRequest(ctx, mrID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if mr.Title != "Updated Title" {
		t.Fatalf("want Updated Title, got %s", mr.Title)
	}
	if mr.HeadSha != "def456" {
		t.Fatalf("want def456, got %s", mr.HeadSha)
	}
}

func TestReviewRunLifecycle(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	_, projID := seedProject(t, q, ctx)
	mrID := seedMR(t, q, ctx, projID)

	// Insert a run
	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID: projID, MergeRequestID: mrID,
		TriggerType: "webhook", HeadSha: "abc",
		Status: "pending", MaxRetries: 3,
		IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	runID, _ := res.LastInsertId()

	// Claim it
	claimable, err := q.GetNextClaimableReviewRun(ctx)
	if err != nil {
		t.Fatalf("get claimable: %v", err)
	}
	if claimable.ID != runID {
		t.Fatalf("want run %d, got %d", runID, claimable.ID)
	}

	err = q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-1", ID: runID})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Complete it (conditional)
	ok, err := q.UpdateReviewRunCompletedIfRunning(ctx, db.UpdateReviewRunCompletedParams{
		ProviderLatencyMs: 500, ProviderTokensTotal: 1000, ID: runID,
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !ok {
		t.Fatal("expected rows affected")
	}

	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("want completed, got %s", run.Status)
	}

	// Completing again should return false (already completed, not running)
	ok, err = q.UpdateReviewRunCompletedIfRunning(ctx, db.UpdateReviewRunCompletedParams{
		ProviderLatencyMs: 100, ProviderTokensTotal: 200, ID: runID,
	})
	if err != nil {
		t.Fatalf("complete again: %v", err)
	}
	if ok {
		t.Fatal("expected no rows affected for already-completed run")
	}
}

func TestAdminDashboardQueriesUseRetryEligibilityAndExcludeSupersedes(t *testing.T) {
	sqlDB := setupDB(t)
	q := sqlitedb.New(sqlDB)
	ctx := context.Background()

	_, projID := seedProject(t, q, ctx)
	mrID := seedMR(t, q, ctx, projID)

	pendingRunID := seedRun(t, q, ctx, projID, mrID)
	retryRunID := seedRun(t, q, ctx, projID, mrID)
	supersededRunID := seedRun(t, q, ctx, projID, mrID)
	failedRunID := seedRun(t, q, ctx, projID, mrID)

	now := time.Now().UTC().Truncate(time.Second)
	pendingCreatedAt := now.Add(-10 * time.Minute)
	retryCreatedAt := now.Add(-24 * time.Hour)
	retryEligibleAt := now.Add(-20 * time.Minute)
	supersededUpdatedAt := now.Add(-5 * time.Minute)
	failedUpdatedAt := now.Add(-2 * time.Minute)

	if _, err := sqlDB.ExecContext(ctx, "UPDATE review_runs SET created_at = ?, updated_at = ? WHERE id = ?", pendingCreatedAt, pendingCreatedAt, pendingRunID); err != nil {
		t.Fatalf("update pending run timestamps: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, "UPDATE review_runs SET status = 'failed', error_code = 'provider_timeout', created_at = ?, updated_at = ?, next_retry_at = ? WHERE id = ?", retryCreatedAt, retryEligibleAt, retryEligibleAt, retryRunID); err != nil {
		t.Fatalf("update retry run timestamps: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, "UPDATE review_runs SET status = 'failed', error_code = 'superseded_by_new_head', updated_at = ?, superseded_by_run_id = ? WHERE id = ?", supersededUpdatedAt, pendingRunID, supersededRunID); err != nil {
		t.Fatalf("update superseded run: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, "UPDATE review_runs SET status = 'failed', error_code = 'provider_failed', updated_at = ? WHERE id = ?", failedUpdatedAt, failedRunID); err != nil {
		t.Fatalf("update failed run: %v", err)
	}

	oldestWaiting, err := q.GetOldestWaitingRunCreatedAt(ctx)
	if err != nil {
		t.Fatalf("GetOldestWaitingRunCreatedAt: %v", err)
	}
	gotOldestWaiting := mustNormalizeSQLiteDashboardTime(t, oldestWaiting)
	if !gotOldestWaiting.Equal(retryEligibleAt) {
		t.Fatalf("oldest waiting time = %s, want retry eligibility %s", gotOldestWaiting, retryEligibleAt)
	}

	supersededCount, err := q.CountSupersededRunsSince(ctx, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("CountSupersededRunsSince: %v", err)
	}
	if supersededCount != 1 {
		t.Fatalf("superseded count = %d, want 1", supersededCount)
	}

	recentFailures, err := q.ListRecentFailedRuns(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentFailedRuns: %v", err)
	}
	if len(recentFailures) != 2 {
		t.Fatalf("recent failures len = %d, want 2", len(recentFailures))
	}
	for _, failure := range recentFailures {
		if failure.ErrorCode == "superseded_by_new_head" {
			t.Fatalf("recent failures unexpectedly included superseded run: %+v", failure)
		}
	}

	failureCounts, err := q.ListFailureCountsByErrorCode(ctx, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("ListFailureCountsByErrorCode: %v", err)
	}
	for _, item := range failureCounts {
		if item.ErrorCode == "superseded_by_new_head" {
			t.Fatalf("failure counts unexpectedly included superseded bucket: %+v", item)
		}
	}
}

func TestReviewRunRetryableFailure(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	_, projID := seedProject(t, q, ctx)
	mrID := seedMR(t, q, ctx, projID)

	res, _ := q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID: projID, MergeRequestID: mrID,
		TriggerType: "webhook", HeadSha: "abc",
		Status: "pending", MaxRetries: 3,
		IdempotencyKey: "key-retry",
	})
	runID, _ := res.LastInsertId()

	// Claim first
	q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "w1", ID: runID})

	// Mark retryable failure
	retryAt := sql.NullTime{Time: time.Now().Add(30 * time.Second), Valid: true}
	ok, err := q.MarkReviewRunRetryableFailureIfRunning(ctx, db.MarkReviewRunRetryableFailureParams{
		ErrorCode:   "provider_error",
		ErrorDetail: sql.NullString{String: "timeout", Valid: true},
		RetryCount:  1,
		NextRetryAt: retryAt,
		ID:          runID,
	})
	if err != nil {
		t.Fatalf("retryable failure: %v", err)
	}
	if !ok {
		t.Fatal("expected rows affected")
	}

	run, _ := q.GetReviewRun(ctx, runID)
	if run.Status != "failed" {
		t.Fatalf("want failed, got %s", run.Status)
	}
	if !run.NextRetryAt.Valid {
		t.Fatal("expected next_retry_at to be set")
	}
}

func TestCancelPendingRunsForMR(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	_, projID := seedProject(t, q, ctx)
	mrID := seedMR(t, q, ctx, projID)

	// Insert 2 pending runs
	q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID: projID, MergeRequestID: mrID,
		TriggerType: "webhook", HeadSha: "a",
		Status: "pending", MaxRetries: 3, IdempotencyKey: "c1",
	})
	q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID: projID, MergeRequestID: mrID,
		TriggerType: "webhook", HeadSha: "b",
		Status: "pending", MaxRetries: 3, IdempotencyKey: "c2",
	})

	err := q.CancelPendingRunsForMR(ctx, mrID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	runs, err := q.ListReviewRunsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range runs {
		if r.Status != "cancelled" {
			t.Fatalf("want cancelled, got %s for run %d", r.Status, r.ID)
		}
	}
}

func TestSupersedeActiveRunsForMR(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	_, projID := seedProject(t, q, ctx)
	mrID := seedMR(t, q, ctx, projID)

	oldPendingRes, _ := q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID: projID, MergeRequestID: mrID,
		TriggerType: "webhook", HeadSha: "old-pending",
		Status: "pending", MaxRetries: 3, IdempotencyKey: "supersede-pending",
	})
	oldPendingID, _ := oldPendingRes.LastInsertId()

	oldRunningRes, _ := q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID: projID, MergeRequestID: mrID,
		TriggerType: "webhook", HeadSha: "old-running",
		Status: "pending", MaxRetries: 3, IdempotencyKey: "supersede-running",
	})
	oldRunningID, _ := oldRunningRes.LastInsertId()
	if err := q.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "worker-a", ID: oldRunningID}); err != nil {
		t.Fatalf("ClaimReviewRun: %v", err)
	}

	oldRetryRes, _ := q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID: projID, MergeRequestID: mrID,
		TriggerType: "webhook", HeadSha: "old-retry",
		Status: "pending", MaxRetries: 3, IdempotencyKey: "supersede-retry",
	})
	oldRetryID, _ := oldRetryRes.LastInsertId()
	if err := q.MarkReviewRunRetryableFailure(ctx, db.MarkReviewRunRetryableFailureParams{
		ErrorCode:   "provider_timeout",
		ErrorDetail: sql.NullString{String: "retry later", Valid: true},
		RetryCount:  1,
		NextRetryAt: sql.NullTime{Time: time.Now().Add(30 * time.Second), Valid: true},
		ID:          oldRetryID,
	}); err != nil {
		t.Fatalf("MarkReviewRunRetryableFailure: %v", err)
	}

	newRunRes, _ := q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID: projID, MergeRequestID: mrID,
		TriggerType: "webhook", HeadSha: "new-head",
		Status: "pending", MaxRetries: 3, IdempotencyKey: "supersede-new",
	})
	newRunID, _ := newRunRes.LastInsertId()

	if err := q.SupersedeActiveRunsForMR(ctx, db.SupersedeActiveRunsForMRParams{
		SupersededByRunID: sql.NullInt64{Int64: newRunID, Valid: true},
		MergeRequestID:    mrID,
		ID:                newRunID,
	}); err != nil {
		t.Fatalf("SupersedeActiveRunsForMR: %v", err)
	}

	for _, runID := range []int64{oldPendingID, oldRunningID, oldRetryID} {
		run, err := q.GetReviewRun(ctx, runID)
		if err != nil {
			t.Fatalf("GetReviewRun(%d): %v", runID, err)
		}
		if run.Status != "cancelled" {
			t.Fatalf("run %d status = %q, want cancelled", runID, run.Status)
		}
		if run.ErrorCode != "superseded_by_new_head" {
			t.Fatalf("run %d error_code = %q, want superseded_by_new_head", runID, run.ErrorCode)
		}
		if run.NextRetryAt.Valid {
			t.Fatalf("run %d next_retry_at = %+v, want NULL", runID, run.NextRetryAt)
		}
		if !run.SupersededByRunID.Valid || run.SupersededByRunID.Int64 != newRunID {
			t.Fatalf("run %d superseded_by_run_id = %+v, want %d", runID, run.SupersededByRunID, newRunID)
		}
	}

	newRun, err := q.GetReviewRun(ctx, newRunID)
	if err != nil {
		t.Fatalf("GetReviewRun(new): %v", err)
	}
	if newRun.Status != "pending" {
		t.Fatalf("new run status = %q, want pending", newRun.Status)
	}
	if newRun.SupersededByRunID.Valid {
		t.Fatalf("new run superseded_by_run_id = %+v, want NULL", newRun.SupersededByRunID)
	}
}

func TestReviewFindings(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	_, projID := seedProject(t, q, ctx)
	mrID := seedMR(t, q, ctx, projID)
	runID := seedRun(t, q, ctx, projID, mrID)

	// Insert finding
	res, err := q.InsertReviewFinding(ctx, db.InsertReviewFindingParams{
		ReviewRunID:         runID,
		MergeRequestID:      mrID,
		Category:            "security",
		Severity:            "high",
		Confidence:          0.9,
		Title:               "SQL injection",
		Path:                "app/handler.go",
		AnchorKind:          "new_line",
		NewLine:             sql.NullInt32{Int32: 42, Valid: true},
		CanonicalKey:        "sec-sqli-001",
		AnchorFingerprint:   "fp-abc",
		SemanticFingerprint: "sem-abc",
		State:               "new",
	})
	if err != nil {
		t.Fatalf("insert finding: %v", err)
	}
	findingID, _ := res.LastInsertId()

	// Get by ID
	f, err := q.GetReviewFinding(ctx, findingID)
	if err != nil {
		t.Fatalf("get finding: %v", err)
	}
	if f.Title != "SQL injection" {
		t.Fatalf("want SQL injection, got %s", f.Title)
	}

	// List active by MR
	findings, err := q.ListActiveFindingsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("want 1, got %d", len(findings))
	}

	// Update state
	err = q.UpdateFindingState(ctx, db.UpdateFindingStateParams{
		State: "resolved", ID: findingID,
	})
	if err != nil {
		t.Fatalf("update state: %v", err)
	}

	// No longer active
	findings, err = q.ListActiveFindingsByMR(ctx, mrID)
	if err != nil {
		t.Fatalf("list active after resolve: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("want 0, got %d", len(findings))
	}
}

func TestGitlabDiscussions(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	_, projID := seedProject(t, q, ctx)
	mrID := seedMR(t, q, ctx, projID)
	runID := seedRun(t, q, ctx, projID, mrID)
	findingID := seedFinding(t, q, ctx, runID, mrID)

	// Insert discussion
	res, err := q.InsertGitlabDiscussion(ctx, db.InsertGitlabDiscussionParams{
		ReviewFindingID:    findingID,
		MergeRequestID:     mrID,
		GitlabDiscussionID: "disc-001",
		DiscussionType:     "diff",
		Resolved:           false,
	})
	if err != nil {
		t.Fatalf("insert discussion: %v", err)
	}
	discID, _ := res.LastInsertId()

	// Get by finding
	d, err := q.GetGitlabDiscussionByFinding(ctx, findingID)
	if err != nil {
		t.Fatalf("get by finding: %v", err)
	}
	if d.GitlabDiscussionID != "disc-001" {
		t.Fatalf("want disc-001, got %s", d.GitlabDiscussionID)
	}

	// Resolve
	err = q.UpdateGitlabDiscussionResolved(ctx, db.UpdateGitlabDiscussionResolvedParams{Resolved: true, ID: discID})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	d, _ = q.GetGitlabDiscussion(ctx, discID)
	if !d.Resolved {
		t.Fatal("expected resolved")
	}
}

func TestCommentActions(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	_, projID := seedProject(t, q, ctx)
	mrID := seedMR(t, q, ctx, projID)
	runID := seedRun(t, q, ctx, projID, mrID)

	res, err := q.InsertCommentAction(ctx, db.InsertCommentActionParams{
		ReviewRunID:    runID,
		ActionType:     "create_thread",
		IdempotencyKey: "ca-1",
		Status:         "pending",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	caID, _ := res.LastInsertId()

	// Get by idempotency key
	ca, err := q.GetCommentActionByIdempotencyKey(ctx, "ca-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ca.ID != caID {
		t.Fatalf("want %d, got %d", caID, ca.ID)
	}

	// Update status
	err = q.UpdateCommentActionStatus(ctx, db.UpdateCommentActionStatusParams{
		Status: "completed", LatencyMs: 100, ID: caID,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	actions, err := q.ListCommentActionsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(actions) != 1 || actions[0].Status != "completed" {
		t.Fatalf("unexpected actions: %+v", actions)
	}
}

func TestAuditLogs(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()

	_, err := q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		EntityType:  "review_run",
		EntityID:    1,
		Action:      "provider_called",
		Actor:       "worker-1",
		Detail:      json.RawMessage(`{"model":"claude-3"}`),
		DeliveryKey: "dk-1",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	logs, err := q.ListAuditLogsByDeliveryKey(ctx, "dk-1")
	if err != nil {
		t.Fatalf("list by dk: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("want 1, got %d", len(logs))
	}

	logs, err = q.ListAuditLogsByEntity(ctx, db.ListAuditLogsByEntityParams{
		EntityType: "review_run", EntityID: 1, Limit: 10, Offset: 0,
	})
	if err != nil {
		t.Fatalf("list by entity: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("want 1, got %d", len(logs))
	}
}

func TestHookEvents(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()

	_, err := q.InsertHookEvent(ctx, db.InsertHookEventParams{
		DeliveryKey:         "he-1",
		HookSource:          "gitlab",
		EventType:           "merge_request",
		Action:              "open",
		HeadSha:             "abc",
		Payload:             json.RawMessage(`{}`),
		VerificationOutcome: "verified",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	ev, err := q.GetHookEventByDeliveryKey(ctx, "he-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ev.DeliveryKey != "he-1" {
		t.Fatalf("want he-1, got %s", ev.DeliveryKey)
	}
}

func TestProjectPolicy(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	_, projID := seedProject(t, q, ctx)

	_, err := q.InsertProjectPolicy(ctx, db.InsertProjectPolicyParams{
		ProjectID:           projID,
		ConfidenceThreshold: 0.7,
		SeverityThreshold:   "medium",
		GateMode:            "threads_resolved",
	})
	if err != nil {
		t.Fatalf("insert policy: %v", err)
	}

	pol, err := q.GetProjectPolicy(ctx, projID)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if pol.ConfidenceThreshold != 0.7 {
		t.Fatalf("want 0.7, got %f", pol.ConfidenceThreshold)
	}
}

func TestMRVersion(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	_, projID := seedProject(t, q, ctx)
	mrID := seedMR(t, q, ctx, projID)

	_, err := q.InsertMRVersion(ctx, db.InsertMRVersionParams{
		MergeRequestID:  mrID,
		GitlabVersionID: 1,
		BaseSha:         "base",
		StartSha:        "start",
		HeadSha:         "head",
		PatchIDSha:      "patch",
	})
	if err != nil {
		t.Fatalf("insert version: %v", err)
	}

	v, err := q.GetLatestMRVersion(ctx, mrID)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if v.HeadSha != "head" {
		t.Fatalf("want head, got %s", v.HeadSha)
	}
}

func TestProviderMetrics(t *testing.T) {
	q := newQueries(t)
	ctx := context.Background()
	_, projID := seedProject(t, q, ctx)
	mrID := seedMR(t, q, ctx, projID)
	runID := seedRun(t, q, ctx, projID, mrID)

	err := q.UpdateReviewRunProviderMetrics(ctx, db.UpdateReviewRunProviderMetricsParams{
		ProviderLatencyMs: 250, ProviderTokensTotal: 5000, ID: runID,
	})
	if err != nil {
		t.Fatalf("update metrics: %v", err)
	}

	run, _ := q.GetReviewRun(ctx, runID)
	if run.ProviderLatencyMs != 250 {
		t.Fatalf("want 250, got %d", run.ProviderLatencyMs)
	}
}

func TestWorkerHeartbeats(t *testing.T) {
	sqlDB := setupDB(t)
	q := sqlitedb.New(sqlDB)
	ctx := context.Background()
	_, projID := seedProject(t, q, ctx)
	mrID := seedMR(t, q, ctx, projID)
	now := time.Date(2026, time.March, 29, 18, 30, 0, 0, time.UTC)

	if err := q.UpsertWorkerHeartbeat(ctx, db.UpsertWorkerHeartbeatParams{
		WorkerID:              "worker-1",
		Hostname:              "host-a",
		Version:               "dev",
		ConfiguredConcurrency: 4,
		StartedAt:             now,
		LastSeenAt:            now,
	}); err != nil {
		t.Fatalf("upsert worker heartbeat: %v", err)
	}

	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO review_runs (project_id, merge_request_id, status, trigger_type, idempotency_key, head_sha, claimed_by, claimed_at, started_at, max_retries)
		VALUES (?, ?, 'running', 'mr_open', ?, ?, ?, ?, ?, 3)`,
		projID, mrID, "sqlite-heartbeat-running", "sha-running", "worker-1", now, now); err != nil {
		t.Fatalf("insert running review run: %v", err)
	}

	heartbeats, err := q.ListActiveWorkerHeartbeats(ctx, now.Add(-30*time.Second))
	if err != nil {
		t.Fatalf("list active worker heartbeats: %v", err)
	}
	if len(heartbeats) != 1 {
		t.Fatalf("active worker heartbeats = %d, want 1", len(heartbeats))
	}
	if heartbeats[0].WorkerID != "worker-1" {
		t.Fatalf("worker id = %q, want worker-1", heartbeats[0].WorkerID)
	}

	counts, err := q.ListRunningRunCountsByWorker(ctx)
	if err != nil {
		t.Fatalf("list running run counts by worker: %v", err)
	}
	if len(counts) != 1 {
		t.Fatalf("running worker counts = %d, want 1", len(counts))
	}
	if counts[0].WorkerID != "worker-1" || counts[0].RunningRuns != 1 {
		t.Fatalf("running worker count = %+v, want worker-1 => 1", counts[0])
	}
}

// --- seed helpers ---

func seedProject(t *testing.T, q *sqlitedb.Queries, ctx context.Context) (instID, projID int64) {
	t.Helper()
	res, err := q.InsertGitlabInstance(ctx, db.InsertGitlabInstanceParams{Url: "https://gl.test", Name: "test"})
	if err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	instID, _ = res.LastInsertId()

	res, err = q.InsertProject(ctx, db.InsertProjectParams{
		GitlabInstanceID: instID, GitlabProjectID: 1, PathWithNamespace: "org/repo", Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	projID, _ = res.LastInsertId()
	return
}

func seedMR(t *testing.T, q *sqlitedb.Queries, ctx context.Context, projID int64) int64 {
	t.Helper()
	res, err := q.InsertMergeRequest(ctx, db.InsertMergeRequestParams{
		ProjectID: projID, MrIid: 1, Title: "Test MR",
		SourceBranch: "feat", TargetBranch: "main",
		Author: "dev", State: "opened", HeadSha: "abc",
	})
	if err != nil {
		t.Fatalf("seed MR: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedRun(t *testing.T, q *sqlitedb.Queries, ctx context.Context, projID, mrID int64) int64 {
	t.Helper()
	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID: projID, MergeRequestID: mrID,
		TriggerType: "webhook", HeadSha: "abc",
		Status: "pending", MaxRetries: 3,
		IdempotencyKey: "run-" + time.Now().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedFinding(t *testing.T, q *sqlitedb.Queries, ctx context.Context, runID, mrID int64) int64 {
	t.Helper()
	res, err := q.InsertReviewFinding(ctx, db.InsertReviewFindingParams{
		ReviewRunID:         runID,
		MergeRequestID:      mrID,
		Category:            "test",
		Severity:            "low",
		Confidence:          0.5,
		Title:               "test finding",
		Path:                "test.go",
		AnchorKind:          "new_line",
		CanonicalKey:        "test-" + time.Now().Format(time.RFC3339Nano),
		AnchorFingerprint:   "fp",
		SemanticFingerprint: "sem",
		State:               "new",
	})
	if err != nil {
		t.Fatalf("seed finding: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func mustNormalizeSQLiteDashboardTime(t *testing.T, raw interface{}) time.Time {
	t.Helper()

	switch v := raw.(type) {
	case time.Time:
		return v.UTC().Truncate(time.Second)
	case string:
		return mustParseSQLiteDashboardTimeString(t, v)
	case []byte:
		return mustParseSQLiteDashboardTimeString(t, string(v))
	default:
		t.Fatalf("unexpected dashboard time type %T", raw)
		return time.Time{}
	}
}

func mustParseSQLiteDashboardTimeString(t *testing.T, value string) time.Time {
	t.Helper()

	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, value); err == nil {
			return ts.UTC().Truncate(time.Second)
		}
	}
	t.Fatalf("unsupported dashboard time string %q", value)
	return time.Time{}
}
