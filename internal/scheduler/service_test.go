package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/hooks"
	runsvc "github.com/mreviewer/mreviewer/internal/runs"
	tracing "github.com/mreviewer/mreviewer/internal/trace"
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

func insertTestMR(t *testing.T, sqlDB *sql.DB, projectID, mrIID int64, headSHA string) int64 {
	t.Helper()
	res, err := sqlDB.Exec(
		"INSERT INTO merge_requests (project_id, mr_iid, title, source_branch, target_branch, author, state, head_sha) VALUES (?, ?, 'Test MR', 'feature', 'main', 'dev', 'opened', ?)",
		projectID,
		mrIID,
		headSHA,
	)
	if err != nil {
		t.Fatalf("insert mr: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

type runOptions struct {
	status         string
	retryCount     int
	maxRetries     int
	nextRetryAt    *time.Time
	idempotencyKey string
	headSHA        string
}

func insertTestRun(t *testing.T, sqlDB *sql.DB, projectID, mrID int64, opts runOptions) int64 {
	t.Helper()
	status := opts.status
	if status == "" {
		status = "pending"
	}
	maxRetries := opts.maxRetries
	if maxRetries == 0 {
		maxRetries = 3
	}
	idempotencyKey := opts.idempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = "run-key"
	}
	headSHA := opts.headSHA
	if headSHA == "" {
		headSHA = "sha-1"
	}

	res, err := sqlDB.Exec(
		`INSERT INTO review_runs (
			project_id, merge_request_id, trigger_type, head_sha, status,
			retry_count, max_retries, next_retry_at, idempotency_key
		) VALUES (?, ?, 'webhook', ?, ?, ?, ?, ?, ?)`,
		projectID,
		mrID,
		headSHA,
		status,
		opts.retryCount,
		maxRetries,
		opts.nextRetryAt,
		idempotencyKey,
	)
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestClaimPendingRun(t *testing.T) {
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-claim")
	runID := insertTestRun(t, sqlDB, projectID, mrID, runOptions{status: "pending", idempotencyKey: "claim-pending"})

	svc := NewService(testLogger(), sqlDB, nil, WithWorkerID("worker-a"))

	claimed, err := svc.ClaimNextRun(context.Background())
	if err != nil {
		t.Fatalf("ClaimNextRun: %v", err)
	}
	if claimed.ID != runID {
		t.Fatalf("claimed run id = %d, want %d", claimed.ID, runID)
	}

	run, err := db.New(sqlDB).GetReviewRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "running" {
		t.Fatalf("status = %q, want running", run.Status)
	}
	if run.ClaimedBy != "worker-a" {
		t.Fatalf("claimed_by = %q, want worker-a", run.ClaimedBy)
	}
	if !run.ClaimedAt.Valid {
		t.Fatal("claimed_at was not set")
	}
	if !run.StartedAt.Valid {
		t.Fatal("started_at was not set")
	}
}

func TestRetryBackoff(t *testing.T) {
	testCases := []struct {
		name          string
		status        string
		retryCount    int
		maxRetries    int
		baseDelay     time.Duration
		maxDelay      time.Duration
		expectedDelay time.Duration
	}{
		{
			name:          "first retry uses base delay",
			status:        "pending",
			retryCount:    0,
			maxRetries:    3,
			baseDelay:     2 * time.Second,
			maxDelay:      10 * time.Second,
			expectedDelay: 2 * time.Second,
		},
		{
			name:          "backoff is capped at max delay",
			status:        "failed",
			retryCount:    3,
			maxRetries:    5,
			baseDelay:     2 * time.Second,
			maxDelay:      10 * time.Second,
			expectedDelay: 10 * time.Second,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB := setupTestDB(t)
			instanceID := insertTestInstance(t, sqlDB)
			projectID := insertTestProject(t, sqlDB, instanceID)
			mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-retry")

			var nextRetryAt *time.Time
			if tc.status == "failed" {
				due := time.Now().Add(-time.Second)
				nextRetryAt = &due
			}

			runID := insertTestRun(t, sqlDB, projectID, mrID, runOptions{
				status:         tc.status,
				retryCount:     tc.retryCount,
				maxRetries:     tc.maxRetries,
				nextRetryAt:    nextRetryAt,
				idempotencyKey: tc.name,
			})

			processor := FuncProcessor(func(context.Context, db.ReviewRun) (ProcessOutcome, error) {
				return ProcessOutcome{}, NewRetryableError("gitlab_unavailable", errors.New("temporary failure"))
			})
			svc := NewService(
				testLogger(),
				sqlDB,
				processor,
				WithWorkerID("worker-a"),
				WithRetryBaseDelay(tc.baseDelay),
				WithRetryMaxDelay(tc.maxDelay),
			)

			before := time.Now()
			processed, err := svc.RunOnce(context.Background())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if processed != 1 {
				t.Fatalf("processed = %d, want 1", processed)
			}

			run, err := db.New(sqlDB).GetReviewRun(context.Background(), runID)
			if err != nil {
				t.Fatalf("GetReviewRun: %v", err)
			}
			if run.Status != "failed" {
				t.Fatalf("status = %q, want failed", run.Status)
			}
			if run.ErrorCode != "gitlab_unavailable" {
				t.Fatalf("error_code = %q, want gitlab_unavailable", run.ErrorCode)
			}
			if run.RetryCount != int32(tc.retryCount+1) {
				t.Fatalf("retry_count = %d, want %d", run.RetryCount, tc.retryCount+1)
			}
			if !run.NextRetryAt.Valid {
				t.Fatal("next_retry_at was not set")
			}

			delay := run.NextRetryAt.Time.Sub(before)
			if delay < tc.expectedDelay-time.Second || delay > tc.expectedDelay+time.Second {
				t.Fatalf("backoff delay = %v, want about %v", delay, tc.expectedDelay)
			}
		})
	}
}

func TestSingleClaimAcrossWorkers(t *testing.T) {
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-race")
	runID := insertTestRun(t, sqlDB, projectID, mrID, runOptions{status: "pending", idempotencyKey: "single-claim"})

	services := []*Service{
		NewService(testLogger(), sqlDB, nil, WithWorkerID("worker-a")),
		NewService(testLogger(), sqlDB, nil, WithWorkerID("worker-b")),
	}

	type result struct {
		workerID string
		run      *db.ReviewRun
		err      error
	}

	start := make(chan struct{})
	results := make(chan result, len(services))
	for _, svc := range services {
		go func(svc *Service) {
			<-start
			run, err := svc.ClaimNextRun(context.Background())
			results <- result{workerID: svc.workerID, run: run, err: err}
		}(svc)
	}
	close(start)

	winners := 0
	winnerID := ""
	for range services {
		res := <-results
		if res.err == nil {
			winners++
			winnerID = res.workerID
			if res.run == nil || res.run.ID != runID {
				t.Fatalf("worker %s claimed %+v, want run %d", res.workerID, res.run, runID)
			}
			continue
		}
		if !errors.Is(res.err, ErrNoClaimableRuns) {
			t.Fatalf("worker %s returned unexpected error: %v", res.workerID, res.err)
		}
	}

	if winners != 1 {
		t.Fatalf("winners = %d, want 1", winners)
	}

	run, err := db.New(sqlDB).GetReviewRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "running" {
		t.Fatalf("status = %q, want running", run.Status)
	}
	if run.ClaimedBy != winnerID {
		t.Fatalf("claimed_by = %q, want %q", run.ClaimedBy, winnerID)
	}
}

func TestGatePublishesFromRuntimePath(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-gate")
	runID := insertTestRun(t, sqlDB, projectID, mrID, runOptions{status: "pending", idempotencyKey: "runtime-gate", headSHA: "sha-gate"})
	if _, err := sqlDB.Exec("INSERT INTO project_policies (project_id, confidence_threshold, severity_threshold, include_paths, exclude_paths, gate_mode, extra) VALUES (?, ?, ?, ?, ?, ?, ?)", projectID, 0.8, "medium", []byte("[]"), []byte("[]"), "external_status", []byte("{}")); err != nil {
		t.Fatalf("insert project policy: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO review_findings (review_run_id, merge_request_id, category, severity, confidence, title, body_markdown, path, anchor_kind, anchor_fingerprint, semantic_fingerprint, state)
		VALUES (?, ?, 'bug', 'high', 0.95, 'Blocking issue', 'body', 'src/main.go', 'new_line', 'anchor-1', 'semantic-1', 'active')`, runID, mrID); err != nil {
		t.Fatalf("insert finding: %v", err)
	}

	status := &fakeStatusPublisher{}
	ci := &fakeCIPublisher{}
	tracer := tracing.NewRecorder()
	processor := FuncProcessor(func(context.Context, db.ReviewRun) (ProcessOutcome, error) {
		findings, err := db.New(sqlDB).ListFindingsByRun(ctx, runID)
		if err != nil {
			return ProcessOutcome{}, err
		}
		return ProcessOutcome{Status: "completed", ReviewFindings: findings}, nil
	})
	svc := NewService(testLogger(), sqlDB, processor, WithWorkerID("worker-gate"), WithTracer(tracer), WithGateService(gate.NewService(status, ci, gate.NewDBAuditLogger(db.New(sqlDB)))))

	processed, err := svc.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if len(status.results) != 1 {
		t.Fatalf("status publish count = %d, want 1", len(status.results))
	}
	if len(ci.results) != 1 {
		t.Fatalf("ci publish count = %d, want 1", len(ci.results))
	}
	if status.results[0].RunID != runID || status.results[0].State != "failed" {
		t.Fatalf("status result = %+v, want failed result for run %d", status.results[0], runID)
	}
	if status.results[0].TraceID == "" {
		t.Fatal("expected trace id on gate result")
	}
	audits, err := db.New(sqlDB).ListAuditLogsByEntity(ctx, db.ListAuditLogsByEntityParams{EntityType: "review_run", EntityID: runID, Limit: 20, Offset: 0})
	if err != nil {
		t.Fatalf("ListAuditLogsByEntity: %v", err)
	}
	var gateDetail map[string]any
	found := false
	for _, audit := range audits {
		if audit.Action != "gate_published" {
			continue
		}
		if err := json.Unmarshal(audit.Detail, &gateDetail); err != nil {
			t.Fatalf("unmarshal gate audit detail: %v", err)
		}
		found = true
		break
	}
	if !found {
		t.Fatal("expected gate_published audit log")
	}
	if gateDetail["state"] != "failed" {
		t.Fatalf("gate audit state = %#v, want failed", gateDetail["state"])
	}
	ids, _ := gateDetail["qualifying_finding_ids"].([]any)
	if len(ids) != 1 {
		t.Fatalf("qualifying_finding_ids = %v, want 1 entry", gateDetail["qualifying_finding_ids"])
	}
	run, err := db.New(sqlDB).GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("run status = %q, want completed", run.Status)
	}
}

type fakeStatusPublisher struct{ results []gate.Result }

func (f *fakeStatusPublisher) PublishStatus(_ context.Context, result gate.Result) error {
	f.results = append(f.results, result)
	return nil
}

type fakeCIPublisher struct{ results []gate.Result }

func (f *fakeCIPublisher) PublishCIGate(_ context.Context, result gate.Result) error {
	f.results = append(f.results, result)
	return nil
}

func TestConcurrentMRIsolation(t *testing.T) {
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID1 := insertTestMR(t, sqlDB, projectID, 1, "sha-a")
	mrID2 := insertTestMR(t, sqlDB, projectID, 2, "sha-b")
	runID1 := insertTestRun(t, sqlDB, projectID, mrID1, runOptions{status: "pending", idempotencyKey: "run-a", headSHA: "sha-a"})
	runID2 := insertTestRun(t, sqlDB, projectID, mrID2, runOptions{status: "pending", idempotencyKey: "run-b", headSHA: "sha-b"})

	services := []*Service{
		NewService(testLogger(), sqlDB, nil, WithWorkerID("worker-a")),
		NewService(testLogger(), sqlDB, nil, WithWorkerID("worker-b")),
	}

	start := make(chan struct{})
	results := make(chan *db.ReviewRun, len(services))
	errCh := make(chan error, len(services))
	for _, svc := range services {
		go func(svc *Service) {
			<-start
			run, err := svc.ClaimNextRun(context.Background())
			if err != nil {
				errCh <- err
				return
			}
			results <- run
		}(svc)
	}
	close(start)

	claimedRuns := make([]*db.ReviewRun, 0, len(services))
	for range services {
		select {
		case err := <-errCh:
			t.Fatalf("ClaimNextRun: %v", err)
		case run := <-results:
			claimedRuns = append(claimedRuns, run)
		}
	}

	if len(claimedRuns) != 2 {
		t.Fatalf("claimed runs = %d, want 2", len(claimedRuns))
	}

	seenRunIDs := map[int64]bool{}
	seenMRIDs := map[int64]bool{}
	for _, run := range claimedRuns {
		seenRunIDs[run.ID] = true
		seenMRIDs[run.MergeRequestID] = true
	}
	if !seenRunIDs[runID1] || !seenRunIDs[runID2] {
		t.Fatalf("claimed run ids = %#v, want both %d and %d", seenRunIDs, runID1, runID2)
	}
	if !seenMRIDs[mrID1] || !seenMRIDs[mrID2] {
		t.Fatalf("claimed merge_request ids = %#v, want both %d and %d", seenMRIDs, mrID1, mrID2)
	}
}

func TestRetryRecoveryWithoutDuplicateWork(t *testing.T) {
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-recover")
	runID := insertTestRun(t, sqlDB, projectID, mrID, runOptions{status: "pending", idempotencyKey: "retry-recovery"})

	processor := &retryThenSucceedProcessor{}
	svc := NewService(
		testLogger(),
		sqlDB,
		processor,
		WithWorkerID("worker-a"),
		WithRetryBaseDelay(time.Second),
		WithRetryMaxDelay(5*time.Second),
	)

	processed, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("first processed = %d, want 1", processed)
	}

	run, err := db.New(sqlDB).GetReviewRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetReviewRun after first run: %v", err)
	}
	if run.Status != "failed" {
		t.Fatalf("status after first attempt = %q, want failed", run.Status)
	}
	if run.RetryCount != 1 {
		t.Fatalf("retry_count after first attempt = %d, want 1", run.RetryCount)
	}

	if _, err := sqlDB.Exec("UPDATE review_runs SET next_retry_at = ? WHERE id = ?", time.Now().Add(-time.Second), runID); err != nil {
		t.Fatalf("force next_retry_at due: %v", err)
	}

	processed, err = svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("second processed = %d, want 1", processed)
	}

	run, err = db.New(sqlDB).GetReviewRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetReviewRun after recovery: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status after recovery = %q, want completed", run.Status)
	}
	if run.RetryCount != 1 {
		t.Fatalf("retry_count after recovery = %d, want 1", run.RetryCount)
	}
	if !run.CompletedAt.Valid {
		t.Fatal("completed_at was not set")
	}
	if run.NextRetryAt.Valid {
		t.Fatal("next_retry_at should be cleared after recovery")
	}

	attempts, downstreamWrites := processor.stats(runID)
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if downstreamWrites != 1 {
		t.Fatalf("downstream writes = %d, want 1", downstreamWrites)
	}

	runs, err := db.New(sqlDB).ListReviewRunsByMR(context.Background(), mrID)
	if err != nil {
		t.Fatalf("ListReviewRunsByMR: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("run rows = %d, want 1", len(runs))
	}
}

func TestCancelledRunCannotComplete(t *testing.T) {
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 42, "sha-cancel-complete")
	runID := insertTestRun(t, sqlDB, projectID, mrID, runOptions{status: "pending", idempotencyKey: "cancelled-complete"})

	processor := newBlockingProcessor(nil)
	svc := NewService(testLogger(), sqlDB, processor, WithWorkerID("worker-a"))

	type runOnceResult struct {
		processed int
		err       error
	}
	resultCh := make(chan runOnceResult, 1)
	go func() {
		processed, err := svc.RunOnce(context.Background())
		resultCh <- runOnceResult{processed: processed, err: err}
	}()

	processor.waitUntilStarted(t)
	cancelMergeRequest(t, sqlDB, 100, 42, "close", "closed", "sha-cancel-complete")
	processor.release()

	result := <-resultCh
	if result.err != nil {
		t.Fatalf("RunOnce: %v", result.err)
	}
	if result.processed != 1 {
		t.Fatalf("processed = %d, want 1", result.processed)
	}

	run, err := db.New(sqlDB).GetReviewRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", run.Status)
	}
	if run.CompletedAt.Valid {
		t.Fatal("completed_at should remain unset for cancelled runs")
	}
}

func TestCancelledRunCannotFail(t *testing.T) {
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 42, "sha-cancel-fail")
	runID := insertTestRun(t, sqlDB, projectID, mrID, runOptions{status: "pending", idempotencyKey: "cancelled-fail"})

	processor := newBlockingProcessor(NewRetryableError("gitlab_unavailable", errors.New("temporary failure")))
	svc := NewService(testLogger(), sqlDB, processor, WithWorkerID("worker-a"))

	type runOnceResult struct {
		processed int
		err       error
	}
	resultCh := make(chan runOnceResult, 1)
	go func() {
		processed, err := svc.RunOnce(context.Background())
		resultCh <- runOnceResult{processed: processed, err: err}
	}()

	processor.waitUntilStarted(t)
	cancelMergeRequest(t, sqlDB, 100, 42, "merge", "merged", "sha-cancel-fail")
	processor.release()

	result := <-resultCh
	if result.err != nil {
		t.Fatalf("RunOnce: %v", result.err)
	}
	if result.processed != 1 {
		t.Fatalf("processed = %d, want 1", result.processed)
	}

	run, err := db.New(sqlDB).GetReviewRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", run.Status)
	}
	if run.RetryCount != 0 {
		t.Fatalf("retry_count = %d, want 0", run.RetryCount)
	}
	if run.NextRetryAt.Valid {
		t.Fatal("next_retry_at should remain unset for cancelled runs")
	}
}

func TestCancelledRunNotReclaimed(t *testing.T) {
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 42, "sha-not-reclaimed")
	runID := insertTestRun(t, sqlDB, projectID, mrID, runOptions{
		status:         "failed",
		retryCount:     1,
		maxRetries:     3,
		nextRetryAt:    timePtr(time.Now().Add(-time.Second)),
		idempotencyKey: "cancelled-not-reclaimed",
	})

	cancelMergeRequest(t, sqlDB, 100, 42, "close", "closed", "sha-not-reclaimed")

	processorCalled := false
	svc := NewService(testLogger(), sqlDB, FuncProcessor(func(context.Context, db.ReviewRun) (ProcessOutcome, error) {
		processorCalled = true
		return ProcessOutcome{}, nil
	}), WithWorkerID("worker-a"))

	processed, err := svc.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 0 {
		t.Fatalf("processed = %d, want 0", processed)
	}
	if processorCalled {
		t.Fatal("processor should not be called for cancelled runs")
	}

	run, err := db.New(sqlDB).GetReviewRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", run.Status)
	}
	if run.NextRetryAt.Valid {
		t.Fatal("next_retry_at should be cleared for cancelled runs")
	}
}

func TestReapStaleRunningRuns(t *testing.T) {
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-reap")
	runID := insertTestRun(t, sqlDB, projectID, mrID, runOptions{
		status:         "pending",
		retryCount:     1,
		maxRetries:     5,
		idempotencyKey: "reap-stale",
	})

	// Simulate a worker crash: claim the run and set claimed_at to 15 minutes ago.
	staleTime := time.Now().Add(-15 * time.Minute)
	if _, err := sqlDB.Exec(
		"UPDATE review_runs SET status = 'running', claimed_by = 'dead-worker', claimed_at = ?, started_at = ? WHERE id = ?",
		staleTime, staleTime, runID,
	); err != nil {
		t.Fatalf("set stale running state: %v", err)
	}

	svc := NewService(testLogger(), sqlDB, nil,
		WithWorkerID("reaper-worker"),
		WithClaimTimeout(10),
	)

	reaped, err := svc.ReapStaleRuns(context.Background())
	if err != nil {
		t.Fatalf("ReapStaleRuns: %v", err)
	}
	if reaped != 1 {
		t.Fatalf("reaped = %d, want 1", reaped)
	}

	run, err := db.New(sqlDB).GetReviewRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "failed" {
		t.Fatalf("status = %q, want failed", run.Status)
	}
	if run.ErrorCode != "worker_timeout" {
		t.Fatalf("error_code = %q, want worker_timeout", run.ErrorCode)
	}
	if run.RetryCount != 2 {
		t.Fatalf("retry_count = %d, want 2", run.RetryCount)
	}
	if !run.NextRetryAt.Valid {
		t.Fatal("next_retry_at was not set")
	}
	if run.ErrorDetail.String != "Run exceeded claim timeout and was reaped for retry" {
		t.Fatalf("error_detail = %q, want reaper message", run.ErrorDetail.String)
	}
}

func TestReapDoesNotTouchFreshRuns(t *testing.T) {
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-fresh")
	runID := insertTestRun(t, sqlDB, projectID, mrID, runOptions{
		status:         "pending",
		idempotencyKey: "fresh-running",
	})

	// Claim the run just now — should NOT be reaped.
	if _, err := sqlDB.Exec(
		"UPDATE review_runs SET status = 'running', claimed_by = 'active-worker', claimed_at = NOW(), started_at = NOW() WHERE id = ?",
		runID,
	); err != nil {
		t.Fatalf("set fresh running state: %v", err)
	}

	svc := NewService(testLogger(), sqlDB, nil, WithClaimTimeout(10))

	reaped, err := svc.ReapStaleRuns(context.Background())
	if err != nil {
		t.Fatalf("ReapStaleRuns: %v", err)
	}
	if reaped != 0 {
		t.Fatalf("reaped = %d, want 0", reaped)
	}

	run, err := db.New(sqlDB).GetReviewRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "running" {
		t.Fatalf("status = %q, want running", run.Status)
	}
}

type retryThenSucceedProcessor struct {
	mu               sync.Mutex
	attempts         map[int64]int
	downstreamWrites map[int64]int
}

func (p *retryThenSucceedProcessor) ProcessRun(ctx context.Context, run db.ReviewRun) (ProcessOutcome, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.attempts == nil {
		p.attempts = make(map[int64]int)
	}
	if p.downstreamWrites == nil {
		p.downstreamWrites = make(map[int64]int)
	}

	p.attempts[run.ID]++
	if p.attempts[run.ID] == 1 {
		return ProcessOutcome{}, NewRetryableError("gitlab_unavailable", errors.New("temporary failure"))
	}

	p.downstreamWrites[run.ID]++
	return ProcessOutcome{}, nil
}

func (p *retryThenSucceedProcessor) stats(runID int64) (attempts int, downstreamWrites int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempts[runID], p.downstreamWrites[runID]
}

type blockingProcessor struct {
	started   chan struct{}
	releaseCh chan struct{}
	err       error
	once      sync.Once
}

func newBlockingProcessor(err error) *blockingProcessor {
	return &blockingProcessor{
		started:   make(chan struct{}),
		releaseCh: make(chan struct{}),
		err:       err,
	}
}

func (p *blockingProcessor) ProcessRun(context.Context, db.ReviewRun) (ProcessOutcome, error) {
	p.once.Do(func() {
		close(p.started)
	})
	<-p.releaseCh
	return ProcessOutcome{}, p.err
}

func (p *blockingProcessor) waitUntilStarted(t *testing.T) {
	t.Helper()

	select {
	case <-p.started:
	case <-time.After(5 * time.Second):
		t.Fatal("processor did not start")
	}
}

func (p *blockingProcessor) release() {
	close(p.releaseCh)
}

func cancelMergeRequest(t *testing.T, sqlDB *sql.DB, projectID, mrIID int64, action, state, headSHA string) {
	t.Helper()

	svc := runsvc.NewService(testLogger(), sqlDB)
	if err := svc.ProcessEvent(context.Background(), hooks.NormalizedEvent{
		GitLabInstanceURL: "https://test.gitlab.com",
		ProjectID:         projectID,
		ProjectPath:       "test/repo",
		MRIID:             mrIID,
		Action:            action,
		HeadSHA:           headSHA,
		TriggerType:       "webhook",
		EventType:         "Merge Request Hook",
		State:             state,
	}, 0); err != nil {
		t.Fatalf("ProcessEvent %s: %v", action, err)
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}
