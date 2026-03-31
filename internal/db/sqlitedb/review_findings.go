package sqlitedb

import (
	"context"
	"database/sql"

	"github.com/mreviewer/mreviewer/internal/db"
)

func (q *Queries) GetFindingByMRAndDiscussionID(ctx context.Context, arg db.GetFindingByMRAndDiscussionIDParams) (db.ReviewFinding, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, review_run_id, merge_request_id, category, severity, confidence, title, body_markdown,
		        path, anchor_kind, old_line, new_line, range_start_kind, range_start_old_line, range_start_new_line,
		        range_end_kind, range_end_old_line, range_end_new_line, anchor_snippet, evidence, suggested_patch,
		        canonical_key, anchor_fingerprint, semantic_fingerprint, state, matched_finding_id, last_seen_run_id,
		        gitlab_discussion_id, error_code, created_at, updated_at
		 FROM review_findings WHERE merge_request_id = ? AND gitlab_discussion_id = ? LIMIT 1`,
		arg.MergeRequestID, arg.GitlabDiscussionID)
	return scanReviewFinding(row)
}

func (q *Queries) GetReviewFinding(ctx context.Context, id int64) (db.ReviewFinding, error) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id, review_run_id, merge_request_id, category, severity, confidence, title, body_markdown,
		        path, anchor_kind, old_line, new_line, range_start_kind, range_start_old_line, range_start_new_line,
		        range_end_kind, range_end_old_line, range_end_new_line, anchor_snippet, evidence, suggested_patch,
		        canonical_key, anchor_fingerprint, semantic_fingerprint, state, matched_finding_id, last_seen_run_id,
		        gitlab_discussion_id, error_code, created_at, updated_at
		 FROM review_findings WHERE id = ? LIMIT 1`, id)
	return scanReviewFinding(row)
}

func (q *Queries) InsertReviewFinding(ctx context.Context, arg db.InsertReviewFindingParams) (sql.Result, error) {
	return q.db.ExecContext(ctx,
		`INSERT INTO review_findings (
		    review_run_id, merge_request_id, category, severity, confidence,
		    title, body_markdown, path, anchor_kind, old_line, new_line,
		    range_start_kind, range_start_old_line, range_start_new_line,
		    range_end_kind, range_end_old_line, range_end_new_line,
		    anchor_snippet, evidence, suggested_patch, canonical_key,
		    anchor_fingerprint, semantic_fingerprint, state
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		arg.ReviewRunID, arg.MergeRequestID, arg.Category, arg.Severity, arg.Confidence,
		arg.Title, arg.BodyMarkdown, arg.Path, arg.AnchorKind, arg.OldLine, arg.NewLine,
		arg.RangeStartKind, arg.RangeStartOldLine, arg.RangeStartNewLine,
		arg.RangeEndKind, arg.RangeEndOldLine, arg.RangeEndNewLine,
		arg.AnchorSnippet, arg.Evidence, arg.SuggestedPatch, arg.CanonicalKey,
		arg.AnchorFingerprint, arg.SemanticFingerprint, arg.State)
}

func (q *Queries) ListActiveFindingsByMR(ctx context.Context, mergeRequestID int64) ([]db.ReviewFinding, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, review_run_id, merge_request_id, category, severity, confidence, title, body_markdown,
		        path, anchor_kind, old_line, new_line, range_start_kind, range_start_old_line, range_start_new_line,
		        range_end_kind, range_end_old_line, range_end_new_line, anchor_snippet, evidence, suggested_patch,
		        canonical_key, anchor_fingerprint, semantic_fingerprint, state, matched_finding_id, last_seen_run_id,
		        gitlab_discussion_id, error_code, created_at, updated_at
		 FROM review_findings WHERE merge_request_id = ? AND state IN ('new', 'posted', 'active') ORDER BY created_at ASC`,
		mergeRequestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReviewFindingRows(rows)
}

func (q *Queries) ListFindingsByMergeRequest(ctx context.Context, mergeRequestID int64) ([]db.ReviewFinding, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, review_run_id, merge_request_id, category, severity, confidence, title, body_markdown,
		        path, anchor_kind, old_line, new_line, range_start_kind, range_start_old_line, range_start_new_line,
		        range_end_kind, range_end_old_line, range_end_new_line, anchor_snippet, evidence, suggested_patch,
		        canonical_key, anchor_fingerprint, semantic_fingerprint, state, matched_finding_id, last_seen_run_id,
		        gitlab_discussion_id, error_code, created_at, updated_at
		 FROM review_findings WHERE merge_request_id = ? ORDER BY created_at ASC`,
		mergeRequestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReviewFindingRows(rows)
}

func (q *Queries) ListFindingsByRun(ctx context.Context, reviewRunID int64) ([]db.ReviewFinding, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, review_run_id, merge_request_id, category, severity, confidence, title, body_markdown,
		        path, anchor_kind, old_line, new_line, range_start_kind, range_start_old_line, range_start_new_line,
		        range_end_kind, range_end_old_line, range_end_new_line, anchor_snippet, evidence, suggested_patch,
		        canonical_key, anchor_fingerprint, semantic_fingerprint, state, matched_finding_id, last_seen_run_id,
		        gitlab_discussion_id, error_code, created_at, updated_at
		 FROM review_findings WHERE review_run_id = ? ORDER BY created_at ASC`,
		reviewRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReviewFindingRows(rows)
}

func (q *Queries) UpdateFindingDiscussionID(ctx context.Context, arg db.UpdateFindingDiscussionIDParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE review_findings SET gitlab_discussion_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		arg.GitlabDiscussionID, arg.ID)
	return err
}

func (q *Queries) UpdateFindingLastSeen(ctx context.Context, arg db.UpdateFindingLastSeenParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE review_findings SET last_seen_run_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		arg.LastSeenRunID, arg.ID)
	return err
}

func (q *Queries) UpdateFindingRelocation(ctx context.Context, arg db.UpdateFindingRelocationParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE review_findings
		 SET path = ?, anchor_kind = ?, old_line = ?, new_line = ?,
		     range_start_kind = ?, range_start_old_line = ?, range_start_new_line = ?,
		     range_end_kind = ?, range_end_old_line = ?, range_end_new_line = ?,
		     anchor_snippet = ?, anchor_fingerprint = ?, semantic_fingerprint = ?,
		     last_seen_run_id = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		arg.Path, arg.AnchorKind, arg.OldLine, arg.NewLine,
		arg.RangeStartKind, arg.RangeStartOldLine, arg.RangeStartNewLine,
		arg.RangeEndKind, arg.RangeEndOldLine, arg.RangeEndNewLine,
		arg.AnchorSnippet, arg.AnchorFingerprint, arg.SemanticFingerprint,
		arg.LastSeenRunID, arg.ID)
	return err
}

func (q *Queries) UpdateFindingState(ctx context.Context, arg db.UpdateFindingStateParams) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE review_findings SET state = ?, matched_finding_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		arg.State, arg.MatchedFindingID, arg.ID)
	return err
}
