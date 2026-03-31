package reviewcore

import (
	"encoding/json"
	"testing"

	"github.com/mreviewer/mreviewer/internal/llm"
)

func TestArtifactFromLegacyResultPreservesReviewerFindingSemantics(t *testing.T) {
	target := ReviewTarget{
		Platform:     PlatformGitLab,
		URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
		Repository:   "group/repo",
		ProjectID:    77,
		ChangeNumber: 23,
	}
	newLine := int32(44)
	result := llm.ReviewResult{
		SchemaVersion: "1.0",
		ReviewRunID:   "run-23",
		Summary:       "Security reviewer found a dangerous query path.",
		Findings: []llm.ReviewFinding{{
			Category:     "security.sql-injection",
			Severity:     "high",
			Confidence:   0.91,
			Title:        "Raw SQL uses untrusted input",
			BodyMarkdown: "The new query concatenates `user_id` directly into SQL.",
			Path:         "internal/db/query.go",
			AnchorKind:   "new_line",
			NewLine:      &newLine,
			Evidence: []string{
				"`user_id` flows into `fmt.Sprintf`",
			},
			CanonicalKey: "security.sql-injection|query.go|44",
		}},
	}

	artifact := ArtifactFromLegacyResult(target, "security", result)
	if artifact.ReviewerID != "security" {
		t.Fatalf("reviewer_id = %q, want security", artifact.ReviewerID)
	}
	if artifact.Summary != result.Summary {
		t.Fatalf("summary = %q, want %q", artifact.Summary, result.Summary)
	}
	if len(artifact.Findings) != 1 {
		t.Fatalf("findings len = %d, want 1", len(artifact.Findings))
	}
	finding := artifact.Findings[0]
	if finding.Title != "Raw SQL uses untrusted input" {
		t.Fatalf("title = %q", finding.Title)
	}
	if finding.Body != "The new query concatenates `user_id` directly into SQL." {
		t.Fatalf("body = %q", finding.Body)
	}
	if finding.Confidence != 0.91 {
		t.Fatalf("confidence = %v, want 0.91", finding.Confidence)
	}
	if finding.Identity.Category != "security.sql-injection" {
		t.Fatalf("identity category = %q", finding.Identity.Category)
	}
	if finding.Identity.EvidenceFingerprint != "security.sql-injection|query.go|44" {
		t.Fatalf("evidence fingerprint = %q", finding.Identity.EvidenceFingerprint)
	}
	if finding.Identity.Location.Path != "internal/db/query.go" {
		t.Fatalf("location path = %q", finding.Identity.Location.Path)
	}
	if finding.Identity.Location.StartLine != 44 || finding.Identity.Location.EndLine != 44 {
		t.Fatalf("line range = %d-%d", finding.Identity.Location.StartLine, finding.Identity.Location.EndLine)
	}
}

func TestPublishCandidatesFromLegacyResultCreatesSummaryAndFindingComments(t *testing.T) {
	newLine := int32(17)
	result := llm.ReviewResult{
		SchemaVersion: "1.0",
		ReviewRunID:   "run-55",
		Summary:       "Two issues found.",
		SummaryNote: &llm.SummaryNote{
			BodyMarkdown: "## Review Summary\n\nTwo issues found.",
		},
		Findings: []llm.ReviewFinding{{
			Category:     "architecture.error-handling",
			Severity:     "medium",
			Title:        "Dropped storage error",
			BodyMarkdown: "The returned error is ignored and the request still reports success.",
			Path:         "internal/service/handler.go",
			AnchorKind:   "new_line",
			NewLine:      &newLine,
		}},
	}

	candidates := PublishCandidatesFromLegacyResult(result)
	if len(candidates) != 2 {
		t.Fatalf("publish candidates len = %d, want 2", len(candidates))
	}
	if candidates[0].Kind != "summary" {
		t.Fatalf("first candidate kind = %q, want summary", candidates[0].Kind)
	}
	if candidates[0].Body != "## Review Summary\n\nTwo issues found." {
		t.Fatalf("summary body = %q", candidates[0].Body)
	}
	if candidates[1].Kind != "finding" {
		t.Fatalf("second candidate kind = %q, want finding", candidates[1].Kind)
	}
	if candidates[1].Title != "Dropped storage error" {
		t.Fatalf("finding title = %q", candidates[1].Title)
	}
	if candidates[1].Body != "The returned error is ignored and the request still reports success." {
		t.Fatalf("finding body = %q", candidates[1].Body)
	}
	if candidates[1].Location.Path != "internal/service/handler.go" {
		t.Fatalf("finding path = %q", candidates[1].Location.Path)
	}
	if candidates[1].Location.StartLine != 17 || candidates[1].Location.EndLine != 17 {
		t.Fatalf("finding lines = %d-%d", candidates[1].Location.StartLine, candidates[1].Location.EndLine)
	}
}

func TestPublishCandidatesFromLegacyResultCarriesGitLabAnchorMetadata(t *testing.T) {
	startNew := int32(10)
	endNew := int32(12)
	result := llm.ReviewResult{
		SchemaVersion: "1.0",
		ReviewRunID:   "run-56",
		Summary:       "Range issue found.",
		Findings: []llm.ReviewFinding{{
			Category:          "architecture.control-flow",
			Severity:          "high",
			Title:             "Inconsistent branch handling",
			BodyMarkdown:      "The new branch skips validation across the selected range.",
			Path:              "pkg/old_name.go -> pkg/new_name.go",
			AnchorKind:        "range",
			RangeStartKind:    "new",
			RangeStartNewLine: &startNew,
			RangeEndKind:      "new",
			RangeEndNewLine:   &endNew,
		}},
	}

	candidates := PublishCandidatesFromLegacyResult(result)
	if len(candidates) != 2 {
		t.Fatalf("publish candidates len = %d, want 2", len(candidates))
	}
	var metadata struct {
		OldPath   string `json:"old_path"`
		NewPath   string `json:"new_path"`
		LineRange *struct {
			Start struct {
				LineType string `json:"type"`
				NewLine  *int32 `json:"new_line,omitempty"`
			} `json:"start"`
			End struct {
				LineType string `json:"type"`
				NewLine  *int32 `json:"new_line,omitempty"`
			} `json:"end"`
		} `json:"line_range,omitempty"`
	}
	if err := json.Unmarshal(candidates[1].Location.PlatformMetadata, &metadata); err != nil {
		t.Fatalf("Unmarshal metadata: %v", err)
	}
	if metadata.OldPath != "pkg/old_name.go" || metadata.NewPath != "pkg/new_name.go" {
		t.Fatalf("metadata paths = old:%q new:%q", metadata.OldPath, metadata.NewPath)
	}
	if metadata.LineRange == nil {
		t.Fatal("metadata line_range = nil, want populated")
	}
	if metadata.LineRange.Start.LineType != "new" || metadata.LineRange.Start.NewLine == nil || *metadata.LineRange.Start.NewLine != 10 {
		t.Fatalf("line_range start = %+v", metadata.LineRange.Start)
	}
	if metadata.LineRange.End.LineType != "new" || metadata.LineRange.End.NewLine == nil || *metadata.LineRange.End.NewLine != 12 {
		t.Fatalf("line_range end = %+v", metadata.LineRange.End)
	}
}
