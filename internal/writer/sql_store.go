package writer

import (
	"context"
	"database/sql"

	"github.com/mreviewer/mreviewer/internal/db"
)

type SQLStore struct {
	queries db.Store
}

func NewSQLStore(sqlDB *sql.DB) *SQLStore {
	if sqlDB == nil {
		return nil
	}
	return &SQLStore{queries: db.New(sqlDB)}
}

func NewSQLStoreWithStore(store db.Store) *SQLStore {
	if store == nil {
		return nil
	}
	return &SQLStore{queries: store}
}

func (s *SQLStore) GetLatestMRVersion(ctx context.Context, mergeRequestID int64) (db.MrVersion, error) {
	return s.queries.GetLatestMRVersion(ctx, mergeRequestID)
}

func (s *SQLStore) GetMergeRequest(ctx context.Context, id int64) (db.MergeRequest, error) {
	return s.queries.GetMergeRequest(ctx, id)
}

func (s *SQLStore) GetProject(ctx context.Context, id int64) (db.Project, error) {
	return s.queries.GetProject(ctx, id)
}

func (s *SQLStore) GetReviewRun(ctx context.Context, id int64) (db.ReviewRun, error) {
	return s.queries.GetReviewRun(ctx, id)
}

func (s *SQLStore) GetProjectPolicy(ctx context.Context, projectID int64) (db.ProjectPolicy, error) {
	return s.queries.GetProjectPolicy(ctx, projectID)
}

func (s *SQLStore) GetReviewFinding(ctx context.Context, id int64) (db.ReviewFinding, error) {
	return s.queries.GetReviewFinding(ctx, id)
}

func (s *SQLStore) GetGitlabDiscussion(ctx context.Context, id int64) (db.GitlabDiscussion, error) {
	return s.queries.GetGitlabDiscussion(ctx, id)
}

func (s *SQLStore) ListFindingsByRun(ctx context.Context, reviewRunID int64) ([]db.ReviewFinding, error) {
	return s.queries.ListFindingsByRun(ctx, reviewRunID)
}

func (s *SQLStore) ListFindingsByMergeRequest(ctx context.Context, mergeRequestID int64) ([]db.ReviewFinding, error) {
	return s.queries.ListActiveFindingsByMR(ctx, mergeRequestID)
}

func (s *SQLStore) GetCommentActionByIdempotencyKey(ctx context.Context, idempotencyKey string) (db.CommentAction, error) {
	return s.queries.GetCommentActionByIdempotencyKey(ctx, idempotencyKey)
}

func (s *SQLStore) GetGitlabDiscussionByFinding(ctx context.Context, reviewFindingID int64) (db.GitlabDiscussion, error) {
	return s.queries.GetGitlabDiscussionByFinding(ctx, reviewFindingID)
}

func (s *SQLStore) GetGitlabDiscussionByMergeRequestAndFinding(ctx context.Context, arg db.GetGitlabDiscussionByMergeRequestAndFindingParams) (db.GitlabDiscussion, error) {
	return s.queries.GetGitlabDiscussionByMergeRequestAndFinding(ctx, arg)
}

func (s *SQLStore) InsertCommentAction(ctx context.Context, arg db.InsertCommentActionParams) (sql.Result, error) {
	return s.queries.InsertCommentAction(ctx, arg)
}

func (s *SQLStore) UpdateCommentActionStatus(ctx context.Context, arg db.UpdateCommentActionStatusParams) error {
	return s.queries.UpdateCommentActionStatus(ctx, arg)
}

func (s *SQLStore) InsertGitlabDiscussion(ctx context.Context, arg db.InsertGitlabDiscussionParams) (sql.Result, error) {
	return s.queries.InsertGitlabDiscussion(ctx, arg)
}

func (s *SQLStore) UpdateFindingDiscussionID(ctx context.Context, arg db.UpdateFindingDiscussionIDParams) error {
	return s.queries.UpdateFindingDiscussionID(ctx, arg)
}

func (s *SQLStore) UpdateGitlabDiscussionResolved(ctx context.Context, arg db.UpdateGitlabDiscussionResolvedParams) error {
	return s.queries.UpdateGitlabDiscussionResolved(ctx, arg)
}

func (s *SQLStore) UpdateGitlabDiscussionSupersededBy(ctx context.Context, arg db.UpdateGitlabDiscussionSupersededByParams) error {
	return s.queries.UpdateGitlabDiscussionSupersededBy(ctx, arg)
}

func (s *SQLStore) MarkReviewRunFailedIfRunning(ctx context.Context, arg db.MarkReviewRunFailedParams) (bool, error) {
	return s.queries.MarkReviewRunFailedIfRunning(ctx, arg)
}
