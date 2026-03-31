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
