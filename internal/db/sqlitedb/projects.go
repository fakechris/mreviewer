package sqlitedb

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/mreviewer/mreviewer/internal/db"
)

func (q *Queries) GetGitlabInstance(ctx context.Context, id int64) (db.GitlabInstance, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, url, name, created_at, updated_at FROM gitlab_instances WHERE id = ? LIMIT 1`, id)
	var i db.GitlabInstance
	err := row.Scan(&i.ID, &i.Url, &i.Name, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) GetGitlabInstanceByURL(ctx context.Context, url string) (db.GitlabInstance, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, url, name, created_at, updated_at FROM gitlab_instances WHERE url = ? LIMIT 1`, url)
	var i db.GitlabInstance
	err := row.Scan(&i.ID, &i.Url, &i.Name, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) GetProject(ctx context.Context, id int64) (db.Project, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled, created_at, updated_at FROM projects WHERE id = ? LIMIT 1`, id)
	var i db.Project
	err := row.Scan(&i.ID, &i.GitlabInstanceID, &i.GitlabProjectID, &i.PathWithNamespace, &i.Enabled, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) GetProjectByGitlabID(ctx context.Context, arg db.GetProjectByGitlabIDParams) (db.Project, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled, created_at, updated_at
		 FROM projects WHERE gitlab_instance_id = ? AND gitlab_project_id = ? LIMIT 1`,
		arg.GitlabInstanceID, arg.GitlabProjectID)
	var i db.Project
	err := row.Scan(&i.ID, &i.GitlabInstanceID, &i.GitlabProjectID, &i.PathWithNamespace, &i.Enabled, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) GetProjectPolicy(ctx context.Context, projectID int64) (db.ProjectPolicy, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, project_id, confidence_threshold, severity_threshold, include_paths, exclude_paths, gate_mode, provider_route, extra, created_at, updated_at
		 FROM project_policies WHERE project_id = ? LIMIT 1`, projectID)
	var i db.ProjectPolicy
	err := row.Scan(&i.ID, &i.ProjectID, &i.ConfidenceThreshold, &i.SeverityThreshold,
		jscan(&i.IncludePaths), jscan(&i.ExcludePaths), &i.GateMode, &i.ProviderRoute, jscan(&i.Extra),
		&i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) InsertGitlabInstance(ctx context.Context, arg db.InsertGitlabInstanceParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO gitlab_instances (url, name) VALUES (?, ?)`,
		arg.Url, arg.Name)
}

func (q *Queries) InsertProject(ctx context.Context, arg db.InsertProjectParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled) VALUES (?, ?, ?, ?)`,
		arg.GitlabInstanceID, arg.GitlabProjectID, arg.PathWithNamespace, arg.Enabled)
}

func (q *Queries) InsertProjectPolicy(ctx context.Context, arg db.InsertProjectPolicyParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO project_policies (project_id, confidence_threshold, severity_threshold, include_paths, exclude_paths, gate_mode, provider_route, extra)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		arg.ProjectID, arg.ConfidenceThreshold, arg.SeverityThreshold,
		asJSON(arg.IncludePaths), asJSON(arg.ExcludePaths), arg.GateMode, arg.ProviderRoute, asJSON(arg.Extra))
}

// UpsertGitlabInstance: ON CONFLICT replaces MySQL's ON DUPLICATE KEY UPDATE.
func (q *Queries) UpsertGitlabInstance(ctx context.Context, arg db.UpsertGitlabInstanceParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO gitlab_instances (url, name) VALUES (?, ?)
		 ON CONFLICT(url) DO UPDATE SET name = excluded.name, updated_at = CURRENT_TIMESTAMP`,
		arg.Url, arg.Name)
}

// UpsertProject: ON CONFLICT replaces MySQL's ON DUPLICATE KEY UPDATE.
func (q *Queries) UpsertProject(ctx context.Context, arg db.UpsertProjectParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled) VALUES (?, ?, ?, ?)
		 ON CONFLICT(gitlab_instance_id, gitlab_project_id) DO UPDATE SET path_with_namespace = excluded.path_with_namespace, updated_at = CURRENT_TIMESTAMP`,
		arg.GitlabInstanceID, arg.GitlabProjectID, arg.PathWithNamespace, arg.Enabled)
}

// asJSON converts json.RawMessage to a value safe for SQLite TEXT columns.
func asJSON(v json.RawMessage) interface{} {
	if v == nil {
		return nil
	}
	return string(v)
}
