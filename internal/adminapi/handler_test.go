package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeSnapshotService struct {
	queue          QueueSnapshot
	concurrency    ConcurrencySnapshot
	failures       FailuresSnapshot
	trends         TrendsSnapshot
	runs           RunsSnapshot
	runDetail      RunDetail
	identities     IdentityMappingsSnapshot
	identity       IdentityMapping
	ownership      OwnershipSnapshot
	suggestions    IdentitySuggestionsSnapshot
	runDetailErr   error
	lastRunFilters RunFilters
	lastIDFilters  IdentityFilters
}

func (f fakeSnapshotService) Queue(_ context.Context) (QueueSnapshot, error) {
	return f.queue, nil
}

func (f fakeSnapshotService) Concurrency(_ context.Context) (ConcurrencySnapshot, error) {
	return f.concurrency, nil
}

func (f fakeSnapshotService) Failures(_ context.Context) (FailuresSnapshot, error) {
	return f.failures, nil
}

func (f fakeSnapshotService) Trends(_ context.Context) (TrendsSnapshot, error) {
	return f.trends, nil
}

func (f *fakeSnapshotService) Runs(_ context.Context, filters RunFilters) (RunsSnapshot, error) {
	f.lastRunFilters = filters
	return f.runs, nil
}

func (f *fakeSnapshotService) RunDetail(_ context.Context, _ int64) (RunDetail, error) {
	return f.runDetail, f.runDetailErr
}

func (f fakeSnapshotService) RetryRun(_ context.Context, _ int64, _ string) (RunDetail, error) {
	return f.runDetail, nil
}

func (f fakeSnapshotService) RerunRun(_ context.Context, _ int64, _ string) (RunDetail, error) {
	return f.runDetail, nil
}

func (f fakeSnapshotService) CancelRun(_ context.Context, _ int64, _ string) (RunDetail, error) {
	return f.runDetail, nil
}

func (f fakeSnapshotService) RequeueRun(_ context.Context, _ int64, _ string) (RunDetail, error) {
	return f.runDetail, nil
}

func (f *fakeSnapshotService) IdentityMappings(_ context.Context, filters IdentityFilters) (IdentityMappingsSnapshot, error) {
	f.lastIDFilters = filters
	return f.identities, nil
}

func (f fakeSnapshotService) ResolveIdentityMapping(_ context.Context, _ int64, _, _, _ string) (IdentityMapping, error) {
	return f.identity, nil
}

func (f fakeSnapshotService) Ownership(_ context.Context, _ IdentityFilters) (OwnershipSnapshot, error) {
	return f.ownership, nil
}

func (f fakeSnapshotService) IdentitySuggestions(_ context.Context, _ int64) (IdentitySuggestionsSnapshot, error) {
	return f.suggestions, nil
}

func TestHandlerRejectsUnauthorizedWhenTokenConfigured(t *testing.T) {
	handler := NewHandler(&fakeSnapshotService{}, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/queue", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandlerServesQueueWithBearerAuth(t *testing.T) {
	handler := NewHandler(&fakeSnapshotService{
		queue: QueueSnapshot{PendingCount: 2},
	}, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/queue", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
}

func TestHandlerServesRunsList(t *testing.T) {
	handler := NewHandler(&fakeSnapshotService{
		runs: RunsSnapshot{
			Items: []RunListItem{{ID: 12, Platform: "gitlab", ProjectPath: "group/repo", Status: "running"}},
		},
	}, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/runs?platform=gitlab&status=running", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var payload RunsSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if len(payload.Items) != 1 || payload.Items[0].ID != 12 {
		t.Fatalf("runs payload = %+v, want run 12", payload.Items)
	}
}

func TestHandlerServesTrends(t *testing.T) {
	handler := NewHandler(&fakeSnapshotService{
		trends: TrendsSnapshot{
			WindowHours: 24,
			Buckets:     []TrendBucket{{RunCount: 2, WebhookRejectedCount: 1}},
		},
	}, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/trends", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var payload TrendsSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode trends: %v", err)
	}
	if payload.WindowHours != 24 || len(payload.Buckets) != 1 {
		t.Fatalf("payload = %+v, want 24h with 1 bucket", payload)
	}
}

func TestHandlerServesRunDetail(t *testing.T) {
	handler := NewHandler(&fakeSnapshotService{
		runDetail: RunDetail{RunListItem: RunListItem{ID: 42, Platform: "github", ProjectPath: "acme/repo"}},
	}, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/runs/42", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var payload RunDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode run detail: %v", err)
	}
	if payload.ID != 42 {
		t.Fatalf("run detail id = %d, want 42", payload.ID)
	}
}

func TestHandlerReturnsNotFoundForUnknownRun(t *testing.T) {
	handler := NewHandler(&fakeSnapshotService{
		runDetailErr: sql.ErrNoRows,
	}, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/runs/404", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandlerRejectsInvalidRunAction(t *testing.T) {
	handler := NewHandler(&fakeSnapshotService{}, "secret-token")
	req := httptest.NewRequest(http.MethodPost, "/admin/api/runs/0/retry", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandlerServesIdentityMappings(t *testing.T) {
	handler := NewHandler(&fakeSnapshotService{
		identities: IdentityMappingsSnapshot{
			Items: []IdentityMapping{{ID: 91, Platform: "gitlab", ProjectPath: "group/repo", GitEmail: "chris@example.com"}},
		},
	}, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/identities?platform=gitlab", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var payload IdentityMappingsSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode identities: %v", err)
	}
	if len(payload.Items) != 1 || payload.Items[0].ID != 91 {
		t.Fatalf("identities payload = %+v, want mapping 91", payload.Items)
	}
}

func TestHandlerServesIdentitySuggestions(t *testing.T) {
	handler := NewHandler(&fakeSnapshotService{
		suggestions: IdentitySuggestionsSnapshot{
			Mapping:     IdentityMapping{ID: 91},
			Suggestions: []IdentitySuggestion{{PlatformUsername: "chris", MatchScore: 95}},
		},
	}, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/identities/91/suggestions", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var payload IdentitySuggestionsSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode suggestions: %v", err)
	}
	if len(payload.Suggestions) != 1 || payload.Suggestions[0].PlatformUsername != "chris" {
		t.Fatalf("suggestions payload = %+v, want chris suggestion", payload)
	}
}

func TestHandlerResolvesIdentityMapping(t *testing.T) {
	handler := NewHandler(&fakeSnapshotService{
		identity: IdentityMapping{ID: 91, PlatformUsername: "reviewer-manual", Status: "manual"},
	}, "secret-token")
	req := httptest.NewRequest(http.MethodPost, "/admin/api/identities/91/resolve", strings.NewReader(`{"platform_username":"reviewer-manual","platform_user_id":"99"}`))
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var payload IdentityMapping
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode identity mapping: %v", err)
	}
	if payload.PlatformUsername != "reviewer-manual" || payload.Status != "manual" {
		t.Fatalf("payload = %+v, want resolved manual mapping", payload)
	}
}

func TestHandlerServesOwnership(t *testing.T) {
	handler := NewHandler(&fakeSnapshotService{
		ownership: OwnershipSnapshot{
			Items: []OwnershipSummary{{PlatformUsername: "chris", IdentityCount: 2}},
		},
	}, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/ownership?platform=gitlab&project=group/repo", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var payload OwnershipSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode ownership: %v", err)
	}
	if len(payload.Items) != 1 || payload.Items[0].PlatformUsername != "chris" {
		t.Fatalf("ownership payload = %+v, want chris owner", payload)
	}
}

func TestHandlerClampsLargeRunsLimit(t *testing.T) {
	service := &fakeSnapshotService{
		runs: RunsSnapshot{
			Items: []RunListItem{{ID: 12}},
		},
	}
	handler := NewHandler(service, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/runs?limit=5000", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if service.lastRunFilters.Limit != maxListLimit {
		t.Fatalf("limit = %d, want %d", service.lastRunFilters.Limit, maxListLimit)
	}
}

func TestParseOptionalInt32RejectsNegativeValues(t *testing.T) {
	if _, err := parseOptionalInt32("-1"); err == nil {
		t.Fatal("parseOptionalInt32(-1) error = nil, want non-nil")
	}
}
