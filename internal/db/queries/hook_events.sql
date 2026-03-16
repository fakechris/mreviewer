-- name: InsertHookEvent :execresult
INSERT INTO hook_events (
    delivery_key, hook_source, event_type, gitlab_instance_id, project_id,
    mr_iid, action, head_sha, payload, verification_outcome, rejection_reason
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetHookEventByDeliveryKey :one
SELECT * FROM hook_events WHERE delivery_key = ? LIMIT 1;

-- name: ListHookEventsByProjectMR :many
SELECT * FROM hook_events
WHERE project_id = ? AND mr_iid = ?
ORDER BY received_at DESC
LIMIT ? OFFSET ?;
