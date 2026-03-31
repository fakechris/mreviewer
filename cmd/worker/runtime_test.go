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
	"time"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/llm"
	platformgithub "github.com/mreviewer/mreviewer/internal/platform/github"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewpack"
	"github.com/mreviewer/mreviewer/internal/rules"
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
	if runtimeDeps.Heartbeat == nil {
		t.Fatal("expected heartbeat service to be configured")
	}
	if runtimeDeps.HeartbeatIdentity.WorkerID == "" {
		t.Fatal("expected heartbeat worker identity to be configured")
	}
	if runtimeDeps.HeartbeatIdentity.ConfiguredConcurrency != 4 {
		t.Fatalf("configured concurrency = %d, want 4", runtimeDeps.HeartbeatIdentity.ConfiguredConcurrency)
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

type fakeRuntimeBundleWriteback struct {
	writeCalls       int
	writeBundleCalls int
	lastRun          db.ReviewRun
	lastBundle       core.ReviewBundle
}

func (f *fakeRuntimeBundleWriteback) Write(_ context.Context, run db.ReviewRun, _ []db.ReviewFinding) error {
	f.writeCalls++
	f.lastRun = run
	return nil
}

func (f *fakeRuntimeBundleWriteback) WriteBundle(_ context.Context, run db.ReviewRun, bundle core.ReviewBundle) error {
	f.writeBundleCalls++
	f.lastRun = run
	f.lastBundle = bundle
	return nil
}

func TestWrapProcessorWithWritebackPrefersBundleWritebackFromOutcome(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-runtime-bundle")
	runID := insertTestRun(t, sqlDB, projectID, mrID, "pending", "runtime-bundle", "sha-runtime-bundle")

	processor := scheduler.FuncProcessor(func(context.Context, db.ReviewRun) (scheduler.ProcessOutcome, error) {
		return scheduler.ProcessOutcome{
			Status: "requested_changes",
			ReviewBundle: core.ReviewBundle{
				Verdict: "requested_changes",
				Target: core.ReviewTarget{
					Platform:     core.PlatformGitLab,
					ProjectID:    101,
					ChangeNumber: 1,
				},
				PublishCandidates: []core.PublishCandidate{{Kind: "summary", Body: "judge summary"}},
			},
		}, nil
	})
	writeback := &fakeRuntimeBundleWriteback{}
	wrapped := wrapProcessorWithWriteback(sqlDB, processor, writeback, defaultRuntimeNewStore)

	run, err := db.New(sqlDB).GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := wrapped.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if outcome.Status != "requested_changes" {
		t.Fatalf("outcome status = %q, want requested_changes", outcome.Status)
	}
	if writeback.writeBundleCalls != 1 {
		t.Fatalf("bundle writeback calls = %d, want 1", writeback.writeBundleCalls)
	}
	if writeback.writeCalls != 0 {
		t.Fatalf("legacy write calls = %d, want 0", writeback.writeCalls)
	}
	if writeback.lastBundle.Verdict != "requested_changes" {
		t.Fatalf("bundle verdict = %q, want requested_changes", writeback.lastBundle.Verdict)
	}
	if writeback.lastRun.Status != "requested_changes" {
		t.Fatalf("run status = %q, want requested_changes", writeback.lastRun.Status)
	}
}

func TestNewReviewRunProcessorProcessesRunViaNewEngine(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-processor-engine")
	runID := insertTestRun(t, sqlDB, projectID, mrID, "pending", "runtime-new-engine", "sha-processor-engine")
	if _, err := sqlDB.Exec(`UPDATE review_runs SET scope_json = ? WHERE id = ?`, db.NullRawMessage([]byte(`{"provider_route":"claude-opus-4-1"}`)), runID); err != nil {
		t.Fatalf("update scope_json: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/101/merge_requests/1":
			writeRuntimeJSON(t, w, http.StatusOK, map[string]any{
				"id":            501,
				"iid":           1,
				"project_id":    101,
				"title":         "Engine-backed worker path",
				"description":   "test",
				"state":         "opened",
				"draft":         false,
				"source_branch": "feature",
				"target_branch": "main",
				"sha":           "sha-processor-engine",
				"web_url":       "https://test.gitlab.com/group/project/-/merge_requests/1",
				"diff_refs": map[string]any{
					"base_sha":  "base-sha",
					"start_sha": "start-sha",
					"head_sha":  "sha-processor-engine",
				},
				"author": map[string]any{"username": "worker-test"},
			})
		case "/api/v4/projects/101/merge_requests/1/versions":
			writeRuntimeJSON(t, w, http.StatusOK, []map[string]any{{
				"id":               1,
				"head_commit_sha":  "sha-processor-engine",
				"base_commit_sha":  "base-sha",
				"start_commit_sha": "start-sha",
				"patch_id_sha":     "patch-sha",
				"created_at":       time.Date(2026, time.March, 30, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
				"merge_request_id": 501,
				"state":            "collected",
				"real_size":        "1",
			}})
		case "/api/v4/projects/101/merge_requests/1/diffs":
			writeRuntimeJSON(t, w, http.StatusOK, []map[string]any{{
				"old_path":     "internal/legacy.go",
				"new_path":     "internal/legacy.go",
				"diff":         "@@ -1 +1 @@\n-old\n+new\n",
				"new_file":     false,
				"renamed_file": false,
				"deleted_file": false,
			}})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := gitlab.NewClient(server.URL, "test-token", gitlab.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	loader := &testWorkerRulesLoader{
		result: rules.LoadResult{
			EffectivePolicy: rules.EffectivePolicy{
				ProviderRoute: "default",
			},
		},
	}
	defaultProvider := &fakeWorkerDynamicProvider{}
	overrideProvider := &fakeWorkerDynamicProvider{
		response: llm.ProviderResponse{
			Result: llm.ReviewResult{
				SchemaVersion: "1.0",
				ReviewRunID:   "runtime-new-engine",
				Summary:       "Found a real issue",
				Status:        "requested_changes",
				Findings: []llm.ReviewFinding{{
					Category:     "bug",
					Severity:     "high",
					Confidence:   0.93,
					Title:        "Unsafe mutation path",
					BodyMarkdown: "The update path skips validation.",
					Path:         "internal/legacy.go",
					AnchorKind:   "new_line",
					NewLine:      int32PtrRuntime(1),
				}},
			},
		},
	}
	registry := llm.NewProviderRegistry(slog.New(slog.NewJSONHandler(io.Discard, nil)), "default", defaultProvider)
	registry.Register("claude-opus-4-1", overrideProvider)

	processor, err := newReviewRunProcessor(&config.Config{}, sqlDB, client, nil, loader, registry)
	if err != nil {
		t.Fatalf("newReviewRunProcessor: %v", err)
	}
	run, err := db.New(sqlDB).GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}

	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}

	if outcome.Status != "requested_changes" {
		t.Fatalf("outcome.Status = %q, want requested_changes", outcome.Status)
	}
	bundle, ok := outcome.ReviewBundle.(core.ReviewBundle)
	if !ok {
		t.Fatalf("outcome review bundle type = %T, want reviewcore.ReviewBundle", outcome.ReviewBundle)
	}
	if bundle.Verdict != "requested_changes" {
		t.Fatalf("outcome review bundle verdict = %q, want requested_changes", bundle.Verdict)
	}
	if len(bundle.Artifacts) != len(reviewpack.DefaultPacks()) {
		t.Fatalf("outcome review bundle artifacts = %d, want %d", len(bundle.Artifacts), len(reviewpack.DefaultPacks()))
	}
	if len(bundle.PublishCandidates) == 0 {
		t.Fatal("expected outcome review bundle publish candidates to be populated")
	}
	if len(outcome.ReviewFindings) != 1 {
		t.Fatalf("outcome findings = %d, want 1", len(outcome.ReviewFindings))
	}
	if defaultProvider.calls != 0 {
		t.Fatalf("default provider calls = %d, want 0", defaultProvider.calls)
	}
	if overrideProvider.calls != len(reviewpack.DefaultPacks()) {
		t.Fatalf("override provider calls = %d, want %d", overrideProvider.calls, len(reviewpack.DefaultPacks()))
	}

	updatedRun, err := db.New(sqlDB).GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun(updated): %v", err)
	}
	if updatedRun.Status != "requested_changes" {
		t.Fatalf("run status = %q, want requested_changes", updatedRun.Status)
	}
	if loader.input.ProjectID != 101 {
		t.Fatalf("rules loader project_id = %d, want 101", loader.input.ProjectID)
	}
	if len(loader.input.ChangedPaths) != 1 || loader.input.ChangedPaths[0] != "internal/legacy.go" {
		t.Fatalf("changed paths = %v, want [internal/legacy.go]", loader.input.ChangedPaths)
	}
}

func TestWorkerRuntimeProcessesNewEngineRunViaBundleWriteback(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	instanceID := insertTestInstance(t, sqlDB)
	projectID := insertTestProject(t, sqlDB, instanceID)
	mrID := insertTestMR(t, sqlDB, projectID, 1, "sha-runtime-new-engine-writeback")
	runID := insertTestRun(t, sqlDB, projectID, mrID, "pending", "runtime-new-engine-writeback", "sha-runtime-new-engine-writeback")
	if _, err := sqlDB.Exec(`UPDATE review_runs SET scope_json = ? WHERE id = ?`, db.NullRawMessage([]byte(`{"provider_route":"claude-opus-4-1"}`)), runID); err != nil {
		t.Fatalf("update scope_json: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/101/merge_requests/1":
			writeRuntimeJSON(t, w, http.StatusOK, map[string]any{
				"id":            501,
				"iid":           1,
				"project_id":    101,
				"title":         "Runtime engine bundle writeback",
				"description":   "test",
				"state":         "opened",
				"draft":         false,
				"source_branch": "feature",
				"target_branch": "main",
				"sha":           "sha-runtime-new-engine-writeback",
				"web_url":       "https://test.gitlab.com/group/project/-/merge_requests/1",
				"diff_refs": map[string]any{
					"base_sha":  "base-sha",
					"start_sha": "start-sha",
					"head_sha":  "sha-runtime-new-engine-writeback",
				},
				"author": map[string]any{"username": "worker-test"},
			})
		case "/api/v4/projects/101/merge_requests/1/versions":
			writeRuntimeJSON(t, w, http.StatusOK, []map[string]any{{
				"id":               1,
				"head_commit_sha":  "sha-runtime-new-engine-writeback",
				"base_commit_sha":  "base-sha",
				"start_commit_sha": "start-sha",
				"patch_id_sha":     "patch-sha",
				"created_at":       time.Date(2026, time.March, 30, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
				"merge_request_id": 501,
				"state":            "collected",
				"real_size":        "1",
			}})
		case "/api/v4/projects/101/merge_requests/1/diffs":
			writeRuntimeJSON(t, w, http.StatusOK, []map[string]any{{
				"old_path":     "internal/legacy.go",
				"new_path":     "internal/legacy.go",
				"diff":         "@@ -1 +1 @@\n-old\n+new\n",
				"new_file":     false,
				"renamed_file": false,
				"deleted_file": false,
			}})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := gitlab.NewClient(server.URL, "test-token", gitlab.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	loader := &testWorkerRulesLoader{
		result: rules.LoadResult{
			EffectivePolicy: rules.EffectivePolicy{
				ProviderRoute: "default",
			},
		},
	}
	defaultProvider := &fakeWorkerDynamicProvider{}
	overrideProvider := &fakeWorkerDynamicProvider{
		response: llm.ProviderResponse{
			Result: llm.ReviewResult{
				SchemaVersion: "1.0",
				ReviewRunID:   "runtime-new-engine-writeback",
				Summary:       "Found a runtime issue",
				Status:        "requested_changes",
				Findings: []llm.ReviewFinding{{
					Category:     "bug",
					Severity:     "high",
					Confidence:   0.93,
					Title:        "Unsafe mutation path",
					BodyMarkdown: "The update path skips validation.",
					Path:         "internal/legacy.go",
					AnchorKind:   "new_line",
					NewLine:      int32PtrRuntime(1),
				}},
			},
		},
	}
	registry := llm.NewProviderRegistry(slog.New(slog.NewJSONHandler(io.Discard, nil)), "default", defaultProvider)
	registry.Register("claude-opus-4-1", overrideProvider)

	processor, err := newReviewRunProcessor(&config.Config{}, sqlDB, client, nil, loader, registry)
	if err != nil {
		t.Fatalf("newReviewRunProcessor: %v", err)
	}
	fakeClient := &fakeDiscussionClient{}
	runtimeDeps := newRuntimeDepsWithWritebackAndGatePublishers(testLogger(), sqlDB, processor, fakeClient, gate.NoopStatusPublisher{}, gate.NoopCIGatePublisher{})

	processed, err := runtimeDeps.Scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if len(fakeClient.discussions) != 1 {
		t.Fatalf("discussion requests = %d, want 1", len(fakeClient.discussions))
	}
	if len(fakeClient.notes) != 1 {
		t.Fatalf("note requests = %d, want 1", len(fakeClient.notes))
	}
	if fakeClient.notes[0].Body == "" {
		t.Fatal("expected summary note body to be populated")
	}
	if fakeClient.discussions[0].Body == "" {
		t.Fatal("expected discussion body to be populated")
	}
	updatedRun, err := db.New(sqlDB).GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun(updated): %v", err)
	}
	if updatedRun.Status != "requested_changes" {
		t.Fatalf("run status = %q, want requested_changes", updatedRun.Status)
	}
	if defaultProvider.calls != 0 {
		t.Fatalf("default provider calls = %d, want 0", defaultProvider.calls)
	}
	if overrideProvider.calls != len(reviewpack.DefaultPacks()) {
		t.Fatalf("override provider calls = %d, want %d", overrideProvider.calls, len(reviewpack.DefaultPacks()))
	}
	actions, err := db.New(sqlDB).ListCommentActionsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListCommentActionsByRun: %v", err)
	}
	if len(actions) != 2 {
		t.Fatalf("comment action count = %d, want 2", len(actions))
	}
}

func TestWorkerRuntimeProcessesGitHubRunViaBundleWriteback(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	queries := db.New(sqlDB)

	instRes, err := sqlDB.Exec(`INSERT INTO gitlab_instances (url, name) VALUES ('https://github.com', 'GitHub')`)
	if err != nil {
		t.Fatalf("insert gitlab_instances: %v", err)
	}
	instanceID, _ := instRes.LastInsertId()
	projectID := insertTestProject(t, sqlDB, instanceID)
	if _, err := sqlDB.Exec(`UPDATE projects SET gitlab_project_id = ?, path_with_namespace = ? WHERE id = ?`, 202, "acme/repo", projectID); err != nil {
		t.Fatalf("update project: %v", err)
	}
	if _, err := queries.UpsertMergeRequest(ctx, db.UpsertMergeRequestParams{
		ProjectID:    projectID,
		MrIid:        19,
		Title:        "GitHub runtime writeback",
		State:        "opened",
		SourceBranch: "feature",
		TargetBranch: "main",
		HeadSha:      "github-runtime-writeback-sha",
		WebUrl:       "https://github.com/acme/repo/pull/19",
		Author:       "octocat",
	}); err != nil {
		t.Fatalf("UpsertMergeRequest: %v", err)
	}
	mr, err := queries.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{
		ProjectID: projectID,
		MrIid:     19,
	})
	if err != nil {
		t.Fatalf("GetMergeRequestByProjectMR: %v", err)
	}
	runID := insertTestRun(t, sqlDB, projectID, mr.ID, "pending", "github-runtime-writeback", "github-runtime-writeback-sha")
	if _, err := sqlDB.Exec(`UPDATE review_runs SET scope_json = ? WHERE id = ?`, db.NullRawMessage([]byte(`{"provider_route":"claude-opus-4-1","platform":"github"}`)), runID); err != nil {
		t.Fatalf("update scope_json: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/repo/pulls/19":
			writeRuntimeJSON(t, w, http.StatusOK, map[string]any{
				"id":       602,
				"number":   19,
				"title":    "GitHub runtime writeback",
				"body":     "body",
				"state":    "open",
				"draft":    false,
				"html_url": "https://github.com/acme/repo/pull/19",
				"user":     map[string]any{"login": "octocat"},
				"base":     map[string]any{"ref": "main", "sha": "base-sha"},
				"head":     map[string]any{"ref": "feature", "sha": "github-runtime-writeback-sha"},
			})
		case "/repos/acme/repo/pulls/19/files":
			if r.URL.Query().Get("page") == "1" || r.URL.Query().Get("page") == "" {
				writeRuntimeJSON(t, w, http.StatusOK, []map[string]any{{
					"filename": "internal/legacy.go",
					"status":   "modified",
					"patch":    "@@ -1 +1 @@\n-old\n+new\n",
				}})
				return
			}
			writeRuntimeJSON(t, w, http.StatusOK, []map[string]any{})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	githubClient, err := platformgithub.NewClient(server.URL, "test-token", platformgithub.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	loader := &testWorkerRulesLoader{
		result: rules.LoadResult{
			EffectivePolicy: rules.EffectivePolicy{ProviderRoute: "default"},
		},
	}
	defaultProvider := &fakeWorkerDynamicProvider{}
	overrideProvider := &fakeWorkerDynamicProvider{
		response: llm.ProviderResponse{
			Result: llm.ReviewResult{
				SchemaVersion: "1.0",
				ReviewRunID:   "github-runtime-writeback",
				Summary:       "Found a GitHub runtime issue",
				Status:        "requested_changes",
				Findings: []llm.ReviewFinding{{
					Category:     "bug",
					Severity:     "high",
					Confidence:   0.93,
					Title:        "Unsafe mutation path",
					BodyMarkdown: "The update path skips validation.",
					Path:         "internal/legacy.go",
					AnchorKind:   "new_line",
					NewLine:      int32PtrRuntime(1),
				}},
			},
		},
	}
	registry := llm.NewProviderRegistry(slog.New(slog.NewJSONHandler(io.Discard, nil)), "default", defaultProvider)
	registry.Register("claude-opus-4-1", overrideProvider)

	cfg := &config.Config{
		GitHubBaseURL: server.URL,
		GitHubToken:   "test-token",
	}
	processor, err := newReviewRunProcessor(cfg, sqlDB, nil, githubClient, loader, registry)
	if err != nil {
		t.Fatalf("newReviewRunProcessor: %v", err)
	}
	githubPublishClient := &fakeGitHubPublishClient{}
	runtimeDeps := newRuntimeDepsWithPlatformWritebacksAndGatePublishers(testLogger(), sqlDB, processor, nil, githubPublishClient, gate.NoopStatusPublisher{}, gate.NoopCIGatePublisher{}, defaultRuntimeNewStore)

	processed, err := runtimeDeps.Scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if len(githubPublishClient.issueComments) != 1 {
		t.Fatalf("issue comments = %d, want 1", len(githubPublishClient.issueComments))
	}
	if len(githubPublishClient.reviewComments) != 1 {
		t.Fatalf("review comments = %d, want 1", len(githubPublishClient.reviewComments))
	}
}

func TestNewReviewRunProcessorProcessesGitHubRunViaNewEngine(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	queries := db.New(sqlDB)

	instRes, err := sqlDB.Exec(`INSERT INTO gitlab_instances (url, name) VALUES ('https://github.com', 'GitHub')`)
	if err != nil {
		t.Fatalf("insert gitlab_instances: %v", err)
	}
	instanceID, _ := instRes.LastInsertId()
	projectID := insertTestProject(t, sqlDB, instanceID)
	if _, err := sqlDB.Exec(`UPDATE projects SET gitlab_project_id = ?, path_with_namespace = ? WHERE id = ?`, 202, "acme/repo", projectID); err != nil {
		t.Fatalf("update project: %v", err)
	}
	if _, err := queries.UpsertMergeRequest(ctx, db.UpsertMergeRequestParams{
		ProjectID:    projectID,
		MrIid:        18,
		Title:        "GitHub automatic review",
		State:        "opened",
		SourceBranch: "feature",
		TargetBranch: "main",
		HeadSha:      "github-head-sha",
		WebUrl:       "https://github.com/acme/repo/pull/18",
		Author:       "octocat",
	}); err != nil {
		t.Fatalf("UpsertMergeRequest: %v", err)
	}
	mr, err := queries.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{
		ProjectID: projectID,
		MrIid:     18,
	})
	if err != nil {
		t.Fatalf("GetMergeRequestByProjectMR: %v", err)
	}
	runID := insertTestRun(t, sqlDB, projectID, mr.ID, "pending", "github-runtime-new-engine", "github-head-sha")
	if _, err := sqlDB.Exec(`UPDATE review_runs SET scope_json = ? WHERE id = ?`, db.NullRawMessage([]byte(`{"provider_route":"claude-opus-4-1","platform":"github"}`)), runID); err != nil {
		t.Fatalf("update scope_json: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/repo/pulls/18":
			writeRuntimeJSON(t, w, http.StatusOK, map[string]any{
				"id":       601,
				"number":   18,
				"title":    "GitHub automatic review",
				"body":     "body",
				"state":    "open",
				"draft":    false,
				"html_url": "https://github.com/acme/repo/pull/18",
				"user":     map[string]any{"login": "octocat"},
				"base":     map[string]any{"ref": "main", "sha": "base-sha"},
				"head":     map[string]any{"ref": "feature", "sha": "github-head-sha"},
			})
		case "/repos/acme/repo/pulls/18/files":
			if r.URL.Query().Get("page") == "1" || r.URL.Query().Get("page") == "" {
				writeRuntimeJSON(t, w, http.StatusOK, []map[string]any{{
					"filename": "internal/legacy.go",
					"status":   "modified",
					"patch":    "@@ -1 +1 @@\n-old\n+new\n",
				}})
				return
			}
			writeRuntimeJSON(t, w, http.StatusOK, []map[string]any{})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	githubClient, err := platformgithub.NewClient(server.URL, "test-token", platformgithub.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	loader := &testWorkerRulesLoader{
		result: rules.LoadResult{
			EffectivePolicy: rules.EffectivePolicy{ProviderRoute: "default"},
		},
	}
	defaultProvider := &fakeWorkerDynamicProvider{}
	overrideProvider := &fakeWorkerDynamicProvider{
		response: llm.ProviderResponse{
			Result: llm.ReviewResult{
				SchemaVersion: "1.0",
				ReviewRunID:   "github-runtime-new-engine",
				Summary:       "Found a GitHub issue",
				Status:        "requested_changes",
				Findings: []llm.ReviewFinding{{
					Category:     "bug",
					Severity:     "high",
					Confidence:   0.93,
					Title:        "Unsafe mutation path",
					BodyMarkdown: "The update path skips validation.",
					Path:         "internal/legacy.go",
					AnchorKind:   "new_line",
					NewLine:      int32PtrRuntime(1),
				}},
			},
		},
	}
	registry := llm.NewProviderRegistry(slog.New(slog.NewJSONHandler(io.Discard, nil)), "default", defaultProvider)
	registry.Register("claude-opus-4-1", overrideProvider)

	cfg := &config.Config{
		GitHubBaseURL: server.URL,
		GitHubToken:   "test-token",
	}
	processor, err := newReviewRunProcessor(cfg, sqlDB, nil, githubClient, loader, registry)
	if err != nil {
		t.Fatalf("newReviewRunProcessor: %v", err)
	}
	run, err := db.New(sqlDB).GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}

	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if outcome.Status != "requested_changes" {
		t.Fatalf("outcome.Status = %q, want requested_changes", outcome.Status)
	}
	if loader.input.RepositoryRef != "acme/repo" {
		t.Fatalf("rules loader repository_ref = %q, want acme/repo", loader.input.RepositoryRef)
	}
	if overrideProvider.calls != len(reviewpack.DefaultPacks()) {
		t.Fatalf("override provider calls = %d, want %d", overrideProvider.calls, len(reviewpack.DefaultPacks()))
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

type fakeGitHubPublishClient struct {
	issueComments  []platformgithub.CreateIssueCommentRequest
	reviewComments []platformgithub.CreateReviewCommentRequest
}

func (f *fakeGitHubPublishClient) GetPullRequestSnapshotByRepositoryRef(_ context.Context, repositoryRef string, pullNumber int64) (platformgithub.PullRequestSnapshot, error) {
	return platformgithub.PullRequestSnapshot{
		PullRequest: platformgithub.PullRequest{
			ID:      602,
			Number:  pullNumber,
			HTMLURL: "https://github.com/" + repositoryRef + "/pull/19",
			HeadSHA: "github-runtime-writeback-sha",
		},
	}, nil
}

func (f *fakeGitHubPublishClient) CreateIssueComment(_ context.Context, req platformgithub.CreateIssueCommentRequest) error {
	f.issueComments = append(f.issueComments, req)
	return nil
}

func (f *fakeGitHubPublishClient) CreateReviewComment(_ context.Context, req platformgithub.CreateReviewCommentRequest) error {
	f.reviewComments = append(f.reviewComments, req)
	return nil
}

type testWorkerRulesLoader struct {
	input  rules.LoadInput
	result rules.LoadResult
}

func (f *testWorkerRulesLoader) Load(_ context.Context, input rules.LoadInput) (rules.LoadResult, error) {
	f.input = input
	return f.result, nil
}

type fakeWorkerDynamicProvider struct {
	response      llm.ProviderResponse
	err           error
	calls         int
	systemPrompts []string
	requests      []ctxpkg.ReviewRequest
}

func (f *fakeWorkerDynamicProvider) Review(ctx context.Context, request ctxpkg.ReviewRequest) (llm.ProviderResponse, error) {
	return f.ReviewWithSystemPrompt(ctx, request, "")
}

func (f *fakeWorkerDynamicProvider) ReviewWithSystemPrompt(_ context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (llm.ProviderResponse, error) {
	f.calls++
	f.systemPrompts = append(f.systemPrompts, systemPrompt)
	f.requests = append(f.requests, request)
	if f.err != nil {
		return llm.ProviderResponse{}, f.err
	}
	return f.response, nil
}

func (f *fakeWorkerDynamicProvider) RequestPayload(_ ctxpkg.ReviewRequest) map[string]any {
	return map[string]any{"provider": "fake-worker"}
}

func (f *fakeWorkerDynamicProvider) RequestPayloadWithSystemPrompt(_ ctxpkg.ReviewRequest, systemPrompt string) map[string]any {
	return map[string]any{"provider": "fake-worker", "system_prompt": systemPrompt}
}

func int32PtrRuntime(v int32) *int32 {
	return &v
}

func writeRuntimeJSON(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
