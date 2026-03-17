package writer

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
)

type DiscussionClient interface {
	CreateDiscussion(ctx context.Context, req CreateDiscussionRequest) (Discussion, error)
}

type Store interface {
	GetLatestMRVersion(ctx context.Context, mergeRequestID int64) (db.MrVersion, error)
	GetMergeRequest(ctx context.Context, id int64) (db.MergeRequest, error)
	GetCommentActionByIdempotencyKey(ctx context.Context, idempotencyKey string) (db.CommentAction, error)
	InsertCommentAction(ctx context.Context, arg db.InsertCommentActionParams) (sql.Result, error)
	UpdateCommentActionStatus(ctx context.Context, arg db.UpdateCommentActionStatusParams) error
	InsertGitlabDiscussion(ctx context.Context, arg db.InsertGitlabDiscussionParams) (sql.Result, error)
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

func (w *Writer) Write(ctx context.Context, run db.ReviewRun, findings []db.ReviewFinding) error {
	if w.client == nil || w.store == nil {
		return fmt.Errorf("writer: dependencies are not configured")
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
	idempotencyKey := fmt.Sprintf("run:%d:finding:%d:create_discussion", run.ID, finding.ID)
	if action, err := w.store.GetCommentActionByIdempotencyKey(ctx, idempotencyKey); err == nil && action.Status == "succeeded" {
		return nil
	}

	start := w.now()
	result, err := w.store.InsertCommentAction(ctx, db.InsertCommentActionParams{ReviewRunID: run.ID, ReviewFindingID: sql.NullInt64{Int64: finding.ID, Valid: true}, ActionType: "create_discussion", IdempotencyKey: idempotencyKey, Status: "pending"})
	if err != nil && !strings.Contains(err.Error(), "Duplicate entry") {
		return fmt.Errorf("writer: insert comment action: %w", err)
	}
	actionID, _ := result.LastInsertId()

	req := CreateDiscussionRequest{
		ProjectID:       mr.ProjectID,
		MergeRequestIID: mr.MrIid,
		ReviewFindingID: finding.ID,
		IdempotencyKey:  idempotencyKey,
		Body:            RenderCommentBody(finding),
		Position:        BuildPosition(version, finding),
	}
	discussion, createErr := w.client.CreateDiscussion(ctx, req)
	status := "succeeded"
	errorCode := ""
	errorDetail := sql.NullString{}
	if createErr != nil {
		status = "failed"
		errorCode = "gitlab_create_discussion_failed"
		errorDetail = sql.NullString{String: createErr.Error(), Valid: true}
	}
	if actionID != 0 {
		if err := w.store.UpdateCommentActionStatus(ctx, db.UpdateCommentActionStatusParams{Status: status, ErrorCode: errorCode, ErrorDetail: errorDetail, LatencyMs: w.now().Sub(start).Milliseconds(), RetryCount: 0, ID: actionID}); err != nil {
			return fmt.Errorf("writer: update comment action: %w", err)
		}
	}
	if createErr != nil {
		return createErr
	}
	if _, err := w.store.InsertGitlabDiscussion(ctx, db.InsertGitlabDiscussionParams{ReviewFindingID: finding.ID, MergeRequestID: mr.ID, GitlabDiscussionID: discussion.ID, DiscussionType: "diff", Resolved: false}); err != nil {
		return fmt.Errorf("writer: insert gitlab discussion: %w", err)
	}
	return nil
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
