-- name: InsertReviewFinding :execresult
INSERT INTO review_findings (
    review_run_id, merge_request_id, category, severity, confidence,
    title, body_markdown, path, anchor_kind, old_line, new_line,
    range_start_kind, range_start_old_line, range_start_new_line,
    range_end_kind, range_end_old_line, range_end_new_line,
    anchor_snippet, evidence, suggested_patch, canonical_key,
    anchor_fingerprint, semantic_fingerprint, state
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetReviewFinding :one
SELECT * FROM review_findings WHERE id = ? LIMIT 1;

-- name: UpdateFindingState :exec
UPDATE review_findings
SET state = ?, matched_finding_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateFindingLastSeen :exec
UPDATE review_findings
SET last_seen_run_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateFindingRelocation :exec
UPDATE review_findings
SET path = ?,
    anchor_kind = ?,
    old_line = ?,
    new_line = ?,
    range_start_kind = ?,
    range_start_old_line = ?,
    range_start_new_line = ?,
    range_end_kind = ?,
    range_end_old_line = ?,
    range_end_new_line = ?,
    anchor_snippet = ?,
    anchor_fingerprint = ?,
    semantic_fingerprint = ?,
    last_seen_run_id = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: ListActiveFindingsByMR :many
SELECT * FROM review_findings
WHERE merge_request_id = ? AND state IN ('new', 'posted', 'active')
ORDER BY created_at ASC;

-- name: ListFindingsByRun :many
SELECT * FROM review_findings
WHERE review_run_id = ?
ORDER BY created_at ASC;


-- name: GetFindingByMRAndDiscussionID :one
SELECT * FROM review_findings
WHERE merge_request_id = ? AND gitlab_discussion_id = ?
LIMIT 1;

-- name: UpdateFindingDiscussionID :exec
UPDATE review_findings
SET gitlab_discussion_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;
