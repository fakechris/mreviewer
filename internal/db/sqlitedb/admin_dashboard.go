package sqlitedb

import (
	"context"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
)

func (q *Queries) CountPendingQueue(ctx context.Context) (int64, error) {
	row := q.db.QueryRowContext(ctx, `SELECT COUNT(*) AS pending_count FROM review_runs WHERE status = 'pending'`)
	var count int64
	err := row.Scan(&count)
	return count, err
}

func (q *Queries) CountRetryScheduledRuns(ctx context.Context) (int64, error) {
	row := q.db.QueryRowContext(ctx, `SELECT COUNT(*) AS retry_scheduled_count FROM review_runs WHERE status = 'failed' AND next_retry_at IS NOT NULL`)
	var count int64
	err := row.Scan(&count)
	return count, err
}

func (q *Queries) GetOldestWaitingRunCreatedAt(ctx context.Context) (interface{}, error) {
	row := q.db.QueryRowContext(ctx, `
SELECT MIN(created_at) AS created_at
FROM review_runs
WHERE status = 'pending'
   OR (status = 'failed' AND next_retry_at IS NOT NULL)
`)
	var createdAt interface{}
	err := row.Scan(&createdAt)
	return createdAt, err
}

func (q *Queries) ListTopQueuedProjects(ctx context.Context, limit int32) ([]db.ListTopQueuedProjectsRow, error) {
	rows, err := q.db.QueryContext(ctx, `
SELECT
	p.path_with_namespace,
	COUNT(*) AS queue_depth
FROM review_runs r
JOIN projects p ON p.id = r.project_id
WHERE r.status = 'pending'
   OR (r.status = 'failed' AND r.next_retry_at IS NOT NULL)
GROUP BY p.id, p.path_with_namespace
ORDER BY queue_depth DESC, p.path_with_namespace ASC
LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []db.ListTopQueuedProjectsRow
	for rows.Next() {
		var item db.ListTopQueuedProjectsRow
		if err := rows.Scan(&item.PathWithNamespace, &item.QueueDepth); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, rows.Err()
}

func (q *Queries) CountSupersededRunsSince(ctx context.Context, updatedAt time.Time) (int64, error) {
	row := q.db.QueryRowContext(ctx, `
SELECT COUNT(*) AS superseded_count
FROM review_runs
WHERE error_code = 'superseded_by_new_head'
  AND updated_at >= ?
`, updatedAt)
	var count int64
	err := row.Scan(&count)
	return count, err
}

func (q *Queries) ListActiveWorkersWithCapacity(ctx context.Context, lastSeenAt time.Time) ([]db.ListActiveWorkersWithCapacityRow, error) {
	rows, err := q.db.QueryContext(ctx, `
SELECT
	h.worker_id,
	h.hostname,
	h.version,
	h.configured_concurrency,
	h.started_at,
	h.last_seen_at,
	COUNT(r.id) AS running_runs
FROM worker_heartbeats h
LEFT JOIN review_runs r
	ON r.claimed_by = h.worker_id
   AND r.status = 'running'
WHERE h.last_seen_at >= ?
GROUP BY
	h.worker_id,
	h.hostname,
	h.version,
	h.configured_concurrency,
	h.started_at,
	h.last_seen_at
ORDER BY h.worker_id ASC
`, lastSeenAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []db.ListActiveWorkersWithCapacityRow
	for rows.Next() {
		var item db.ListActiveWorkersWithCapacityRow
		if err := rows.Scan(
			&item.WorkerID,
			&item.Hostname,
			&item.Version,
			&item.ConfiguredConcurrency,
			&item.StartedAt,
			&item.LastSeenAt,
			&item.RunningRuns,
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

func (q *Queries) ListRecentFailedRuns(ctx context.Context, limit int32) ([]db.ListRecentFailedRunsRow, error) {
	rows, err := q.db.QueryContext(ctx, `
SELECT
	id,
	project_id,
	merge_request_id,
	trigger_type,
	head_sha,
	error_code,
	updated_at
FROM review_runs
WHERE status = 'failed'
  AND error_code <> ''
ORDER BY updated_at DESC, id DESC
LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []db.ListRecentFailedRunsRow
	for rows.Next() {
		var item db.ListRecentFailedRunsRow
		if err := rows.Scan(
			&item.ID,
			&item.ProjectID,
			&item.MergeRequestID,
			&item.TriggerType,
			&item.HeadSha,
			&item.ErrorCode,
			&item.UpdatedAt,
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

func (q *Queries) ListFailureCountsByErrorCode(ctx context.Context, updatedAt time.Time) ([]db.ListFailureCountsByErrorCodeRow, error) {
	rows, err := q.db.QueryContext(ctx, `
SELECT
	error_code,
	COUNT(*) AS count
FROM review_runs
WHERE status = 'failed'
  AND error_code <> ''
  AND updated_at >= ?
GROUP BY error_code
ORDER BY count DESC, error_code ASC
`, updatedAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []db.ListFailureCountsByErrorCodeRow
	for rows.Next() {
		var item db.ListFailureCountsByErrorCodeRow
		if err := rows.Scan(&item.ErrorCode, &item.Count); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, rows.Err()
}

func (q *Queries) ListWebhookVerificationCounts(ctx context.Context, createdAt time.Time) ([]db.ListWebhookVerificationCountsRow, error) {
	rows, err := q.db.QueryContext(ctx, `
SELECT
	verification_outcome,
	COUNT(*) AS count
FROM audit_logs
WHERE verification_outcome IN ('rejected', 'deduplicated')
  AND created_at >= ?
GROUP BY verification_outcome
ORDER BY verification_outcome ASC
`, createdAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []db.ListWebhookVerificationCountsRow
	for rows.Next() {
		var item db.ListWebhookVerificationCountsRow
		if err := rows.Scan(&item.VerificationOutcome, &item.Count); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, rows.Err()
}
