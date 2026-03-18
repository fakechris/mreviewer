-- name: InsertGitlabDiscussion :execresult
INSERT INTO gitlab_discussions (
    review_finding_id, merge_request_id, gitlab_discussion_id, discussion_type, resolved
) VALUES (?, ?, ?, ?, ?);

-- name: GetGitlabDiscussion :one
SELECT * FROM gitlab_discussions WHERE id = ? LIMIT 1;

-- name: GetGitlabDiscussionByFinding :one
SELECT * FROM gitlab_discussions
WHERE review_finding_id = ?
ORDER BY created_at DESC
LIMIT 1;

-- name: GetGitlabDiscussionByMergeRequestAndFinding :one
SELECT * FROM gitlab_discussions
WHERE merge_request_id = ?
  AND review_finding_id = ?
ORDER BY created_at DESC
LIMIT 1;

-- name: UpdateGitlabDiscussionResolved :exec
UPDATE gitlab_discussions
SET resolved = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;


-- name: UpdateGitlabDiscussionSupersededBy :exec
UPDATE gitlab_discussions
SET superseded_by_discussion_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;
