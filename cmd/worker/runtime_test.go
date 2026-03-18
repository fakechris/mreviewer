package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/scheduler"
	tracing "github.com/mreviewer/mreviewer/internal/trace"
	"github.com/mreviewer/mreviewer/internal/writer"
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
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("instance last insert id: %v", err)
	}
	return id
}

func insertTestProject(t *testing.T, sqlDB *sql.DB, instanceID int64) int64 {
	t.Helper()
	res, err := sqlDB.Exec(`INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled)
		VALUES (?, ?, ?, TRUE)`, instanceID, 101, "group/project")
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project last insert id: %v", err)
	}
	return id
}

func insertTestMR(t *testing.T, sqlDB *sql.DB, projectID int64, iid int64, headSHA string) int64 {
	t.Helper()
	res, err := sqlDB.Exec(`INSERT INTO merge_requests (project_id, mr_iid, title, state, target_branch, source_branch, head_sha)
		VALUES (?, ?, ?, 'opened', 'main', 'feature', ?)`, projectID, iid, "Runtime gate test", headSHA)
	if err != nil {
		t.Fatalf("insert merge request: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("merge request last insert id: %v", err)
	}
	return id
}

func insertTestRun(t *testing.T, sqlDB *sql.DB, projectID, mrID int64, status, idempotencyKey, headSHA string) int64 {
	t.Helper()
	res, err := sqlDB.Exec(`INSERT INTO review_runs (project_id, merge_request_id, status, trigger_type, idempotency_key, head_sha, max_retries)
		VALUES (?, ?, ?, 'mr_open', ?, ?, 3)`, projectID, mrID, status, idempotencyKey, headSHA)
	if err != nil {
		t.Fatalf("insert review run: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("review run last insert id: %v", err)
	}
	return id
}

func TestWorkerRuntimeInjectsGateService(t *testing.T) {
	runtimeDeps := newRuntimeDeps(testLogger(), setupTestDB(t), scheduler.FuncProcessor(func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error) {
		return scheduler.ProcessOutcome{}, nil
	}))
	if runtimeDeps.GateService == nil {
		t.Fatal("expected gate service to be configured")
	}
	if runtimeDeps.Scheduler == nil {
		t.Fatal("expected scheduler service to be configured")
	}
	if runtimeDeps.Metrics == nil {
		t.Fatal("expected metrics registry to be configured")
	}
	if runtimeDeps.Tracer == nil {
		t.Fatal("expected tracer to be configured")
	}

	status := &fakeStatusPublisher{}
	ci := &fakeCIPublisher{}
	runtimeDeps = newRuntimeDepsWithGatePublishers(testLogger(), setupTestDB(t), scheduler.FuncProcessor(func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error) {
		return scheduler.ProcessOutcome{}, nil
	}), status, ci)
	if runtimeDeps.GateService == nil || runtimeDeps.Scheduler == nil {
		t.Fatal("expected runtime dependencies to remain fully configured")
	}
	if len(status.results) != 0 || len(ci.results) != 0 {
		t.Fatal("gate publishers should not publish during construction")
	}
}

func TestWorkerRuntimeInjectsTelemetry(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-telemetry")
	runID := insertTestRun(t, sqlDB, projectID, mrID, "pending", "runtime-telemetry", "sha-telemetry")
	if _, err := sqlDB.Exec(`INSERT INTO mr_versions (merge_request_id, gitlab_version_id, base_sha, start_sha, head_sha, patch_id_sha)
		VALUES (?, ?, ?, ?, ?, ?)`, mrID, 1, "base-sha", "start-sha", "sha-telemetry", "patch-sha"); err != nil {
		t.Fatalf("insert mr version: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO review_findings (review_run_id, merge_request_id, category, severity, confidence, title, body_markdown, path, anchor_kind, new_line, anchor_snippet, anchor_fingerprint, semantic_fingerprint, state)
		VALUES (?, ?, 'bug', 'high', 0.95, 'Runtime wiring issue', 'body', 'src/main.go', 'new_line', 42, 'snippet', 'anchor-telemetry', 'semantic-telemetry', 'active')`, runID, mrID); err != nil {
		t.Fatalf("insert finding: %v", err)
	}

	processor := scheduler.FuncProcessor(func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error) {
		findings, err := db.New(sqlDB).ListFindingsByRun(ctx, runID)
		if err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		return scheduler.ProcessOutcome{Status: "completed", ProviderLatencyMs: 25, ProviderTokensTotal: 77, ReviewFindings: findings}, nil
	})
	runtimeDeps := newRuntimeDeps(testLogger(), sqlDB, processor)
	runtimeWriter := writer.New(&fakeDiscussionClient{}, &runtimeWriterStore{queries: db.New(sqlDB)}).WithMetrics(runtimeDeps.Metrics).WithTracer(runtimeDeps.Tracer)
	originalProcessor := processor
	runtimeDeps.Scheduler = scheduler.NewService(testLogger(), sqlDB, scheduler.FuncProcessor(func(ctx context.Context, run db.ReviewRun) (scheduler.ProcessOutcome, error) {
		outcome, err := originalProcessor.ProcessRun(ctx, run)
		if err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		runtimeDeps.Metrics.ObserveHistogram("provider_latency_ms", nil, outcome.ProviderLatencyMs)
		runtimeDeps.Metrics.AddCounter("provider_tokens_total", nil, outcome.ProviderTokensTotal)
		if err := runtimeWriter.Write(ctx, db.ReviewRun{ID: run.ID, MergeRequestID: run.MergeRequestID, ProjectID: run.ProjectID, TriggerType: run.TriggerType, Status: outcome.Status}, outcome.ReviewFindings); err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		return outcome, nil
	}), scheduler.WithMetrics(runtimeDeps.Metrics), scheduler.WithTracer(runtimeDeps.Tracer), scheduler.WithGateService(runtimeDeps.GateService))

	processed, err := runtimeDeps.Scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if got := runtimeDeps.Metrics.CounterValue("review_run_started_total", map[string]string{"trigger_type": "mr_open"}); got != 1 {
		t.Fatalf("started counter = %d, want 1", got)
	}
	if got := runtimeDeps.Metrics.CounterValue("review_run_completed_total", map[string]string{"trigger_type": "mr_open"}); got != 1 {
		t.Fatalf("completed counter = %d, want 1", got)
	}
	if got := runtimeDeps.Metrics.CounterValue("provider_tokens_total", nil); got != 77 {
		t.Fatalf("provider tokens = %d, want 77", got)
	}
	if got := runtimeDeps.Metrics.HistogramValues("provider_latency_ms", nil); !reflect.DeepEqual(got, []int64{25}) {
		t.Fatalf("provider latency samples = %v, want [25]", got)
	}
	if got := runtimeDeps.Metrics.HistogramValues("comment_writer_latency_ms", map[string]string{"status": "completed"}); len(got) != 1 {
		t.Fatalf("writer latency sample count = %d, want 1", len(got))
	}
	spans := runtimeDeps.Tracer.Spans()
	if len(spans) == 0 {
		t.Fatal("expected runtime tracer spans to be recorded")
	}
	traceID := spans[0].TraceID
	for _, span := range spans {
		if span.TraceID != traceID {
			t.Fatalf("span %s trace_id = %q, want %q", span.Name, span.TraceID, traceID)
		}
	}
}

func TestGatePublishesFromRuntimePath(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-gate")
	runID := insertTestRun(t, sqlDB, projectID, mrID, "pending", "runtime-gate", "sha-gate")
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
	processor := scheduler.FuncProcessor(func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error) {
		findings, err := db.New(sqlDB).ListFindingsByRun(ctx, runID)
		if err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		return scheduler.ProcessOutcome{Status: "completed", ReviewFindings: findings}, nil
	})
	runtimeDeps := newRuntimeDepsWithGatePublishers(testLogger(), sqlDB, processor, status, ci)
	runtimeDeps.Scheduler = scheduler.NewService(testLogger(), sqlDB, processor, scheduler.WithWorkerID("worker-gate"), scheduler.WithTracer(tracer), scheduler.WithGateService(runtimeDeps.GateService))

	processed, err := runtimeDeps.Scheduler.RunOnce(ctx)
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

type fakeDiscussionClient struct{}

func (f *fakeDiscussionClient) CreateDiscussion(_ context.Context, req writer.CreateDiscussionRequest) (writer.Discussion, error) {
	return writer.Discussion{ID: req.IdempotencyKey}, nil
}

func (f *fakeDiscussionClient) CreateNote(_ context.Context, req writer.CreateNoteRequest) (writer.Discussion, error) {
	return writer.Discussion{ID: req.IdempotencyKey}, nil
}

func (f *fakeDiscussionClient) ResolveDiscussion(context.Context, writer.ResolveDiscussionRequest) error {
	return nil
}

type runtimeWriterStore struct{ queries *db.Queries }

func (s *runtimeWriterStore) GetLatestMRVersion(ctx context.Context, mergeRequestID int64) (db.MrVersion, error) {
	return s.queries.GetLatestMRVersion(ctx, mergeRequestID)
}

func (s *runtimeWriterStore) GetMergeRequest(ctx context.Context, id int64) (db.MergeRequest, error) {
	return s.queries.GetMergeRequest(ctx, id)
}

func (s *runtimeWriterStore) GetReviewRun(ctx context.Context, id int64) (db.ReviewRun, error) {
	return s.queries.GetReviewRun(ctx, id)
}

func (s *runtimeWriterStore) GetReviewFinding(ctx context.Context, id int64) (db.ReviewFinding, error) {
	return s.queries.GetReviewFinding(ctx, id)
}

func (s *runtimeWriterStore) GetGitlabDiscussion(ctx context.Context, id int64) (db.GitlabDiscussion, error) {
	return s.queries.GetGitlabDiscussion(ctx, id)
}

func (s *runtimeWriterStore) ListFindingsByRun(ctx context.Context, reviewRunID int64) ([]db.ReviewFinding, error) {
	return s.queries.ListFindingsByRun(ctx, reviewRunID)
}

func (s *runtimeWriterStore) ListFindingsByMergeRequest(ctx context.Context, mergeRequestID int64) ([]db.ReviewFinding, error) {
	return s.queries.ListActiveFindingsByMR(ctx, mergeRequestID)
}

func (s *runtimeWriterStore) GetCommentActionByIdempotencyKey(ctx context.Context, idempotencyKey string) (db.CommentAction, error) {
	return s.queries.GetCommentActionByIdempotencyKey(ctx, idempotencyKey)
}

func (s *runtimeWriterStore) GetGitlabDiscussionByFinding(ctx context.Context, reviewFindingID int64) (db.GitlabDiscussion, error) {
	return s.queries.GetGitlabDiscussionByFinding(ctx, reviewFindingID)
}

func (s *runtimeWriterStore) GetGitlabDiscussionByMergeRequestAndFinding(ctx context.Context, arg db.GetGitlabDiscussionByMergeRequestAndFindingParams) (db.GitlabDiscussion, error) {
	return s.queries.GetGitlabDiscussionByMergeRequestAndFinding(ctx, arg)
}

func (s *runtimeWriterStore) InsertCommentAction(ctx context.Context, arg db.InsertCommentActionParams) (sql.Result, error) {
	return s.queries.InsertCommentAction(ctx, arg)
}

func (s *runtimeWriterStore) UpdateCommentActionStatus(ctx context.Context, arg db.UpdateCommentActionStatusParams) error {
	return s.queries.UpdateCommentActionStatus(ctx, arg)
}

func (s *runtimeWriterStore) InsertGitlabDiscussion(ctx context.Context, arg db.InsertGitlabDiscussionParams) (sql.Result, error) {
	return s.queries.InsertGitlabDiscussion(ctx, arg)
}

func (s *runtimeWriterStore) UpdateFindingDiscussionID(ctx context.Context, arg db.UpdateFindingDiscussionIDParams) error {
	return s.queries.UpdateFindingDiscussionID(ctx, arg)
}

func (s *runtimeWriterStore) UpdateGitlabDiscussionResolved(ctx context.Context, arg db.UpdateGitlabDiscussionResolvedParams) error {
	return s.queries.UpdateGitlabDiscussionResolved(ctx, arg)
}

func (s *runtimeWriterStore) UpdateGitlabDiscussionSupersededBy(ctx context.Context, arg db.UpdateGitlabDiscussionSupersededByParams) error {
	return s.queries.UpdateGitlabDiscussionSupersededBy(ctx, arg)
}

func (s *runtimeWriterStore) MarkReviewRunFailedIfRunning(ctx context.Context, arg db.MarkReviewRunFailedParams) (bool, error) {
	return s.queries.MarkReviewRunFailedIfRunning(ctx, arg)
}
