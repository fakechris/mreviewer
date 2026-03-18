package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/metrics"
	tracing "github.com/mreviewer/mreviewer/internal/trace"
)

type DiscussionClient interface {
	CreateDiscussion(ctx context.Context, req CreateDiscussionRequest) (Discussion, error)
	CreateNote(ctx context.Context, req CreateNoteRequest) (Discussion, error)
	ResolveDiscussion(ctx context.Context, req ResolveDiscussionRequest) error
}

type Store interface {
	GetLatestMRVersion(ctx context.Context, mergeRequestID int64) (db.MrVersion, error)
	GetMergeRequest(ctx context.Context, id int64) (db.MergeRequest, error)
	GetReviewFinding(ctx context.Context, id int64) (db.ReviewFinding, error)
	GetGitlabDiscussion(ctx context.Context, id int64) (db.GitlabDiscussion, error)
	ListFindingsByRun(ctx context.Context, reviewRunID int64) ([]db.ReviewFinding, error)
	GetCommentActionByIdempotencyKey(ctx context.Context, idempotencyKey string) (db.CommentAction, error)
	GetGitlabDiscussionByFinding(ctx context.Context, reviewFindingID int64) (db.GitlabDiscussion, error)
	GetGitlabDiscussionByMergeRequestAndFinding(ctx context.Context, arg db.GetGitlabDiscussionByMergeRequestAndFindingParams) (db.GitlabDiscussion, error)
	InsertCommentAction(ctx context.Context, arg db.InsertCommentActionParams) (sql.Result, error)
	UpdateCommentActionStatus(ctx context.Context, arg db.UpdateCommentActionStatusParams) error
	InsertGitlabDiscussion(ctx context.Context, arg db.InsertGitlabDiscussionParams) (sql.Result, error)
	UpdateFindingDiscussionID(ctx context.Context, arg db.UpdateFindingDiscussionIDParams) error
	UpdateGitlabDiscussionResolved(ctx context.Context, arg db.UpdateGitlabDiscussionResolvedParams) error
	UpdateGitlabDiscussionSupersededBy(ctx context.Context, arg db.UpdateGitlabDiscussionSupersededByParams) error
	UpdateReviewRunStatus(ctx context.Context, arg db.UpdateReviewRunStatusParams) error
}

type Writer struct {
	client  DiscussionClient
	store   Store
	now     func() time.Time
	metrics *metrics.Registry
	tracer  *tracing.Recorder
}

func New(client DiscussionClient, store Store) *Writer {
	return &Writer{client: client, store: store, now: time.Now}
}

func (w *Writer) WithMetrics(registry *metrics.Registry) *Writer {
	w.metrics = registry
	return w
}

func (w *Writer) WithTracer(recorder *tracing.Recorder) *Writer {
	w.tracer = recorder
	return w
}

type CreateDiscussionRequest struct {
	ProjectID       int64    `json:"project_id"`
	MergeRequestIID int64    `json:"merge_request_iid"`
	Body            string   `json:"body"`
	Position        Position `json:"position"`
	ReviewFindingID int64    `json:"review_finding_id"`
	IdempotencyKey  string   `json:"idempotency_key"`
}

type CreateNoteRequest struct {
	ProjectID       int64  `json:"project_id"`
	MergeRequestIID int64  `json:"merge_request_iid"`
	Body            string `json:"body"`
	ReviewFindingID int64  `json:"review_finding_id"`
	IdempotencyKey  string `json:"idempotency_key"`
}

type ResolveDiscussionRequest struct {
	ProjectID       int64  `json:"project_id"`
	MergeRequestIID int64  `json:"merge_request_iid"`
	DiscussionID    string `json:"discussion_id"`
	Resolved        bool   `json:"resolved"`
}

type Position struct {
	PositionType string `json:"position_type"`
	BaseSHA      string `json:"base_sha"`
	StartSHA     string `json:"start_sha"`
	HeadSHA      string `json:"head_sha"`
	OldPath      string `json:"old_path"`
	NewPath      string `json:"new_path"`
	OldLine      *int32 `json:"old_line,omitempty"`
	NewLine      *int32 `json:"new_line,omitempty"`
}

type Discussion struct {
	ID string `json:"id"`
}

const (
	maxWriteRetries                = 3
	defaultRetryBackoff            = 10 * time.Millisecond
	commentActionStatusSucceeded   = "succeeded"
	commentActionStatusPending     = "pending"
	commentActionStatusFailed      = "failed"
	actionTypeCreateDiscussion     = "create_discussion"
	actionTypeCreateFileDiscussion = "create_file_discussion"
	actionTypeCreateGeneralNote    = "create_general_note"
	actionTypeSummaryNote          = "summary_note"
	actionTypeResolveDiscussion    = "resolve_discussion"
	writerErrorParserFallback      = "writer_parser_error_note"
	writerErrorDiscussionCreate    = "gitlab_create_discussion_failed"
	writerErrorDiscussionPosition  = "gitlab_position_invalid"
	writerErrorFileCreate          = "gitlab_create_file_discussion_failed"
	writerErrorNoteCreate          = "gitlab_create_note_failed"
	writerErrorDiscussionResolve   = "gitlab_resolve_discussion_failed"
	writerErrorUnavailable         = "gitlab_unavailable"
	runStatusParserError           = "parser_error"
)

func (w *Writer) Write(ctx context.Context, run db.ReviewRun, findings []db.ReviewFinding) error {
	ctx, endSpan := w.startSpan(ctx, "gitlab.create_discussion", map[string]string{"run_id": fmt.Sprintf("%d", run.ID)})
	defer endSpan()
	started := w.now()
	if w.client == nil || w.store == nil {
		return fmt.Errorf("writer: dependencies are not configured")
	}
	if strings.EqualFold(strings.TrimSpace(run.Status), "parser_error") {
		return w.writeParserErrorNote(ctx, run)
	}
	mr, err := w.store.GetMergeRequest(ctx, run.MergeRequestID)
	if err != nil {
		return fmt.Errorf("writer: load merge request: %w", err)
	}
	version, err := w.store.GetLatestMRVersion(ctx, run.MergeRequestID)
	if err != nil {
		return fmt.Errorf("writer: load latest MR version: %w", err)
	}
	for _, finding := range findings {
		if err := w.writeFinding(ctx, run, mr, version, finding); err != nil {
			return err
		}
	}
	if !isTerminalRun(run.Status) {
		return nil
	}
	if err := w.resolveCompletedFindings(ctx, run, mr); err != nil {
		w.recordMetrics(run, started, err)
		return err
	}
	err = w.writeSummaryNote(ctx, run, mr, findings)
	w.recordMetrics(run, started, err)
	return err
}

func (w *Writer) writeFinding(ctx context.Context, run db.ReviewRun, mr db.MergeRequest, version db.MrVersion, finding db.ReviewFinding) error {
	idempotencyKey := fmt.Sprintf("run:%d:finding:%d:%s", run.ID, finding.ID, actionTypeCreateDiscussion)
	if action, err := w.store.GetCommentActionByIdempotencyKey(ctx, idempotencyKey); err == nil && action.Status == commentActionStatusSucceeded {
		return nil
	}
	body := RenderCommentBody(finding)
	diffReq := CreateDiscussionRequest{ProjectID: mr.ProjectID, MergeRequestIID: mr.MrIid, ReviewFindingID: finding.ID, IdempotencyKey: idempotencyKey, Body: body, Position: BuildPosition(version, finding)}
	discussion, err := w.performDiscussionAction(ctx, run, finding, idempotencyKey, actionTypeCreateDiscussion, "diff", diffReq)
	if err == nil {
		return w.persistDiscussion(ctx, mr, finding, discussion, "diff")
	}
	if !isPositionFailure(err) {
		return w.persistRunFailure(ctx, run, classifyWriteError(err), err)
	}

	fileReq := diffReq
	fileReq.IdempotencyKey = fmt.Sprintf("run:%d:finding:%d:%s", run.ID, finding.ID, actionTypeCreateFileDiscussion)
	fileReq.Position = BuildFileLevelPosition(version, finding)
	fileReq.Body = renderFallbackBody(finding, body)
	discussion, fileErr := w.performDiscussionAction(ctx, run, finding, fileReq.IdempotencyKey, actionTypeCreateFileDiscussion, "file", fileReq)
	if fileErr == nil {
		return w.persistDiscussion(ctx, mr, finding, discussion, "file")
	}
	if !isPositionFailure(fileErr) {
		return w.persistRunFailure(ctx, run, classifyWriteError(fileErr), fileErr)
	}

	noteReq := CreateNoteRequest{ProjectID: mr.ProjectID, MergeRequestIID: mr.MrIid, ReviewFindingID: finding.ID, IdempotencyKey: fmt.Sprintf("run:%d:finding:%d:%s", run.ID, finding.ID, actionTypeCreateGeneralNote), Body: renderGeneralNoteBody(finding, body)}
	note, noteErr := w.performNoteAction(ctx, run, finding, noteReq.IdempotencyKey, actionTypeCreateGeneralNote, noteReq)
	if noteErr != nil {
		return w.persistRunFailure(ctx, run, classifyWriteError(noteErr), noteErr)
	}
	return w.persistDiscussion(ctx, mr, finding, note, "note")
}

func (w *Writer) writeParserErrorNote(ctx context.Context, run db.ReviewRun) error {
	idempotencyKey := fmt.Sprintf("run:%d:parser_error_summary_note", run.ID)
	if action, err := w.store.GetCommentActionByIdempotencyKey(ctx, idempotencyKey); err == nil && action.Status == commentActionStatusSucceeded {
		return nil
	}
	mr, err := w.store.GetMergeRequest(ctx, run.MergeRequestID)
	if err != nil {
		return fmt.Errorf("writer: load merge request: %w", err)
	}
	noteReq := CreateNoteRequest{ProjectID: mr.ProjectID, MergeRequestIID: mr.MrIid, IdempotencyKey: idempotencyKey, Body: fmt.Sprintf("AI review could not parse provider output for review run %d. A general fallback note was emitted instead of inline comments.", run.ID)}
	_, err = w.performNoteAction(ctx, run, db.ReviewFinding{}, idempotencyKey, actionTypeSummaryNote, noteReq)
	if err != nil {
		return w.persistRunFailure(ctx, run, writerErrorParserFallback, err)
	}
	return nil
}

func (w *Writer) resolveCompletedFindings(ctx context.Context, run db.ReviewRun, mr db.MergeRequest) error {
	runFindings, err := w.store.ListFindingsByRun(ctx, run.ID)
	if err != nil {
		return fmt.Errorf("writer: list run findings: %w", err)
	}
	newDiscussions := map[int64]string{}
	for _, finding := range runFindings {
		if finding.ID == 0 {
			continue
		}
		if finding.GitlabDiscussionID != "" {
			newDiscussions[finding.ID] = finding.GitlabDiscussionID
			continue
		}
		discussion, lookupErr := w.store.GetGitlabDiscussionByFinding(ctx, finding.ID)
		if lookupErr == nil && strings.TrimSpace(discussion.GitlabDiscussionID) != "" {
			newDiscussions[finding.ID] = discussion.GitlabDiscussionID
		}
	}
	for _, finding := range runFindings {
		switch finding.State {
		case "fixed", "stale":
			if err := w.resolveFindingDiscussion(ctx, run, mr, finding, sql.NullInt64{}); err != nil {
				return err
			}
		case "superseded":
			if err := w.resolveFindingDiscussion(ctx, run, mr, finding, nullableReplacementDiscussionID(finding.MatchedFindingID, newDiscussions)); err != nil {
				return err
			}
		}
	}
	return nil
}

func nullableReplacementDiscussionID(matched sql.NullInt64, discussions map[int64]string) sql.NullInt64 {
	if !matched.Valid {
		return sql.NullInt64{}
	}
	replacementDiscussionID, ok := discussions[matched.Int64]
	if !ok || strings.TrimSpace(replacementDiscussionID) == "" {
		return sql.NullInt64{}
	}
	return matched
}

func (w *Writer) resolveFindingDiscussion(ctx context.Context, run db.ReviewRun, mr db.MergeRequest, finding db.ReviewFinding, supersededBy sql.NullInt64) error {
	if finding.ID == 0 {
		return nil
	}
	discussion, err := w.store.GetGitlabDiscussionByFinding(ctx, finding.ID)
	if err != nil {
		return nil
	}
	if strings.TrimSpace(discussion.GitlabDiscussionID) == "" {
		return nil
	}
	idempotencyKey := fmt.Sprintf("run:%d:finding:%d:%s", run.ID, finding.ID, actionTypeResolveDiscussion)
	actionID, existing, err := w.ensureAction(ctx, run.ID, nullableFindingID(finding.ID), actionTypeResolveDiscussion, idempotencyKey)
	if err != nil {
		return fmt.Errorf("writer: insert comment action: %w", err)
	}
	if existing.Status != commentActionStatusSucceeded {
		callErr := w.retryWrite(ctx, func() error {
			return w.client.ResolveDiscussion(ctx, ResolveDiscussionRequest{ProjectID: mr.ProjectID, MergeRequestIID: mr.MrIid, DiscussionID: discussion.GitlabDiscussionID, Resolved: true})
		})
		if err := w.finalizeAction(ctx, actionID, existing, callErr, classifyResolveError(callErr)); err != nil {
			return err
		}
		if callErr != nil && classifyResolveError(callErr) != "" {
			return callErr
		}
	}
	if err := w.store.UpdateGitlabDiscussionResolved(ctx, db.UpdateGitlabDiscussionResolvedParams{Resolved: true, ID: discussion.ID}); err != nil {
		return err
	}
	if supersededBy.Valid {
		return w.linkSupersededDiscussion(ctx, discussion, supersededBy.Int64)
	}
	return nil
}

func (w *Writer) writeSummaryNote(ctx context.Context, run db.ReviewRun, mr db.MergeRequest, findings []db.ReviewFinding) error {
	idempotencyKey := fmt.Sprintf("run:%d:summary_note", run.ID)
	if action, err := w.store.GetCommentActionByIdempotencyKey(ctx, idempotencyKey); err == nil && action.Status == commentActionStatusSucceeded {
		return nil
	}
	body := renderSummaryBody(run, findings)
	_, err := w.performNoteAction(ctx, run, db.ReviewFinding{}, idempotencyKey, actionTypeSummaryNote, CreateNoteRequest{ProjectID: mr.ProjectID, MergeRequestIID: mr.MrIid, IdempotencyKey: idempotencyKey, Body: body})
	if err != nil {
		return w.persistRunFailure(ctx, run, classifyWriteError(err), err)
	}
	return nil
}

func (w *Writer) recordMetrics(run db.ReviewRun, started time.Time, err error) {
	if w.metrics == nil {
		return
	}
	labels := map[string]string{"status": strings.ToLower(strings.TrimSpace(run.Status))}
	if err != nil {
		labels["error_code"] = classifyWriteError(err)
	}
	w.metrics.ObserveDuration("comment_writer_latency_ms", labels, w.now().Sub(started))
}

func (w *Writer) startSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func()) {
	if w.tracer == nil {
		return ctx, func() {}
	}
	return w.tracer.Start(ctx, name, attrs)
}

func (w *Writer) performDiscussionAction(ctx context.Context, run db.ReviewRun, finding db.ReviewFinding, idempotencyKey, actionType, discussionType string, req CreateDiscussionRequest) (Discussion, error) {
	actionID, existing, err := w.ensureAction(ctx, run.ID, nullableFindingID(finding.ID), actionType, idempotencyKey)
	if err != nil {
		return Discussion{}, fmt.Errorf("writer: insert comment action: %w", err)
	}
	if existing.Status == commentActionStatusSucceeded {
		return w.restoreDiscussion(ctx, finding.ID, run.MergeRequestID)
	}

	var discussion Discussion
	callErr := w.retryWrite(ctx, func() error {
		var err error
		discussion, err = w.client.CreateDiscussion(ctx, req)
		return err
	})
	if err := w.finalizeAction(ctx, actionID, existing, callErr, classifyDiscussionError(discussionType, callErr)); err != nil {
		return Discussion{}, err
	}
	if callErr != nil {
		return Discussion{}, callErr
	}
	return discussion, nil
}

func (w *Writer) performNoteAction(ctx context.Context, run db.ReviewRun, finding db.ReviewFinding, idempotencyKey, actionType string, req CreateNoteRequest) (Discussion, error) {
	actionID, existing, err := w.ensureAction(ctx, run.ID, nullableFindingID(finding.ID), actionType, idempotencyKey)
	if err != nil {
		return Discussion{}, fmt.Errorf("writer: insert comment action: %w", err)
	}
	if existing.Status == commentActionStatusSucceeded {
		return w.restoreDiscussion(ctx, finding.ID, run.MergeRequestID)
	}

	var note Discussion
	callErr := w.retryWrite(ctx, func() error {
		var err error
		note, err = w.client.CreateNote(ctx, req)
		return err
	})
	if err := w.finalizeAction(ctx, actionID, existing, callErr, classifyWriteError(callErr)); err != nil {
		return Discussion{}, err
	}
	if callErr != nil {
		return Discussion{}, callErr
	}
	return note, nil
}

func (w *Writer) ensureAction(ctx context.Context, runID int64, findingID sql.NullInt64, actionType, idempotencyKey string) (int64, db.CommentAction, error) {
	if action, err := w.store.GetCommentActionByIdempotencyKey(ctx, idempotencyKey); err == nil {
		return action.ID, action, nil
	}
	result, err := w.store.InsertCommentAction(ctx, db.InsertCommentActionParams{ReviewRunID: runID, ReviewFindingID: findingID, ActionType: actionType, IdempotencyKey: idempotencyKey, Status: commentActionStatusPending})
	if err != nil {
		if strings.Contains(err.Error(), "Duplicate entry") {
			action, lookupErr := w.store.GetCommentActionByIdempotencyKey(ctx, idempotencyKey)
			return action.ID, action, lookupErr
		}
		return 0, db.CommentAction{}, err
	}
	actionID, _ := result.LastInsertId()
	return actionID, db.CommentAction{ID: actionID, IdempotencyKey: idempotencyKey, Status: commentActionStatusPending}, nil
}

func (w *Writer) finalizeAction(ctx context.Context, actionID int64, existing db.CommentAction, actionErr error, errorCode string) error {
	status := commentActionStatusSucceeded
	detail := sql.NullString{}
	retryCount := existing.RetryCount
	if actionErr != nil {
		if errorCode == "" {
			detail = sql.NullString{}
		} else {
			status = commentActionStatusFailed
			detail = sql.NullString{String: actionErr.Error(), Valid: true}
			retryCount++
		}
	}
	if actionID == 0 {
		actionID = existing.ID
	}
	return w.store.UpdateCommentActionStatus(ctx, db.UpdateCommentActionStatusParams{Status: status, ErrorCode: errorCodeIfFailed(status, errorCode), ErrorDetail: detail, LatencyMs: defaultRetryBackoff.Milliseconds(), RetryCount: retryCount, ID: actionID})
}

func (w *Writer) linkSupersededDiscussion(ctx context.Context, discussion db.GitlabDiscussion, replacementFindingID int64) error {
	if discussion.ID == 0 || replacementFindingID == 0 {
		return nil
	}
	replacement, err := w.store.GetReviewFinding(ctx, replacementFindingID)
	if err != nil {
		return nil
	}
	replacementDiscussionID := strings.TrimSpace(replacement.GitlabDiscussionID)
	if replacementDiscussionID == "" {
		linked, err := w.store.GetGitlabDiscussionByFinding(ctx, replacementFindingID)
		if err != nil {
			return nil
		}
		replacementDiscussionID = strings.TrimSpace(linked.GitlabDiscussionID)
	}
	if replacementDiscussionID == "" {
		return nil
	}
	current := discussion
	if current.SupersededByDiscussionID.Valid {
		existing, err := w.store.GetGitlabDiscussion(ctx, current.SupersededByDiscussionID.Int64)
		if err == nil && strings.TrimSpace(existing.GitlabDiscussionID) == replacementDiscussionID {
			return nil
		}
	}
	replacementDiscussion, err := w.store.GetGitlabDiscussionByFinding(ctx, replacementFindingID)
	if err != nil {
		return nil
	}
	return w.store.UpdateGitlabDiscussionSupersededBy(ctx, db.UpdateGitlabDiscussionSupersededByParams{SupersededByDiscussionID: sql.NullInt64{Int64: replacementDiscussion.ID, Valid: true}, ID: discussion.ID})
}

func (w *Writer) persistDiscussion(ctx context.Context, mr db.MergeRequest, finding db.ReviewFinding, discussion Discussion, discussionType string) error {
	if finding.ID == 0 || discussion.ID == "" {
		return nil
	}
	if _, err := w.restoreDiscussion(ctx, finding.ID, mr.ID); err == nil {
		return nil
	}
	if _, err := w.store.InsertGitlabDiscussion(ctx, db.InsertGitlabDiscussionParams{ReviewFindingID: finding.ID, MergeRequestID: mr.ID, GitlabDiscussionID: discussion.ID, DiscussionType: discussionType, Resolved: false}); err != nil && !strings.Contains(err.Error(), "Duplicate entry") {
		return fmt.Errorf("writer: insert gitlab discussion: %w", err)
	}
	if err := w.persistFindingDiscussionLink(ctx, finding.ID, discussion.ID); err != nil {
		return err
	}
	return nil
}

func (w *Writer) persistFindingDiscussionLink(ctx context.Context, findingID int64, discussionID string) error {
	if findingID == 0 || strings.TrimSpace(discussionID) == "" {
		return nil
	}
	finding, err := w.store.GetReviewFinding(ctx, findingID)
	if err != nil {
		return nil
	}
	if strings.TrimSpace(finding.GitlabDiscussionID) == discussionID {
		return nil
	}
	return w.store.UpdateFindingDiscussionID(ctx, db.UpdateFindingDiscussionIDParams{GitlabDiscussionID: discussionID, ID: findingID})
}

func (w *Writer) restoreDiscussion(ctx context.Context, findingID, mergeRequestID int64) (Discussion, error) {
	if findingID == 0 {
		return Discussion{}, sql.ErrNoRows
	}
	if existing, err := w.store.GetGitlabDiscussionByMergeRequestAndFinding(ctx, db.GetGitlabDiscussionByMergeRequestAndFindingParams{MergeRequestID: mergeRequestID, ReviewFindingID: findingID}); err == nil {
		return Discussion{ID: existing.GitlabDiscussionID}, nil
	}
	return Discussion{}, sql.ErrNoRows
}

func (w *Writer) retryWrite(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt < maxWriteRetries; attempt++ {
		err = fn()
		if err == nil || !isRetryableWriteError(err) || attempt == maxWriteRetries-1 {
			return err
		}
		if sleepErr := sleepContext(ctx, defaultRetryBackoff*time.Duration(1<<attempt)); sleepErr != nil {
			return sleepErr
		}
	}
	return err
}

func (w *Writer) persistRunFailure(ctx context.Context, run db.ReviewRun, code string, err error) error {
	if code == "" || err == nil {
		return err
	}
	updateErr := w.store.UpdateReviewRunStatus(ctx, db.UpdateReviewRunStatusParams{Status: run.Status, ErrorCode: code, ErrorDetail: sql.NullString{String: err.Error(), Valid: true}, ID: run.ID})
	if updateErr != nil {
		return fmt.Errorf("writer: persist run failure: %w", updateErr)
	}
	return err
}

func BuildFileLevelPosition(version db.MrVersion, finding db.ReviewFinding) Position {
	position := BuildPosition(version, finding)
	position.PositionType = "file"
	position.OldLine = nil
	position.NewLine = nil
	return position
}

func renderFallbackBody(finding db.ReviewFinding, body string) string {
	parts := []string{body}
	if line := renderTargetLine(finding); line != "" {
		parts = append(parts, "Original target line: "+line)
	}
	return strings.Join(parts, "\n\n")
}

func renderGeneralNoteBody(finding db.ReviewFinding, body string) string {
	parts := []string{body}
	if path := strings.TrimSpace(finding.Path); path != "" {
		parts = append(parts, "File: `"+path+"`")
	}
	if snippet := strings.TrimSpace(finding.AnchorSnippet.String); snippet != "" {
		parts = append(parts, "Anchor context:\n```\n"+snippet+"\n```")
	}
	if line := renderTargetLine(finding); line != "" {
		parts = append(parts, "Original target line: "+line)
	}
	return strings.Join(parts, "\n\n")
}

func renderTargetLine(finding db.ReviewFinding) string {
	if finding.NewLine.Valid {
		return fmt.Sprintf("new_line=%d", finding.NewLine.Int32)
	}
	if finding.OldLine.Valid {
		return fmt.Sprintf("old_line=%d", finding.OldLine.Int32)
	}
	return ""
}

func isTerminalRun(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "completed" || status == runStatusParserError
}

func renderSummaryBody(run db.ReviewRun, findings []db.ReviewFinding) string {
	active := 0
	resolved := 0
	filtered := 0
	for _, finding := range findings {
		switch strings.ToLower(strings.TrimSpace(finding.State)) {
		case "filtered":
			filtered++
		case "fixed", "stale", "superseded", "ignored":
			resolved++
		default:
			active++
		}
	}
	overallRisk := "none"
	if active > 0 {
		overallRisk = "elevated"
	}
	if len(findings) == 0 {
		return fmt.Sprintf("AI review summary for run %d\n\n- overall_risk: %s\n- findings_posted: 0\n- findings_resolved: 0\n- findings_filtered: 0\n\nNo issues found.", run.ID, overallRisk)
	}
	return fmt.Sprintf("AI review summary for run %d\n\n- overall_risk: %s\n- findings_posted: %d\n- findings_resolved: %d\n- findings_filtered: %d", run.ID, overallRisk, active, resolved, filtered)
}

func nullableFindingID(id int64) sql.NullInt64 {
	if id == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: id, Valid: true}
}

func classifyDiscussionError(discussionType string, err error) string {
	if err == nil {
		return ""
	}
	if isRetryableWriteError(err) {
		return writerErrorUnavailable
	}
	if isPositionFailure(err) {
		if discussionType == "file" {
			return writerErrorFileCreate
		}
		return writerErrorDiscussionPosition
	}
	if discussionType == "file" {
		return writerErrorFileCreate
	}
	return writerErrorDiscussionCreate
}

func classifyWriteError(err error) string {
	if err == nil {
		return ""
	}
	if isRetryableWriteError(err) {
		return writerErrorUnavailable
	}
	if isPositionFailure(err) {
		return writerErrorDiscussionPosition
	}
	return writerErrorNoteCreate
}

func classifyResolveError(err error) string {
	if err == nil {
		return ""
	}
	if isRetryableWriteError(err) {
		return writerErrorUnavailable
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "already resolved") {
		return ""
	}
	return writerErrorDiscussionResolve
}

func errorCodeIfFailed(status, code string) string {
	if status == commentActionStatusFailed {
		return code
	}
	return ""
}

func isRetryableWriteError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *net.OpError
	if errors.As(err, &urlErr) {
		return true
	}
	var statusErr interface{ StatusCode() int }
	if errors.As(err, &statusErr) {
		code := statusErr.StatusCode()
		return code == http.StatusTooManyRequests || code >= 500
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "timeout") || strings.Contains(message, "connection refused") || strings.Contains(message, "tempor") || strings.Contains(message, "429") || strings.Contains(message, "503")
}

func isPositionFailure(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "400") || strings.Contains(message, "position") || strings.Contains(message, "line_code") || strings.Contains(message, "invalid line")
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func BuildPosition(version db.MrVersion, finding db.ReviewFinding) Position {
	oldPath, newPath := resolvePaths(finding)
	position := Position{
		PositionType: "text",
		BaseSHA:      version.BaseSha,
		StartSHA:     version.StartSha,
		HeadSHA:      version.HeadSha,
		OldPath:      oldPath,
		NewPath:      newPath,
	}
	switch canonicalAnchorKind(finding.AnchorKind) {
	case "old_line":
		if finding.OldLine.Valid {
			value := finding.OldLine.Int32
			position.OldLine = &value
		}
	case "context_line":
		if finding.OldLine.Valid {
			value := finding.OldLine.Int32
			position.OldLine = &value
		}
		if finding.NewLine.Valid {
			value := finding.NewLine.Int32
			position.NewLine = &value
		}
	default:
		if finding.NewLine.Valid {
			value := finding.NewLine.Int32
			position.NewLine = &value
		}
	}
	return position
}

func RenderCommentBody(finding db.ReviewFinding) string {
	parts := []string{fmt.Sprintf("**%s**", strings.TrimSpace(finding.Title))}
	if body := strings.TrimSpace(finding.BodyMarkdown.String); body != "" {
		parts = append(parts, body)
	}
	if evidence := strings.TrimSpace(finding.Evidence.String); evidence != "" {
		parts = append(parts, "Evidence:\n"+renderBulletList(evidence))
	}
	if suggestion := strings.TrimSpace(finding.SuggestedPatch.String); suggestion != "" {
		parts = append(parts, "Suggested fix:\n"+suggestion)
	}
	parts = append(parts, fmt.Sprintf("<!-- ai-review:finding_id=%d anchor_fp=%s semantic_fp=%s confidence=%.2f -->", finding.ID, finding.AnchorFingerprint, finding.SemanticFingerprint, finding.Confidence))
	return strings.Join(parts, "\n\n")
}

func renderBulletList(evidence string) string {
	lines := strings.Split(strings.TrimSpace(evidence), "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts = append(parts, "- "+line)
	}
	return strings.Join(parts, "\n")
}

func resolvePaths(finding db.ReviewFinding) (string, string) {
	path := strings.TrimSpace(finding.Path)
	if path == "" {
		return "", ""
	}
	parts := strings.SplitN(path, " -> ", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return path, path
}

func canonicalAnchorKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "old", "old_line", "deleted", "removed":
		return "old_line"
	case "context", "context_line", "unchanged":
		return "context_line"
	default:
		return "new_line"
	}
}
