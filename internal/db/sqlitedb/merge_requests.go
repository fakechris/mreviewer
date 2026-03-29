package sqlitedb

import (
	"context"
	"database/sql"

	"github.com/mreviewer/mreviewer/internal/db"
)

func (q *Queries) GetLatestMRVersion(ctx context.Context, mergeRequestID int64) (db.MrVersion, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, merge_request_id, gitlab_version_id, base_sha, start_sha, head_sha, patch_id_sha, created_at
		 FROM mr_versions WHERE merge_request_id = ? ORDER BY created_at DESC LIMIT 1`, mergeRequestID)
	var i db.MrVersion
	err := row.Scan(&i.ID, &i.MergeRequestID, &i.GitlabVersionID, &i.BaseSha, &i.StartSha, &i.HeadSha, &i.PatchIDSha, &i.CreatedAt)
	return i, err
}

func (q *Queries) GetMergeRequest(ctx context.Context, id int64) (db.MergeRequest, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, project_id, mr_iid, title, source_branch, target_branch, author, state, is_draft, head_sha, web_url, created_at, updated_at
		 FROM merge_requests WHERE id = ? LIMIT 1`, id)
	var i db.MergeRequest
	err := row.Scan(&i.ID, &i.ProjectID, &i.MrIid, &i.Title, &i.SourceBranch, &i.TargetBranch,
		&i.Author, &i.State, &i.IsDraft, &i.HeadSha, &i.WebUrl, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) GetMergeRequestByProjectMR(ctx context.Context, arg db.GetMergeRequestByProjectMRParams) (db.MergeRequest, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, project_id, mr_iid, title, source_branch, target_branch, author, state, is_draft, head_sha, web_url, created_at, updated_at
		 FROM merge_requests WHERE project_id = ? AND mr_iid = ? LIMIT 1`,
		arg.ProjectID, arg.MrIid)
	var i db.MergeRequest
	err := row.Scan(&i.ID, &i.ProjectID, &i.MrIid, &i.Title, &i.SourceBranch, &i.TargetBranch,
		&i.Author, &i.State, &i.IsDraft, &i.HeadSha, &i.WebUrl, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) InsertMRVersion(ctx context.Context, arg db.InsertMRVersionParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO mr_versions (merge_request_id, gitlab_version_id, base_sha, start_sha, head_sha, patch_id_sha)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		arg.MergeRequestID, arg.GitlabVersionID, arg.BaseSha, arg.StartSha, arg.HeadSha, arg.PatchIDSha)
}

func (q *Queries) InsertMergeRequest(ctx context.Context, arg db.InsertMergeRequestParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO merge_requests (project_id, mr_iid, title, source_branch, target_branch, author, state, is_draft, head_sha, web_url)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		arg.ProjectID, arg.MrIid, arg.Title, arg.SourceBranch, arg.TargetBranch,
		arg.Author, arg.State, arg.IsDraft, arg.HeadSha, arg.WebUrl)
}

func (q *Queries) UpdateMergeRequestState(ctx context.Context, arg db.UpdateMergeRequestStateParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE merge_requests SET state = ?, head_sha = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		arg.State, arg.HeadSha, arg.ID)
	return err
}

// UpsertMergeRequest: ON CONFLICT replaces MySQL's ON DUPLICATE KEY UPDATE.
func (q *Queries) UpsertMergeRequest(ctx context.Context, arg db.UpsertMergeRequestParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO merge_requests (project_id, mr_iid, title, source_branch, target_branch, author, state, is_draft, head_sha, web_url)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_id, mr_iid) DO UPDATE SET
		   title = excluded.title,
		   source_branch = excluded.source_branch,
		   target_branch = excluded.target_branch,
		   author = excluded.author,
		   state = excluded.state,
		   is_draft = excluded.is_draft,
		   head_sha = excluded.head_sha,
		   web_url = excluded.web_url,
		   updated_at = CURRENT_TIMESTAMP`,
		arg.ProjectID, arg.MrIid, arg.Title, arg.SourceBranch, arg.TargetBranch,
		arg.Author, arg.State, arg.IsDraft, arg.HeadSha, arg.WebUrl)
}
