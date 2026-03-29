package sqlitedb

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mreviewer/mreviewer/internal/db"
)

func (q *Queries) CancelPendingRunsForMR(ctx context.Context, mergeRequestID int64) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE review_runs
		 SET status = 'cancelled', next_retry_at = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE merge_request_id = ?
		   AND (status IN ('pending', 'running') OR (status = 'failed' AND next_retry_at IS NOT NULL))`,
		mergeRequestID)
	return err
}

func (q *Queries) ClaimReviewRun(ctx context.Context, arg db.ClaimReviewRunParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE review_runs
		 SET status = 'running',
		     claimed_by = ?,
		     claimed_at = CURRENT_TIMESTAMP,
		     started_at = COALESCE(started_at, CURRENT_TIMESTAMP),
		     next_retry_at = NULL,
		     error_code = '',
		     error_detail = NULL,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		arg.ClaimedBy, arg.ID)
	return err
}

// GetNextClaimableReviewRun: FOR UPDATE SKIP LOCKED removed (SQLite single-writer).
func (q *Queries) GetNextClaimableReviewRun(ctx context.Context) (db.ReviewRun, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, project_id, merge_request_id, hook_event_id, trigger_type, head_sha,
		        status, error_code, error_detail, retry_count, max_retries, next_retry_at,
		        claimed_by, claimed_at, started_at, completed_at, provider_latency_ms,
		        provider_tokens_total, idempotency_key, created_at, updated_at, scope_json
		 FROM review_runs
		 WHERE status = 'pending'
		    OR (status = 'failed' AND next_retry_at IS NOT NULL AND next_retry_at <= CURRENT_TIMESTAMP)
		 ORDER BY
		   CASE WHEN status = 'pending' THEN 0 ELSE 1 END,
		   COALESCE(next_retry_at, created_at) ASC,
		   created_at ASC,
		   id ASC
		 LIMIT 1`)
	return scanReviewRun(row)
}

func (q *Queries) GetReviewRun(ctx context.Context, id int64) (db.ReviewRun, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, project_id, merge_request_id, hook_event_id, trigger_type, head_sha,
		        status, error_code, error_detail, retry_count, max_retries, next_retry_at,
		        claimed_by, claimed_at, started_at, completed_at, provider_latency_ms,
		        provider_tokens_total, idempotency_key, created_at, updated_at, scope_json
		 FROM review_runs WHERE id = ? LIMIT 1`, id)
	return scanReviewRun(row)
}

func (q *Queries) GetReviewRunByIdempotencyKey(ctx context.Context, idempotencyKey string) (db.ReviewRun, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, project_id, merge_request_id, hook_event_id, trigger_type, head_sha,
		        status, error_code, error_detail, retry_count, max_retries, next_retry_at,
		        claimed_by, claimed_at, started_at, completed_at, provider_latency_ms,
		        provider_tokens_total, idempotency_key, created_at, updated_at, scope_json
		 FROM review_runs WHERE idempotency_key = ? LIMIT 1`, idempotencyKey)
	return scanReviewRun(row)
}

func (q *Queries) InsertReviewRun(ctx context.Context, arg db.InsertReviewRunParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO review_runs (project_id, merge_request_id, hook_event_id, trigger_type, head_sha, status, max_retries, idempotency_key, scope_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		arg.ProjectID, arg.MergeRequestID, arg.HookEventID, arg.TriggerType,
		arg.HeadSha, arg.Status, arg.MaxRetries, arg.IdempotencyKey, arg.ScopeJson)
}

func (q *Queries) ListPendingRuns(ctx context.Context, limit int32) ([]db.ReviewRun, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, project_id, merge_request_id, hook_event_id, trigger_type, head_sha,
		        status, error_code, error_detail, retry_count, max_retries, next_retry_at,
		        claimed_by, claimed_at, started_at, completed_at, provider_latency_ms,
		        provider_tokens_total, idempotency_key, created_at, updated_at, scope_json
		 FROM review_runs
		 WHERE status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= CURRENT_TIMESTAMP)
		 ORDER BY created_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReviewRunRows(rows)
}

func (q *Queries) ListReviewRunsByMR(ctx context.Context, mergeRequestID int64) ([]db.ReviewRun, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, project_id, merge_request_id, hook_event_id, trigger_type, head_sha,
		        status, error_code, error_detail, retry_count, max_retries, next_retry_at,
		        claimed_by, claimed_at, started_at, completed_at, provider_latency_ms,
		        provider_tokens_total, idempotency_key, created_at, updated_at, scope_json
		 FROM review_runs WHERE merge_request_id = ? ORDER BY created_at DESC`, mergeRequestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReviewRunRows(rows)
}

func (q *Queries) MarkReviewRunFailed(ctx context.Context, arg db.MarkReviewRunFailedParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE review_runs
		 SET status = 'failed', error_code = ?, error_detail = ?, retry_count = ?,
		     next_retry_at = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		arg.ErrorCode, arg.ErrorDetail, arg.RetryCount, arg.ID)
	return err
}

func (q *Queries) MarkReviewRunRetryableFailure(ctx context.Context, arg db.MarkReviewRunRetryableFailureParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE review_runs
		 SET status = 'failed', error_code = ?, error_detail = ?, retry_count = ?,
		     next_retry_at = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		arg.ErrorCode, arg.ErrorDetail, arg.RetryCount, arg.NextRetryAt, arg.ID)
	return err
}

// ReapStaleRunningRuns: datetime() replaces MySQL's NOW() + INTERVAL / NOW() - INTERVAL.
func (q *Queries) ReapStaleRunningRuns(ctx context.Context, dateSUB interface{}) (int64, error) {
	// Build the datetime modifier string (e.g. "-30 minutes") and pass it as
	// a parameter to avoid SQL injection via the dateSUB value.
	modifier := fmt.Sprintf("-%v minutes", dateSUB)
	result, err := q.db.ExecContext(ctx,
		`UPDATE review_runs
		 SET status = 'failed',
		     error_code = 'worker_timeout',
		     error_detail = 'Run exceeded claim timeout and was reaped for retry',
		     retry_count = retry_count + 1,
		     next_retry_at = datetime('now', '+30 seconds'),
		     updated_at = CURRENT_TIMESTAMP
		 WHERE status = 'running'
		   AND claimed_at < datetime('now', ?)`, modifier)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (q *Queries) UpdateReviewRunCompleted(ctx context.Context, arg db.UpdateReviewRunCompletedParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE review_runs
		 SET status = 'completed', completed_at = CURRENT_TIMESTAMP,
		     provider_latency_ms = ?, provider_tokens_total = ?,
		     error_code = '', error_detail = NULL, next_retry_at = NULL,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		arg.ProviderLatencyMs, arg.ProviderTokensTotal, arg.ID)
	return err
}

func (q *Queries) UpdateReviewRunStatus(ctx context.Context, arg db.UpdateReviewRunStatusParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE review_runs SET status = ?, error_code = ?, error_detail = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		arg.Status, arg.ErrorCode, arg.ErrorDetail, arg.ID)
	return err
}

func (q *Queries) UpdateRunScopeJSON(ctx context.Context, arg db.UpdateRunScopeJSONParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE review_runs SET scope_json = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		arg.ScopeJson, arg.ID)
	return err
}

// --- Custom Store methods (conditional updates only if status='running') ---

func (q *Queries) MarkReviewRunRetryableFailureIfRunning(ctx context.Context, arg db.MarkReviewRunRetryableFailureParams) (bool, error) {
	result, err := q.db.ExecContext(ctx,
		`UPDATE review_runs
		 SET status = 'failed', error_code = ?, error_detail = ?, retry_count = ?,
		     next_retry_at = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = 'running'`,
		arg.ErrorCode, arg.ErrorDetail, arg.RetryCount, arg.NextRetryAt, arg.ID)
	if err != nil {
		return false, err
	}
	return rowsAffected(result)
}

func (q *Queries) MarkReviewRunFailedIfRunning(ctx context.Context, arg db.MarkReviewRunFailedParams) (bool, error) {
	result, err := q.db.ExecContext(ctx,
		`UPDATE review_runs
		 SET status = 'failed', error_code = ?, error_detail = ?, retry_count = ?,
		     next_retry_at = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = 'running'`,
		arg.ErrorCode, arg.ErrorDetail, arg.RetryCount, arg.ID)
	if err != nil {
		return false, err
	}
	return rowsAffected(result)
}

func (q *Queries) UpdateReviewRunCompletedIfRunning(ctx context.Context, arg db.UpdateReviewRunCompletedParams) (bool, error) {
	result, err := q.db.ExecContext(ctx,
		`UPDATE review_runs
		 SET status = 'completed', completed_at = CURRENT_TIMESTAMP,
		     provider_latency_ms = ?, provider_tokens_total = ?,
		     error_code = '', error_detail = NULL, next_retry_at = NULL,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = 'running'`,
		arg.ProviderLatencyMs, arg.ProviderTokensTotal, arg.ID)
	if err != nil {
		return false, err
	}
	return rowsAffected(result)
}

func (q *Queries) UpdateReviewRunProviderMetrics(ctx context.Context, arg db.UpdateReviewRunProviderMetricsParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE review_runs SET provider_latency_ms = ?, provider_tokens_total = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		arg.ProviderLatencyMs, arg.ProviderTokensTotal, arg.ID)
	return err
}
