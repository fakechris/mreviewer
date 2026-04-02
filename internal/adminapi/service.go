package adminapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
)

const (
	defaultActiveWorkerWindow = 90 * time.Second
	defaultVisibleWorkerWindow = 24 * time.Hour
	defaultLookbackWindow     = 24 * time.Hour
	defaultTopProjectsLimit   = 5
	defaultRecentFailures     = 10
	defaultRecentRuns         = 50
	defaultOwnershipLimit     = 200
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
	ListRunTrendBuckets(ctx context.Context, since time.Time) ([]db.ListRunTrendBucketsRow, error)
	ListWebhookVerificationTrendBuckets(ctx context.Context, since time.Time) ([]db.ListWebhookVerificationTrendBucketsRow, error)
	ListPlatformRunRollups(ctx context.Context, since time.Time) ([]db.ListPlatformRunRollupsRow, error)
	ListProjectRunRollups(ctx context.Context, arg db.ListProjectRunRollupsParams) ([]db.ListProjectRunRollupsRow, error)
	ListRecentRuns(ctx context.Context, arg db.ListRecentRunsParams) ([]db.ListRecentRunsRow, error)
	GetRunDetail(ctx context.Context, id int64) (db.GetRunDetailRow, error)
	ListIdentityMappings(ctx context.Context, arg db.ListIdentityMappingsParams) ([]db.ListIdentityMappingsRow, error)
	GetIdentityMapping(ctx context.Context, id int64) (db.IdentityMapping, error)
	ResolveIdentityMapping(ctx context.Context, arg db.ResolveIdentityMappingParams) error
}

type Option func(*Service)

type Service struct {
	store    Store
	now      func() time.Time
	actionTx actionTxRunner
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
	StaleWorkerCount           int64          `json:"stale_worker_count"`
}

type ActiveWorker struct {
	WorkerID              string    `json:"worker_id"`
	Hostname              string    `json:"hostname"`
	Version               string    `json:"version"`
	ConfiguredConcurrency int32     `json:"configured_concurrency"`
	StartedAt             time.Time `json:"started_at"`
	LastSeenAt            time.Time `json:"last_seen_at"`
	RunningRuns           int64     `json:"running_runs"`
	HeartbeatAgeSeconds   int64     `json:"heartbeat_age_seconds"`
	Stale                 bool      `json:"stale"`
}

type FailuresSnapshot struct {
	RecentFailedRuns           []RecentFailedRun `json:"recent_failed_runs"`
	ErrorBuckets               []ErrorBucket     `json:"error_buckets"`
	WebhookRejectedLast24h     int64             `json:"webhook_rejected_last_24h"`
	WebhookDeduplicatedLast24h int64             `json:"webhook_deduplicated_last_24h"`
}

type TrendsSnapshot struct {
	WindowHours int              `json:"window_hours"`
	Buckets     []TrendBucket    `json:"buckets"`
	Platforms   []PlatformRollup `json:"platforms"`
	Projects    []ProjectRollup  `json:"projects"`
}

type TrendBucket struct {
	BucketStart              time.Time `json:"bucket_start"`
	RunCount                 int64     `json:"run_count"`
	PendingCount             int64     `json:"pending_count"`
	RunningCount             int64     `json:"running_count"`
	CompletedCount           int64     `json:"completed_count"`
	FailedCount              int64     `json:"failed_count"`
	CancelledCount           int64     `json:"cancelled_count"`
	WebhookRejectedCount     int64     `json:"webhook_rejected_count"`
	WebhookDeduplicatedCount int64     `json:"webhook_deduplicated_count"`
}

type PlatformRollup struct {
	Platform       string `json:"platform"`
	RunCount       int64  `json:"run_count"`
	PendingCount   int64  `json:"pending_count"`
	RunningCount   int64  `json:"running_count"`
	CompletedCount int64  `json:"completed_count"`
	FailedCount    int64  `json:"failed_count"`
	CancelledCount int64  `json:"cancelled_count"`
}

type ProjectRollup struct {
	Platform       string `json:"platform"`
	ProjectPath    string `json:"project_path"`
	RunCount       int64  `json:"run_count"`
	PendingCount   int64  `json:"pending_count"`
	RunningCount   int64  `json:"running_count"`
	CompletedCount int64  `json:"completed_count"`
	FailedCount    int64  `json:"failed_count"`
	CancelledCount int64  `json:"cancelled_count"`
}

type RunFilters struct {
	Platform    string
	Status      string
	ErrorCode   string
	ProjectPath string
	HeadSHA     string
	Limit       int32
}

type RunsSnapshot struct {
	Items []RunListItem `json:"items"`
}

type IdentityFilters struct {
	Platform    string
	Status      string
	ProjectPath string
	Limit       int32
}

type IdentityMappingsSnapshot struct {
	Items []IdentityMapping `json:"items"`
}

type OwnershipSnapshot struct {
	Items           []OwnershipSummary `json:"items"`
	UnresolvedCount int64              `json:"unresolved_count"`
}

type OwnershipSummary struct {
	Platform         string   `json:"platform"`
	ProjectPath      string   `json:"project_path"`
	PlatformUsername string   `json:"platform_username"`
	PlatformUserID   string   `json:"platform_user_id"`
	IdentityCount    int64    `json:"identity_count"`
	ObservedRoles    []string `json:"observed_roles"`
	Statuses         []string `json:"statuses"`
	LastSeenRunID    int64    `json:"last_seen_run_id"`
}

type IdentitySuggestionsSnapshot struct {
	Mapping     IdentityMapping      `json:"mapping"`
	Suggestions []IdentitySuggestion `json:"suggestions"`
}

type IdentitySuggestion struct {
	PlatformUsername string   `json:"platform_username"`
	PlatformUserID   string   `json:"platform_user_id"`
	ProjectPath      string   `json:"project_path"`
	SourceStatus     string   `json:"source_status"`
	IdentityCount    int64    `json:"identity_count"`
	MatchScore       int64    `json:"match_score"`
	Reasons          []string `json:"reasons"`
}

type IdentityMapping struct {
	ID               int64             `json:"id"`
	Platform         string            `json:"platform"`
	ProjectPath      string            `json:"project_path"`
	GitIdentityKey   string            `json:"git_identity_key"`
	GitEmail         string            `json:"git_email"`
	GitName          string            `json:"git_name"`
	ObservedRole     string            `json:"observed_role"`
	PlatformUsername string            `json:"platform_username"`
	PlatformUserID   string            `json:"platform_user_id"`
	HeadSHA          string            `json:"head_sha"`
	Confidence       float64           `json:"confidence"`
	Source           string            `json:"source"`
	Status           string            `json:"status"`
	LastSeenRunID    sql.NullInt64     `json:"last_seen_run_id"`
	ResolvedBy       string            `json:"resolved_by"`
	ResolvedAt       sql.NullTime      `json:"resolved_at"`
	ResolutionDetail db.NullRawMessage `json:"resolution_detail"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type RunListItem struct {
	ID                        int64          `json:"id"`
	Platform                  string         `json:"platform"`
	ProjectPath               string         `json:"project_path"`
	WebURL                    sql.NullString `json:"web_url"`
	MergeRequestID            int64          `json:"merge_request_id"`
	Status                    string         `json:"status"`
	ErrorCode                 string         `json:"error_code"`
	TriggerType               string         `json:"trigger_type"`
	HeadSHA                   string         `json:"head_sha"`
	ClaimedBy                 string         `json:"claimed_by"`
	RetryCount                int32          `json:"retry_count"`
	NextRetryAt               sql.NullTime   `json:"next_retry_at"`
	ProviderLatencyMs         int64          `json:"provider_latency_ms"`
	ProviderTokensTotal       int64          `json:"provider_tokens_total"`
	HookAction                string         `json:"hook_action"`
	HookVerificationOutcome   string         `json:"hook_verification_outcome"`
	FindingCount              int64          `json:"finding_count"`
	CommentActionCount        int64          `json:"comment_action_count"`
	CreatedAt                 time.Time      `json:"created_at"`
	UpdatedAt                 time.Time      `json:"updated_at"`
	StartedAt                 sql.NullTime   `json:"started_at"`
	CompletedAt               sql.NullTime   `json:"completed_at"`
	QueueAgeSeconds           int64          `json:"queue_age_seconds"`
	QueueWaitSeconds          int64          `json:"queue_wait_seconds"`
	ProcessingDurationSeconds int64          `json:"processing_duration_seconds"`
}

type RunDetail struct {
	RunListItem
	HookEventID       sql.NullInt64     `json:"hook_event_id"`
	ErrorDetail       sql.NullString    `json:"error_detail"`
	MaxRetries        int32             `json:"max_retries"`
	IdempotencyKey    string            `json:"idempotency_key"`
	ScopeJSON         db.NullRawMessage `json:"scope_json"`
	SupersededByRunID sql.NullInt64     `json:"superseded_by_run_id"`
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
	rows, err := s.store.ListActiveWorkersWithCapacity(ctx, s.now().UTC().Add(-defaultVisibleWorkerWindow))
	if err != nil {
		return ConcurrencySnapshot{}, err
	}

	snapshot := ConcurrencySnapshot{ActiveWorkers: make([]ActiveWorker, 0, len(rows))}
	for _, row := range rows {
		heartbeatAgeSeconds := int64(s.now().UTC().Sub(row.LastSeenAt.UTC()).Seconds())
		stale := row.LastSeenAt.UTC().Before(s.now().UTC().Add(-defaultActiveWorkerWindow))
		snapshot.ActiveWorkers = append(snapshot.ActiveWorkers, ActiveWorker{
			WorkerID:              row.WorkerID,
			Hostname:              row.Hostname,
			Version:               row.Version,
			ConfiguredConcurrency: row.ConfiguredConcurrency,
			StartedAt:             row.StartedAt,
			LastSeenAt:            row.LastSeenAt,
			RunningRuns:           row.RunningRuns,
			HeartbeatAgeSeconds:   heartbeatAgeSeconds,
			Stale:                 stale,
		})
		snapshot.TotalConfiguredConcurrency += int64(row.ConfiguredConcurrency)
		snapshot.TotalRunningRuns += row.RunningRuns
		if stale {
			snapshot.StaleWorkerCount++
		}
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

func (s *Service) Trends(ctx context.Context) (TrendsSnapshot, error) {
	since := s.now().UTC().Add(-defaultLookbackWindow)
	runBuckets, err := s.store.ListRunTrendBuckets(ctx, since)
	if err != nil {
		return TrendsSnapshot{}, err
	}
	webhookBuckets, err := s.store.ListWebhookVerificationTrendBuckets(ctx, since)
	if err != nil {
		return TrendsSnapshot{}, err
	}
	platformRows, err := s.store.ListPlatformRunRollups(ctx, since)
	if err != nil {
		return TrendsSnapshot{}, err
	}
	projectRows, err := s.store.ListProjectRunRollups(ctx, db.ListProjectRunRollupsParams{
		Since:      since,
		LimitCount: defaultTopProjectsLimit,
	})
	if err != nil {
		return TrendsSnapshot{}, err
	}

	bucketsByKey := make(map[string]int, len(runBuckets))
	snapshot := TrendsSnapshot{
		WindowHours: int(defaultLookbackWindow / time.Hour),
		Buckets:     make([]TrendBucket, 0, len(runBuckets)),
		Platforms:   make([]PlatformRollup, 0, len(platformRows)),
		Projects:    make([]ProjectRollup, 0, len(projectRows)),
	}
	for _, row := range runBuckets {
		bucketStart, err := parseTimeString(row.BucketStart)
		if err != nil {
			return TrendsSnapshot{}, err
		}
		snapshot.Buckets = append(snapshot.Buckets, TrendBucket{
			BucketStart:    bucketStart,
			RunCount:       row.RunCount,
			PendingCount:   row.PendingCount,
			RunningCount:   row.RunningCount,
			CompletedCount: row.CompletedCount,
			FailedCount:    row.FailedCount,
			CancelledCount: row.CancelledCount,
		})
		bucketsByKey[row.BucketStart] = len(snapshot.Buckets) - 1
	}
	for _, row := range webhookBuckets {
		idx, ok := bucketsByKey[row.BucketStart]
		if !ok {
			bucketStart, err := parseTimeString(row.BucketStart)
			if err != nil {
				return TrendsSnapshot{}, err
			}
			snapshot.Buckets = append(snapshot.Buckets, TrendBucket{BucketStart: bucketStart})
			idx = len(snapshot.Buckets) - 1
			bucketsByKey[row.BucketStart] = idx
		}
		switch row.VerificationOutcome {
		case "rejected":
			snapshot.Buckets[idx].WebhookRejectedCount = row.Count
		case "deduplicated":
			snapshot.Buckets[idx].WebhookDeduplicatedCount = row.Count
		}
	}
	sort.Slice(snapshot.Buckets, func(i, j int) bool {
		return snapshot.Buckets[i].BucketStart.After(snapshot.Buckets[j].BucketStart)
	})
	for _, row := range platformRows {
		snapshot.Platforms = append(snapshot.Platforms, PlatformRollup{
			Platform:       row.Platform,
			RunCount:       row.RunCount,
			PendingCount:   row.PendingCount,
			RunningCount:   row.RunningCount,
			CompletedCount: row.CompletedCount,
			FailedCount:    row.FailedCount,
			CancelledCount: row.CancelledCount,
		})
	}
	for _, row := range projectRows {
		snapshot.Projects = append(snapshot.Projects, ProjectRollup{
			Platform:       row.Platform,
			ProjectPath:    row.ProjectPath,
			RunCount:       row.RunCount,
			PendingCount:   row.PendingCount,
			RunningCount:   row.RunningCount,
			CompletedCount: row.CompletedCount,
			FailedCount:    row.FailedCount,
			CancelledCount: row.CancelledCount,
		})
	}
	return snapshot, nil
}

func (s *Service) Runs(ctx context.Context, filters RunFilters) (RunsSnapshot, error) {
	limit := filters.Limit
	if limit <= 0 {
		limit = defaultRecentRuns
	}
	rows, err := s.store.ListRecentRuns(ctx, db.ListRecentRunsParams{
		Platform:    filters.Platform,
		Status:      filters.Status,
		ErrorCode:   filters.ErrorCode,
		ProjectPath: filters.ProjectPath,
		HeadSha:     filters.HeadSHA,
		LimitCount:  limit,
	})
	if err != nil {
		return RunsSnapshot{}, err
	}
	snapshot := RunsSnapshot{Items: make([]RunListItem, 0, len(rows))}
	for _, row := range rows {
		snapshot.Items = append(snapshot.Items, buildRunListItem(s.now(), row))
	}
	return snapshot, nil
}

func (s *Service) RunDetail(ctx context.Context, runID int64) (RunDetail, error) {
	row, err := s.store.GetRunDetail(ctx, runID)
	if err != nil {
		return RunDetail{}, err
	}
	return RunDetail{
		RunListItem: buildRunListItem(s.now(), db.ListRecentRunsRow{
			ID:                      row.ID,
			Platform:                row.Platform,
			ProjectPath:             row.ProjectPath,
			WebUrl:                  row.WebUrl,
			MergeRequestID:          row.MergeRequestID,
			Status:                  row.Status,
			ErrorCode:               row.ErrorCode,
			TriggerType:             row.TriggerType,
			HeadSha:                 row.HeadSha,
			ClaimedBy:               row.ClaimedBy,
			RetryCount:              row.RetryCount,
			NextRetryAt:             row.NextRetryAt,
			ProviderLatencyMs:       row.ProviderLatencyMs,
			ProviderTokensTotal:     row.ProviderTokensTotal,
			HookAction:              row.HookAction,
			HookVerificationOutcome: row.HookVerificationOutcome,
			FindingCount:            row.FindingCount,
			CommentActionCount:      row.CommentActionCount,
			CreatedAt:               row.CreatedAt,
			UpdatedAt:               row.UpdatedAt,
			StartedAt:               row.StartedAt,
			CompletedAt:             row.CompletedAt,
		}),
		HookEventID:       row.HookEventID,
		ErrorDetail:       row.ErrorDetail,
		MaxRetries:        row.MaxRetries,
		IdempotencyKey:    row.IdempotencyKey,
		ScopeJSON:         row.ScopeJson,
		SupersededByRunID: row.SupersededByRunID,
	}, nil
}

func (s *Service) IdentityMappings(ctx context.Context, filters IdentityFilters) (IdentityMappingsSnapshot, error) {
	limit := filters.Limit
	if limit <= 0 {
		limit = defaultRecentRuns
	}
	rows, err := s.store.ListIdentityMappings(ctx, db.ListIdentityMappingsParams{
		Platform:    filters.Platform,
		Status:      filters.Status,
		ProjectPath: filters.ProjectPath,
		LimitCount:  limit,
	})
	if err != nil {
		return IdentityMappingsSnapshot{}, err
	}
	snapshot := IdentityMappingsSnapshot{Items: make([]IdentityMapping, 0, len(rows))}
	for _, row := range rows {
		snapshot.Items = append(snapshot.Items, IdentityMapping{
			ID:               row.ID,
			Platform:         row.Platform,
			ProjectPath:      row.ProjectPath,
			GitIdentityKey:   row.GitIdentityKey,
			GitEmail:         row.GitEmail,
			GitName:          row.GitName,
			ObservedRole:     row.ObservedRole,
			PlatformUsername: row.PlatformUsername,
			PlatformUserID:   row.PlatformUserID,
			HeadSHA:          row.HeadSha,
			Confidence:       row.Confidence,
			Source:           row.Source,
			Status:           row.Status,
			LastSeenRunID:    row.LastSeenRunID,
			ResolvedBy:       row.ResolvedBy,
			ResolvedAt:       row.ResolvedAt,
			ResolutionDetail: row.ResolutionDetail,
			CreatedAt:        row.CreatedAt,
			UpdatedAt:        row.UpdatedAt,
		})
	}
	return snapshot, nil
}

func (s *Service) Ownership(ctx context.Context, filters IdentityFilters) (OwnershipSnapshot, error) {
	limit := filters.Limit
	if limit <= 0 {
		limit = defaultOwnershipLimit
	}
	rows, err := s.store.ListIdentityMappings(ctx, db.ListIdentityMappingsParams{
		Platform:    filters.Platform,
		Status:      filters.Status,
		ProjectPath: filters.ProjectPath,
		LimitCount:  limit,
	})
	if err != nil {
		return OwnershipSnapshot{}, err
	}
	type ownershipKey struct {
		platform         string
		projectPath      string
		platformUsername string
		platformUserID   string
	}
	grouped := map[ownershipKey]int{}
	snapshot := OwnershipSnapshot{Items: []OwnershipSummary{}}
	for _, row := range rows {
		if strings.TrimSpace(row.PlatformUsername) == "" {
			snapshot.UnresolvedCount++
			continue
		}
		key := ownershipKey{
			platform:         row.Platform,
			projectPath:      row.ProjectPath,
			platformUsername: row.PlatformUsername,
			platformUserID:   row.PlatformUserID,
		}
		idx, ok := grouped[key]
		if !ok {
			snapshot.Items = append(snapshot.Items, OwnershipSummary{
				Platform:         row.Platform,
				ProjectPath:      row.ProjectPath,
				PlatformUsername: row.PlatformUsername,
				PlatformUserID:   row.PlatformUserID,
			})
			idx = len(snapshot.Items) - 1
			grouped[key] = idx
		}
		snapshot.Items[idx].IdentityCount++
		if row.LastSeenRunID.Valid && row.LastSeenRunID.Int64 > snapshot.Items[idx].LastSeenRunID {
			snapshot.Items[idx].LastSeenRunID = row.LastSeenRunID.Int64
		}
		snapshot.Items[idx].ObservedRoles = appendUnique(snapshot.Items[idx].ObservedRoles, row.ObservedRole)
		snapshot.Items[idx].Statuses = appendUnique(snapshot.Items[idx].Statuses, row.Status)
	}
	return snapshot, nil
}

func (s *Service) IdentitySuggestions(ctx context.Context, mappingID int64) (IdentitySuggestionsSnapshot, error) {
	if mappingID <= 0 {
		return IdentitySuggestionsSnapshot{}, &ActionError{StatusCode: 400, Message: "identity mapping id is required"}
	}
	target, err := s.store.GetIdentityMapping(ctx, mappingID)
	if err != nil {
		return IdentitySuggestionsSnapshot{}, err
	}
	rows, err := s.store.ListIdentityMappings(ctx, db.ListIdentityMappingsParams{
		Platform:   target.Platform,
		LimitCount: defaultOwnershipLimit,
	})
	if err != nil {
		return IdentitySuggestionsSnapshot{}, err
	}
	type suggestionKey struct {
		username string
		userID   string
		project  string
		status   string
	}
	grouped := map[suggestionKey]int{}
	result := IdentitySuggestionsSnapshot{
		Mapping:     buildIdentityMapping(target),
		Suggestions: []IdentitySuggestion{},
	}
	for _, row := range rows {
		if row.ID == mappingID || strings.TrimSpace(row.PlatformUsername) == "" {
			continue
		}
		score, reasons := scoreIdentitySuggestion(target, row)
		if score <= 0 {
			continue
		}
		key := suggestionKey{
			username: row.PlatformUsername,
			userID:   row.PlatformUserID,
			project:  row.ProjectPath,
			status:   row.Status,
		}
		idx, ok := grouped[key]
		if !ok {
			result.Suggestions = append(result.Suggestions, IdentitySuggestion{
				PlatformUsername: row.PlatformUsername,
				PlatformUserID:   row.PlatformUserID,
				ProjectPath:      row.ProjectPath,
				SourceStatus:     row.Status,
				MatchScore:       score,
				Reasons:          reasons,
			})
			idx = len(result.Suggestions) - 1
			grouped[key] = idx
		}
		result.Suggestions[idx].IdentityCount++
		if score > result.Suggestions[idx].MatchScore {
			result.Suggestions[idx].MatchScore = score
			result.Suggestions[idx].Reasons = reasons
		}
	}
	sortIdentitySuggestions(result.Suggestions)
	return result, nil
}

func (s *Service) ResolveIdentityMapping(ctx context.Context, mappingID int64, platformUsername, platformUserID, actor string) (IdentityMapping, error) {
	if s == nil || s.actionTx == nil {
		return IdentityMapping{}, fmt.Errorf("adminapi: action transactions are not configured")
	}
	platformUsername = strings.TrimSpace(platformUsername)
	if mappingID <= 0 || platformUsername == "" {
		return IdentityMapping{}, &ActionError{StatusCode: 400, Message: "identity mapping and platform username are required"}
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		actor = "admin"
	}
	var snapshot IdentityMapping
	err := s.actionTx(ctx, func(ctx context.Context, store ActionStore) error {
		if err := store.ResolveIdentityMapping(ctx, db.ResolveIdentityMappingParams{
			ID:               mappingID,
			PlatformUsername: platformUsername,
			PlatformUserID:   strings.TrimSpace(platformUserID),
			ResolvedBy:       actor,
		}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &ActionError{StatusCode: 404, Message: "identity mapping not found"}
			}
			return err
		}
		mapping, err := store.GetIdentityMapping(ctx, mappingID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &ActionError{StatusCode: 404, Message: "identity mapping not found"}
			}
			return err
		}
		if _, err := store.InsertAuditLog(ctx, db.InsertAuditLogParams{
			EntityType: "identity_mapping",
			EntityID:   mappingID,
			Action:     "resolve_identity_mapping",
			Actor:      actor,
			Detail:     mustMarshalIdentityResolutionDetail(mappingID, platformUsername, platformUserID),
		}); err != nil {
			return err
		}
		snapshot = buildIdentityMapping(mapping)
		return nil
	})
	return snapshot, err
}

func buildIdentityMapping(mapping db.IdentityMapping) IdentityMapping {
	return IdentityMapping{
		ID:               mapping.ID,
		Platform:         mapping.Platform,
		ProjectPath:      mapping.ProjectPath,
		GitIdentityKey:   mapping.GitIdentityKey,
		GitEmail:         mapping.GitEmail,
		GitName:          mapping.GitName,
		ObservedRole:     mapping.ObservedRole,
		PlatformUsername: mapping.PlatformUsername,
		PlatformUserID:   mapping.PlatformUserID,
		HeadSHA:          mapping.HeadSha,
		Confidence:       mapping.Confidence,
		Source:           mapping.Source,
		Status:           mapping.Status,
		LastSeenRunID:    mapping.LastSeenRunID,
		ResolvedBy:       mapping.ResolvedBy,
		ResolvedAt:       mapping.ResolvedAt,
		ResolutionDetail: mapping.ResolutionDetail,
		CreatedAt:        mapping.CreatedAt,
		UpdatedAt:        mapping.UpdatedAt,
	}
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func scoreIdentitySuggestion(target db.IdentityMapping, candidate db.ListIdentityMappingsRow) (int64, []string) {
	var score int64
	reasons := make([]string, 0, 4)
	if target.GitIdentityKey != "" && candidate.GitIdentityKey == target.GitIdentityKey {
		score += 90
		reasons = append(reasons, "same git identity")
	}
	if target.GitEmail != "" && strings.EqualFold(candidate.GitEmail, target.GitEmail) {
		score += 75
		reasons = append(reasons, "same email")
	}
	if target.GitName != "" && strings.EqualFold(candidate.GitName, target.GitName) {
		score += 35
		reasons = append(reasons, "same git name")
	}
	if target.ProjectPath != "" && candidate.ProjectPath == target.ProjectPath {
		score += 20
		reasons = append(reasons, "same project")
	}
	if candidate.Status == "manual" {
		score += 10
		reasons = append(reasons, "manual mapping")
	}
	return score, reasons
}

func sortIdentitySuggestions(items []IdentitySuggestion) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].MatchScore == items[j].MatchScore {
			if items[i].IdentityCount == items[j].IdentityCount {
				if items[i].PlatformUsername == items[j].PlatformUsername {
					return items[i].ProjectPath < items[j].ProjectPath
				}
				return items[i].PlatformUsername < items[j].PlatformUsername
			}
			return items[i].IdentityCount > items[j].IdentityCount
		}
		return items[i].MatchScore > items[j].MatchScore
	})
}

func buildRunListItem(now time.Time, row db.ListRecentRunsRow) RunListItem {
	item := RunListItem{
		ID:                      row.ID,
		Platform:                row.Platform,
		ProjectPath:             row.ProjectPath,
		WebURL:                  row.WebUrl,
		MergeRequestID:          row.MergeRequestID,
		Status:                  row.Status,
		ErrorCode:               row.ErrorCode,
		TriggerType:             row.TriggerType,
		HeadSHA:                 row.HeadSha,
		ClaimedBy:               row.ClaimedBy,
		RetryCount:              row.RetryCount,
		NextRetryAt:             row.NextRetryAt,
		ProviderLatencyMs:       row.ProviderLatencyMs,
		ProviderTokensTotal:     row.ProviderTokensTotal,
		HookAction:              row.HookAction,
		HookVerificationOutcome: row.HookVerificationOutcome,
		FindingCount:            row.FindingCount,
		CommentActionCount:      row.CommentActionCount,
		CreatedAt:               row.CreatedAt,
		UpdatedAt:               row.UpdatedAt,
		StartedAt:               row.StartedAt,
		CompletedAt:             row.CompletedAt,
	}
	item.QueueAgeSeconds = int64(now.UTC().Sub(row.CreatedAt.UTC()).Seconds())
	if row.StartedAt.Valid {
		item.QueueWaitSeconds = int64(row.StartedAt.Time.UTC().Sub(row.CreatedAt.UTC()).Seconds())
	} else {
		item.QueueWaitSeconds = item.QueueAgeSeconds
	}
	if row.StartedAt.Valid {
		end := now.UTC()
		if row.CompletedAt.Valid {
			end = row.CompletedAt.Time.UTC()
		}
		item.ProcessingDurationSeconds = int64(end.Sub(row.StartedAt.Time.UTC()).Seconds())
	}
	return item
}
