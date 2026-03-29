package sqlitedb

import (
	"context"
	"database/sql"

	"github.com/mreviewer/mreviewer/internal/db"
)

func (q *Queries) GetGitlabDiscussion(ctx context.Context, id int64) (db.GitlabDiscussion, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, review_finding_id, merge_request_id, gitlab_discussion_id, discussion_type, resolved, superseded_by_discussion_id, created_at, updated_at
		 FROM gitlab_discussions WHERE id = ? LIMIT 1`, id)
	var i db.GitlabDiscussion
	err := row.Scan(&i.ID, &i.ReviewFindingID, &i.MergeRequestID, &i.GitlabDiscussionID,
		&i.DiscussionType, &i.Resolved, &i.SupersededByDiscussionID, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) GetGitlabDiscussionByFinding(ctx context.Context, reviewFindingID int64) (db.GitlabDiscussion, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, review_finding_id, merge_request_id, gitlab_discussion_id, discussion_type, resolved, superseded_by_discussion_id, created_at, updated_at
		 FROM gitlab_discussions WHERE review_finding_id = ? ORDER BY created_at DESC LIMIT 1`, reviewFindingID)
	var i db.GitlabDiscussion
	err := row.Scan(&i.ID, &i.ReviewFindingID, &i.MergeRequestID, &i.GitlabDiscussionID,
		&i.DiscussionType, &i.Resolved, &i.SupersededByDiscussionID, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) GetGitlabDiscussionByMergeRequestAndFinding(ctx context.Context, arg db.GetGitlabDiscussionByMergeRequestAndFindingParams) (db.GitlabDiscussion, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, review_finding_id, merge_request_id, gitlab_discussion_id, discussion_type, resolved, superseded_by_discussion_id, created_at, updated_at
		 FROM gitlab_discussions WHERE merge_request_id = ? AND review_finding_id = ? ORDER BY created_at DESC LIMIT 1`,
		arg.MergeRequestID, arg.ReviewFindingID)
	var i db.GitlabDiscussion
	err := row.Scan(&i.ID, &i.ReviewFindingID, &i.MergeRequestID, &i.GitlabDiscussionID,
		&i.DiscussionType, &i.Resolved, &i.SupersededByDiscussionID, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (q *Queries) InsertGitlabDiscussion(ctx context.Context, arg db.InsertGitlabDiscussionParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO gitlab_discussions (review_finding_id, merge_request_id, gitlab_discussion_id, discussion_type, resolved)
		 VALUES (?, ?, ?, ?, ?)`,
		arg.ReviewFindingID, arg.MergeRequestID, arg.GitlabDiscussionID, arg.DiscussionType, arg.Resolved)
}

func (q *Queries) UpdateGitlabDiscussionResolved(ctx context.Context, arg db.UpdateGitlabDiscussionResolvedParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE gitlab_discussions SET resolved = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		arg.Resolved, arg.ID)
	return err
}

func (q *Queries) UpdateGitlabDiscussionSupersededBy(ctx context.Context, arg db.UpdateGitlabDiscussionSupersededByParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE gitlab_discussions SET superseded_by_discussion_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		arg.SupersededByDiscussionID, arg.ID)
	return err
}
