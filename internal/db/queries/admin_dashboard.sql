-- name: CountPendingQueue :one
SELECT COUNT(*) AS pending_count
FROM review_runs
WHERE status = 'pending';

-- name: CountRetryScheduledRuns :one
SELECT COUNT(*) AS retry_scheduled_count
FROM review_runs
WHERE status = 'failed'
  AND next_retry_at IS NOT NULL;

-- name: GetOldestWaitingRunCreatedAt :one
SELECT MIN(
           CASE
               WHEN status = 'pending' THEN created_at
               WHEN status = 'failed'
                   AND next_retry_at IS NOT NULL
                   AND next_retry_at <= CURRENT_TIMESTAMP THEN next_retry_at
               ELSE NULL
               END
       ) AS created_at
FROM review_runs
WHERE status = 'pending'
   OR (status = 'failed' AND next_retry_at IS NOT NULL);

-- name: ListTopQueuedProjects :many
SELECT
    p.path_with_namespace,
    COUNT(*) AS queue_depth
FROM review_runs r
JOIN projects p ON p.id = r.project_id
WHERE r.status = 'pending'
   OR (r.status = 'failed' AND r.next_retry_at IS NOT NULL)
GROUP BY p.id, p.path_with_namespace
ORDER BY queue_depth DESC, p.path_with_namespace ASC
LIMIT ?;

-- name: CountSupersededRunsSince :one
SELECT COUNT(*) AS superseded_count
FROM review_runs
WHERE superseded_by_run_id IS NOT NULL
  AND updated_at >= ?;

-- name: ListActiveWorkersWithCapacity :many
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
ORDER BY h.worker_id ASC;

-- name: ListRecentFailedRuns :many
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
  AND superseded_by_run_id IS NULL
ORDER BY updated_at DESC, id DESC
LIMIT ?;

-- name: ListFailureCountsByErrorCode :many
SELECT
    error_code,
    COUNT(*) AS count
FROM review_runs
WHERE status = 'failed'
  AND error_code <> ''
  AND superseded_by_run_id IS NULL
  AND updated_at >= ?
GROUP BY error_code
ORDER BY count DESC, error_code ASC;

-- name: ListWebhookVerificationCounts :many
SELECT
    verification_outcome,
    COUNT(*) AS count
FROM audit_logs
WHERE verification_outcome IN ('rejected', 'deduplicated')
  AND created_at >= ?
GROUP BY verification_outcome
ORDER BY verification_outcome ASC;
