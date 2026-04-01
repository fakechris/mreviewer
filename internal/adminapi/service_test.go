package adminapi

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
)

type fakeStore struct {
	pendingCount              int64
	retryScheduledCount       int64
	oldestWaitingCreatedAt    sql.NullTime
	topQueuedProjects         []db.ListTopQueuedProjectsRow
	supersededCount           int64
	activeWorkers             []db.ListActiveWorkersWithCapacityRow
	recentFailedRuns          []db.ListRecentFailedRunsRow
	failureCounts             []db.ListFailureCountsByErrorCodeRow
	webhookVerificationCounts []db.ListWebhookVerificationCountsRow
	recentRuns                []db.ListRecentRunsRow
	runDetail                 db.GetRunDetailRow
	identityMappings          []db.ListIdentityMappingsRow
	resolvedIdentityMapping   db.IdentityMapping
	resolvedIdentityParams    db.ResolveIdentityMappingParams
	insertedAudit             db.InsertAuditLogParams
}

func (f fakeStore) CountPendingQueue(context.Context) (int64, error) {
	return f.pendingCount, nil
}

func (f fakeStore) CountRetryScheduledRuns(context.Context) (int64, error) {
	return f.retryScheduledCount, nil
}

func (f fakeStore) GetOldestWaitingRunCreatedAt(context.Context) (interface{}, error) {
	return f.oldestWaitingCreatedAt, nil
}

func (f fakeStore) ListTopQueuedProjects(context.Context, int32) ([]db.ListTopQueuedProjectsRow, error) {
	return f.topQueuedProjects, nil
}

func (f fakeStore) CountSupersededRunsSince(context.Context, time.Time) (int64, error) {
	return f.supersededCount, nil
}

func (f fakeStore) ListActiveWorkersWithCapacity(context.Context, time.Time) ([]db.ListActiveWorkersWithCapacityRow, error) {
	return f.activeWorkers, nil
}

func (f fakeStore) ListRecentFailedRuns(context.Context, int32) ([]db.ListRecentFailedRunsRow, error) {
	return f.recentFailedRuns, nil
}

func (f fakeStore) ListFailureCountsByErrorCode(context.Context, time.Time) ([]db.ListFailureCountsByErrorCodeRow, error) {
	return f.failureCounts, nil
}

func (f fakeStore) ListWebhookVerificationCounts(context.Context, time.Time) ([]db.ListWebhookVerificationCountsRow, error) {
	return f.webhookVerificationCounts, nil
}

func (f fakeStore) ListRecentRuns(context.Context, db.ListRecentRunsParams) ([]db.ListRecentRunsRow, error) {
	return f.recentRuns, nil
}

func (f fakeStore) GetRunDetail(context.Context, int64) (db.GetRunDetailRow, error) {
	return f.runDetail, nil
}

func (f fakeStore) ListIdentityMappings(context.Context, db.ListIdentityMappingsParams) ([]db.ListIdentityMappingsRow, error) {
	return f.identityMappings, nil
}

func (f fakeStore) GetIdentityMapping(context.Context, int64) (db.IdentityMapping, error) {
	return f.resolvedIdentityMapping, nil
}

func (f *fakeStore) ResolveIdentityMapping(_ context.Context, arg db.ResolveIdentityMappingParams) error {
	f.resolvedIdentityParams = arg
	return nil
}

func (f *fakeStore) InsertAuditLog(_ context.Context, arg db.InsertAuditLogParams) (sql.Result, error) {
	f.insertedAudit = arg
	return fakeSQLResult(1), nil
}

func (f *fakeStore) GetReviewRun(context.Context, int64) (db.ReviewRun, error) {
	return db.ReviewRun{}, nil
}

func (f *fakeStore) InsertReviewRun(context.Context, db.InsertReviewRunParams) (sql.Result, error) {
	return fakeSQLResult(1), nil
}

func (f *fakeStore) RetryReviewRunNow(context.Context, int64) error {
	return nil
}

func (f *fakeStore) CancelReviewRun(context.Context, int64, string, string) error {
	return nil
}

func (f *fakeStore) RequeueReviewRun(context.Context, int64) error {
	return nil
}

func TestServiceQueueSummary(t *testing.T) {
	now := time.Date(2026, time.March, 29, 19, 0, 0, 0, time.UTC)
	svc := NewService(&fakeStore{
		pendingCount:           3,
		retryScheduledCount:    2,
		oldestWaitingCreatedAt: sql.NullTime{Time: now.Add(-5 * time.Minute), Valid: true},
		topQueuedProjects: []db.ListTopQueuedProjectsRow{
			{PathWithNamespace: "group/a", QueueDepth: 3},
			{PathWithNamespace: "group/b", QueueDepth: 2},
		},
		supersededCount: 4,
	}, WithNow(func() time.Time { return now }))

	snapshot, err := svc.Queue(context.Background())
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if snapshot.PendingCount != 3 {
		t.Fatalf("pending_count = %d, want 3", snapshot.PendingCount)
	}
	if snapshot.RetryScheduledCount != 2 {
		t.Fatalf("retry_scheduled_count = %d, want 2", snapshot.RetryScheduledCount)
	}
	if snapshot.OldestWaitingAgeSeconds != 300 {
		t.Fatalf("oldest_waiting_age_seconds = %d, want 300", snapshot.OldestWaitingAgeSeconds)
	}
	if len(snapshot.TopProjects) != 2 {
		t.Fatalf("top_projects = %d, want 2", len(snapshot.TopProjects))
	}
	if snapshot.SupersededLast24h != 4 {
		t.Fatalf("superseded_last_24h = %d, want 4", snapshot.SupersededLast24h)
	}
}

func TestServiceConcurrencySummary(t *testing.T) {
	now := time.Date(2026, time.March, 29, 19, 5, 0, 0, time.UTC)
	svc := NewService(&fakeStore{
		activeWorkers: []db.ListActiveWorkersWithCapacityRow{
			{WorkerID: "worker-1", Hostname: "host-a", Version: "dev", ConfiguredConcurrency: 4, RunningRuns: 3},
			{WorkerID: "worker-2", Hostname: "host-b", Version: "dev", ConfiguredConcurrency: 2, RunningRuns: 1},
		},
	}, WithNow(func() time.Time { return now }))

	snapshot, err := svc.Concurrency(context.Background())
	if err != nil {
		t.Fatalf("Concurrency: %v", err)
	}
	if len(snapshot.ActiveWorkers) != 2 {
		t.Fatalf("active_workers = %d, want 2", len(snapshot.ActiveWorkers))
	}
	if snapshot.TotalConfiguredConcurrency != 6 {
		t.Fatalf("total_configured_concurrency = %d, want 6", snapshot.TotalConfiguredConcurrency)
	}
	if snapshot.TotalRunningRuns != 4 {
		t.Fatalf("total_running_runs = %d, want 4", snapshot.TotalRunningRuns)
	}
}

func TestServiceFailuresSummary(t *testing.T) {
	now := time.Date(2026, time.March, 29, 19, 10, 0, 0, time.UTC)
	svc := NewService(&fakeStore{
		recentFailedRuns: []db.ListRecentFailedRunsRow{
			{ID: 11, ErrorCode: "provider_failed", TriggerType: "mr_open"},
		},
		failureCounts: []db.ListFailureCountsByErrorCodeRow{
			{ErrorCode: "provider_failed", Count: 2},
			{ErrorCode: "worker_timeout", Count: 1},
		},
		webhookVerificationCounts: []db.ListWebhookVerificationCountsRow{
			{VerificationOutcome: "rejected", Count: 3},
			{VerificationOutcome: "deduplicated", Count: 5},
		},
	}, WithNow(func() time.Time { return now }))

	snapshot, err := svc.Failures(context.Background())
	if err != nil {
		t.Fatalf("Failures: %v", err)
	}
	if len(snapshot.RecentFailedRuns) != 1 {
		t.Fatalf("recent_failed_runs = %d, want 1", len(snapshot.RecentFailedRuns))
	}
	if len(snapshot.ErrorBuckets) != 2 {
		t.Fatalf("error_buckets = %d, want 2", len(snapshot.ErrorBuckets))
	}
	if snapshot.WebhookRejectedLast24h != 3 {
		t.Fatalf("webhook_rejected_last_24h = %d, want 3", snapshot.WebhookRejectedLast24h)
	}
	if snapshot.WebhookDeduplicatedLast24h != 5 {
		t.Fatalf("webhook_deduplicated_last_24h = %d, want 5", snapshot.WebhookDeduplicatedLast24h)
	}
}

func TestServiceListRuns(t *testing.T) {
	now := time.Date(2026, time.March, 29, 19, 15, 0, 0, time.UTC)
	svc := NewService(&fakeStore{
		recentRuns: []db.ListRecentRunsRow{
			{
				ID:                 41,
				Platform:           "github",
				ProjectPath:        "acme/repo",
				MergeRequestID:     17,
				Status:             "failed",
				ErrorCode:          "publish_failed",
				TriggerType:        "webhook",
				HeadSha:            "deadbeef",
				ClaimedBy:          "worker-1",
				FindingCount:       2,
				CommentActionCount: 1,
				CreatedAt:          now.Add(-10 * time.Minute),
				UpdatedAt:          now.Add(-1 * time.Minute),
			},
		},
	}, WithNow(func() time.Time { return now }))

	snapshot, err := svc.Runs(context.Background(), RunFilters{Platform: "github", Status: "failed"})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(snapshot.Items) != 1 {
		t.Fatalf("runs len = %d, want 1", len(snapshot.Items))
	}
	if snapshot.Items[0].Platform != "github" {
		t.Fatalf("run platform = %q, want github", snapshot.Items[0].Platform)
	}
	if snapshot.Items[0].QueueAgeSeconds <= 0 {
		t.Fatalf("queue_age_seconds = %d, want > 0", snapshot.Items[0].QueueAgeSeconds)
	}
}

func TestServiceGetRunDetail(t *testing.T) {
	now := time.Date(2026, time.March, 29, 19, 20, 0, 0, time.UTC)
	svc := NewService(&fakeStore{
		runDetail: db.GetRunDetailRow{
			ID:                  44,
			Platform:            "gitlab",
			ProjectPath:         "group/repo",
			MergeRequestID:      18,
			Status:              "completed",
			ErrorCode:           "",
			TriggerType:         "webhook",
			HeadSha:             "cafebabe",
			ClaimedBy:           "worker-2",
			FindingCount:        3,
			CommentActionCount:  2,
			CreatedAt:           now.Add(-12 * time.Minute),
			UpdatedAt:           now.Add(-2 * time.Minute),
			StartedAt:           sql.NullTime{Time: now.Add(-11 * time.Minute), Valid: true},
			CompletedAt:         sql.NullTime{Time: now.Add(-3 * time.Minute), Valid: true},
			ProviderLatencyMs:   820,
			ProviderTokensTotal: 5100,
		},
	}, WithNow(func() time.Time { return now }))

	detail, err := svc.RunDetail(context.Background(), 44)
	if err != nil {
		t.Fatalf("RunDetail: %v", err)
	}
	if detail.ID != 44 {
		t.Fatalf("detail id = %d, want 44", detail.ID)
	}
	if detail.ProcessingDurationSeconds <= 0 {
		t.Fatalf("processing_duration_seconds = %d, want > 0", detail.ProcessingDurationSeconds)
	}
	if detail.ProviderTokensTotal != 5100 {
		t.Fatalf("provider_tokens_total = %d, want 5100", detail.ProviderTokensTotal)
	}
}

func TestServiceListIdentityMappings(t *testing.T) {
	svc := NewService(&fakeStore{
		identityMappings: []db.ListIdentityMappingsRow{{
			ID:               71,
			Platform:         "gitlab",
			ProjectPath:      "group/repo",
			GitIdentityKey:   "email:chris@example.com",
			GitEmail:         "chris@example.com",
			GitName:          "Chris Dev",
			ObservedRole:     "commit_author",
			PlatformUsername: "chris",
			Status:           "auto",
		}},
	})

	snapshot, err := svc.IdentityMappings(context.Background(), IdentityFilters{Platform: "gitlab"})
	if err != nil {
		t.Fatalf("IdentityMappings: %v", err)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].ID != 71 {
		t.Fatalf("identity mappings = %+v, want mapping 71", snapshot.Items)
	}
}

func TestServiceResolveIdentityMappingUsesActionTxAndWritesAudit(t *testing.T) {
	store := &fakeStore{
		resolvedIdentityMapping: db.IdentityMapping{
			ID:               71,
			PlatformUsername: "resolved-user",
			Status:           "manual",
		},
	}
	svc := NewService(store, WithActionTxRunner(func(ctx context.Context, fn ActionTxFunc) error {
		return fn(ctx, store)
	}))

	mapping, err := svc.ResolveIdentityMapping(context.Background(), 71, "resolved-user", "99", "admin")
	if err != nil {
		t.Fatalf("ResolveIdentityMapping: %v", err)
	}
	if mapping.PlatformUsername != "resolved-user" {
		t.Fatalf("platform username = %q, want resolved-user", mapping.PlatformUsername)
	}
	if store.resolvedIdentityParams.ID != 71 {
		t.Fatalf("resolved mapping id = %d, want 71", store.resolvedIdentityParams.ID)
	}
	if store.insertedAudit.Action != "resolve_identity_mapping" {
		t.Fatalf("audit action = %q, want resolve_identity_mapping", store.insertedAudit.Action)
	}
	if store.insertedAudit.EntityType != "identity_mapping" {
		t.Fatalf("audit entity type = %q, want identity_mapping", store.insertedAudit.EntityType)
	}
}
