package sqlitedb

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/mreviewer/mreviewer/internal/db"
)

func (q *Queries) GetHookEventByDeliveryKey(ctx context.Context, deliveryKey string) (db.HookEvent, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, delivery_key, hook_source, event_type, gitlab_instance_id, project_id, mr_iid, action, head_sha, payload, verification_outcome, rejection_reason, received_at
		 FROM hook_events WHERE delivery_key = ? LIMIT 1`, deliveryKey)
	var i db.HookEvent
	err := row.Scan(&i.ID, &i.DeliveryKey, &i.HookSource, &i.EventType,
		&i.GitlabInstanceID, &i.ProjectID, &i.MrIid, &i.Action, &i.HeadSha,
		jscan(&i.Payload), &i.VerificationOutcome, &i.RejectionReason, &i.ReceivedAt)
	return i, err
}

func (q *Queries) InsertHookEvent(ctx context.Context, arg db.InsertHookEventParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO hook_events (delivery_key, hook_source, event_type, gitlab_instance_id, project_id, mr_iid, action, head_sha, payload, verification_outcome, rejection_reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		arg.DeliveryKey, arg.HookSource, arg.EventType, arg.GitlabInstanceID,
		arg.ProjectID, arg.MrIid, arg.Action, arg.HeadSha, asJSONHook(arg.Payload),
		arg.VerificationOutcome, arg.RejectionReason)
}

func (q *Queries) ListHookEventsByProjectMR(ctx context.Context, arg db.ListHookEventsByProjectMRParams) ([]db.HookEvent, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, delivery_key, hook_source, event_type, gitlab_instance_id, project_id, mr_iid, action, head_sha, payload, verification_outcome, rejection_reason, received_at
		 FROM hook_events WHERE project_id = ? AND mr_iid = ? ORDER BY received_at DESC LIMIT ? OFFSET ?`,
		arg.ProjectID, arg.MrIid, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []db.HookEvent{}
	for rows.Next() {
		var i db.HookEvent
		if err := rows.Scan(&i.ID, &i.DeliveryKey, &i.HookSource, &i.EventType,
			&i.GitlabInstanceID, &i.ProjectID, &i.MrIid, &i.Action, &i.HeadSha,
			jscan(&i.Payload), &i.VerificationOutcome, &i.RejectionReason, &i.ReceivedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, rows.Err()
}

func asJSONHook(v json.RawMessage) interface{} {
	if v == nil {
		return nil
	}
	return string(v)
}
