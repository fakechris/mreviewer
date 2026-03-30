package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeSnapshotService struct {
	queue       QueueSnapshot
	concurrency ConcurrencySnapshot
	failures    FailuresSnapshot
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

func TestHandlerRejectsUnauthorizedWhenTokenConfigured(t *testing.T) {
	handler := NewHandler(fakeSnapshotService{}, "secret-token")
	req := httptest.NewRequest(http.MethodGet, "/admin/api/queue", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandlerServesQueueWithBearerAuth(t *testing.T) {
	handler := NewHandler(fakeSnapshotService{
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
