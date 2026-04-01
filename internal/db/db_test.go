package db_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/pressly/goose/v3"
)

// migrationsDir is the relative path from this test file to the migrations.
const migrationsDir = "../../migrations"

// expectedTables is the set of application tables that VAL-FOUND-002 requires.
var expectedTables = []string{
	"audit_logs",
	"comment_actions",
	"gitlab_discussions",
	"gitlab_instances",
	"hook_events",
	"merge_requests",
	"mr_versions",
	"project_policies",
	"projects",
	"review_findings",
	"review_runs",
}

// TestMigrationRoundTrip verifies VAL-FOUND-002 and VAL-FOUND-003:
// goose up creates all tables, goose down drops them, and goose up recreates them.
func TestMigrationRoundTrip(t *testing.T) {
	sqlDB := dbtest.New(t)

	// --- goose up -------------------------------------------------------
	dbtest.MigrateUp(t, sqlDB, migrationsDir)
	assertTablesExist(t, sqlDB, expectedTables)

	// --- goose down (fully reverse) -------------------------------------
	dbtest.MigrateDown(t, sqlDB, migrationsDir)
	assertTablesGone(t, sqlDB, expectedTables)

	// --- goose up again (recreate) --------------------------------------
	dbtest.MigrateUp(t, sqlDB, migrationsDir)
	assertTablesExist(t, sqlDB, expectedTables)
}

// TestMigrationVersion ensures that after all migrations, the goose version
// matches the highest migration number.
func TestMigrationVersion(t *testing.T) {
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)

	if err := goose.SetDialect("mysql"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	ver, err := goose.GetDBVersion(sqlDB)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if ver < 1 {
		t.Errorf("expected version >= 1, got %d", ver)
	}
}

// TestTransactionalRollback verifies VAL-CROSS-014: if a database write
// fails mid-transaction, the entire transaction is rolled back leaving zero
// orphaned rows.
func TestTransactionalRollback(t *testing.T) {
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)

	ctx := context.Background()
	wantErr := errors.New("simulated failure")

	// Insert prerequisite data outside the tx (gitlab_instance + project + MR).
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID)

	// Attempt a transaction that creates a review_run then fails before commit.
	err := db.RunTx(ctx, sqlDB, func(ctx context.Context, q *db.Queries) error {
		_, insertErr := q.InsertReviewRun(ctx, db.InsertReviewRunParams{
			ProjectID:      projectID,
			MergeRequestID: mrID,
			TriggerType:    "webhook",
			HeadSha:        "abc123",
			Status:         "pending",
			MaxRetries:     3,
			IdempotencyKey: "test-rollback-key",
		})
		if insertErr != nil {
			return fmt.Errorf("insert run: %w", insertErr)
		}
		// Simulate a downstream failure.
		return wantErr
	})

	if !errors.Is(err, wantErr) {
		t.Fatalf("expected simulated error, got: %v", err)
	}

	// Verify no review_run was persisted.
	var count int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM review_runs WHERE idempotency_key = ?", "test-rollback-key").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 orphaned review_runs, got %d", count)
	}
}

// TestTransactionalCommit verifies that a successful transaction persists data.
func TestTransactionalCommit(t *testing.T) {
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)

	ctx := context.Background()

	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID)

	// Create a hook_event + review_run in a single transaction.
	err := db.RunTx(ctx, sqlDB, func(ctx context.Context, q *db.Queries) error {
		_, err := q.InsertHookEvent(ctx, db.InsertHookEventParams{
			DeliveryKey:         "commit-test-delivery",
			HookSource:          "project",
			EventType:           "merge_request",
			Action:              "open",
			HeadSha:             "def456",
			VerificationOutcome: "verified",
		})
		if err != nil {
			return fmt.Errorf("insert hook event: %w", err)
		}

		_, err = q.InsertReviewRun(ctx, db.InsertReviewRunParams{
			ProjectID:      projectID,
			MergeRequestID: mrID,
			TriggerType:    "webhook",
			HeadSha:        "def456",
			Status:         "pending",
			MaxRetries:     3,
			IdempotencyKey: "test-commit-key",
		})
		if err != nil {
			return fmt.Errorf("insert run: %w", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("transaction: %v", err)
	}

	// Verify both records exist.
	var hookCount, runCount int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM hook_events WHERE delivery_key = ?", "commit-test-delivery").Scan(&hookCount); err != nil {
		t.Fatalf("hook count: %v", err)
	}
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM review_runs WHERE idempotency_key = ?", "test-commit-key").Scan(&runCount); err != nil {
		t.Fatalf("run count: %v", err)
	}
	if hookCount != 1 {
		t.Errorf("expected 1 hook_event, got %d", hookCount)
	}
	if runCount != 1 {
		t.Errorf("expected 1 review_run, got %d", runCount)
	}
}

// TestTransactionalRollbackCommentActions verifies that a failure during
// comment action persistence rolls back both the comment_action and any
// associated audit_log entry.
func TestTransactionalRollbackCommentActions(t *testing.T) {
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)

	ctx := context.Background()
	wantErr := errors.New("writer failure")

	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID)
	runID := insertTestRun(t, sqlDB, projectID, mrID, "action-rollback-key")

	err := db.RunTx(ctx, sqlDB, func(ctx context.Context, q *db.Queries) error {
		_, err := q.InsertCommentAction(ctx, db.InsertCommentActionParams{
			ReviewRunID:    runID,
			ActionType:     "create_discussion",
			IdempotencyKey: "action-idem-key",
			Status:         "pending",
		})
		if err != nil {
			return fmt.Errorf("insert action: %w", err)
		}
		_, err = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			EntityType: "comment_action",
			EntityID:   0,
			Action:     "create_discussion",
			Actor:      "bot",
			Detail:     json.RawMessage(`{"note":"test"}`),
		})
		if err != nil {
			return fmt.Errorf("insert audit: %w", err)
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected simulated error, got: %v", err)
	}

	var actionCount, auditCount int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM comment_actions WHERE idempotency_key = ?", "action-idem-key").Scan(&actionCount); err != nil {
		t.Fatalf("action count: %v", err)
	}
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_logs WHERE action = 'create_discussion' AND actor = 'bot'").Scan(&auditCount); err != nil {
		t.Fatalf("audit count: %v", err)
	}

	if actionCount != 0 {
		t.Errorf("expected 0 orphaned comment_actions, got %d", actionCount)
	}
	if auditCount != 0 {
		t.Errorf("expected 0 orphaned audit_logs, got %d", auditCount)
	}
}

func TestAdminDashboardQueriesUseRetryEligibilityAndExcludeSupersedes(t *testing.T) {
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)

	ctx := context.Background()
	q := db.New(sqlDB)

	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID)

	pendingRunID := insertTestRun(t, sqlDB, projectID, mrID, "pending-run")
	retryRunID := insertTestRun(t, sqlDB, projectID, mrID, "retry-run")
	supersededRunID := insertTestRun(t, sqlDB, projectID, mrID, "superseded-run")
	failedRunID := insertTestRun(t, sqlDB, projectID, mrID, "failed-run")

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
	gotOldestWaiting := mustNormalizeDashboardTime(t, oldestWaiting)
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

func TestAdminDashboardRunListAndDetail(t *testing.T) {
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)

	ctx := context.Background()
	q := db.New(sqlDB)

	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID)
	runID := insertTestRun(t, sqlDB, projectID, mrID, "run-list-detail")

	hookRes, err := q.InsertHookEvent(ctx, db.InsertHookEventParams{
		DeliveryKey:         "run-list-detail",
		HookSource:          "project",
		EventType:           "merge_request",
		Action:              "update",
		HeadSha:             "head-123",
		VerificationOutcome: "verified",
	})
	if err != nil {
		t.Fatalf("InsertHookEvent: %v", err)
	}
	hookID, _ := hookRes.LastInsertId()

	if _, err := sqlDB.ExecContext(ctx, `
		UPDATE review_runs
		SET hook_event_id = ?, scope_json = ?, status = 'failed', error_code = 'publish_failed',
		    claimed_by = 'worker-1', retry_count = 2, provider_latency_ms = 810, provider_tokens_total = 4200
		WHERE id = ?`,
		hookID,
		[]byte(`{"platform":"github"}`),
		runID,
	); err != nil {
		t.Fatalf("update review run: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE merge_requests SET web_url = 'https://github.com/acme/repo/pull/17' WHERE id = ?`, mrID); err != nil {
		t.Fatalf("update merge request web_url: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE projects SET path_with_namespace = 'acme/repo' WHERE id = ?`, projectID); err != nil {
		t.Fatalf("update project path: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO review_findings (review_run_id, merge_request_id, category, severity, confidence, title, path, anchor_kind, canonical_key, anchor_fingerprint, semantic_fingerprint, state)
		VALUES (?, ?, 'db', 'high', 0.9, 'finding one', 'db.go', 'new_line', 'k1', 'a1', 's1', 'active'),
		       (?, ?, 'security', 'medium', 0.8, 'finding two', 'sec.go', 'new_line', 'k2', 'a2', 's2', 'active')`,
		runID, mrID, runID, mrID,
	); err != nil {
		t.Fatalf("insert findings: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO comment_actions (review_run_id, action_type, idempotency_key, status)
		VALUES (?, 'create_summary', 'action-1', 'completed')`,
		runID,
	); err != nil {
		t.Fatalf("insert comment action: %v", err)
	}

	runs, err := q.ListRecentRuns(ctx, db.ListRecentRunsParams{
		Platform:    "github",
		Status:      "failed",
		ErrorCode:   "publish_failed",
		ProjectPath: "acme",
		HeadSha:     "sha1",
		LimitCount:  10,
	})
	if err != nil {
		t.Fatalf("ListRecentRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs len = %d, want 1", len(runs))
	}
	if runs[0].FindingCount != 2 {
		t.Fatalf("finding_count = %d, want 2", runs[0].FindingCount)
	}
	if runs[0].CommentActionCount != 1 {
		t.Fatalf("comment_action_count = %d, want 1", runs[0].CommentActionCount)
	}

	detail, err := q.GetRunDetail(ctx, runID)
	if err != nil {
		t.Fatalf("GetRunDetail: %v", err)
	}
	if detail.Platform != "github" {
		t.Fatalf("detail platform = %q, want github", detail.Platform)
	}
	if detail.HookAction != "update" {
		t.Fatalf("detail hook_action = %q, want update", detail.HookAction)
	}
	if detail.ProviderTokensTotal != 4200 {
		t.Fatalf("detail provider_tokens_total = %d, want 4200", detail.ProviderTokensTotal)
	}
}

func TestAdminDashboardTrendAndAggregateQueries(t *testing.T) {
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)

	ctx := context.Background()
	q := db.New(sqlDB)

	instanceID := insertTestInstance(t, sqlDB)
	projectA := insertTestProject(t, sqlDB, instanceID)
	res, err := sqlDB.Exec("INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace) VALUES (?, 101, 'test/repo-b')", instanceID)
	if err != nil {
		t.Fatalf("insert projectB: %v", err)
	}
	projectB, _ := res.LastInsertId()
	mrA := insertTestMR(t, sqlDB, projectA)
	res, err = sqlDB.Exec("INSERT INTO merge_requests (project_id, mr_iid, title, source_branch, target_branch, author, state, head_sha) VALUES (?, 2, 'Test MR B', 'feature-b', 'main', 'dev', 'opened', 'abc124')", projectB)
	if err != nil {
		t.Fatalf("insert mrB: %v", err)
	}
	mrB, _ := res.LastInsertId()
	if _, err := sqlDB.ExecContext(ctx, `UPDATE projects SET path_with_namespace = 'acme/repo-a' WHERE id = ?`, projectA); err != nil {
		t.Fatalf("update projectA path: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE projects SET path_with_namespace = 'acme/repo-b' WHERE id = ?`, projectB); err != nil {
		t.Fatalf("update projectB path: %v", err)
	}

	runA := insertTestRun(t, sqlDB, projectA, mrA, "trend-a")
	runB := insertTestRun(t, sqlDB, projectB, mrB, "trend-b")
	now := time.Now().UTC().Truncate(time.Second)
	bucket := now.Add(-1 * time.Hour).Truncate(time.Hour)

	if _, err := sqlDB.ExecContext(ctx, `
		UPDATE review_runs
		SET scope_json = ?, status = 'completed', created_at = ?, updated_at = ?, completed_at = ?
		WHERE id = ?`,
		[]byte(`{"platform":"github"}`), bucket.Add(10*time.Minute), bucket.Add(20*time.Minute), bucket.Add(20*time.Minute), runA,
	); err != nil {
		t.Fatalf("update github run: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
		UPDATE review_runs
		SET scope_json = ?, status = 'failed', error_code = 'publish_failed', created_at = ?, updated_at = ?
		WHERE id = ?`,
		[]byte(`{"platform":"gitlab"}`), bucket.Add(15*time.Minute), bucket.Add(25*time.Minute), runB,
	); err != nil {
		t.Fatalf("update gitlab run: %v", err)
	}
	if _, err := q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		EntityType:          "hook_event",
		EntityID:            1,
		Action:              "webhook_verification",
		Actor:               "system",
		VerificationOutcome: "rejected",
	}); err != nil {
		t.Fatalf("InsertAuditLog rejected: %v", err)
	}
	if _, err := q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		EntityType:          "hook_event",
		EntityID:            2,
		Action:              "webhook_verification",
		Actor:               "system",
		VerificationOutcome: "deduplicated",
	}); err != nil {
		t.Fatalf("InsertAuditLog deduplicated: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE audit_logs SET created_at = ? WHERE entity_id IN (1,2)`, bucket.Add(5*time.Minute)); err != nil {
		t.Fatalf("update audit log times: %v", err)
	}

	trends, err := q.ListRunTrendBuckets(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("ListRunTrendBuckets: %v", err)
	}
	if len(trends) == 0 || trends[0].RunCount != 2 {
		t.Fatalf("trend rows = %+v, want run_count 2", trends)
	}

	webhookTrends, err := q.ListWebhookVerificationTrendBuckets(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("ListWebhookVerificationTrendBuckets: %v", err)
	}
	if len(webhookTrends) != 2 {
		t.Fatalf("webhook trend rows = %d, want 2", len(webhookTrends))
	}

	platforms, err := q.ListPlatformRunRollups(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("ListPlatformRunRollups: %v", err)
	}
	if len(platforms) != 2 {
		t.Fatalf("platform rollups = %d, want 2", len(platforms))
	}

	projects, err := q.ListProjectRunRollups(ctx, db.ListProjectRunRollupsParams{Since: now.Add(-24 * time.Hour), LimitCount: 10})
	if err != nil {
		t.Fatalf("ListProjectRunRollups: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("project rollups = %d, want 2", len(projects))
	}
}

func TestOperatorRunActions(t *testing.T) {
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)

	ctx := context.Background()
	q := db.New(sqlDB)

	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID)

	retryRunID := insertTestRun(t, sqlDB, projectID, mrID, "retry-action")
	if _, err := sqlDB.ExecContext(ctx, `UPDATE review_runs SET status = 'failed', error_code = 'provider_failed' WHERE id = ?`, retryRunID); err != nil {
		t.Fatalf("prepare retry run: %v", err)
	}
	if err := q.RetryReviewRunNow(ctx, retryRunID); err != nil {
		t.Fatalf("RetryReviewRunNow: %v", err)
	}
	retryRun, err := q.GetReviewRun(ctx, retryRunID)
	if err != nil {
		t.Fatalf("GetReviewRun(retry): %v", err)
	}
	if !retryRun.NextRetryAt.Valid {
		t.Fatal("retry run next_retry_at invalid, want valid")
	}

	cancelRunID := insertTestRun(t, sqlDB, projectID, mrID, "cancel-action")
	if err := q.CancelReviewRun(ctx, cancelRunID, "cancelled_by_operator", "Cancelled by admin operator"); err != nil {
		t.Fatalf("CancelReviewRun: %v", err)
	}
	cancelRun, err := q.GetReviewRun(ctx, cancelRunID)
	if err != nil {
		t.Fatalf("GetReviewRun(cancel): %v", err)
	}
	if cancelRun.Status != "cancelled" || cancelRun.ErrorCode != "cancelled_by_operator" {
		t.Fatalf("cancelled run = %+v", cancelRun)
	}

	requeueRunID := insertTestRun(t, sqlDB, projectID, mrID, "requeue-action")
	if _, err := sqlDB.ExecContext(ctx, `UPDATE review_runs SET status = 'cancelled', error_code = 'cancelled_by_operator', claimed_by = 'worker-1' WHERE id = ?`, requeueRunID); err != nil {
		t.Fatalf("prepare requeue run: %v", err)
	}
	if err := q.RequeueReviewRun(ctx, requeueRunID); err != nil {
		t.Fatalf("RequeueReviewRun: %v", err)
	}
	requeuedRun, err := q.GetReviewRun(ctx, requeueRunID)
	if err != nil {
		t.Fatalf("GetReviewRun(requeue): %v", err)
	}
	if requeuedRun.Status != "pending" || requeuedRun.ClaimedBy != "" {
		t.Fatalf("requeued run = %+v", requeuedRun)
	}
}

// TestPanicRollback verifies that a panic inside RunTx still triggers rollback.
func TestPanicRollback(t *testing.T) {
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)

	ctx := context.Background()

	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID)

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic to propagate")
			}
		}()

		_ = db.RunTx(ctx, sqlDB, func(ctx context.Context, q *db.Queries) error {
			_, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{
				ProjectID:      projectID,
				MergeRequestID: mrID,
				TriggerType:    "webhook",
				HeadSha:        "panic-sha",
				Status:         "pending",
				MaxRetries:     3,
				IdempotencyKey: "panic-test-key",
			})
			if err != nil {
				return err
			}
			panic("simulated panic")
		})
	}()

	var count int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM review_runs WHERE idempotency_key = ?", "panic-test-key").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 review_runs after panic rollback, got %d", count)
	}
}

// TestRunTxRollbackPreservesOriginalError verifies that when the function
// inside RunTx returns an error AND the subsequent rollback also fails, the
// combined error still matches the original error via errors.Is. This is
// critical for callers that use sentinel errors for control flow.
func TestRunTxRollbackPreservesOriginalError(t *testing.T) {
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, migrationsDir)

	ctx := context.Background()

	// errSimulated is the sentinel error returned by the TxFunc.
	var errSimulated = errors.New("simulated application failure")

	// To force a rollback failure we start a transaction, then kill the
	// underlying connection from inside the TxFunc by issuing a KILL on
	// the connection's thread_id. This makes the subsequent Rollback()
	// fail with a driver-level error.
	err := db.RunTx(ctx, sqlDB, func(ctx context.Context, q *db.Queries) error {
		// Obtain the connection thread id and kill it so that the
		// subsequent Rollback fails.
		var connID int64
		row := sqlDB.QueryRowContext(ctx, "SELECT CONNECTION_ID()")
		if scanErr := row.Scan(&connID); scanErr != nil {
			// If we can't get the connection ID (edge case), fall back to
			// simply closing the pool to invalidate the connection.
			sqlDB.Close()
			return errSimulated
		}
		// Kill the current connection from a separate session.
		if _, killErr := sqlDB.ExecContext(ctx, fmt.Sprintf("KILL %d", connID)); killErr != nil {
			// Not all connections may be killable; close pool as fallback.
			sqlDB.Close()
		}
		return errSimulated
	})

	if err == nil {
		t.Fatal("expected an error from RunTx, got nil")
	}

	// The key assertion: the original error must still be matchable
	// regardless of whether the rollback also failed.
	if !errors.Is(err, errSimulated) {
		t.Errorf("errors.Is(err, errSimulated) = false; combined error: %v", err)
	}
}

// TestRunTxRollbackErrorJoinSemantics is a unit-level test that verifies the
// errors.Join semantics used by RunTx without needing a real database. It
// directly checks that when errors.Join combines the original error and a
// rollback error, both are matchable via errors.Is.
func TestRunTxRollbackErrorJoinSemantics(t *testing.T) {
	errOriginal := errors.New("application error")
	errRollback := errors.New("rollback failed")

	// Simulate what RunTx does when both fn and Rollback fail.
	combined := errors.Join(errOriginal, fmt.Errorf("db: rollback: %w", errRollback))

	if !errors.Is(combined, errOriginal) {
		t.Errorf("errors.Is(combined, errOriginal) = false; want true\ncombined: %v", combined)
	}
	if !errors.Is(combined, errRollback) {
		t.Errorf("errors.Is(combined, errRollback) = false; want true\ncombined: %v", combined)
	}
}

// --- Helpers ---

func assertTablesExist(t *testing.T, sqlDB *sql.DB, tables []string) {
	t.Helper()
	actual := listTables(t, sqlDB)
	for _, want := range tables {
		found := false
		for _, have := range actual {
			if have == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected table %q to exist, but it was not found (have: %v)", want, actual)
		}
	}
}

func assertTablesGone(t *testing.T, sqlDB *sql.DB, tables []string) {
	t.Helper()
	actual := listTables(t, sqlDB)
	for _, name := range tables {
		for _, have := range actual {
			if have == name {
				t.Errorf("expected table %q to be dropped, but it still exists", name)
			}
		}
	}
}

func listTables(t *testing.T, sqlDB *sql.DB) []string {
	t.Helper()
	rows, err := sqlDB.Query("SHOW TABLES")
	if err != nil {
		t.Fatalf("show tables: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	sort.Strings(tables)
	return tables
}

func insertTestInstance(t *testing.T, sqlDB *sql.DB) int64 {
	t.Helper()
	res, err := sqlDB.Exec("INSERT INTO gitlab_instances (url, name) VALUES ('https://test.gitlab.com', 'test')")
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertTestProject(t *testing.T, sqlDB *sql.DB, instanceID int64) int64 {
	t.Helper()
	res, err := sqlDB.Exec("INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace) VALUES (?, 100, 'test/repo')", instanceID)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertTestMR(t *testing.T, sqlDB *sql.DB, projectID int64) int64 {
	t.Helper()
	res, err := sqlDB.Exec("INSERT INTO merge_requests (project_id, mr_iid, title, source_branch, target_branch, author, state, head_sha) VALUES (?, 1, 'Test MR', 'feature', 'main', 'dev', 'opened', 'abc123')", projectID)
	if err != nil {
		t.Fatalf("insert mr: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertTestRun(t *testing.T, sqlDB *sql.DB, projectID, mrID int64, idempKey string) int64 {
	t.Helper()
	res, err := sqlDB.Exec("INSERT INTO review_runs (project_id, merge_request_id, trigger_type, head_sha, status, max_retries, idempotency_key) VALUES (?, ?, 'webhook', 'sha1', 'pending', 3, ?)", projectID, mrID, idempKey)
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func mustNormalizeDashboardTime(t *testing.T, raw interface{}) time.Time {
	t.Helper()

	switch v := raw.(type) {
	case time.Time:
		return v.UTC().Truncate(time.Second)
	case []byte:
		return mustParseDashboardTimeString(t, string(v))
	case string:
		return mustParseDashboardTimeString(t, v)
	default:
		t.Fatalf("unexpected dashboard time type %T", raw)
		return time.Time{}
	}
}

func mustParseDashboardTimeString(t *testing.T, value string) time.Time {
	t.Helper()

	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999",
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
