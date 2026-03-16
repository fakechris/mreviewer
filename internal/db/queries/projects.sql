-- name: InsertGitlabInstance :execresult
INSERT INTO gitlab_instances (url, name) VALUES (?, ?);

-- name: GetGitlabInstance :one
SELECT * FROM gitlab_instances WHERE id = ? LIMIT 1;

-- name: GetGitlabInstanceByURL :one
SELECT * FROM gitlab_instances WHERE url = ? LIMIT 1;

-- name: InsertProject :execresult
INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled)
VALUES (?, ?, ?, ?);

-- name: GetProject :one
SELECT * FROM projects WHERE id = ? LIMIT 1;

-- name: GetProjectByGitlabID :one
SELECT * FROM projects
WHERE gitlab_instance_id = ? AND gitlab_project_id = ?
LIMIT 1;

-- name: InsertProjectPolicy :execresult
INSERT INTO project_policies (
    project_id, confidence_threshold, severity_threshold,
    include_paths, exclude_paths, gate_mode, provider_route, extra
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetProjectPolicy :one
SELECT * FROM project_policies WHERE project_id = ? LIMIT 1;
