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
	runTrendBuckets           []db.ListRunTrendBucketsRow
	webhookTrendBuckets       []db.ListWebhookVerificationTrendBucketsRow
	platformRollups           []db.ListPlatformRunRollupsRow
	projectRollups            []db.ListProjectRunRollupsRow
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

func (f fakeStore) ListRunTrendBuckets(context.Context, time.Time) ([]db.ListRunTrendBucketsRow, error) {
	return f.runTrendBuckets, nil
}

func (f fakeStore) ListWebhookVerificationTrendBuckets(context.Context, time.Time) ([]db.ListWebhookVerificationTrendBucketsRow, error) {
	return f.webhookTrendBuckets, nil
}

func (f fakeStore) ListPlatformRunRollups(context.Context, time.Time) ([]db.ListPlatformRunRollupsRow, error) {
	return f.platformRollups, nil
}

func (f fakeStore) ListProjectRunRollups(context.Context, db.ListProjectRunRollupsParams) ([]db.ListProjectRunRollupsRow, error) {
	return f.projectRollups, nil
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
			{WorkerID: "worker-1", Hostname: "host-a", Version: "dev", ConfiguredConcurrency: 4, RunningRuns: 3, LastSeenAt: now.Add(-30 * time.Second)},
			{WorkerID: "worker-2", Hostname: "host-b", Version: "dev", ConfiguredConcurrency: 2, RunningRuns: 1, LastSeenAt: now.Add(-3 * time.Minute)},
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
	if snapshot.StaleWorkerCount != 1 {
		t.Fatalf("stale_worker_count = %d, want 1", snapshot.StaleWorkerCount)
	}
	if snapshot.ActiveWorkers[1].HeartbeatAgeSeconds != 180 {
		t.Fatalf("heartbeat_age_seconds = %d, want 180", snapshot.ActiveWorkers[1].HeartbeatAgeSeconds)
	}
	if !snapshot.ActiveWorkers[1].Stale {
		t.Fatal("worker-2 stale = false, want true")
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

func TestServiceTrendsSummary(t *testing.T) {
	now := time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC)
	svc := NewService(&fakeStore{
		runTrendBuckets: []db.ListRunTrendBucketsRow{
			{
				BucketStart:    "2026-04-01 11:00:00",
				RunCount:       3,
				PendingCount:   1,
				RunningCount:   0,
				CompletedCount: 1,
				FailedCount:    1,
				CancelledCount: 0,
			},
		},
		webhookTrendBuckets: []db.ListWebhookVerificationTrendBucketsRow{
			{BucketStart: "2026-04-01 11:00:00", VerificationOutcome: "rejected", Count: 2},
			{BucketStart: "2026-04-01 11:00:00", VerificationOutcome: "deduplicated", Count: 1},
		},
		platformRollups: []db.ListPlatformRunRollupsRow{
			{Platform: "github", RunCount: 2, CompletedCount: 1, FailedCount: 1},
			{Platform: "gitlab", RunCount: 1, PendingCount: 1},
		},
		projectRollups: []db.ListProjectRunRollupsRow{
			{Platform: "github", ProjectPath: "acme/repo", RunCount: 2, CompletedCount: 1, FailedCount: 1},
		},
	}, WithNow(func() time.Time { return now }))

	snapshot, err := svc.Trends(context.Background())
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	if snapshot.WindowHours != 24 {
		t.Fatalf("window_hours = %d, want 24", snapshot.WindowHours)
	}
	if len(snapshot.Buckets) != 1 {
		t.Fatalf("buckets len = %d, want 1", len(snapshot.Buckets))
	}
	if snapshot.Buckets[0].WebhookRejectedCount != 2 || snapshot.Buckets[0].WebhookDeduplicatedCount != 1 {
		t.Fatalf("bucket webhook counts = %+v, want rejected=2 deduplicated=1", snapshot.Buckets[0])
	}
	if len(snapshot.Platforms) != 2 {
		t.Fatalf("platforms len = %d, want 2", len(snapshot.Platforms))
	}
	if len(snapshot.Projects) != 1 || snapshot.Projects[0].ProjectPath != "acme/repo" {
		t.Fatalf("projects = %+v, want acme/repo", snapshot.Projects)
	}
}

func TestServiceTrendsSummaryKeepsWebhookCountsAcrossBucketExpansion(t *testing.T) {
	now := time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC)
	svc := NewService(&fakeStore{
		runTrendBuckets: []db.ListRunTrendBucketsRow{
			{BucketStart: "2026-04-01 11:00:00", RunCount: 1},
		},
		webhookTrendBuckets: []db.ListWebhookVerificationTrendBucketsRow{
			{BucketStart: "2026-04-01 10:00:00", VerificationOutcome: "rejected", Count: 2},
			{BucketStart: "2026-04-01 11:00:00", VerificationOutcome: "deduplicated", Count: 1},
		},
	}, WithNow(func() time.Time { return now }))

	snapshot, err := svc.Trends(context.Background())
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	if len(snapshot.Buckets) != 2 {
		t.Fatalf("buckets len = %d, want 2", len(snapshot.Buckets))
	}
	if !snapshot.Buckets[0].BucketStart.After(snapshot.Buckets[1].BucketStart) {
		t.Fatalf("bucket order = [%s, %s], want descending by bucket_start", snapshot.Buckets[0].BucketStart, snapshot.Buckets[1].BucketStart)
	}
	for _, bucket := range snapshot.Buckets {
		if bucket.BucketStart.Equal(time.Date(2026, time.April, 1, 11, 0, 0, 0, time.UTC)) && bucket.WebhookDeduplicatedCount != 1 {
			t.Fatalf("11:00 bucket = %+v, want deduplicated=1", bucket)
		}
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

func TestServiceIdentitySuggestions(t *testing.T) {
	svc := NewService(&fakeStore{
		resolvedIdentityMapping: db.IdentityMapping{
			ID:             71,
			Platform:       "gitlab",
			ProjectPath:    "group/repo",
			GitIdentityKey: "email:chris@example.com",
			GitEmail:       "chris@example.com",
			GitName:        "Chris Dev",
			Status:         "unresolved",
		},
		identityMappings: []db.ListIdentityMappingsRow{
			{
				ID:               72,
				Platform:         "gitlab",
				ProjectPath:      "group/repo",
				GitIdentityKey:   "email:chris@example.com",
				GitEmail:         "chris@example.com",
				GitName:          "Chris Dev",
				PlatformUsername: "chris",
				PlatformUserID:   "99",
				Status:           "manual",
			},
			{
				ID:               73,
				Platform:         "gitlab",
				ProjectPath:      "group/other",
				GitIdentityKey:   "name:Chris Dev",
				GitName:          "Chris Dev",
				PlatformUsername: "christopher",
				Status:           "auto",
			},
		},
	})

	snapshot, err := svc.IdentitySuggestions(context.Background(), 71)
	if err != nil {
		t.Fatalf("IdentitySuggestions: %v", err)
	}
	if snapshot.Mapping.ID != 71 {
		t.Fatalf("mapping id = %d, want 71", snapshot.Mapping.ID)
	}
	if len(snapshot.Suggestions) == 0 {
		t.Fatal("suggestions len = 0, want > 0")
	}
	if snapshot.Suggestions[0].PlatformUsername != "chris" {
		t.Fatalf("top suggestion = %+v, want chris", snapshot.Suggestions[0])
	}
	if snapshot.Suggestions[0].MatchScore <= snapshot.Suggestions[len(snapshot.Suggestions)-1].MatchScore {
		t.Fatalf("suggestions not ranked descending: %+v", snapshot.Suggestions)
	}
}

func TestServiceIdentitySuggestionsAggregatesAfterSliceGrowth(t *testing.T) {
	svc := NewService(&fakeStore{
		resolvedIdentityMapping: db.IdentityMapping{
			ID:             71,
			Platform:       "gitlab",
			ProjectPath:    "group/repo",
			GitEmail:       "chris@example.com",
			GitName:        "Chris Dev",
			GitIdentityKey: "email:chris@example.com",
		},
		identityMappings: []db.ListIdentityMappingsRow{
			{ID: 72, Platform: "gitlab", ProjectPath: "group/repo", GitEmail: "chris@example.com", GitIdentityKey: "email:chris@example.com", PlatformUsername: "chris", PlatformUserID: "99", Status: "manual"},
			{ID: 73, Platform: "gitlab", ProjectPath: "group/other", GitName: "Chris Dev", PlatformUsername: "other", Status: "auto"},
			{ID: 74, Platform: "gitlab", ProjectPath: "group/repo", GitEmail: "chris@example.com", GitIdentityKey: "email:chris@example.com", PlatformUsername: "chris", PlatformUserID: "99", Status: "manual"},
		},
	})

	snapshot, err := svc.IdentitySuggestions(context.Background(), 71)
	if err != nil {
		t.Fatalf("IdentitySuggestions: %v", err)
	}
	if len(snapshot.Suggestions) < 2 {
		t.Fatalf("suggestions len = %d, want >= 2", len(snapshot.Suggestions))
	}
	if snapshot.Suggestions[0].PlatformUsername != "chris" || snapshot.Suggestions[0].IdentityCount != 2 {
		t.Fatalf("top suggestion = %+v, want chris with identity_count=2", snapshot.Suggestions[0])
	}
}

func TestServiceOwnershipSummary(t *testing.T) {
	svc := NewService(&fakeStore{
		identityMappings: []db.ListIdentityMappingsRow{
			{
				ID:               71,
				Platform:         "gitlab",
				ProjectPath:      "group/repo",
				ObservedRole:     "commit_author",
				PlatformUsername: "chris",
				PlatformUserID:   "99",
				Status:           "manual",
				LastSeenRunID:    sql.NullInt64{Int64: 31, Valid: true},
			},
			{
				ID:               72,
				Platform:         "gitlab",
				ProjectPath:      "group/repo",
				ObservedRole:     "commit_committer",
				PlatformUsername: "chris",
				PlatformUserID:   "99",
				Status:           "auto",
				LastSeenRunID:    sql.NullInt64{Int64: 32, Valid: true},
			},
			{
				ID:             73,
				Platform:       "gitlab",
				ProjectPath:    "group/repo",
				ObservedRole:   "commit_author",
				Status:         "unresolved",
				LastSeenRunID:  sql.NullInt64{Int64: 33, Valid: true},
				GitIdentityKey: "email:other@example.com",
			},
		},
	})

	snapshot, err := svc.Ownership(context.Background(), IdentityFilters{Platform: "gitlab", ProjectPath: "group/repo"})
	if err != nil {
		t.Fatalf("Ownership: %v", err)
	}
	if snapshot.UnresolvedCount != 1 {
		t.Fatalf("unresolved_count = %d, want 1", snapshot.UnresolvedCount)
	}
	if len(snapshot.Items) != 1 {
		t.Fatalf("ownership items = %d, want 1", len(snapshot.Items))
	}
	if snapshot.Items[0].IdentityCount != 2 {
		t.Fatalf("identity_count = %d, want 2", snapshot.Items[0].IdentityCount)
	}
	if len(snapshot.Items[0].ObservedRoles) != 2 {
		t.Fatalf("observed_roles = %+v, want 2 roles", snapshot.Items[0].ObservedRoles)
	}
}

func TestServiceOwnershipSummaryAggregatesAfterSliceGrowth(t *testing.T) {
	svc := NewService(&fakeStore{
		identityMappings: []db.ListIdentityMappingsRow{
			{ID: 71, Platform: "gitlab", ProjectPath: "group/repo", ObservedRole: "commit_author", PlatformUsername: "chris", PlatformUserID: "99", Status: "manual", LastSeenRunID: sql.NullInt64{Int64: 31, Valid: true}},
			{ID: 72, Platform: "gitlab", ProjectPath: "group/repo", ObservedRole: "commit_author", PlatformUsername: "other", PlatformUserID: "100", Status: "manual", LastSeenRunID: sql.NullInt64{Int64: 32, Valid: true}},
			{ID: 73, Platform: "gitlab", ProjectPath: "group/repo", ObservedRole: "commit_committer", PlatformUsername: "chris", PlatformUserID: "99", Status: "auto", LastSeenRunID: sql.NullInt64{Int64: 33, Valid: true}},
		},
	})

	snapshot, err := svc.Ownership(context.Background(), IdentityFilters{Platform: "gitlab", ProjectPath: "group/repo"})
	if err != nil {
		t.Fatalf("Ownership: %v", err)
	}
	if len(snapshot.Items) != 2 {
		t.Fatalf("ownership items = %d, want 2", len(snapshot.Items))
	}
	for _, item := range snapshot.Items {
		if item.PlatformUsername == "chris" {
			if item.IdentityCount != 2 || item.LastSeenRunID != 33 || len(item.ObservedRoles) != 2 {
				t.Fatalf("chris ownership = %+v, want identity_count=2 last_seen_run_id=33 roles=2", item)
			}
		}
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
