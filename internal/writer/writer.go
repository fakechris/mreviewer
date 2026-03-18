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
)

type DiscussionClient interface {
	CreateDiscussion(ctx context.Context, req CreateDiscussionRequest) (Discussion, error)
	CreateNote(ctx context.Context, req CreateNoteRequest) (Discussion, error)
}

type Store interface {
	GetLatestMRVersion(ctx context.Context, mergeRequestID int64) (db.MrVersion, error)
	GetMergeRequest(ctx context.Context, id int64) (db.MergeRequest, error)
	GetCommentActionByIdempotencyKey(ctx context.Context, idempotencyKey string) (db.CommentAction, error)
	GetGitlabDiscussionByMergeRequestAndFinding(ctx context.Context, arg db.GetGitlabDiscussionByMergeRequestAndFindingParams) (db.GitlabDiscussion, error)
	InsertCommentAction(ctx context.Context, arg db.InsertCommentActionParams) (sql.Result, error)
	UpdateCommentActionStatus(ctx context.Context, arg db.UpdateCommentActionStatusParams) error
	InsertGitlabDiscussion(ctx context.Context, arg db.InsertGitlabDiscussionParams) (sql.Result, error)
	UpdateReviewRunStatus(ctx context.Context, arg db.UpdateReviewRunStatusParams) error
}

type Writer struct {
	client DiscussionClient
	store  Store
	now    func() time.Time
}

func New(client DiscussionClient, store Store) *Writer {
	return &Writer{client: client, store: store, now: time.Now}
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
	writerErrorParserFallback      = "writer_parser_error_note"
	writerErrorDiscussionCreate    = "gitlab_create_discussion_failed"
	writerErrorDiscussionPosition  = "gitlab_position_invalid"
	writerErrorFileCreate          = "gitlab_create_file_discussion_failed"
	writerErrorNoteCreate          = "gitlab_create_note_failed"
	writerErrorUnavailable         = "gitlab_unavailable"
)

func (w *Writer) Write(ctx context.Context, run db.ReviewRun, findings []db.ReviewFinding) error {
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
	return nil
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
	_, err = w.performNoteAction(ctx, run, db.ReviewFinding{}, idempotencyKey, "summary_note", noteReq)
	if err != nil {
		return w.persistRunFailure(ctx, run, writerErrorParserFallback, err)
	}
	return nil
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
		status = commentActionStatusFailed
		detail = sql.NullString{String: actionErr.Error(), Valid: true}
		retryCount++
	}
	if actionID == 0 {
		actionID = existing.ID
	}
	return w.store.UpdateCommentActionStatus(ctx, db.UpdateCommentActionStatusParams{Status: status, ErrorCode: errorCodeIfFailed(status, errorCode), ErrorDetail: detail, LatencyMs: defaultRetryBackoff.Milliseconds(), RetryCount: retryCount, ID: actionID})
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
	return nil
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
