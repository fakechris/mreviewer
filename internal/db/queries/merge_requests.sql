-- name: InsertMergeRequest :execresult
INSERT INTO merge_requests (
    project_id, mr_iid, title, source_branch, target_branch,
    author, state, is_draft, head_sha, web_url
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpsertMergeRequest :execresult
INSERT INTO merge_requests (
    project_id, mr_iid, title, source_branch, target_branch,
    author, state, is_draft, head_sha, web_url
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    title = VALUES(title),
    source_branch = VALUES(source_branch),
    target_branch = VALUES(target_branch),
    author = VALUES(author),
    state = VALUES(state),
    is_draft = VALUES(is_draft),
    head_sha = VALUES(head_sha),
    web_url = VALUES(web_url),
    updated_at = CURRENT_TIMESTAMP;

-- name: GetMergeRequest :one
SELECT * FROM merge_requests WHERE id = ? LIMIT 1;

-- name: GetMergeRequestByProjectMR :one
SELECT * FROM merge_requests
WHERE project_id = ? AND mr_iid = ?
LIMIT 1;

-- name: UpdateMergeRequestState :exec
UPDATE merge_requests
SET state = ?, head_sha = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: InsertMRVersion :execresult
INSERT INTO mr_versions (
    merge_request_id, gitlab_version_id, base_sha, start_sha, head_sha, patch_id_sha
) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetLatestMRVersion :one
SELECT * FROM mr_versions
WHERE merge_request_id = ?
ORDER BY created_at DESC
LIMIT 1;
