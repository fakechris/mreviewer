package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
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

			processor := FuncProcessor(func(context.Context, db.ReviewRun) error {
				return NewRetryableError("gitlab_unavailable", errors.New("temporary failure"))
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

type retryThenSucceedProcessor struct {
	mu               sync.Mutex
	attempts         map[int64]int
	downstreamWrites map[int64]int
}

func (p *retryThenSucceedProcessor) ProcessRun(ctx context.Context, run db.ReviewRun) error {
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
		return NewRetryableError("gitlab_unavailable", errors.New("temporary failure"))
	}

	p.downstreamWrites[run.ID]++
	return nil
}

func (p *retryThenSucceedProcessor) stats(runID int64) (attempts int, downstreamWrites int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempts[runID], p.downstreamWrites[runID]
}
