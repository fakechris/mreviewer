package gitlab

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/reviewcomment"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type RuntimeWriteback struct {
	queries   *db.Queries
	publisher *Publisher
}

const (
	runtimeActionSummary    = "summary_note"
	runtimeActionDiscussion = "create_discussion"
)

func NewRuntimeWriteback(sqlDB *sql.DB, client DiscussionClient) *RuntimeWriteback {
	if sqlDB == nil || client == nil {
		return nil
	}
	return &RuntimeWriteback{
		queries:   db.New(sqlDB),
		publisher: NewPublisher(client),
	}
}

func (w *RuntimeWriteback) WriteBundle(ctx context.Context, run db.ReviewRun, bundle reviewcore.ReviewBundle) error {
	if w == nil || w.queries == nil || w.publisher == nil {
		return fmt.Errorf("gitlab runtime writeback: dependencies are required")
	}

	project, err := w.queries.GetProject(ctx, run.ProjectID)
	if err != nil {
		return fmt.Errorf("gitlab runtime writeback: load project: %w", err)
	}
	mr, err := w.queries.GetMergeRequest(ctx, run.MergeRequestID)
	if err != nil {
		return fmt.Errorf("gitlab runtime writeback: load merge request: %w", err)
	}

	published, err := w.publisher.PublishWithResult(ctx, PublishRequest{
		ProjectID:       project.GitlabProjectID,
		MergeRequestIID: mr.MrIid,
		Mode:            PublishModeFullReviewComments,
		Bundle:          bundle,
	})
	if err != nil {
		return err
	}

	return w.persistPublishedArtifacts(ctx, run, mr.ID, published)
}

func (w *RuntimeWriteback) persistPublishedArtifacts(ctx context.Context, run db.ReviewRun, mergeRequestID int64, published PublishResult) error {
	if w == nil || w.queries == nil {
		return nil
	}
	for idx, item := range published.Published {
		candidate := item.Candidate
		actionType := actionTypeForCandidate(candidate)
		idempotencyKey := fmt.Sprintf("run:%d:bundle:%d:%s", run.ID, idx, actionType)
		findingID, err := w.persistFindingIfNeeded(ctx, run, mergeRequestID, candidate, item.Discussion)
		if err != nil {
			return err
		}
		if _, err := w.queries.InsertCommentAction(ctx, db.InsertCommentActionParams{
			ReviewRunID:     run.ID,
			ReviewFindingID: nullableFindingID(findingID),
			ActionType:      actionType,
			IdempotencyKey:  idempotencyKey,
			Status:          "succeeded",
		}); err != nil && !db.IsDuplicateKeyError(err) {
			return fmt.Errorf("gitlab runtime writeback: persist comment action: %w", err)
		}
	}
	return nil
}

func (w *RuntimeWriteback) persistFindingIfNeeded(ctx context.Context, run db.ReviewRun, mergeRequestID int64, candidate reviewcore.PublishCandidate, discussion reviewcomment.Discussion) (int64, error) {
	if candidate.Type != "finding" {
		return 0, nil
	}
	params := db.InsertReviewFindingParams{
		ReviewRunID:         run.ID,
		MergeRequestID:      mergeRequestID,
		Category:            "review",
		Severity:            defaultSeverity(candidate.Severity),
		Confidence:          1.0,
		Title:               strings.TrimSpace(candidate.Title),
		BodyMarkdown:        nullableString(publishBody(candidate)),
		Path:                candidatePath(candidate),
		AnchorKind:          anchorKind(candidate.Location),
		OldLine:             oldLine(candidate.Location),
		NewLine:             newLine(candidate.Location),
		RangeStartKind:      sql.NullString{},
		RangeStartOldLine:   sql.NullInt32{},
		RangeStartNewLine:   sql.NullInt32{},
		RangeEndKind:        sql.NullString{},
		RangeEndOldLine:     sql.NullInt32{},
		RangeEndNewLine:     sql.NullInt32{},
		AnchorSnippet:       sql.NullString{},
		Evidence:            sql.NullString{},
		SuggestedPatch:      sql.NullString{},
		CanonicalKey:        canonicalKey(candidate),
		AnchorFingerprint:   fingerprint("anchor", candidate),
		SemanticFingerprint: fingerprint("semantic", candidate),
		State:               "active",
	}
	result, err := w.queries.InsertReviewFinding(ctx, params)
	if err != nil {
		return 0, fmt.Errorf("gitlab runtime writeback: insert review finding: %w", err)
	}
	findingID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("gitlab runtime writeback: last insert id: %w", err)
	}
	if strings.TrimSpace(discussion.ID) == "" {
		return findingID, nil
	}
	if _, err := w.queries.InsertGitlabDiscussion(ctx, db.InsertGitlabDiscussionParams{
		ReviewFindingID:    findingID,
		MergeRequestID:     mergeRequestID,
		GitlabDiscussionID: discussion.ID,
		DiscussionType:     "diff",
		Resolved:           false,
	}); err != nil && !db.IsDuplicateKeyError(err) {
		return 0, fmt.Errorf("gitlab runtime writeback: insert discussion: %w", err)
	}
	if err := w.queries.UpdateFindingDiscussionID(ctx, db.UpdateFindingDiscussionIDParams{
		GitlabDiscussionID: discussion.ID,
		ID:                findingID,
	}); err != nil {
		return 0, fmt.Errorf("gitlab runtime writeback: update finding discussion id: %w", err)
	}
	return findingID, nil
}

func actionTypeForCandidate(candidate reviewcore.PublishCandidate) string {
	if candidate.Type == "summary" {
		return runtimeActionSummary
	}
	return runtimeActionDiscussion
}

func nullableFindingID(findingID int64) sql.NullInt64 {
	if findingID == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: findingID, Valid: true}
}

func nullableString(value string) sql.NullString {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func candidatePath(candidate reviewcore.PublishCandidate) string {
	if candidate.Location == nil {
		return ""
	}
	return strings.TrimSpace(candidate.Location.Path)
}

func anchorKind(location *reviewcore.CanonicalLocation) string {
	if location == nil {
		return ""
	}
	if location.Side == reviewcore.LocationSideOld {
		return "old_line"
	}
	return "new_line"
}

func oldLine(location *reviewcore.CanonicalLocation) sql.NullInt32 {
	if location == nil || location.Side != reviewcore.LocationSideOld || location.Line <= 0 {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: int32(location.Line), Valid: true}
}

func newLine(location *reviewcore.CanonicalLocation) sql.NullInt32 {
	if location == nil || location.Side == reviewcore.LocationSideOld || location.Line <= 0 {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: int32(location.Line), Valid: true}
}

func canonicalKey(candidate reviewcore.PublishCandidate) string {
	path := candidatePath(candidate)
	return strings.ToLower(strings.TrimSpace(candidate.Title) + "::" + path)
}

func fingerprint(prefix string, candidate reviewcore.PublishCandidate) string {
	path := candidatePath(candidate)
	body := publishBody(candidate)
	sum := sha256.Sum256([]byte(strings.Join([]string{prefix, path, candidate.Title, body, candidate.Severity}, "\x00")))
	return fmt.Sprintf("%x", sum[:])
}

func defaultSeverity(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "medium"
	}
	return value
}
