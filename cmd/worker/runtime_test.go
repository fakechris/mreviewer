package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/gitlab"
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
	runtimeWriter := writer.New(&fakeDiscussionClient{}, writer.NewSQLStore(sqlDB)).WithMetrics(runtimeDeps.Metrics).WithTracer(runtimeDeps.Tracer)
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
	runtimeDeps.Scheduler = scheduler.NewService(testLogger(), sqlDB, processor,
		scheduler.WithWorkerID("worker-gate"),
		scheduler.WithTracer(tracer),
		scheduler.WithStatusPublisher(status),
		scheduler.WithGateService(runtimeDeps.GateService),
	)

	processed, err := runtimeDeps.Scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if len(status.results) != 2 {
		t.Fatalf("status publish count = %d, want 2", len(status.results))
	}
	if len(ci.results) != 1 {
		t.Fatalf("ci publish count = %d, want 1", len(ci.results))
	}
	if status.results[0].RunID != runID || status.results[0].State != "running" {
		t.Fatalf("first status result = %+v, want running result for run %d", status.results[0], runID)
	}
	if status.results[1].RunID != runID || status.results[1].State != "failed" {
		t.Fatalf("second status result = %+v, want failed result for run %d", status.results[1], runID)
	}
	if status.results[1].TraceID == "" {
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

func TestWorkerRuntimeWritesBackFindings(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-writeback")
	runID := insertTestRun(t, sqlDB, projectID, mrID, "pending", "runtime-writeback", "sha-writeback")
	if _, err := sqlDB.Exec(`INSERT INTO mr_versions (merge_request_id, gitlab_version_id, base_sha, start_sha, head_sha, patch_id_sha)
		VALUES (?, ?, ?, ?, ?, ?)`, mrID, 1, "base-sha", "start-sha", "sha-writeback", "patch-sha"); err != nil {
		t.Fatalf("insert mr version: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO review_findings (review_run_id, merge_request_id, category, severity, confidence, title, body_markdown, path, anchor_kind, new_line, anchor_snippet, anchor_fingerprint, semantic_fingerprint, state)
		VALUES (?, ?, 'bug', 'high', 0.95, 'Writeback issue', 'body', 'src/main.go', 'new_line', 42, 'snippet', 'anchor-writeback', 'semantic-writeback', 'active')`, runID, mrID); err != nil {
		t.Fatalf("insert finding: %v", err)
	}

	client := &fakeDiscussionClient{}
	processor := scheduler.FuncProcessor(func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error) {
		findings, err := db.New(sqlDB).ListFindingsByRun(ctx, runID)
		if err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		return scheduler.ProcessOutcome{Status: "completed", ReviewFindings: findings}, nil
	})
	runtimeDeps := newRuntimeDepsWithWritebackAndGatePublishers(testLogger(), sqlDB, processor, client, gate.NoopStatusPublisher{}, gate.NoopCIGatePublisher{})

	processed, err := runtimeDeps.Scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if len(client.discussions) != 1 {
		t.Fatalf("discussion requests = %d, want 1", len(client.discussions))
	}
	if len(client.notes) != 1 {
		t.Fatalf("note requests = %d, want 1 summary note", len(client.notes))
	}
	if client.discussions[0].ReviewFindingID == 0 {
		t.Fatalf("discussion request = %+v, want review finding id", client.discussions[0])
	}
	actions, err := db.New(sqlDB).ListCommentActionsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListCommentActionsByRun: %v", err)
	}
	if len(actions) != 2 {
		t.Fatalf("comment action count = %d, want 2", len(actions))
	}
}

func TestWorkerRuntimeWritesBackViaGitLabClient(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-writeback-http")
	runID := insertTestRun(t, sqlDB, projectID, mrID, "pending", "runtime-writeback-http", "sha-writeback-http")
	if _, err := sqlDB.Exec(`INSERT INTO mr_versions (merge_request_id, gitlab_version_id, base_sha, start_sha, head_sha, patch_id_sha)
		VALUES (?, ?, ?, ?, ?, ?)`, mrID, 1, "base-sha", "start-sha", "sha-writeback-http", "patch-sha"); err != nil {
		t.Fatalf("insert mr version: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO review_findings (review_run_id, merge_request_id, category, severity, confidence, title, body_markdown, path, anchor_kind, new_line, anchor_snippet, anchor_fingerprint, semantic_fingerprint, state)
		VALUES (?, ?, 'bug', 'high', 0.95, 'HTTP writeback issue', 'body', 'src/main.go', 'new_line', 42, 'snippet', 'anchor-http', 'semantic-http', 'active')`, runID, mrID); err != nil {
		t.Fatalf("insert finding: %v", err)
	}

	var discussionRequests []map[string]any
	var noteRequests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "test-token" {
			t.Fatalf("PRIVATE-TOKEN = %q, want test-token", got)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v4/projects/101/merge_requests/1/discussions":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode discussion body: %v", err)
			}
			discussionRequests = append(discussionRequests, body)
			writeRuntimeJSON(t, w, http.StatusCreated, map[string]any{"id": "discussion-http"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v4/projects/101/merge_requests/1/notes":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode note body: %v", err)
			}
			noteRequests = append(noteRequests, body)
			writeRuntimeJSON(t, w, http.StatusCreated, map[string]any{"id": 99})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	gitlabClient, err := gitlab.NewClient(server.URL, "test-token", gitlab.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	processor := scheduler.FuncProcessor(func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error) {
		findings, err := db.New(sqlDB).ListFindingsByRun(ctx, runID)
		if err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		return scheduler.ProcessOutcome{Status: "completed", ReviewFindings: findings}, nil
	})
	runtimeDeps := newRuntimeDepsWithWritebackAndGatePublishers(testLogger(), sqlDB, processor, gitlabClient, gate.NoopStatusPublisher{}, gate.NoopCIGatePublisher{})

	processed, err := runtimeDeps.Scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if len(discussionRequests) != 1 {
		t.Fatalf("discussion requests = %d, want 1", len(discussionRequests))
	}
	if len(noteRequests) != 1 {
		t.Fatalf("note requests = %d, want 1", len(noteRequests))
	}
	position, ok := discussionRequests[0]["position"].(map[string]any)
	if !ok {
		t.Fatalf("discussion position = %#v, want object", discussionRequests[0]["position"])
	}
	if position["head_sha"] != "sha-writeback-http" {
		t.Fatalf("position head_sha = %#v, want sha-writeback-http", position["head_sha"])
	}
	if noteRequests[0]["body"] == "" {
		t.Fatal("expected summary note body to be populated")
	}
	discussions, err := db.New(sqlDB).ListCommentActionsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListCommentActionsByRun: %v", err)
	}
	if len(discussions) != 2 {
		t.Fatalf("comment action count = %d, want 2", len(discussions))
	}
}

func TestWorkerRuntimeAllowsProcessorManagedCompletedStatus(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-processor-completed")
	runID := insertTestRun(t, sqlDB, projectID, mrID, "pending", "runtime-processor-completed", "sha-processor-completed")
	if _, err := sqlDB.Exec(`INSERT INTO mr_versions (merge_request_id, gitlab_version_id, base_sha, start_sha, head_sha, patch_id_sha)
		VALUES (?, ?, ?, ?, ?, ?)`, mrID, 1, "base-sha", "start-sha", "sha-processor-completed", "patch-sha"); err != nil {
		t.Fatalf("insert mr version: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO review_findings (review_run_id, merge_request_id, category, severity, confidence, title, body_markdown, path, anchor_kind, new_line, anchor_snippet, anchor_fingerprint, semantic_fingerprint, state)
		VALUES (?, ?, 'bug', 'high', 0.95, 'Processor-managed completion', 'body', 'src/main.go', 'new_line', 42, 'snippet', 'anchor-processor-completed', 'semantic-processor-completed', 'active')`, runID, mrID); err != nil {
		t.Fatalf("insert finding: %v", err)
	}

	client := &fakeDiscussionClient{}
	queries := db.New(sqlDB)
	processor := scheduler.FuncProcessor(func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error) {
		findings, err := queries.ListFindingsByRun(ctx, runID)
		if err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		if err := queries.UpdateReviewRunStatus(ctx, db.UpdateReviewRunStatusParams{ID: runID, Status: "completed"}); err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		return scheduler.ProcessOutcome{Status: "completed", ProviderLatencyMs: 53, ProviderTokensTotal: 1200, ReviewFindings: findings}, nil
	})
	runtimeDeps := newRuntimeDepsWithWritebackAndGatePublishers(testLogger(), sqlDB, processor, client, gate.NoopStatusPublisher{}, gate.NoopCIGatePublisher{})

	processed, err := runtimeDeps.Scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if len(client.discussions) != 1 {
		t.Fatalf("discussion requests = %d, want 1", len(client.discussions))
	}
	if len(client.notes) != 1 {
		t.Fatalf("note requests = %d, want 1", len(client.notes))
	}
	run, err := queries.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.ProviderLatencyMs != 53 {
		t.Fatalf("provider_latency_ms = %d, want 53", run.ProviderLatencyMs)
	}
	if run.ProviderTokensTotal != 1200 {
		t.Fatalf("provider_tokens_total = %d, want 1200", run.ProviderTokensTotal)
	}
}

func TestWorkerRuntimeAllowsProcessorManagedParserErrorStatus(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-processor-parser-error")
	runID := insertTestRun(t, sqlDB, projectID, mrID, "pending", "runtime-processor-parser-error", "sha-processor-parser-error")

	client := &fakeDiscussionClient{}
	queries := db.New(sqlDB)
	processor := scheduler.FuncProcessor(func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error) {
		if err := queries.UpdateReviewRunStatus(ctx, db.UpdateReviewRunStatusParams{ID: runID, Status: "parser_error"}); err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		return scheduler.ProcessOutcome{Status: "parser_error", ProviderLatencyMs: 41, ProviderTokensTotal: 900}, nil
	})
	runtimeDeps := newRuntimeDepsWithWritebackAndGatePublishers(testLogger(), sqlDB, processor, client, gate.NoopStatusPublisher{}, gate.NoopCIGatePublisher{})

	processed, err := runtimeDeps.Scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if len(client.discussions) != 0 {
		t.Fatalf("discussion requests = %d, want 0", len(client.discussions))
	}
	if len(client.notes) != 1 {
		t.Fatalf("note requests = %d, want 1 parser-error note", len(client.notes))
	}
	run, err := queries.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.ProviderLatencyMs != 41 {
		t.Fatalf("provider_latency_ms = %d, want 41", run.ProviderLatencyMs)
	}
	if run.ProviderTokensTotal != 900 {
		t.Fatalf("provider_tokens_total = %d, want 900", run.ProviderTokensTotal)
	}
}

func TestWorkerRuntimeAllowsProcessorManagedRequestedChangesStatus(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-processor-requested-changes")
	runID := insertTestRun(t, sqlDB, projectID, mrID, "pending", "runtime-processor-requested-changes", "sha-processor-requested-changes")
	if _, err := sqlDB.Exec(`INSERT INTO mr_versions (merge_request_id, gitlab_version_id, base_sha, start_sha, head_sha, patch_id_sha)
		VALUES (?, ?, ?, ?, ?, ?)`, mrID, 1, "base-sha", "start-sha", "sha-processor-requested-changes", "patch-sha"); err != nil {
		t.Fatalf("insert mr version: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO review_findings (review_run_id, merge_request_id, category, severity, confidence, title, body_markdown, path, anchor_kind, new_line, anchor_snippet, anchor_fingerprint, semantic_fingerprint, state)
		VALUES (?, ?, 'bug', 'high', 0.95, 'Processor-managed requested changes', 'body', 'src/main.go', 'new_line', 42, 'snippet', 'anchor-processor-requested-changes', 'semantic-processor-requested-changes', 'active')`, runID, mrID); err != nil {
		t.Fatalf("insert finding: %v", err)
	}

	client := &fakeDiscussionClient{}
	queries := db.New(sqlDB)
	processor := scheduler.FuncProcessor(func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error) {
		findings, err := queries.ListFindingsByRun(ctx, runID)
		if err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		if err := queries.UpdateReviewRunStatus(ctx, db.UpdateReviewRunStatusParams{ID: runID, Status: "requested_changes"}); err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		return scheduler.ProcessOutcome{Status: "requested_changes", ProviderLatencyMs: 37, ProviderTokensTotal: 640, ReviewFindings: findings}, nil
	})
	runtimeDeps := newRuntimeDepsWithWritebackAndGatePublishers(testLogger(), sqlDB, processor, client, gate.NoopStatusPublisher{}, gate.NoopCIGatePublisher{})

	processed, err := runtimeDeps.Scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if len(client.discussions) != 1 {
		t.Fatalf("discussion requests = %d, want 1", len(client.discussions))
	}
	if len(client.notes) != 1 {
		t.Fatalf("note requests = %d, want 1 summary note", len(client.notes))
	}
	run, err := queries.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "requested_changes" {
		t.Fatalf("run status = %q, want requested_changes", run.Status)
	}
	if run.ProviderLatencyMs != 37 {
		t.Fatalf("provider_latency_ms = %d, want 37", run.ProviderLatencyMs)
	}
	if run.ProviderTokensTotal != 640 {
		t.Fatalf("provider_tokens_total = %d, want 640", run.ProviderTokensTotal)
	}
}

func TestWorkerRuntimeAllowsWriterManagedFailedStatus(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-writer-failed")
	runID := insertTestRun(t, sqlDB, projectID, mrID, "pending", "runtime-writer-failed", "sha-writer-failed")
	if _, err := sqlDB.Exec(`INSERT INTO mr_versions (merge_request_id, gitlab_version_id, base_sha, start_sha, head_sha, patch_id_sha)
		VALUES (?, ?, ?, ?, ?, ?)`, mrID, 1, "base-sha", "start-sha", "sha-writer-failed", "patch-sha"); err != nil {
		t.Fatalf("insert mr version: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO review_findings (review_run_id, merge_request_id, category, severity, confidence, title, body_markdown, path, anchor_kind, new_line, anchor_snippet, anchor_fingerprint, semantic_fingerprint, state)
		VALUES (?, ?, 'bug', 'high', 0.95, 'Writer-managed failure', 'body', 'src/main.go', 'new_line', 42, 'snippet', 'anchor-writer-failed', 'semantic-writer-failed', 'active')`, runID, mrID); err != nil {
		t.Fatalf("insert finding: %v", err)
	}

	client := &fakeDiscussionClient{discussionErr: errors.New("gitlab unavailable")}
	processor := scheduler.FuncProcessor(func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error) {
		findings, err := db.New(sqlDB).ListFindingsByRun(ctx, runID)
		if err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		return scheduler.ProcessOutcome{Status: "completed", ReviewFindings: findings}, nil
	})
	runtimeDeps := newRuntimeDepsWithWritebackAndGatePublishers(testLogger(), sqlDB, processor, client, gate.NoopStatusPublisher{}, gate.NoopCIGatePublisher{})

	processed, err := runtimeDeps.Scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	run, err := db.New(sqlDB).GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	if run.Status != "failed" {
		t.Fatalf("run status = %q, want failed", run.Status)
	}
	if run.ErrorCode == "" {
		t.Fatal("expected run error code to be persisted")
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

type fakeDiscussionClient struct {
	discussions    []writer.CreateDiscussionRequest
	notes          []writer.CreateNoteRequest
	resolveRequest []writer.ResolveDiscussionRequest
	discussionErr  error
	noteErr        error
	resolveErr     error
}

func (f *fakeDiscussionClient) CreateDiscussion(_ context.Context, req writer.CreateDiscussionRequest) (writer.Discussion, error) {
	f.discussions = append(f.discussions, req)
	if f.discussionErr != nil {
		return writer.Discussion{}, f.discussionErr
	}
	return writer.Discussion{ID: req.IdempotencyKey}, nil
}

func (f *fakeDiscussionClient) CreateNote(_ context.Context, req writer.CreateNoteRequest) (writer.Discussion, error) {
	f.notes = append(f.notes, req)
	if f.noteErr != nil {
		return writer.Discussion{}, f.noteErr
	}
	return writer.Discussion{ID: req.IdempotencyKey}, nil
}

func (f *fakeDiscussionClient) ResolveDiscussion(_ context.Context, req writer.ResolveDiscussionRequest) error {
	f.resolveRequest = append(f.resolveRequest, req)
	return f.resolveErr
}

func writeRuntimeJSON(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
