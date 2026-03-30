// Package runs implements the review run lifecycle: creating pending runs
// from normalized MR events and cancelling runs on close/merge.
package runs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/hooks"
)

// defaultMaxRetries is the maximum retry count for a new review run.
const defaultMaxRetries = 3

// Service handles review run lifecycle operations: creating pending runs
// from normalized webhook events and cancelling runs on close/merge.
type Service struct {
	logger   *slog.Logger
	db       *sql.DB
	newStore func(db.DBTX) db.Store
}

// ServiceOption configures optional Service behaviour.
type ServiceOption func(*Service)

// WithStoreFactory overrides the default db.Store constructor used when
// creating transactions. The default is db.New (MySQL).
func WithStoreFactory(fn func(db.DBTX) db.Store) ServiceOption {
	return func(s *Service) { s.newStore = fn }
}

// NewService creates a new run lifecycle service.
func NewService(logger *slog.Logger, database *sql.DB, opts ...ServiceOption) *Service {
	s := &Service{
		logger:   logger,
		db:       database,
		newStore: func(conn db.DBTX) db.Store { return db.New(conn) },
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ProcessEvent handles a normalized MR lifecycle event. It determines
// whether to create a new pending run or cancel existing runs based on
// the action.
//
// For open/update actions: creates a pending review run (with idempotency
// dedup via the normalized idempotency key).
// For close/merge actions: cancels any pending, running, or retry-scheduled
// review runs for the MR.
//
// The hookEventID is the ID of the hook_events row created during ingress.
// It may be 0 if unknown.
func (s *Service) ProcessEvent(ctx context.Context, ev hooks.NormalizedEvent, hookEventID int64) error {
	return db.RunTxWithStore(ctx, s.db, s.newStore, func(ctx context.Context, store db.Store) error {
		return s.ProcessEventWithQuerier(ctx, store, ev, hookEventID)
	})
}

// ProcessEventWithQuerier handles a normalized MR lifecycle event using the
// provided querier. Callers that already run inside a transaction should use
// this method so hook-event and review-run writes commit atomically.
func (s *Service) ProcessEventWithQuerier(ctx context.Context, q db.Querier, ev hooks.NormalizedEvent, hookEventID int64) error {
	action := strings.ToLower(ev.Action)

	switch action {
	case "open", "reopen":
		return s.createPendingRun(ctx, q, ev, hookEventID)
	case "update", "ci_trigger", "manual_trigger":
		return s.handleUpdate(ctx, q, ev, hookEventID)
	case "close", "merge":
		return s.cancelRuns(ctx, q, ev, action)
	default:
		s.logger.InfoContext(ctx, "ignoring unhandled MR action",
			"action", action,
			"mr_iid", ev.MRIID,
			"project_id", ev.ProjectID,
		)
		return nil
	}
}

// handleUpdate processes an MR update event. Only updates with a new HEAD SHA
// (indicating a code push, signaled by oldrev in the original payload or a
// different head_sha) create a new review run.
func (s *Service) handleUpdate(ctx context.Context, q db.Querier, ev hooks.NormalizedEvent, hookEventID int64) error {
	// An update event should create a new run only if it has a head_sha.
	// The webhook payload includes oldrev when a new commit is pushed.
	// The normalization layer captures the new HEAD SHA. If head_sha is empty
	// (deferred), we still create the run — the scheduler will resolve it later.
	if err := s.createPendingRun(ctx, q, ev, hookEventID); err != nil {
		return err
	}

	newRun, err := q.GetReviewRunByIdempotencyKey(ctx, ev.IdempotencyKey)
	if err != nil {
		return fmt.Errorf("load newly created review run: %w", err)
	}

	if err := q.SupersedeActiveRunsForMR(ctx, db.SupersedeActiveRunsForMRParams{
		SupersededByRunID: sql.NullInt64{Int64: newRun.ID, Valid: true},
		MergeRequestID:    newRun.MergeRequestID,
		ID:                newRun.ID,
	}); err != nil {
		return fmt.Errorf("supersede active runs: %w", err)
	}

	return nil
}

// createPendingRun creates a new pending review run inside a transaction.
// It ensures the gitlab_instance, project, and merge_request records exist
// (upserting as needed), then inserts the review_run with idempotency dedup.
func (s *Service) createPendingRun(ctx context.Context, q db.Querier, ev hooks.NormalizedEvent, hookEventID int64) error {
	// 1. Ensure gitlab_instance exists.
	instanceID, err := s.ensureInstance(ctx, q, ev.GitLabInstanceURL)
	if err != nil {
		return fmt.Errorf("ensure instance: %w", err)
	}

	// 2. Ensure project exists.
	projectID, err := s.ensureProject(ctx, q, instanceID, ev.ProjectID, ev.ProjectPath)
	if err != nil {
		return fmt.Errorf("ensure project: %w", err)
	}

	// 3. Upsert merge request.
	mrID, err := s.upsertMergeRequest(ctx, q, projectID, ev)
	if err != nil {
		return fmt.Errorf("upsert merge request: %w", err)
	}

	// 4. Check idempotency: if a review_run already exists for this
	//    idempotency key, skip creation.
	existing, err := q.GetReviewRunByIdempotencyKey(ctx, ev.IdempotencyKey)
	if err == nil && existing.ID > 0 {
		s.logger.InfoContext(ctx, "review run already exists for idempotency key, skipping",
			"idempotency_key", ev.IdempotencyKey,
			"existing_run_id", existing.ID,
			"mr_iid", ev.MRIID,
		)
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check idempotency: %w", err)
	}

	// 5. Insert review run.
	var hookEvIDNull sql.NullInt64
	if hookEventID > 0 {
		hookEvIDNull = sql.NullInt64{Int64: hookEventID, Valid: true}
	}

	result, err := q.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mrID,
		HookEventID:    hookEvIDNull,
		TriggerType:    ev.TriggerType,
		HeadSha:        ev.HeadSHA,
		Status:         "pending",
		MaxRetries:     defaultMaxRetries,
		IdempotencyKey: ev.IdempotencyKey,
		ScopeJson:      db.NullRawMessage(ev.ScopeJSON),
	})
	if err != nil {
		// Handle race condition: another concurrent request may have
		// inserted the same idempotency key.
		if db.IsDuplicateKeyError(err) {
			s.logger.InfoContext(ctx, "review run idempotency key collision, skipping",
				"idempotency_key", ev.IdempotencyKey,
				"mr_iid", ev.MRIID,
			)
			return nil
		}
		return fmt.Errorf("insert review run: %w", err)
	}

	runID, _ := result.LastInsertId()
	s.logger.InfoContext(ctx, "created pending review run",
		"run_id", runID,
		"mr_iid", ev.MRIID,
		"project_id", ev.ProjectID,
		"head_sha", ev.HeadSHA,
		"trigger_type", ev.TriggerType,
		"idempotency_key", ev.IdempotencyKey,
	)

	return nil
}

// cancelRuns cancels any pending, running, or retry-scheduled review runs for
// the given MR.
func (s *Service) cancelRuns(ctx context.Context, q db.Querier, ev hooks.NormalizedEvent, action string) error {
	// 1. Ensure gitlab_instance exists (needed to find the project).
	instanceID, err := s.ensureInstance(ctx, q, ev.GitLabInstanceURL)
	if err != nil {
		return fmt.Errorf("ensure instance: %w", err)
	}

	// 2. Find the project.
	project, err := q.GetProjectByGitlabID(ctx, db.GetProjectByGitlabIDParams{
		GitlabInstanceID: instanceID,
		GitlabProjectID:  ev.ProjectID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No project means no runs to cancel.
			s.logger.InfoContext(ctx, "no project found for cancel, nothing to do",
				"gitlab_project_id", ev.ProjectID,
				"mr_iid", ev.MRIID,
			)
			return nil
		}
		return fmt.Errorf("get project: %w", err)
	}

	// 3. Find the merge request.
	mr, err := q.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{
		ProjectID: project.ID,
		MrIid:     ev.MRIID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.logger.InfoContext(ctx, "no merge request found for cancel, nothing to do",
				"project_id", project.ID,
				"mr_iid", ev.MRIID,
			)
			return nil
		}
		return fmt.Errorf("get merge request: %w", err)
	}

	// 4. Update MR state.
	newState := "closed"
	if action == "merge" {
		newState = "merged"
	}
	if err := q.UpdateMergeRequestState(ctx, db.UpdateMergeRequestStateParams{
		State:   newState,
		HeadSha: ev.HeadSHA,
		ID:      mr.ID,
	}); err != nil {
		return fmt.Errorf("update mr state: %w", err)
	}

	// 5. Cancel any active review runs, including retry-scheduled failures.
	if err := q.CancelPendingRunsForMR(ctx, mr.ID); err != nil {
		return fmt.Errorf("cancel runs: %w", err)
	}

	s.logger.InfoContext(ctx, "cancelled runs for MR",
		"mr_iid", ev.MRIID,
		"project_id", ev.ProjectID,
		"action", action,
		"merge_request_id", mr.ID,
	)

	return nil
}

// ensureInstance upserts a gitlab_instances row and returns its ID.
func (s *Service) ensureInstance(ctx context.Context, q db.Querier, instanceURL string) (int64, error) {
	if instanceURL == "" {
		return 0, fmt.Errorf("empty gitlab instance URL")
	}

	// Try upsert first.
	result, err := q.UpsertGitlabInstance(ctx, db.UpsertGitlabInstanceParams{
		Url:  instanceURL,
		Name: instanceURL,
	})
	if err != nil {
		return 0, fmt.Errorf("upsert gitlab instance: %w", err)
	}

	// ON DUPLICATE KEY UPDATE may not return a new last_insert_id if no row
	// was inserted. Fall back to lookup.
	id, _ := result.LastInsertId()
	if id > 0 {
		return id, nil
	}

	instance, err := q.GetGitlabInstanceByURL(ctx, instanceURL)
	if err != nil {
		return 0, fmt.Errorf("get gitlab instance by url: %w", err)
	}
	return instance.ID, nil
}

// ensureProject upserts a projects row and returns its internal ID.
func (s *Service) ensureProject(ctx context.Context, q db.Querier, instanceID, gitlabProjectID int64, projectPath string) (int64, error) {
	result, err := q.UpsertProject(ctx, db.UpsertProjectParams{
		GitlabInstanceID:  instanceID,
		GitlabProjectID:   gitlabProjectID,
		PathWithNamespace: projectPath,
		Enabled:           true,
	})
	if err != nil {
		return 0, fmt.Errorf("upsert project: %w", err)
	}

	id, _ := result.LastInsertId()
	if id > 0 {
		return id, nil
	}

	project, err := q.GetProjectByGitlabID(ctx, db.GetProjectByGitlabIDParams{
		GitlabInstanceID: instanceID,
		GitlabProjectID:  gitlabProjectID,
	})
	if err != nil {
		return 0, fmt.Errorf("get project by gitlab id: %w", err)
	}
	return project.ID, nil
}

// upsertMergeRequest upserts a merge_requests row and returns its internal ID.
func (s *Service) upsertMergeRequest(ctx context.Context, q db.Querier, projectID int64, ev hooks.NormalizedEvent) (int64, error) {
	result, err := q.UpsertMergeRequest(ctx, db.UpsertMergeRequestParams{
		ProjectID:    projectID,
		MrIid:        ev.MRIID,
		Title:        ev.Title,
		SourceBranch: ev.SourceBranch,
		TargetBranch: ev.TargetBranch,
		Author:       ev.Author,
		State:        ev.State,
		IsDraft:      ev.IsDraft,
		HeadSha:      ev.HeadSHA,
		WebUrl:       ev.WebURL,
	})
	if err != nil {
		return 0, fmt.Errorf("upsert merge request: %w", err)
	}

	id, _ := result.LastInsertId()
	if id > 0 {
		return id, nil
	}

	mr, err := q.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{
		ProjectID: projectID,
		MrIid:     ev.MRIID,
	})
	if err != nil {
		return 0, fmt.Errorf("get merge request: %w", err)
	}
	return mr.ID, nil
}
