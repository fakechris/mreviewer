package adminapi

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
)

const (
	defaultActiveWorkerWindow = 90 * time.Second
	defaultLookbackWindow     = 24 * time.Hour
	defaultTopProjectsLimit   = 5
	defaultRecentFailures     = 10
)

type Store interface {
	CountPendingQueue(ctx context.Context) (int64, error)
	CountRetryScheduledRuns(ctx context.Context) (int64, error)
	GetOldestWaitingRunCreatedAt(ctx context.Context) (interface{}, error)
	ListTopQueuedProjects(ctx context.Context, limit int32) ([]db.ListTopQueuedProjectsRow, error)
	CountSupersededRunsSince(ctx context.Context, updatedAt time.Time) (int64, error)
	ListActiveWorkersWithCapacity(ctx context.Context, lastSeenAt time.Time) ([]db.ListActiveWorkersWithCapacityRow, error)
	ListRecentFailedRuns(ctx context.Context, limit int32) ([]db.ListRecentFailedRunsRow, error)
	ListFailureCountsByErrorCode(ctx context.Context, updatedAt time.Time) ([]db.ListFailureCountsByErrorCodeRow, error)
	ListWebhookVerificationCounts(ctx context.Context, createdAt time.Time) ([]db.ListWebhookVerificationCountsRow, error)
}

type Option func(*Service)

type Service struct {
	store Store
	now   func() time.Time
}

type QueueSnapshot struct {
	PendingCount            int64           `json:"pending_count"`
	RetryScheduledCount     int64           `json:"retry_scheduled_count"`
	OldestWaitingAgeSeconds int64           `json:"oldest_waiting_age_seconds"`
	TopProjects             []QueuedProject `json:"top_projects"`
	SupersededLast24h       int64           `json:"superseded_last_24h"`
}

type QueuedProject struct {
	PathWithNamespace string `json:"path_with_namespace"`
	QueueDepth        int64  `json:"queue_depth"`
}

type ConcurrencySnapshot struct {
	ActiveWorkers              []ActiveWorker `json:"active_workers"`
	TotalConfiguredConcurrency int64          `json:"total_configured_concurrency"`
	TotalRunningRuns           int64          `json:"total_running_runs"`
}

type ActiveWorker struct {
	WorkerID              string    `json:"worker_id"`
	Hostname              string    `json:"hostname"`
	Version               string    `json:"version"`
	ConfiguredConcurrency int32     `json:"configured_concurrency"`
	StartedAt             time.Time `json:"started_at"`
	LastSeenAt            time.Time `json:"last_seen_at"`
	RunningRuns           int64     `json:"running_runs"`
}

type FailuresSnapshot struct {
	RecentFailedRuns           []RecentFailedRun `json:"recent_failed_runs"`
	ErrorBuckets               []ErrorBucket     `json:"error_buckets"`
	WebhookRejectedLast24h     int64             `json:"webhook_rejected_last_24h"`
	WebhookDeduplicatedLast24h int64             `json:"webhook_deduplicated_last_24h"`
}

type RecentFailedRun struct {
	ID             int64     `json:"id"`
	ProjectID      int64     `json:"project_id"`
	MergeRequestID int64     `json:"merge_request_id"`
	TriggerType    string    `json:"trigger_type"`
	HeadSha        string    `json:"head_sha"`
	ErrorCode      string    `json:"error_code"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ErrorBucket struct {
	ErrorCode string `json:"error_code"`
	Count     int64  `json:"count"`
}

func NewService(store Store, opts ...Option) *Service {
	svc := &Service{store: store, now: time.Now}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	if svc.now == nil {
		svc.now = time.Now
	}
	return svc
}

func WithNow(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func (s *Service) Queue(ctx context.Context) (QueueSnapshot, error) {
	pendingCount, err := s.store.CountPendingQueue(ctx)
	if err != nil {
		return QueueSnapshot{}, err
	}
	retryScheduledCount, err := s.store.CountRetryScheduledRuns(ctx)
	if err != nil {
		return QueueSnapshot{}, err
	}
	oldestWaitingRaw, err := s.store.GetOldestWaitingRunCreatedAt(ctx)
	if err != nil {
		return QueueSnapshot{}, err
	}
	topProjects, err := s.store.ListTopQueuedProjects(ctx, defaultTopProjectsLimit)
	if err != nil {
		return QueueSnapshot{}, err
	}
	supersededCount, err := s.store.CountSupersededRunsSince(ctx, s.now().UTC().Add(-defaultLookbackWindow))
	if err != nil {
		return QueueSnapshot{}, err
	}

	snapshot := QueueSnapshot{
		PendingCount:        pendingCount,
		RetryScheduledCount: retryScheduledCount,
		SupersededLast24h:   supersededCount,
		TopProjects:         make([]QueuedProject, 0, len(topProjects)),
	}
	oldestWaiting, err := normalizeOptionalTime(oldestWaitingRaw)
	if err != nil {
		return QueueSnapshot{}, err
	}
	if oldestWaiting.Valid {
		snapshot.OldestWaitingAgeSeconds = int64(s.now().UTC().Sub(oldestWaiting.Time.UTC()).Seconds())
	}
	for _, item := range topProjects {
		snapshot.TopProjects = append(snapshot.TopProjects, QueuedProject{
			PathWithNamespace: item.PathWithNamespace,
			QueueDepth:        item.QueueDepth,
		})
	}
	return snapshot, nil
}

func normalizeOptionalTime(value interface{}) (sql.NullTime, error) {
	switch v := value.(type) {
	case nil:
		return sql.NullTime{}, nil
	case sql.NullTime:
		return v, nil
	case time.Time:
		return sql.NullTime{Time: v, Valid: true}, nil
	case []byte:
		parsed, err := parseTimeString(string(v))
		if err != nil {
			return sql.NullTime{}, err
		}
		return sql.NullTime{Time: parsed, Valid: true}, nil
	case string:
		parsed, err := parseTimeString(v)
		if err != nil {
			return sql.NullTime{}, err
		}
		return sql.NullTime{Time: parsed, Valid: true}, nil
	default:
		return sql.NullTime{}, fmt.Errorf("adminapi: unsupported time value %T", value)
	}
}

func parseTimeString(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", time.RFC3339} {
		if ts, err := time.Parse(layout, value); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("adminapi: parse time %q", value)
}

func (s *Service) Concurrency(ctx context.Context) (ConcurrencySnapshot, error) {
	rows, err := s.store.ListActiveWorkersWithCapacity(ctx, s.now().UTC().Add(-defaultActiveWorkerWindow))
	if err != nil {
		return ConcurrencySnapshot{}, err
	}

	snapshot := ConcurrencySnapshot{ActiveWorkers: make([]ActiveWorker, 0, len(rows))}
	for _, row := range rows {
		snapshot.ActiveWorkers = append(snapshot.ActiveWorkers, ActiveWorker{
			WorkerID:              row.WorkerID,
			Hostname:              row.Hostname,
			Version:               row.Version,
			ConfiguredConcurrency: row.ConfiguredConcurrency,
			StartedAt:             row.StartedAt,
			LastSeenAt:            row.LastSeenAt,
			RunningRuns:           row.RunningRuns,
		})
		snapshot.TotalConfiguredConcurrency += int64(row.ConfiguredConcurrency)
		snapshot.TotalRunningRuns += row.RunningRuns
	}
	return snapshot, nil
}

func (s *Service) Failures(ctx context.Context) (FailuresSnapshot, error) {
	recentFailedRuns, err := s.store.ListRecentFailedRuns(ctx, defaultRecentFailures)
	if err != nil {
		return FailuresSnapshot{}, err
	}
	failureCounts, err := s.store.ListFailureCountsByErrorCode(ctx, s.now().UTC().Add(-defaultLookbackWindow))
	if err != nil {
		return FailuresSnapshot{}, err
	}
	webhookCounts, err := s.store.ListWebhookVerificationCounts(ctx, s.now().UTC().Add(-defaultLookbackWindow))
	if err != nil {
		return FailuresSnapshot{}, err
	}

	snapshot := FailuresSnapshot{
		RecentFailedRuns: make([]RecentFailedRun, 0, len(recentFailedRuns)),
		ErrorBuckets:     make([]ErrorBucket, 0, len(failureCounts)),
	}
	for _, item := range recentFailedRuns {
		snapshot.RecentFailedRuns = append(snapshot.RecentFailedRuns, RecentFailedRun{
			ID:             item.ID,
			ProjectID:      item.ProjectID,
			MergeRequestID: item.MergeRequestID,
			TriggerType:    item.TriggerType,
			HeadSha:        item.HeadSha,
			ErrorCode:      item.ErrorCode,
			UpdatedAt:      item.UpdatedAt,
		})
	}
	for _, item := range failureCounts {
		snapshot.ErrorBuckets = append(snapshot.ErrorBuckets, ErrorBucket{
			ErrorCode: item.ErrorCode,
			Count:     item.Count,
		})
	}
	for _, item := range webhookCounts {
		switch item.VerificationOutcome {
		case "rejected":
			snapshot.WebhookRejectedLast24h = item.Count
		case "deduplicated":
			snapshot.WebhookDeduplicatedLast24h = item.Count
		}
	}
	return snapshot, nil
}
