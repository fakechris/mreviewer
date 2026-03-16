-- name: InsertReviewRun :execresult
INSERT INTO review_runs (
    project_id, merge_request_id, hook_event_id, trigger_type, head_sha,
    status, max_retries, idempotency_key
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetReviewRun :one
SELECT * FROM review_runs WHERE id = ? LIMIT 1;

-- name: GetReviewRunByIdempotencyKey :one
SELECT * FROM review_runs WHERE idempotency_key = ? LIMIT 1;

-- name: UpdateReviewRunStatus :exec
UPDATE review_runs
SET status = ?, error_code = ?, error_detail = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateReviewRunCompleted :exec
UPDATE review_runs
SET status = 'completed', completed_at = CURRENT_TIMESTAMP,
    provider_latency_ms = ?, provider_tokens_total = ?, updated_at = CURRENT_TIMESTAMP
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
