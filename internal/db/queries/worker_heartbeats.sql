-- name: UpsertWorkerHeartbeat :exec
INSERT INTO worker_heartbeats (
    worker_id, hostname, version, configured_concurrency, started_at, last_seen_at
) VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    hostname = VALUES(hostname),
    version = VALUES(version),
    configured_concurrency = VALUES(configured_concurrency),
    last_seen_at = VALUES(last_seen_at),
    updated_at = CURRENT_TIMESTAMP;

-- name: ListActiveWorkerHeartbeats :many
SELECT * FROM worker_heartbeats
WHERE last_seen_at >= ?
ORDER BY last_seen_at DESC, worker_id ASC;

-- name: ListRunningRunCountsByWorker :many
SELECT claimed_by AS worker_id, COUNT(*) AS running_runs
FROM review_runs
WHERE status = 'running'
  AND claimed_by <> ''
GROUP BY claimed_by
ORDER BY claimed_by ASC;
