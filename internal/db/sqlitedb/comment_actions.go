package sqlitedb

import (
	"context"
	"database/sql"

	"github.com/mreviewer/mreviewer/internal/db"
)

func (q *Queries) GetCommentActionByIdempotencyKey(ctx context.Context, idempotencyKey string) (db.CommentAction, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, review_run_id, review_finding_id, action_type, idempotency_key, status, error_code, error_detail, retry_count, latency_ms, created_at, updated_at
		 FROM comment_actions WHERE idempotency_key = ? LIMIT 1`, idempotencyKey)
	var i db.CommentAction
	err := row.Scan(&i.ID, &i.ReviewRunID, &i.ReviewFindingID, &i.ActionType,
		&i.IdempotencyKey, &i.Status, &i.ErrorCode, &i.ErrorDetail,
		&i.RetryCount, &i.LatencyMs, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) InsertCommentAction(ctx context.Context, arg db.InsertCommentActionParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO comment_actions (review_run_id, review_finding_id, action_type, idempotency_key, status)
		 VALUES (?, ?, ?, ?, ?)`,
		arg.ReviewRunID, arg.ReviewFindingID, arg.ActionType, arg.IdempotencyKey, arg.Status)
}

func (q *Queries) ListCommentActionsByRun(ctx context.Context, reviewRunID int64) ([]db.CommentAction, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, review_run_id, review_finding_id, action_type, idempotency_key, status, error_code, error_detail, retry_count, latency_ms, created_at, updated_at
		 FROM comment_actions WHERE review_run_id = ? ORDER BY created_at ASC`, reviewRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []db.CommentAction{}
	for rows.Next() {
		var i db.CommentAction
		if err := rows.Scan(&i.ID, &i.ReviewRunID, &i.ReviewFindingID, &i.ActionType,
			&i.IdempotencyKey, &i.Status, &i.ErrorCode, &i.ErrorDetail,
			&i.RetryCount, &i.LatencyMs, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, rows.Err()
}

func (q *Queries) UpdateCommentActionStatus(ctx context.Context, arg db.UpdateCommentActionStatusParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE comment_actions
		 SET status = ?, error_code = ?, error_detail = ?, latency_ms = ?,
		     retry_count = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		arg.Status, arg.ErrorCode, arg.ErrorDetail, arg.LatencyMs, arg.RetryCount, arg.ID)
	return err
}
