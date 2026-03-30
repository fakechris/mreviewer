package sqlitedb

import (
	"context"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
)

func (q *Queries) UpsertWorkerHeartbeat(ctx context.Context, arg db.UpsertWorkerHeartbeatParams) error {
	_, err := q.db.ExecContext(ctx, `
INSERT INTO worker_heartbeats (
	worker_id, hostname, version, configured_concurrency, started_at, last_seen_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(worker_id) DO UPDATE SET
	hostname = excluded.hostname,
	version = excluded.version,
	configured_concurrency = excluded.configured_concurrency,
	last_seen_at = excluded.last_seen_at,
	updated_at = CURRENT_TIMESTAMP
`, arg.WorkerID, arg.Hostname, arg.Version, arg.ConfiguredConcurrency, arg.StartedAt, arg.LastSeenAt)
	return err
}

func (q *Queries) ListActiveWorkerHeartbeats(ctx context.Context, activeSince time.Time) ([]db.WorkerHeartbeat, error) {
	rows, err := q.db.QueryContext(ctx, `
SELECT worker_id, hostname, version, configured_concurrency, started_at, last_seen_at, created_at, updated_at
FROM worker_heartbeats
WHERE last_seen_at >= ?
ORDER BY last_seen_at DESC, worker_id ASC
`, activeSince)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []db.WorkerHeartbeat
	for rows.Next() {
		var item db.WorkerHeartbeat
		if err := rows.Scan(
			&item.WorkerID,
			&item.Hostname,
			&item.Version,
			&item.ConfiguredConcurrency,
			&item.StartedAt,
			&item.LastSeenAt,
			&item.CreatedAt,
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

func (q *Queries) ListRunningRunCountsByWorker(ctx context.Context) ([]db.ListRunningRunCountsByWorkerRow, error) {
	rows, err := q.db.QueryContext(ctx, `
SELECT claimed_by AS worker_id, COUNT(*) AS running_runs
FROM review_runs
WHERE status = 'running'
  AND claimed_by <> ''
GROUP BY claimed_by
ORDER BY claimed_by ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []db.ListRunningRunCountsByWorkerRow
	for rows.Next() {
		var item db.ListRunningRunCountsByWorkerRow
		if err := rows.Scan(&item.WorkerID, &item.RunningRuns); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return items, rows.Err()
}
