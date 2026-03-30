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

func TestServiceQueueSummary(t *testing.T) {
	now := time.Date(2026, time.March, 29, 19, 0, 0, 0, time.UTC)
	svc := NewService(fakeStore{
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
	svc := NewService(fakeStore{
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
	svc := NewService(fakeStore{
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
