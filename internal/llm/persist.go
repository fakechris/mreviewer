package llm

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mreviewer/mreviewer/internal/db"
)

func PersistReviewResult(ctx context.Context, store ProcessorStore, run db.ReviewRun, mr db.MergeRequest, result ReviewResult, reviewedPaths, deletedPaths map[string]struct{}, matcher SemanticMatcher) ([]db.ReviewFinding, string, error) {
	if err := persistFindingsWithMatcher(ctx, store, run, mr, result, reviewedPaths, deletedPaths, matcher); err != nil {
		return nil, "", fmt.Errorf("persist findings: %w", err)
	}
	if result.Status == parserErrorCode {
		if err := persistSummaryNoteFallback(ctx, store, run, result); err != nil {
			return nil, "", fmt.Errorf("persist parser-error summary note fallback: %w", err)
		}
	}
	findingsForOutcome, err := store.ListActiveFindingsByMR(ctx, mr.ID)
	if err != nil {
		return nil, "", fmt.Errorf("load active findings for outcome: %w", err)
	}
	finalStatus := canonicalRunStatus(result.Status, len(findingsForOutcome))
	if err := store.UpdateReviewRunStatus(ctx, db.UpdateReviewRunStatusParams{
		Status:      finalStatus,
		ErrorCode:   "",
		ErrorDetail: sql.NullString{},
		ID:          run.ID,
	}); err != nil {
		return nil, "", fmt.Errorf("update run status: %w", err)
	}
	return findingsForOutcome, finalStatus, nil
}
