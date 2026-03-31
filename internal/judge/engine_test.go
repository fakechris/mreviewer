package judge

import (
	"testing"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

func TestJudgeDeduplicatesArtifactsByCanonicalIdentity(t *testing.T) {
	artifacts := []core.ReviewerArtifact{
		{
			ReviewerID: "security",
			Findings: []core.Finding{{
				Category: "security.sql-injection",
				Severity: "high",
				Claim:    "raw sql uses untrusted input",
				Identity: core.FindingIdentityInput{
					Category:            "security.sql-injection",
					NormalizedClaim:     "raw sql uses untrusted input",
					EvidenceFingerprint: "sql/raw:user_id",
					Location: core.CanonicalLocation{
						Path:      "internal/db/query.go",
						Side:      core.DiffSideNew,
						StartLine: 44,
						EndLine:   44,
					},
				},
			}},
		},
		{
			ReviewerID: "architecture",
			Findings: []core.Finding{{
				Category: "security.sql-injection",
				Severity: "high",
				Claim:    "raw sql uses untrusted input",
				Identity: core.FindingIdentityInput{
					Category:            "security.sql-injection",
					NormalizedClaim:     "raw sql uses untrusted input",
					EvidenceFingerprint: "sql/raw:user_id",
					Location: core.CanonicalLocation{
						Path:      "internal/db/query.go",
						Side:      core.DiffSideNew,
						StartLine: 44,
						EndLine:   44,
					},
				},
			}},
		},
	}

	result := New().Decide(artifacts)
	if len(result.MergedFindings) != 1 {
		t.Fatalf("merged findings len = %d, want 1", len(result.MergedFindings))
	}
	if result.Verdict == "" {
		t.Fatalf("verdict should not be empty")
	}
	if len(result.MergedFindings[0].SourceReviewers) != 2 {
		t.Fatalf("source reviewers = %v", result.MergedFindings[0].SourceReviewers)
	}
}

func TestJudgePreservesHighestSeverityAcrossDuplicateFindings(t *testing.T) {
	decision := New().Decide([]core.ReviewerArtifact{
		{
			ReviewerID: "architecture",
			Findings: []core.Finding{{
				Category: "architecture.error-handling",
				Severity: "medium",
				Identity: core.FindingIdentityInput{
					Category:            "architecture.error-handling",
					NormalizedClaim:     "storage error is dropped",
					EvidenceFingerprint: "storage-error-dropped",
					Location: core.CanonicalLocation{
						Path:      "internal/service/handler.go",
						Side:      core.DiffSideNew,
						StartLine: 19,
						EndLine:   19,
					},
				},
			}},
		},
		{
			ReviewerID: "security",
			Findings: []core.Finding{{
				Category: "architecture.error-handling",
				Severity: "critical",
				Identity: core.FindingIdentityInput{
					Category:            "architecture.error-handling",
					NormalizedClaim:     "storage error is dropped",
					EvidenceFingerprint: "storage-error-dropped",
					Location: core.CanonicalLocation{
						Path:      "internal/service/handler.go",
						Side:      core.DiffSideNew,
						StartLine: 19,
						EndLine:   19,
					},
				},
			}},
		},
	})

	if decision.Verdict != "requested_changes" {
		t.Fatalf("verdict = %q, want requested_changes", decision.Verdict)
	}
	if len(decision.MergedFindings) != 1 {
		t.Fatalf("merged findings len = %d, want 1", len(decision.MergedFindings))
	}
	if decision.MergedFindings[0].Finding.Severity != "critical" {
		t.Fatalf("merged severity = %q, want critical", decision.MergedFindings[0].Finding.Severity)
	}
}
