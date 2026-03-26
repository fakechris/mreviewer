package llm

import (
	"context"
	"database/sql"

	"github.com/mreviewer/mreviewer/internal/db"
)

// ProcessorStore abstracts all database operations used by Processor and its
// supporting functions (persistFindings, persistSummaryNoteFallback, etc.).
// This interface enables testing with fakes and future alternative backends
// (e.g. SQLite, in-memory for stateless CLI).
type ProcessorStore interface {
	// Entity loading
	GetMergeRequest(ctx context.Context, id int64) (db.MergeRequest, error)
	GetProject(ctx context.Context, id int64) (db.Project, error)
	GetGitlabInstance(ctx context.Context, id int64) (db.GitlabInstance, error)
	GetProjectPolicy(ctx context.Context, projectID int64) (db.ProjectPolicy, error)

	// MR version tracking
	InsertMRVersion(ctx context.Context, arg db.InsertMRVersionParams) (sql.Result, error)

	// Review run lifecycle
	UpdateReviewRunStatus(ctx context.Context, arg db.UpdateReviewRunStatusParams) error
	UpdateRunScopeJSON(ctx context.Context, runID int64, scopeJSON []byte) error

	// Findings
	ListActiveFindingsByMR(ctx context.Context, mergeRequestID int64) ([]db.ReviewFinding, error)
	InsertReviewFinding(ctx context.Context, arg db.InsertReviewFindingParams) (sql.Result, error)
	UpdateFindingState(ctx context.Context, arg db.UpdateFindingStateParams) error
	UpdateFindingLastSeen(ctx context.Context, arg db.UpdateFindingLastSeenParams) error
	UpdateFindingRelocation(ctx context.Context, arg db.UpdateFindingRelocationParams) error

	// Comment actions (summary note fallback)
	InsertCommentAction(ctx context.Context, arg db.InsertCommentActionParams) (sql.Result, error)

	// Historical context (satisfies context.HistoricalStore)
	GetGitlabDiscussionByMergeRequestAndFinding(ctx context.Context, arg db.GetGitlabDiscussionByMergeRequestAndFindingParams) (db.GitlabDiscussion, error)
}

// SQLProcessorStore wraps *db.Queries to implement ProcessorStore.
// This is the default production implementation — zero behavior change from
// the previous direct-access pattern.
type SQLProcessorStore struct {
	queries *db.Queries
}

// NewSQLProcessorStore creates a ProcessorStore backed by MySQL via sqlc-generated queries.
func NewSQLProcessorStore(sqlDB *sql.DB) *SQLProcessorStore {
	return &SQLProcessorStore{queries: db.New(sqlDB)}
}

func (s *SQLProcessorStore) GetMergeRequest(ctx context.Context, id int64) (db.MergeRequest, error) {
	return s.queries.GetMergeRequest(ctx, id)
}

func (s *SQLProcessorStore) GetProject(ctx context.Context, id int64) (db.Project, error) {
	return s.queries.GetProject(ctx, id)
}

func (s *SQLProcessorStore) GetGitlabInstance(ctx context.Context, id int64) (db.GitlabInstance, error) {
	return s.queries.GetGitlabInstance(ctx, id)
}

func (s *SQLProcessorStore) GetProjectPolicy(ctx context.Context, projectID int64) (db.ProjectPolicy, error) {
	return s.queries.GetProjectPolicy(ctx, projectID)
}

func (s *SQLProcessorStore) InsertMRVersion(ctx context.Context, arg db.InsertMRVersionParams) (sql.Result, error) {
	return s.queries.InsertMRVersion(ctx, arg)
}

func (s *SQLProcessorStore) UpdateReviewRunStatus(ctx context.Context, arg db.UpdateReviewRunStatusParams) error {
	return s.queries.UpdateReviewRunStatus(ctx, arg)
}

func (s *SQLProcessorStore) UpdateRunScopeJSON(ctx context.Context, runID int64, scopeJSON []byte) error {
	return s.queries.UpdateRunScopeJSON(ctx, db.UpdateRunScopeJSONParams{
		ScopeJson: scopeJSON,
		ID:        runID,
	})
}

func (s *SQLProcessorStore) ListActiveFindingsByMR(ctx context.Context, mergeRequestID int64) ([]db.ReviewFinding, error) {
	return s.queries.ListActiveFindingsByMR(ctx, mergeRequestID)
}

func (s *SQLProcessorStore) InsertReviewFinding(ctx context.Context, arg db.InsertReviewFindingParams) (sql.Result, error) {
	return s.queries.InsertReviewFinding(ctx, arg)
}

func (s *SQLProcessorStore) UpdateFindingState(ctx context.Context, arg db.UpdateFindingStateParams) error {
	return s.queries.UpdateFindingState(ctx, arg)
}

func (s *SQLProcessorStore) UpdateFindingLastSeen(ctx context.Context, arg db.UpdateFindingLastSeenParams) error {
	return s.queries.UpdateFindingLastSeen(ctx, arg)
}

func (s *SQLProcessorStore) UpdateFindingRelocation(ctx context.Context, arg db.UpdateFindingRelocationParams) error {
	return s.queries.UpdateFindingRelocation(ctx, arg)
}

func (s *SQLProcessorStore) InsertCommentAction(ctx context.Context, arg db.InsertCommentActionParams) (sql.Result, error) {
	return s.queries.InsertCommentAction(ctx, arg)
}

func (s *SQLProcessorStore) GetGitlabDiscussionByMergeRequestAndFinding(ctx context.Context, arg db.GetGitlabDiscussionByMergeRequestAndFindingParams) (db.GitlabDiscussion, error) {
	return s.queries.GetGitlabDiscussionByMergeRequestAndFinding(ctx, arg)
}
