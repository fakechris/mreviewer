-- name: InsertReviewRun :execresult
INSERT INTO review_runs (
    project_id, merge_request_id, hook_event_id, trigger_type, head_sha,
    status, max_retries, idempotency_key
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetReviewRun :one
SELECT * FROM review_runs WHERE id = ? LIMIT 1;

-- name: GetReviewRunByIdempotencyKey :one
SELECT * FROM review_runs WHERE idempotency_key = ? LIMIT 1;

-- name: GetNextClaimableReviewRun :one
SELECT * FROM review_runs
WHERE status = 'pending'
   OR (status = 'failed' AND next_retry_at IS NOT NULL AND next_retry_at <= CURRENT_TIMESTAMP)
ORDER BY
    CASE WHEN status = 'pending' THEN 0 ELSE 1 END,
    COALESCE(next_retry_at, created_at) ASC,
    created_at ASC,
    id ASC
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: ClaimReviewRun :exec
UPDATE review_runs
SET status = 'running',
    claimed_by = ?,
    claimed_at = CURRENT_TIMESTAMP,
    started_at = COALESCE(started_at, CURRENT_TIMESTAMP),
    next_retry_at = NULL,
    error_code = '',
    error_detail = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateReviewRunStatus :exec
UPDATE review_runs
SET status = ?, error_code = ?, error_detail = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: MarkReviewRunRetryableFailure :exec
UPDATE review_runs
SET status = 'failed',
    error_code = ?,
    error_detail = ?,
    retry_count = ?,
    next_retry_at = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: MarkReviewRunFailed :exec
UPDATE review_runs
SET status = 'failed',
    error_code = ?,
    error_detail = ?,
    retry_count = ?,
    next_retry_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateReviewRunCompleted :exec
UPDATE review_runs
SET status = 'completed', completed_at = CURRENT_TIMESTAMP,
    provider_latency_ms = ?, provider_tokens_total = ?,
    error_code = '', error_detail = NULL, next_retry_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: CancelPendingRunsForMR :exec
UPDATE review_runs
SET status = 'cancelled', updated_at = CURRENT_TIMESTAMP
WHERE merge_request_id = ? AND status IN ('pending', 'running');

-- name: ListPendingRuns :many
SELECT * FROM review_runs
WHERE status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= CURRENT_TIMESTAMP)
ORDER BY created_at ASC
LIMIT ?;

-- name: ListReviewRunsByMR :many
SELECT * FROM review_runs
WHERE merge_request_id = ?
ORDER BY created_at DESC;
