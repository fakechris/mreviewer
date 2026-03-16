-- name: InsertAuditLog :execresult
INSERT INTO audit_logs (
    entity_type, entity_id, action, actor, detail,
    delivery_key, hook_source, verification_outcome, rejection_reason, error_code
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAuditLogsByEntity :many
SELECT * FROM audit_logs
WHERE entity_type = ? AND entity_id = ?
ORDER BY created_at DESC
LIMIT ? OFFSET ?;

-- name: ListAuditLogsByDeliveryKey :many
SELECT * FROM audit_logs
WHERE delivery_key = ?
ORDER BY created_at DESC;
