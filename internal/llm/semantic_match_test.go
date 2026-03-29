package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
)

type fakeSemanticMatcher struct {
	result bool
	err    error
	called int
	pairs  [][2]SemanticFindingSummary
}

func (f *fakeSemanticMatcher) IsSameFinding(_ context.Context, a, b SemanticFindingSummary) (bool, error) {
	f.called++
	f.pairs = append(f.pairs, [2]SemanticFindingSummary{a, b})
	return f.result, f.err
}

func TestNoopSemanticMatcherReturnsFalse(t *testing.T) {
	m := NoopSemanticMatcher{}
	same, err := m.IsSameFinding(context.Background(), SemanticFindingSummary{}, SemanticFindingSummary{})
	if err != nil {
		t.Fatal(err)
	}
	if same {
		t.Fatal("NoopSemanticMatcher should return false")
	}
}

func TestLLMSemanticMatcherSameIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": `{"same": true, "reason": "Both describe nil pointer dereference in processPayment"}`}},
			},
			"usage": map[string]any{"completion_tokens": 20},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	m := NewLLMSemanticMatcher(server.URL, "", "test-model")
	same, err := m.IsSameFinding(context.Background(),
		SemanticFindingSummary{Path: "billing.go", Title: "Nil pointer dereference", Category: "bug"},
		SemanticFindingSummary{Path: "billing.go", Title: "Potential nil access in payment handler", Category: "bug"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !same {
		t.Fatal("expected same=true")
	}
}

func TestLLMSemanticMatcherDifferentIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": `{"same": false, "reason": "Different issues: one is about nil pointer, other about SQL injection"}`}},
			},
			"usage": map[string]any{"completion_tokens": 25},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	m := NewLLMSemanticMatcher(server.URL, "", "test-model")
	same, err := m.IsSameFinding(context.Background(),
		SemanticFindingSummary{Path: "billing.go", Title: "Nil pointer dereference", Category: "bug"},
		SemanticFindingSummary{Path: "billing.go", Title: "SQL injection in query builder", Category: "security"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if same {
		t.Fatal("expected same=false")
	}
}

func TestLLMSemanticMatcherAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	m := NewLLMSemanticMatcher(server.URL, "", "test-model")
	same, err := m.IsSameFinding(context.Background(),
		SemanticFindingSummary{Path: "a.go", Title: "bug"},
		SemanticFindingSummary{Path: "a.go", Title: "bug2"},
	)
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if same {
		t.Fatal("expected same=false on error")
	}
}

func TestLLMSemanticMatcherMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "not json"}},
			},
			"usage": map[string]any{"completion_tokens": 5},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	m := NewLLMSemanticMatcher(server.URL, "", "test-model")
	same, err := m.IsSameFinding(context.Background(),
		SemanticFindingSummary{Path: "a.go", Title: "bug"},
		SemanticFindingSummary{Path: "a.go", Title: "bug2"},
	)
	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
	if same {
		t.Fatal("expected same=false on parse error")
	}
}

func TestLLMSemanticMatcherSendsAuthHeader(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": `{"same": false, "reason": "test"}`}},
			},
			"usage": map[string]any{"completion_tokens": 5},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	m := NewLLMSemanticMatcher(server.URL, "sk-test-key", "test-model")
	m.IsSameFinding(context.Background(),
		SemanticFindingSummary{Path: "a.go"},
		SemanticFindingSummary{Path: "a.go"},
	)
	if gotAuth != "Bearer sk-test-key" {
		t.Fatalf("auth = %q, want Bearer sk-test-key", gotAuth)
	}
}

func TestSemanticMatcherIntegrationInPersistFindings(t *testing.T) {
	// This test verifies that the semantic matcher is called during
	// persistFindingsWithMatcher when fingerprints don't match.
	matcher := &fakeSemanticMatcher{result: true}

	sqlDB, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewSQLProcessorStore(sqlDB)
	q := newTestQueries(t, sqlDB)
	ctx := context.Background()
	runID, mr := setupTestRunAndMR(t, q, ctx)

	if err := q.ClaimReviewRun(ctx, claimParams(runID)); err != nil {
		t.Fatal(err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}

	// Insert a base finding with specific canonical_key
	baseFinding := ReviewFinding{
		Category: "bug", Severity: "high", Confidence: 0.9,
		Title: "Nil pointer in handler", BodyMarkdown: "May panic.",
		Path: "src/service/foo.go", AnchorKind: "new_line",
		NewLine: int32Ptr(10), CanonicalKey: "nil_deref_handler",
		IntroducedByThisChange: true,
	}
	baseResult := ReviewResult{
		SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID),
		Summary: "base", Status: "completed", Findings: []ReviewFinding{baseFinding},
	}
	if err := persistFindings(ctx, store, run, mr, baseResult, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Verify finding was inserted
	findings, err := store.ListActiveFindingsByMR(ctx, mr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("base findings = %d, want 1", len(findings))
	}

	// Create a new run with a finding that has DIFFERENT canonical_key
	// but describes the same issue (matcher returns true)
	newRunID := setupAdditionalRun(t, q, ctx, run.ProjectID, mr.ID)
	if err := q.ClaimReviewRun(ctx, claimParams(newRunID)); err != nil {
		t.Fatal(err)
	}
	newRun, err := q.GetReviewRun(ctx, newRunID)
	if err != nil {
		t.Fatal(err)
	}

	crossModelFinding := ReviewFinding{
		Category: "bug", Severity: "high", Confidence: 0.85,
		Title: "Potential nil access in processRequest", BodyMarkdown: "Risk of panic.",
		Path: "src/service/foo.go", AnchorKind: "new_line",
		NewLine: int32Ptr(12), CanonicalKey: "nil_access_processrequest",
		IntroducedByThisChange: true,
	}
	crossModelResult := ReviewResult{
		SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", newRunID),
		Summary: "cross-model", Status: "completed", Findings: []ReviewFinding{crossModelFinding},
	}

	// With matcher returning true, the cross-model finding should supersede the base
	if err := persistFindingsWithMatcher(ctx, store, newRun, mr, crossModelResult, nil, nil, matcher); err != nil {
		t.Fatal(err)
	}

	if matcher.called == 0 {
		t.Fatal("semantic matcher was never called")
	}

	// The base finding should be superseded
	findings, err = store.ListActiveFindingsByMR(ctx, mr.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Should have the new finding as active (base was superseded)
	activeCount := 0
	for _, f := range findings {
		if f.ReviewRunID == newRunID {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("active findings from new run = %d, want 1", activeCount)
	}
}

func TestSemanticMatcherNotCalledWhenFingerprintMatches(t *testing.T) {
	matcher := &fakeSemanticMatcher{result: true}

	sqlDB, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewSQLProcessorStore(sqlDB)
	q := newTestQueries(t, sqlDB)
	ctx := context.Background()
	runID, mr := setupTestRunAndMR(t, q, ctx)

	if err := q.ClaimReviewRun(ctx, claimParams(runID)); err != nil {
		t.Fatal(err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}

	// Insert a finding
	finding := ReviewFinding{
		Category: "bug", Severity: "high", Confidence: 0.9,
		Title: "Nil pointer", BodyMarkdown: "May panic.",
		Path: "src/service/foo.go", AnchorKind: "new_line",
		NewLine: int32Ptr(10), CanonicalKey: "same_key",
		IntroducedByThisChange: true,
	}
	if err := persistFindings(ctx, store, run, mr, ReviewResult{
		SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID),
		Summary: "base", Status: "completed", Findings: []ReviewFinding{finding},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Create new run with identical finding (same canonical_key, same path)
	newRunID := setupAdditionalRun(t, q, ctx, run.ProjectID, mr.ID)
	if err := q.ClaimReviewRun(ctx, claimParams(newRunID)); err != nil {
		t.Fatal(err)
	}
	newRun, err := q.GetReviewRun(ctx, newRunID)
	if err != nil {
		t.Fatal(err)
	}

	// Same finding — fingerprints will match, so matcher should NOT be called
	if err := persistFindingsWithMatcher(ctx, store, newRun, mr, ReviewResult{
		SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", newRunID),
		Summary: "same", Status: "completed", Findings: []ReviewFinding{finding},
	}, nil, nil, matcher); err != nil {
		t.Fatal(err)
	}

	if matcher.called != 0 {
		t.Fatalf("matcher.called = %d, want 0 (fingerprint match should short-circuit)", matcher.called)
	}
}

func TestSemanticMatcherErrorIsConservative(t *testing.T) {
	matcher := &fakeSemanticMatcher{result: false, err: fmt.Errorf("LLM unavailable")}

	sqlDB, cleanup := setupTestDB(t)
	defer cleanup()
	store := NewSQLProcessorStore(sqlDB)
	q := newTestQueries(t, sqlDB)
	ctx := context.Background()
	runID, mr := setupTestRunAndMR(t, q, ctx)

	if err := q.ClaimReviewRun(ctx, claimParams(runID)); err != nil {
		t.Fatal(err)
	}
	run, err := q.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}

	baseFinding := ReviewFinding{
		Category: "bug", Severity: "high", Confidence: 0.9,
		Title: "Issue A", BodyMarkdown: "Details A.",
		Path: "src/a.go", AnchorKind: "new_line",
		NewLine: int32Ptr(10), CanonicalKey: "key_a",
		IntroducedByThisChange: true,
	}
	if err := persistFindings(ctx, store, run, mr, ReviewResult{
		SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", runID),
		Summary: "base", Status: "completed", Findings: []ReviewFinding{baseFinding},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}

	newRunID := setupAdditionalRun(t, q, ctx, run.ProjectID, mr.ID)
	if err := q.ClaimReviewRun(ctx, claimParams(newRunID)); err != nil {
		t.Fatal(err)
	}
	newRun, err := q.GetReviewRun(ctx, newRunID)
	if err != nil {
		t.Fatal(err)
	}

	differentKey := ReviewFinding{
		Category: "bug", Severity: "high", Confidence: 0.85,
		Title: "Issue B", BodyMarkdown: "Details B.",
		Path: "src/a.go", AnchorKind: "new_line",
		NewLine: int32Ptr(12), CanonicalKey: "key_b",
		IntroducedByThisChange: true,
	}

	// Matcher errors → conservative: treats as different findings
	if err := persistFindingsWithMatcher(ctx, store, newRun, mr, ReviewResult{
		SchemaVersion: "1.0", ReviewRunID: fmt.Sprintf("%d", newRunID),
		Summary: "new", Status: "completed", Findings: []ReviewFinding{differentKey},
	}, nil, nil, matcher); err != nil {
		t.Fatal(err)
	}

	findings, err := store.ListActiveFindingsByMR(ctx, mr.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Both findings should be active (matcher error → no dedup)
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2 (conservative on error)", len(findings))
	}
}

// --- test helpers ---

const semanticTestMigrationsDir = "../../migrations"

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, semanticTestMigrationsDir)
	return sqlDB, func() {}
}

func newTestQueries(t *testing.T, sqlDB *sql.DB) *db.Queries {
	t.Helper()
	return db.New(sqlDB)
}

func setupTestRunAndMR(t *testing.T, q *db.Queries, ctx context.Context) (int64, db.MergeRequest) {
	t.Helper()
	res, err := q.UpsertGitlabInstance(ctx, db.UpsertGitlabInstanceParams{Url: "https://gitlab.example.com", Name: "GitLab"})
	if err != nil {
		t.Fatalf("UpsertGitlabInstance: %v", err)
	}
	instanceID, _ := res.LastInsertId()
	if instanceID == 0 {
		inst, err := q.GetGitlabInstanceByURL(ctx, "https://gitlab.example.com")
		if err != nil {
			t.Fatalf("GetGitlabInstanceByURL: %v", err)
		}
		instanceID = inst.ID
	}
	res, err = q.UpsertProject(ctx, db.UpsertProjectParams{GitlabInstanceID: instanceID, GitlabProjectID: 101, PathWithNamespace: "group/project", Enabled: true})
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	projectID, _ := res.LastInsertId()
	if projectID == 0 {
		proj, err := q.GetProjectByGitlabID(ctx, db.GetProjectByGitlabIDParams{GitlabInstanceID: instanceID, GitlabProjectID: 101})
		if err != nil {
			t.Fatalf("GetProjectByGitlabID: %v", err)
		}
		projectID = proj.ID
	}
	res, err = q.UpsertMergeRequest(ctx, db.UpsertMergeRequestParams{ProjectID: projectID, MrIid: 7, Title: "Title", SourceBranch: "feature", TargetBranch: "main", Author: "alice", State: "opened", IsDraft: false, HeadSha: "head", WebUrl: "https://gitlab.example.com/group/project/-/merge_requests/7"})
	if err != nil {
		t.Fatalf("UpsertMergeRequest: %v", err)
	}
	mrID, _ := res.LastInsertId()
	if mrID == 0 {
		m, err := q.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{ProjectID: projectID, MrIid: 7})
		if err != nil {
			t.Fatalf("GetMergeRequestByProjectMR: %v", err)
		}
		mrID = m.ID
	}
	res, err = q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: projectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head", Status: "pending", MaxRetries: 3, IdempotencyKey: fmt.Sprintf("rr-%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	runID, _ := res.LastInsertId()
	mr, err := q.GetMergeRequest(ctx, mrID)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	return runID, mr
}

func setupAdditionalRun(t *testing.T, q *db.Queries, ctx context.Context, projectID, mrID int64) int64 {
	t.Helper()
	res, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: projectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head2", Status: "pending", MaxRetries: 3, IdempotencyKey: fmt.Sprintf("rr-%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func claimParams(runID int64) db.ClaimReviewRunParams {
	return db.ClaimReviewRunParams{ClaimedBy: "worker-test", ID: runID}
}

func int32Ptr(v int32) *int32 { return &v }
