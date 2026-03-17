package db

import "context"

const markReviewRunRetryableFailureIfRunning = `
UPDATE review_runs
SET status = 'failed',
    error_code = ?,
    error_detail = ?,
    retry_count = ?,
    next_retry_at = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND status = 'running'
`

const markReviewRunFailedIfRunning = `
UPDATE review_runs
SET status = 'failed',
    error_code = ?,
    error_detail = ?,
    retry_count = ?,
    next_retry_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND status = 'running'
`

const updateReviewRunCompletedIfRunning = `
UPDATE review_runs
SET status = 'completed', completed_at = CURRENT_TIMESTAMP,
    provider_latency_ms = ?, provider_tokens_total = ?,
    error_code = '', error_detail = NULL, next_retry_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND status = 'running'
`

func (q *Queries) MarkReviewRunRetryableFailureIfRunning(ctx context.Context, arg MarkReviewRunRetryableFailureParams) (bool, error) {
	result, err := q.db.ExecContext(ctx, markReviewRunRetryableFailureIfRunning,
		arg.ErrorCode,
		arg.ErrorDetail,
		arg.RetryCount,
		arg.NextRetryAt,
		arg.ID,
	)
	if err != nil {
		return false, err
	}

	return rowsAffected(result)
}

func (q *Queries) MarkReviewRunFailedIfRunning(ctx context.Context, arg MarkReviewRunFailedParams) (bool, error) {
	result, err := q.db.ExecContext(ctx, markReviewRunFailedIfRunning,
		arg.ErrorCode,
		arg.ErrorDetail,
		arg.RetryCount,
		arg.ID,
	)
	if err != nil {
		return false, err
	}

	return rowsAffected(result)
}

func (q *Queries) UpdateReviewRunCompletedIfRunning(ctx context.Context, arg UpdateReviewRunCompletedParams) (bool, error) {
	result, err := q.db.ExecContext(ctx, updateReviewRunCompletedIfRunning,
		arg.ProviderLatencyMs,
		arg.ProviderTokensTotal,
		arg.ID,
	)
	if err != nil {
		return false, err
	}

	return rowsAffected(result)
}

func rowsAffected(result interface{ RowsAffected() (int64, error) }) (bool, error) {
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return affected > 0, nil
}
