package llm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/db"
)

func persistFindings(ctx context.Context, queries *db.Queries, run db.ReviewRun, mr db.MergeRequest, result ReviewResult, reviewedPaths, deletedPaths map[string]struct{}) error {
	existing, err := queries.ListActiveFindingsByMR(ctx, mr.ID)
	if err != nil {
		return err
	}
	policy, err := queries.GetProjectPolicy(ctx, run.ProjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	thresholds := thresholdsFromPolicy(policy, err == nil)

	seenInRun := make(map[string]struct{}, len(result.Findings))
	persisted := make([]persistedFinding, 0, len(result.Findings))
	for _, finding := range result.Findings {
		normalized := normalizeFinding(finding)
		anchorFingerprint := computeAnchorFingerprint(normalized)
		if _, ok := seenInRun[anchorFingerprint]; ok {
			continue
		}
		seenInRun[anchorFingerprint] = struct{}{}
		persisted = append(persisted, persistedFinding{
			normalized:          normalized,
			anchorFingerprint:   anchorFingerprint,
			semanticFingerprint: computeSemanticFingerprint(normalized),
			state:               evaluateFindingState(normalized, thresholds),
		})
	}
	matchedExistingIDs := make(map[int64]struct{})
	updatedLastSeenIDs := make(map[int64]struct{})

	for _, finding := range persisted {
		if finding.state == findingStateFiltered {
			if _, err := insertFinding(ctx, queries, run, mr, finding); err != nil {
				return err
			}
			continue
		}
		if finding.state == findingStateDeleted {
			continue
		}

		matched, err := matchExistingFinding(ctx, queries, run, existing, finding)
		if err != nil {
			return err
		}
		if matched.existingID != 0 {
			matchedExistingIDs[matched.existingID] = struct{}{}
			updatedLastSeenIDs[matched.existingID] = struct{}{}
		}
		if matched.skipInsert {
			continue
		}

		insertedID, err := insertFinding(ctx, queries, run, mr, finding)
		if err != nil {
			return err
		}

		if matched.supersedeID != 0 {
			if err := queries.UpdateFindingState(ctx, db.UpdateFindingStateParams{
				State:            findingStateSuperseded,
				MatchedFindingID: sql.NullInt64{Int64: insertedID, Valid: true},
				ID:               matched.supersedeID,
			}); err != nil {
				return err
			}
		}
	}

	for _, current := range existing {
		if _, ok := matchedExistingIDs[current.ID]; ok {
			continue
		}
		_, seenThisRun := updatedLastSeenIDs[current.ID]
		nextState, ok, err := transitionMissingFinding(current, reviewedPaths, deletedPaths, seenThisRun)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := queries.UpdateFindingState(ctx, db.UpdateFindingStateParams{State: nextState, ID: current.ID}); err != nil {
			return err
		}
	}

	return nil
}

func reviewedScopeFromAssembly(assembled ctxpkg.AssemblyResult) (map[string]struct{}, map[string]struct{}) {
	reviewedPaths := make(map[string]struct{}, len(assembled.Request.Changes))
	deletedPaths := make(map[string]struct{})
	for _, change := range assembled.Request.Changes {
		path := normalizePath(change.Path)
		if path == "" {
			continue
		}
		reviewedPaths[path] = struct{}{}
		if change.Status == "deleted" {
			deletedPaths[path] = struct{}{}
		}
	}
	return reviewedPaths, deletedPaths
}

type findingMatchDecision struct {
	skipInsert  bool
	supersedeID int64
	existingID  int64
}

type persistedFinding struct {
	normalized          normalizedFinding
	anchorFingerprint   string
	semanticFingerprint string
	state               string
}

const (
	findingStateNew        = "new"
	findingStatePosted     = "posted"
	findingStateActive     = "active"
	findingStateFixed      = "fixed"
	findingStateSuperseded = "superseded"
	findingStateStale      = "stale"
	findingStateIgnored    = "ignored"
	findingStateFiltered   = "filtered"
	findingStateDeleted    = "__deleted__"
)

var validFindingTransitions = map[string]map[string]struct{}{
	findingStateNew: {
		findingStatePosted:     {},
		findingStateFixed:      {},
		findingStateSuperseded: {},
		findingStateStale:      {},
		findingStateIgnored:    {},
	},
	findingStatePosted: {
		findingStateActive:     {},
		findingStateFixed:      {},
		findingStateSuperseded: {},
		findingStateStale:      {},
		findingStateIgnored:    {},
	},
	findingStateActive: {
		findingStateFixed:      {},
		findingStateSuperseded: {},
		findingStateStale:      {},
		findingStateIgnored:    {},
	},
}

type findingThresholds struct {
	confidence float64
	severity   string
}

func thresholdsFromPolicy(policy db.ProjectPolicy, ok bool) findingThresholds {
	thresholds := findingThresholds{confidence: 0, severity: "low"}
	if !ok {
		return thresholds
	}
	if policy.ConfidenceThreshold > 0 {
		thresholds.confidence = policy.ConfidenceThreshold
	}
	if level := normalizeSeverity(policy.SeverityThreshold); level != "" {
		thresholds.severity = level
	}
	return thresholds
}

func evaluateFindingState(finding normalizedFinding, thresholds findingThresholds) string {
	if finding.isDeletedFile() {
		return findingStateDeleted
	}
	if finding.Confidence < thresholds.confidence {
		return findingStateFiltered
	}
	if severityRank(finding.Severity) < severityRank(thresholds.severity) {
		return findingStateFiltered
	}
	return findingStateNew
}

func transitionMissingFinding(current db.ReviewFinding, reviewedPaths, deletedPaths map[string]struct{}, seenThisRun bool) (string, bool, error) {
	path := normalizePath(current.Path)
	if _, ok := deletedPaths[path]; ok {
		canonicalAnchorKind := normalizeAnchorKind(current.AnchorKind)
		if canonicalAnchorKind == "old_line" {
			return nextFindingState(current.State, findingStateFixed)
		}
		if canonicalAnchorKind == "deleted" {
			return nextFindingState(current.State, findingStateFixed)
		}
	}
	if current.LastSeenRunID.Valid && !seenThisRun {
		if _, ok := reviewedPaths[path]; ok {
			return nextFindingState(current.State, findingStateFixed)
		}
		if len(reviewedPaths) == 0 && len(deletedPaths) == 0 {
			return "", false, nil
		}
		return nextFindingState(current.State, findingStateStale)
	}
	if current.LastSeenRunID.Valid {
		return "", false, nil
	}
	if _, ok := reviewedPaths[path]; ok {
		return nextFindingState(current.State, findingStateFixed)
	}
	if len(reviewedPaths) == 0 && len(deletedPaths) == 0 {
		return "", false, nil
	}
	return nextFindingState(current.State, findingStateStale)
}

func nextFindingState(current, next string) (string, bool, error) {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if current == "" || next == "" || current == next {
		return "", false, nil
	}
	allowed, ok := validFindingTransitions[current]
	if !ok {
		return "", false, fmt.Errorf("llm: no transitions allowed from %q", current)
	}
	if _, ok := allowed[next]; !ok {
		return "", false, fmt.Errorf("llm: invalid finding transition %q -> %q", current, next)
	}
	return next, true, nil
}

func matchExistingFinding(ctx context.Context, queries *db.Queries, run db.ReviewRun, existing []db.ReviewFinding, finding persistedFinding) (findingMatchDecision, error) {
	for _, current := range existing {
		if current.AnchorKind == "new_line" && finding.state == findingStateDeleted && normalizePath(current.Path) == finding.normalized.Path {
			continue
		}
		if finding.state == findingStateDeleted && current.AnchorKind == "old_line" && normalizePath(current.Path) == finding.normalized.Path {
			continue
		}
		if current.AnchorKind == "deleted" && finding.normalized.AnchorKind == "old_line" && normalizePath(current.Path) == finding.normalized.Path {
			continue
		}
		if current.AnchorFingerprint != finding.anchorFingerprint {
			continue
		}

		if current.ReviewRunID == run.ID || (current.LastSeenRunID.Valid && current.LastSeenRunID.Int64 == run.ID) {
			return findingMatchDecision{skipInsert: true, existingID: current.ID}, nil
		}

		if current.ReviewRunID != run.ID {
			if err := queries.UpdateFindingLastSeen(ctx, db.UpdateFindingLastSeenParams{
				LastSeenRunID: sql.NullInt64{Int64: run.ID, Valid: true},
				ID:            current.ID,
			}); err != nil {
				return findingMatchDecision{}, err
			}
			return findingMatchDecision{skipInsert: true, existingID: current.ID}, nil
		}
	}

	for _, current := range existing {
		if current.AnchorKind == "new_line" && finding.state == findingStateDeleted && normalizePath(current.Path) == finding.normalized.Path {
			continue
		}
		if finding.state == findingStateDeleted && current.AnchorKind == "old_line" && normalizePath(current.Path) == finding.normalized.Path {
			continue
		}
		if current.AnchorKind == "deleted" && finding.normalized.AnchorKind == "old_line" && normalizePath(current.Path) == finding.normalized.Path {
			continue
		}
		if current.SemanticFingerprint != finding.semanticFingerprint {
			continue
		}
		if current.ReviewRunID == run.ID || (current.LastSeenRunID.Valid && current.LastSeenRunID.Int64 == run.ID) {
			continue
		}
		if relocationMatches(current, finding.normalized) {
			if err := queries.UpdateFindingRelocation(ctx, db.UpdateFindingRelocationParams{
				Path:                finding.normalized.Path,
				AnchorKind:          finding.normalized.AnchorKind,
				OldLine:             finding.normalized.OldLine,
				NewLine:             finding.normalized.NewLine,
				RangeStartKind:      nullableString(finding.normalized.RangeStartKind),
				RangeStartOldLine:   finding.normalized.RangeStartOldLine,
				RangeStartNewLine:   finding.normalized.RangeStartNewLine,
				RangeEndKind:        nullableString(finding.normalized.RangeEndKind),
				RangeEndOldLine:     finding.normalized.RangeEndOldLine,
				RangeEndNewLine:     finding.normalized.RangeEndNewLine,
				AnchorSnippet:       nullableString(finding.normalized.AnchorSnippet),
				AnchorFingerprint:   finding.anchorFingerprint,
				SemanticFingerprint: finding.semanticFingerprint,
				LastSeenRunID:       sql.NullInt64{Int64: run.ID, Valid: true},
				ID:                  current.ID,
			}); err != nil {
				return findingMatchDecision{}, err
			}
			return findingMatchDecision{skipInsert: true, existingID: current.ID}, nil
		}
		return findingMatchDecision{supersedeID: current.ID, existingID: current.ID}, nil
	}

	return findingMatchDecision{}, nil
}

func insertFinding(ctx context.Context, queries *db.Queries, run db.ReviewRun, mr db.MergeRequest, finding persistedFinding) (int64, error) {
	var oldLine, newLine sql.NullInt32
	if finding.normalized.OldLine.Valid {
		oldLine = finding.normalized.OldLine
	}
	if finding.normalized.NewLine.Valid {
		newLine = finding.normalized.NewLine
	}
	result, err := queries.InsertReviewFinding(ctx, db.InsertReviewFindingParams{
		ReviewRunID:         run.ID,
		MergeRequestID:      mr.ID,
		Category:            finding.normalized.Category,
		Severity:            finding.normalized.Severity,
		Confidence:          finding.normalized.Confidence,
		Title:               finding.normalized.Title,
		BodyMarkdown:        nullableString(finding.normalized.BodyMarkdown),
		Path:                finding.normalized.Path,
		AnchorKind:          finding.normalized.AnchorKind,
		OldLine:             oldLine,
		NewLine:             newLine,
		RangeStartKind:      nullableString(finding.normalized.RangeStartKind),
		RangeStartOldLine:   finding.normalized.RangeStartOldLine,
		RangeStartNewLine:   finding.normalized.RangeStartNewLine,
		RangeEndKind:        nullableString(finding.normalized.RangeEndKind),
		RangeEndOldLine:     finding.normalized.RangeEndOldLine,
		RangeEndNewLine:     finding.normalized.RangeEndNewLine,
		AnchorSnippet:       nullableString(finding.normalized.AnchorSnippet),
		Evidence:            nullableString(finding.normalized.Evidence),
		SuggestedPatch:      nullableString(finding.normalized.SuggestedPatch),
		CanonicalKey:        finding.normalized.CanonicalKey,
		AnchorFingerprint:   finding.anchorFingerprint,
		SemanticFingerprint: finding.semanticFingerprint,
		State:               finding.state,
	})
	if err != nil {
		return 0, err
	}
	insertedID, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return insertedID, nil
}

func relocationMatches(current db.ReviewFinding, candidate normalizedFinding) bool {
	if normalizePath(current.Path) != candidate.Path {
		return false
	}
	if current.AnchorKind != candidate.AnchorKind {
		return false
	}
	return true
}

type normalizedFinding struct {
	Category               string
	Severity               string
	Confidence             float64
	Title                  string
	BodyMarkdown           string
	Path                   string
	AnchorKind             string
	OldLine                sql.NullInt32
	NewLine                sql.NullInt32
	RangeStartKind         string
	RangeStartOldLine      sql.NullInt32
	RangeStartNewLine      sql.NullInt32
	RangeEndKind           string
	RangeEndOldLine        sql.NullInt32
	RangeEndNewLine        sql.NullInt32
	AnchorSnippet          string
	Evidence               string
	SuggestedPatch         string
	CanonicalKey           string
	Symbol                 string
	TriggerCondition       string
	Impact                 string
	IntroducedByThisChange bool
	BlindSpots             []string
}

func (f normalizedFinding) isDeletedFile() bool {
	return normalizeAnchorKind(f.AnchorKind) == "old_line" && f.OldLine.Valid && !f.NewLine.Valid
}

func normalizeFinding(finding ReviewFinding) normalizedFinding {
	normalized := normalizedFinding{
		Category:               strings.TrimSpace(finding.Category),
		Severity:               strings.TrimSpace(finding.Severity),
		Confidence:             finding.Confidence,
		Title:                  strings.TrimSpace(finding.Title),
		BodyMarkdown:           strings.TrimSpace(finding.BodyMarkdown),
		Path:                   normalizePath(finding.Path),
		AnchorKind:             normalizeAnchorKind(finding.AnchorKind),
		AnchorSnippet:          normalizeWhitespace(finding.AnchorSnippet),
		Evidence:               normalizeEvidence(finding.Evidence),
		SuggestedPatch:         strings.TrimSpace(finding.SuggestedPatch),
		CanonicalKey:           strings.TrimSpace(finding.CanonicalKey),
		Symbol:                 strings.TrimSpace(finding.Symbol),
		TriggerCondition:       strings.TrimSpace(finding.TriggerCondition),
		Impact:                 strings.TrimSpace(finding.Impact),
		IntroducedByThisChange: finding.IntroducedByThisChange,
		BlindSpots:             finding.BlindSpots,
	}
	if finding.OldLine != nil {
		normalized.OldLine = sql.NullInt32{Int32: *finding.OldLine, Valid: true}
	}
	if finding.NewLine != nil {
		normalized.NewLine = sql.NullInt32{Int32: *finding.NewLine, Valid: true}
	}
	normalized.RangeStartKind = canonicalRangeLineType(finding.RangeStartKind)
	normalized.RangeEndKind = canonicalRangeLineType(finding.RangeEndKind)
	if finding.RangeStartOldLine != nil {
		normalized.RangeStartOldLine = sql.NullInt32{Int32: *finding.RangeStartOldLine, Valid: true}
	}
	if finding.RangeStartNewLine != nil {
		normalized.RangeStartNewLine = sql.NullInt32{Int32: *finding.RangeStartNewLine, Valid: true}
	}
	if finding.RangeEndOldLine != nil {
		normalized.RangeEndOldLine = sql.NullInt32{Int32: *finding.RangeEndOldLine, Valid: true}
	}
	if finding.RangeEndNewLine != nil {
		normalized.RangeEndNewLine = sql.NullInt32{Int32: *finding.RangeEndNewLine, Valid: true}
	}
	if normalized.AnchorKind == "" && normalized.NewLine.Valid && !normalized.OldLine.Valid {
		normalized.AnchorKind = "new"
	}
	if normalized.AnchorKind == "" && normalized.OldLine.Valid && !normalized.NewLine.Valid {
		normalized.AnchorKind = "old"
	}
	if normalized.CanonicalKey == "" {
		normalized.CanonicalKey = canonicalKeyFallback(normalized.Title, normalized.Path)
	}
	return normalized
}

func normalizeSeverity(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func severityRank(value string) int {
	switch normalizeSeverity(value) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	case "info", "nit":
		return 0
	default:
		return math.MinInt
	}
}

func computeAnchorFingerprint(finding normalizedFinding) string {
	return hashFingerprint(strings.Join([]string{
		finding.Path,
		finding.AnchorKind,
		finding.AnchorSnippet,
		finding.Category,
		finding.CanonicalKey,
	}, "\x00"))
}

func computeSemanticFingerprint(finding normalizedFinding) string {
	return hashFingerprint(strings.Join([]string{
		finding.Path,
		finding.Category,
		finding.CanonicalKey,
		finding.Symbol,
	}, "\x00"))
}

func hashFingerprint(input string) string {
	sum := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", sum[:])
}

func normalizePath(path string) string {
	trimmed := strings.TrimSpace(path)
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	for strings.Contains(trimmed, "//") {
		trimmed = strings.ReplaceAll(trimmed, "//", "/")
	}
	return strings.TrimPrefix(trimmed, "./")
}

func normalizeAnchorKind(kind string) string {
	trimmed := strings.ToLower(strings.TrimSpace(kind))
	switch trimmed {
	case "new", "new_line", "added":
		return "new_line"
	case "old", "old_line", "deleted":
		return "old_line"
	case "context", "context_line", "unchanged":
		return "context_line"
	}
	return trimmed
}

func canonicalRangeLineType(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "old", "old_line", "removed":
		return "old"
	case "new", "new_line", "added":
		return "new"
	case "context", "context_line", "unchanged":
		return "context"
	default:
		return ""
	}
}

func normalizeEvidence(evidence []string) string {
	parts := make([]string, 0, len(evidence))
	for _, item := range evidence {
		item = normalizeWhitespace(item)
		if item == "" {
			continue
		}
		parts = append(parts, item)
	}
	return strings.Join(parts, "\n")
}

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func canonicalKeyFallback(title, path string) string {
	return strings.ToLower(strings.TrimSpace(title) + "::" + normalizePath(path))
}
