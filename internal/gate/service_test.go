package gate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
)

func TestExternalStatusAdapter(t *testing.T) {
	status := &fakeStatusPublisher{}
	ci := &fakeCIPublisher{}
	svc := NewService(status, ci, nil)
	result := Result{RunID: 55, State: "failed", BlockingFindings: 1}
	if err := svc.Publish(context.Background(), result); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(status.results) != 1 {
		t.Fatalf("status publish count = %d, want 1", len(status.results))
	}
	if len(ci.results) != 1 {
		t.Fatalf("ci publish count = %d, want 1", len(ci.results))
	}
}

func TestCIGateAdapter(t *testing.T) {
	status := &fakeStatusPublisher{}
	ci := &fakeCIPublisher{}
	svc := NewService(status, ci, nil)
	result := Result{RunID: 77, State: "passed", Summary: "gate passed"}
	if err := svc.Publish(context.Background(), result); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if ci.results[0].RunID != 77 || ci.results[0].State != "passed" {
		t.Fatalf("ci result = %+v, want run 77 passed", ci.results[0])
	}
}

func TestGateQualifyingFindings(t *testing.T) {
	policy := &db.ProjectPolicy{ConfidenceThreshold: 0.8, SeverityThreshold: "medium", GateMode: "threads_resolved"}
	run := db.ReviewRun{ID: 55, ProjectID: 12, MergeRequestID: 34, HeadSha: "abc"}
	findings := []db.ReviewFinding{
		{ID: 1, Severity: "high", Confidence: 0.91, State: "active"},
		{ID: 2, Severity: "nit", Confidence: 0.99, State: "active"},
		{ID: 3, Severity: "high", Confidence: 0.91, State: "ignored"},
		{ID: 4, Severity: "medium", Confidence: 0.6, State: "active"},
	}
	result := ComputeResult(run, policy, findings, "trace-1")
	if result.State != "failed" {
		t.Fatalf("state = %q, want failed", result.State)
	}
	if result.BlockingFindings != 1 {
		t.Fatalf("blocking findings = %d, want 1", result.BlockingFindings)
	}
	if len(result.QualifyingFindingIDs) != 1 || result.QualifyingFindingIDs[0] != 1 {
		t.Fatalf("qualifying ids = %v, want [1]", result.QualifyingFindingIDs)
	}
}

func TestAuditTrailCompleteness(t *testing.T) {
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, "/Users/chris/workspace/mreviewer/migrations")
	queries := db.New(sqlDB)
	audit := NewDBAuditLogger(queries)
	run := db.ReviewRun{ID: 55}
	result := Result{RunID: 55, State: "failed", BlockingFindings: 2, QualifyingFindingIDs: []int64{1, 2}, TraceID: "trace-55"}
	if err := audit.LogGateResult(ctx, run, result); err != nil {
		t.Fatalf("LogGateResult: %v", err)
	}
	logs, err := queries.ListAuditLogsByEntity(ctx, db.ListAuditLogsByEntityParams{EntityType: "review_run", EntityID: 55, Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListAuditLogsByEntity: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("audit log count = %d, want 1", len(logs))
	}
	var detail map[string]any
	if err := json.Unmarshal(logs[0].Detail, &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail["trace_id"] != "trace-55" {
		t.Fatalf("trace_id = %#v, want trace-55", detail["trace_id"])
	}
	ids, _ := detail["qualifying_finding_ids"].([]any)
	if len(ids) != 2 {
		t.Fatalf("qualifying ids = %v, want 2 entries", detail["qualifying_finding_ids"])
	}
}

type fakeStatusPublisher struct{ results []Result }

func (f *fakeStatusPublisher) PublishStatus(_ context.Context, result Result) error {
	f.results = append(f.results, result)
	return nil
}

type fakeCIPublisher struct{ results []Result }

func (f *fakeCIPublisher) PublishCIGate(_ context.Context, result Result) error {
	f.results = append(f.results, result)
	return nil
}
