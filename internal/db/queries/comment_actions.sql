-- name: InsertCommentAction :execresult
INSERT INTO comment_actions (
    review_run_id, review_finding_id, action_type, idempotency_key, status
) VALUES (?, ?, ?, ?, ?);

-- name: GetCommentActionByIdempotencyKey :one
SELECT * FROM comment_actions WHERE idempotency_key = ? LIMIT 1;

-- name: UpdateCommentActionStatus :exec
UPDATE comment_actions
SET status = ?, error_code = ?, error_detail = ?, latency_ms = ?,
    retry_count = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: ListCommentActionsByRun :many
SELECT * FROM comment_actions
WHERE review_run_id = ?
ORDER BY created_at ASC;
