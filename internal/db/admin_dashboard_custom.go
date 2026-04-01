package db

import (
	"context"
	"database/sql"
	"time"
)

type ListRecentRunsParams struct {
	Platform    string `json:"platform"`
	Status      string `json:"status"`
	ErrorCode   string `json:"error_code"`
	ProjectPath string `json:"project_path"`
	HeadSha     string `json:"head_sha"`
	LimitCount  int32  `json:"limit_count"`
}

type ListRecentRunsRow struct {
	ID                      int64          `json:"id"`
	Platform                string         `json:"platform"`
	ProjectPath             string         `json:"project_path"`
	WebUrl                  sql.NullString `json:"web_url"`
	MergeRequestID          int64          `json:"merge_request_id"`
	Status                  string         `json:"status"`
	ErrorCode               string         `json:"error_code"`
	TriggerType             string         `json:"trigger_type"`
	HeadSha                 string         `json:"head_sha"`
	ClaimedBy               string         `json:"claimed_by"`
	RetryCount              int32          `json:"retry_count"`
	NextRetryAt             sql.NullTime   `json:"next_retry_at"`
	ProviderLatencyMs       int64          `json:"provider_latency_ms"`
	ProviderTokensTotal     int64          `json:"provider_tokens_total"`
	HookAction              string         `json:"hook_action"`
	HookVerificationOutcome string         `json:"hook_verification_outcome"`
	FindingCount            int64          `json:"finding_count"`
	CommentActionCount      int64          `json:"comment_action_count"`
	CreatedAt               time.Time      `json:"created_at"`
	UpdatedAt               time.Time      `json:"updated_at"`
	StartedAt               sql.NullTime   `json:"started_at"`
	CompletedAt             sql.NullTime   `json:"completed_at"`
}

type GetRunDetailRow struct {
	ID                      int64          `json:"id"`
	Platform                string         `json:"platform"`
	ProjectPath             string         `json:"project_path"`
	WebUrl                  sql.NullString `json:"web_url"`
	MergeRequestID          int64          `json:"merge_request_id"`
	HookEventID             sql.NullInt64  `json:"hook_event_id"`
	Status                  string         `json:"status"`
	ErrorCode               string         `json:"error_code"`
	ErrorDetail             sql.NullString `json:"error_detail"`
	TriggerType             string         `json:"trigger_type"`
	HeadSha                 string         `json:"head_sha"`
	ClaimedBy               string         `json:"claimed_by"`
	RetryCount              int32          `json:"retry_count"`
	MaxRetries              int32          `json:"max_retries"`
	NextRetryAt             sql.NullTime   `json:"next_retry_at"`
	ProviderLatencyMs       int64          `json:"provider_latency_ms"`
	ProviderTokensTotal     int64          `json:"provider_tokens_total"`
	IdempotencyKey          string         `json:"idempotency_key"`
	ScopeJson               NullRawMessage `json:"scope_json"`
	SupersededByRunID       sql.NullInt64  `json:"superseded_by_run_id"`
	HookAction              string         `json:"hook_action"`
	HookVerificationOutcome string         `json:"hook_verification_outcome"`
	FindingCount            int64          `json:"finding_count"`
	CommentActionCount      int64          `json:"comment_action_count"`
	CreatedAt               time.Time      `json:"created_at"`
	UpdatedAt               time.Time      `json:"updated_at"`
	StartedAt               sql.NullTime   `json:"started_at"`
	CompletedAt             sql.NullTime   `json:"completed_at"`
}

func (q *Queries) ListRecentRuns(ctx context.Context, arg ListRecentRunsParams) ([]ListRecentRunsRow, error) {
	rows, err := q.db.QueryContext(ctx, `
SELECT
	r.id,
	CASE
		WHEN COALESCE(JSON_UNQUOTE(JSON_EXTRACT(r.scope_json, '$.platform')), '') = 'github' THEN 'github'
		ELSE 'gitlab'
	END AS platform,
	p.path_with_namespace AS project_path,
	mr.web_url,
	r.merge_request_id,
	r.status,
	r.error_code,
	r.trigger_type,
	r.head_sha,
	r.claimed_by,
	r.retry_count,
	r.next_retry_at,
	r.provider_latency_ms,
	r.provider_tokens_total,
	COALESCE(h.action, '') AS hook_action,
	COALESCE(h.verification_outcome, '') AS hook_verification_outcome,
	COUNT(DISTINCT rf.id) AS finding_count,
	COUNT(DISTINCT ca.id) AS comment_action_count,
	r.created_at,
	r.updated_at,
	r.started_at,
	r.completed_at
FROM review_runs r
JOIN projects p ON p.id = r.project_id
LEFT JOIN merge_requests mr ON mr.id = r.merge_request_id
LEFT JOIN hook_events h ON h.id = r.hook_event_id
LEFT JOIN review_findings rf ON rf.review_run_id = r.id
LEFT JOIN comment_actions ca ON ca.review_run_id = r.id
WHERE (? = ''
		OR CASE
			WHEN COALESCE(JSON_UNQUOTE(JSON_EXTRACT(r.scope_json, '$.platform')), '') = 'github' THEN 'github'
			ELSE 'gitlab'
		END = ?)
  AND (? = '' OR r.status = ?)
  AND (? = '' OR r.error_code = ?)
  AND (? = '' OR p.path_with_namespace LIKE CONCAT('%', ?, '%'))
  AND (? = '' OR r.head_sha = ?)
GROUP BY
	r.id,
	platform,
	p.path_with_namespace,
	mr.web_url,
	r.merge_request_id,
	r.status,
	r.error_code,
	r.trigger_type,
	r.head_sha,
	r.claimed_by,
	r.retry_count,
	r.next_retry_at,
	r.provider_latency_ms,
	r.provider_tokens_total,
	h.action,
	h.verification_outcome,
	r.created_at,
	r.updated_at,
	r.started_at,
	r.completed_at
ORDER BY r.created_at DESC, r.id DESC
LIMIT ?
`, arg.Platform, arg.Platform, arg.Status, arg.Status, arg.ErrorCode, arg.ErrorCode, arg.ProjectPath, arg.ProjectPath, arg.HeadSha, arg.HeadSha, arg.LimitCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ListRecentRunsRow
	for rows.Next() {
		var item ListRecentRunsRow
		if err := rows.Scan(
			&item.ID,
			&item.Platform,
			&item.ProjectPath,
			&item.WebUrl,
			&item.MergeRequestID,
			&item.Status,
			&item.ErrorCode,
			&item.TriggerType,
			&item.HeadSha,
			&item.ClaimedBy,
			&item.RetryCount,
			&item.NextRetryAt,
			&item.ProviderLatencyMs,
			&item.ProviderTokensTotal,
			&item.HookAction,
			&item.HookVerificationOutcome,
			&item.FindingCount,
			&item.CommentActionCount,
			&item.CreatedAt,
			&item.UpdatedAt,
			&item.StartedAt,
			&item.CompletedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, rows.Err()
}

func (q *Queries) GetRunDetail(ctx context.Context, id int64) (GetRunDetailRow, error) {
	row := q.db.QueryRowContext(ctx, `
SELECT
	r.id,
	CASE
		WHEN COALESCE(JSON_UNQUOTE(JSON_EXTRACT(r.scope_json, '$.platform')), '') = 'github' THEN 'github'
		ELSE 'gitlab'
	END AS platform,
	p.path_with_namespace AS project_path,
	mr.web_url,
	r.merge_request_id,
	r.hook_event_id,
	r.status,
	r.error_code,
	r.error_detail,
	r.trigger_type,
	r.head_sha,
	r.claimed_by,
	r.retry_count,
	r.max_retries,
	r.next_retry_at,
	r.provider_latency_ms,
	r.provider_tokens_total,
	r.idempotency_key,
	r.scope_json,
	r.superseded_by_run_id,
	COALESCE(h.action, '') AS hook_action,
	COALESCE(h.verification_outcome, '') AS hook_verification_outcome,
	COUNT(DISTINCT rf.id) AS finding_count,
	COUNT(DISTINCT ca.id) AS comment_action_count,
	r.created_at,
	r.updated_at,
	r.started_at,
	r.completed_at
FROM review_runs r
JOIN projects p ON p.id = r.project_id
LEFT JOIN merge_requests mr ON mr.id = r.merge_request_id
LEFT JOIN hook_events h ON h.id = r.hook_event_id
LEFT JOIN review_findings rf ON rf.review_run_id = r.id
LEFT JOIN comment_actions ca ON ca.review_run_id = r.id
WHERE r.id = ?
GROUP BY
	r.id,
	platform,
	p.path_with_namespace,
	mr.web_url,
	r.merge_request_id,
	r.hook_event_id,
	r.status,
	r.error_code,
	r.error_detail,
	r.trigger_type,
	r.head_sha,
	r.claimed_by,
	r.retry_count,
	r.max_retries,
	r.next_retry_at,
	r.provider_latency_ms,
	r.provider_tokens_total,
	r.idempotency_key,
	r.scope_json,
	r.superseded_by_run_id,
	h.action,
	h.verification_outcome,
	r.created_at,
	r.updated_at,
	r.started_at,
	r.completed_at
LIMIT 1
`, id)

	var item GetRunDetailRow
	err := row.Scan(
		&item.ID,
		&item.Platform,
		&item.ProjectPath,
		&item.WebUrl,
		&item.MergeRequestID,
		&item.HookEventID,
		&item.Status,
		&item.ErrorCode,
		&item.ErrorDetail,
		&item.TriggerType,
		&item.HeadSha,
		&item.ClaimedBy,
		&item.RetryCount,
		&item.MaxRetries,
		&item.NextRetryAt,
		&item.ProviderLatencyMs,
		&item.ProviderTokensTotal,
		&item.IdempotencyKey,
		&item.ScopeJson,
		&item.SupersededByRunID,
		&item.HookAction,
		&item.HookVerificationOutcome,
		&item.FindingCount,
		&item.CommentActionCount,
		&item.CreatedAt,
		&item.UpdatedAt,
		&item.StartedAt,
		&item.CompletedAt,
	)
	return item, err
}
