package sqlitedb

import (
	"context"
	"database/sql"
	"github.com/mreviewer/mreviewer/internal/db"
)

func (q *Queries) InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO audit_logs (entity_type, entity_id, action, actor, detail, delivery_key, hook_source, verification_outcome, rejection_reason, error_code)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		arg.EntityType, arg.EntityID, arg.Action, arg.Actor, asJSON(arg.Detail),
		arg.DeliveryKey, arg.HookSource, arg.VerificationOutcome, arg.RejectionReason, arg.ErrorCode)
}

func (q *Queries) ListAuditLogsByDeliveryKey(ctx context.Context, deliveryKey string) ([]db.AuditLog, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, entity_type, entity_id, action, actor, detail, delivery_key, hook_source, verification_outcome, rejection_reason, error_code, created_at
		 FROM audit_logs WHERE delivery_key = ? ORDER BY created_at DESC`, deliveryKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAuditLogRows(rows)
}

func (q *Queries) ListAuditLogsByEntity(ctx context.Context, arg db.ListAuditLogsByEntityParams) ([]db.AuditLog, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, entity_type, entity_id, action, actor, detail, delivery_key, hook_source, verification_outcome, rejection_reason, error_code, created_at
		 FROM audit_logs WHERE entity_type = ? AND entity_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		arg.EntityType, arg.EntityID, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAuditLogRows(rows)
}

func scanAuditLogRows(rows *sql.Rows) ([]db.AuditLog, error) {
	items := []db.AuditLog{}
	for rows.Next() {
		var i db.AuditLog
		if err := rows.Scan(&i.ID, &i.EntityType, &i.EntityID, &i.Action, &i.Actor,
			jscan(&i.Detail), &i.DeliveryKey, &i.HookSource, &i.VerificationOutcome,
			&i.RejectionReason, &i.ErrorCode, &i.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, rows.Err()
}

