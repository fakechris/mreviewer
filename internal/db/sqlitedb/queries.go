// Package sqlitedb provides a hand-written implementation of the db.Store
// interface for SQLite. It reuses the model types from the parent db package
// and translates MySQL-specific SQL to SQLite-compatible equivalents.
package sqlitedb

import (
	"database/sql"

	"github.com/mreviewer/mreviewer/internal/db"
)

// Queries implements db.Store for SQLite.
type Queries struct {
	db db.DBTX
}

// New creates a new SQLite Queries instance.
func New(conn db.DBTX) *Queries {
	return &Queries{db: conn}
}

// WithTx returns a copy of Queries bound to the given transaction.
func (q *Queries) WithTx(tx *sql.Tx) *Queries {
	return &Queries{db: tx}
}

// rowsAffected is a helper that returns true when at least one row was changed.
func rowsAffected(result sql.Result) (bool, error) {
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Verify that *Queries satisfies db.Store at compile time.
var _ db.Store = (*Queries)(nil)

// --- delegated scan helpers live in each domain file ---

// scanReviewRun scans a single ReviewRun from a *sql.Row.
func scanReviewRun(row *sql.Row) (db.ReviewRun, error) {
	var i db.ReviewRun
	err := row.Scan(
		&i.ID, &i.ProjectID, &i.MergeRequestID, &i.HookEventID,
		&i.TriggerType, &i.HeadSha, &i.Status, &i.ErrorCode, &i.ErrorDetail,
		&i.SupersededByRunID, &i.RetryCount, &i.MaxRetries, &i.NextRetryAt, &i.ClaimedBy, &i.ClaimedAt,
		&i.StartedAt, &i.CompletedAt, &i.ProviderLatencyMs, &i.ProviderTokensTotal,
		&i.IdempotencyKey, &i.CreatedAt, &i.UpdatedAt, &i.ScopeJson,
	)
	return i, err
}

// scanReviewRunRows scans multiple ReviewRun rows.
func scanReviewRunRows(rows *sql.Rows) ([]db.ReviewRun, error) {
	items := []db.ReviewRun{}
	for rows.Next() {
		var i db.ReviewRun
		if err := rows.Scan(
			&i.ID, &i.ProjectID, &i.MergeRequestID, &i.HookEventID,
			&i.TriggerType, &i.HeadSha, &i.Status, &i.ErrorCode, &i.ErrorDetail,
			&i.SupersededByRunID, &i.RetryCount, &i.MaxRetries, &i.NextRetryAt, &i.ClaimedBy, &i.ClaimedAt,
			&i.StartedAt, &i.CompletedAt, &i.ProviderLatencyMs, &i.ProviderTokensTotal,
			&i.IdempotencyKey, &i.CreatedAt, &i.UpdatedAt, &i.ScopeJson,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// scanReviewFinding scans a single ReviewFinding from a *sql.Row.
func scanReviewFinding(row *sql.Row) (db.ReviewFinding, error) {
	var i db.ReviewFinding
	err := row.Scan(
		&i.ID, &i.ReviewRunID, &i.MergeRequestID, &i.Category, &i.Severity,
		&i.Confidence, &i.Title, &i.BodyMarkdown, &i.Path, &i.AnchorKind,
		&i.OldLine, &i.NewLine, &i.RangeStartKind, &i.RangeStartOldLine,
		&i.RangeStartNewLine, &i.RangeEndKind, &i.RangeEndOldLine,
		&i.RangeEndNewLine, &i.AnchorSnippet, &i.Evidence, &i.SuggestedPatch,
		&i.CanonicalKey, &i.AnchorFingerprint, &i.SemanticFingerprint,
		&i.State, &i.MatchedFindingID, &i.LastSeenRunID,
		&i.GitlabDiscussionID, &i.ErrorCode, &i.CreatedAt, &i.UpdatedAt,
	)
	return i, err
}

// scanReviewFindingRows scans multiple ReviewFinding rows.
func scanReviewFindingRows(rows *sql.Rows) ([]db.ReviewFinding, error) {
	items := []db.ReviewFinding{}
	for rows.Next() {
		var i db.ReviewFinding
		if err := rows.Scan(
			&i.ID, &i.ReviewRunID, &i.MergeRequestID, &i.Category, &i.Severity,
			&i.Confidence, &i.Title, &i.BodyMarkdown, &i.Path, &i.AnchorKind,
			&i.OldLine, &i.NewLine, &i.RangeStartKind, &i.RangeStartOldLine,
			&i.RangeStartNewLine, &i.RangeEndKind, &i.RangeEndOldLine,
			&i.RangeEndNewLine, &i.AnchorSnippet, &i.Evidence, &i.SuggestedPatch,
			&i.CanonicalKey, &i.AnchorFingerprint, &i.SemanticFingerprint,
			&i.State, &i.MatchedFindingID, &i.LastSeenRunID,
			&i.GitlabDiscussionID, &i.ErrorCode, &i.CreatedAt, &i.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
