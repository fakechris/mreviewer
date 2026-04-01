package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
)

type fakeActionStore struct {
	run            db.ReviewRun
	runDetail      db.GetRunDetailRow
	retriedRunID   int64
	cancelledRunID int64
	requeuedRunID  int64
	insertedRun    db.InsertReviewRunParams
	insertedAudit  db.InsertAuditLogParams
	insertedRunID  int64
	getRunErr      error
	insertRunErr   error
	insertAuditErr error
}

func (f *fakeActionStore) CountPendingQueue(context.Context) (int64, error)       { return 0, nil }
func (f *fakeActionStore) CountRetryScheduledRuns(context.Context) (int64, error) { return 0, nil }
func (f *fakeActionStore) GetOldestWaitingRunCreatedAt(context.Context) (interface{}, error) {
	return nil, nil
}
func (f *fakeActionStore) ListTopQueuedProjects(context.Context, int32) ([]db.ListTopQueuedProjectsRow, error) {
	return nil, nil
}
func (f *fakeActionStore) CountSupersededRunsSince(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (f *fakeActionStore) ListActiveWorkersWithCapacity(context.Context, time.Time) ([]db.ListActiveWorkersWithCapacityRow, error) {
	return nil, nil
}
func (f *fakeActionStore) ListRecentFailedRuns(context.Context, int32) ([]db.ListRecentFailedRunsRow, error) {
	return nil, nil
}
func (f *fakeActionStore) ListFailureCountsByErrorCode(context.Context, time.Time) ([]db.ListFailureCountsByErrorCodeRow, error) {
	return nil, nil
}
func (f *fakeActionStore) ListWebhookVerificationCounts(context.Context, time.Time) ([]db.ListWebhookVerificationCountsRow, error) {
	return nil, nil
}
func (f *fakeActionStore) ListRunTrendBuckets(context.Context, time.Time) ([]db.ListRunTrendBucketsRow, error) {
	return nil, nil
}
func (f *fakeActionStore) ListWebhookVerificationTrendBuckets(context.Context, time.Time) ([]db.ListWebhookVerificationTrendBucketsRow, error) {
	return nil, nil
}
func (f *fakeActionStore) ListPlatformRunRollups(context.Context, time.Time) ([]db.ListPlatformRunRollupsRow, error) {
	return nil, nil
}
func (f *fakeActionStore) ListProjectRunRollups(context.Context, db.ListProjectRunRollupsParams) ([]db.ListProjectRunRollupsRow, error) {
	return nil, nil
}
func (f *fakeActionStore) ListRecentRuns(context.Context, db.ListRecentRunsParams) ([]db.ListRecentRunsRow, error) {
	return nil, nil
}
func (f *fakeActionStore) ListIdentityMappings(context.Context, db.ListIdentityMappingsParams) ([]db.ListIdentityMappingsRow, error) {
	return nil, nil
}
func (f *fakeActionStore) GetRunDetail(_ context.Context, id int64) (db.GetRunDetailRow, error) {
	detail := f.runDetail
	detail.ID = id
	return detail, nil
}
func (f *fakeActionStore) GetIdentityMapping(context.Context, int64) (db.IdentityMapping, error) {
	return db.IdentityMapping{}, nil
}
func (f *fakeActionStore) ResolveIdentityMapping(context.Context, db.ResolveIdentityMappingParams) error {
	return nil
}
func (f *fakeActionStore) GetReviewRun(context.Context, int64) (db.ReviewRun, error) {
	if f.getRunErr != nil {
		return db.ReviewRun{}, f.getRunErr
	}
	return f.run, nil
}
func (f *fakeActionStore) RetryReviewRunNow(_ context.Context, id int64) error {
	f.retriedRunID = id
	return nil
}
func (f *fakeActionStore) CancelReviewRun(_ context.Context, id int64, _, _ string) error {
	f.cancelledRunID = id
	return nil
}
func (f *fakeActionStore) RequeueReviewRun(_ context.Context, id int64) error {
	f.requeuedRunID = id
	return nil
}
func (f *fakeActionStore) InsertReviewRun(_ context.Context, arg db.InsertReviewRunParams) (sql.Result, error) {
	if f.insertRunErr != nil {
		return nil, f.insertRunErr
	}
	f.insertedRun = arg
	return fakeSQLResult(f.insertedRunID), nil
}
func (f *fakeActionStore) InsertAuditLog(_ context.Context, arg db.InsertAuditLogParams) (sql.Result, error) {
	if f.insertAuditErr != nil {
		return nil, f.insertAuditErr
	}
	f.insertedAudit = arg
	return fakeSQLResult(1), nil
}

type fakeSQLResult int64

func (f fakeSQLResult) LastInsertId() (int64, error) { return int64(f), nil }
func (f fakeSQLResult) RowsAffected() (int64, error) { return 1, nil }

func TestServiceRetryRun(t *testing.T) {
	store := &fakeActionStore{
		run:       db.ReviewRun{ID: 11, Status: "failed"},
		runDetail: db.GetRunDetailRow{ID: 11, Status: "failed", Platform: "gitlab"},
	}
	svc := NewService(store, WithActionTxRunner(func(ctx context.Context, fn ActionTxFunc) error {
		return fn(ctx, store)
	}))

	detail, err := svc.RetryRun(context.Background(), 11, "admin")
	if err != nil {
		t.Fatalf("RetryRun: %v", err)
	}
	if store.retriedRunID != 11 {
		t.Fatalf("retriedRunID = %d, want 11", store.retriedRunID)
	}
	if detail.ID != 11 {
		t.Fatalf("detail id = %d, want 11", detail.ID)
	}
	if store.insertedAudit.Action != "retry_run" {
		t.Fatalf("audit action = %q, want retry_run", store.insertedAudit.Action)
	}
}

func TestServiceRetryRunRejectsInvalidState(t *testing.T) {
	store := &fakeActionStore{run: db.ReviewRun{ID: 12, Status: "pending"}}
	svc := NewService(store, WithActionTxRunner(func(ctx context.Context, fn ActionTxFunc) error {
		return fn(ctx, store)
	}))

	_, err := svc.RetryRun(context.Background(), 12, "admin")
	if err == nil {
		t.Fatal("RetryRun error = nil, want non-nil")
	}
	var actionErr *ActionError
	if !errors.As(err, &actionErr) || actionErr.StatusCode != 409 {
		t.Fatalf("RetryRun error = %v, want 409 action error", err)
	}
}

func TestServiceRerunClonesPendingRun(t *testing.T) {
	store := &fakeActionStore{
		run: db.ReviewRun{
			ID:             19,
			ProjectID:      5,
			MergeRequestID: 9,
			HookEventID:    sql.NullInt64{Int64: 7, Valid: true},
			TriggerType:    "webhook",
			HeadSha:        "head-1",
			Status:         "completed",
			MaxRetries:     4,
			ScopeJson:      db.NullRawMessage([]byte(`{"platform":"github"}`)),
		},
		runDetail:     db.GetRunDetailRow{Platform: "github", Status: "pending"},
		insertedRunID: 77,
	}
	svc := NewService(store,
		WithNow(func() time.Time { return time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC) }),
		WithActionTxRunner(func(ctx context.Context, fn ActionTxFunc) error { return fn(ctx, store) }),
	)

	detail, err := svc.RerunRun(context.Background(), 19, "admin")
	if err != nil {
		t.Fatalf("RerunRun: %v", err)
	}
	if store.insertedRun.ProjectID != 5 || store.insertedRun.Status != "pending" {
		t.Fatalf("inserted run = %+v", store.insertedRun)
	}
	if detail.ID != 77 {
		t.Fatalf("detail id = %d, want 77", detail.ID)
	}
	if store.insertedAudit.Action != "rerun_run" {
		t.Fatalf("audit action = %q, want rerun_run", store.insertedAudit.Action)
	}
}

func TestServiceCancelAndRequeueRun(t *testing.T) {
	store := &fakeActionStore{
		run:       db.ReviewRun{ID: 21, Status: "running"},
		runDetail: db.GetRunDetailRow{ID: 21, Status: "cancelled"},
	}
	svc := NewService(store, WithActionTxRunner(func(ctx context.Context, fn ActionTxFunc) error {
		return fn(ctx, store)
	}))

	if _, err := svc.CancelRun(context.Background(), 21, "admin"); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	if store.cancelledRunID != 21 {
		t.Fatalf("cancelledRunID = %d, want 21", store.cancelledRunID)
	}

	store.run = db.ReviewRun{ID: 22, Status: "cancelled"}
	store.runDetail = db.GetRunDetailRow{ID: 22, Status: "pending"}
	if _, err := svc.RequeueRun(context.Background(), 22, "admin"); err != nil {
		t.Fatalf("RequeueRun: %v", err)
	}
	if store.requeuedRunID != 22 {
		t.Fatalf("requeuedRunID = %d, want 22", store.requeuedRunID)
	}
}

func TestActionErrorJSONDetail(t *testing.T) {
	payload, err := json.Marshal(&ActionError{StatusCode: 409, Message: "bad transition"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(payload) == "" {
		t.Fatal("expected non-empty json payload")
	}
}
