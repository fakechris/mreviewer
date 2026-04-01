package compare

import (
	"testing"

	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

func TestBuildReportFindsSharedAndUniqueFindings(t *testing.T) {
	report := BuildReport([]reviewcore.ComparisonArtifact{
		{
			ReviewerID:   "security",
			ReviewerType: "pack",
			Findings: []reviewcore.Finding{
				testFinding("auth-bypass", "security", "auth bypass"),
				testFinding("nil-deref", "correctness", "nil dereference"),
			},
		},
		{
			ReviewerID:   "architecture",
			ReviewerType: "pack",
			Findings: []reviewcore.Finding{
				testFinding("auth-bypass", "security", "auth bypass"),
				testFinding("n-plus-one", "database", "n+1 query"),
			},
		},
	})

	if len(report.SharedFindings) != 1 {
		t.Fatalf("shared findings = %d, want 1", len(report.SharedFindings))
	}
	if got := report.SharedFindings[0].Identity.NormalizedClaim; got != "auth bypass" {
		t.Fatalf("shared finding claim = %q, want auth bypass", got)
	}

	if len(report.UniqueFindingsByReviewer["council:security"]) != 1 {
		t.Fatalf("security unique findings = %d, want 1", len(report.UniqueFindingsByReviewer["council:security"]))
	}
	if len(report.UniqueFindingsByReviewer["council:architecture"]) != 1 {
		t.Fatalf("architecture unique findings = %d, want 1", len(report.UniqueFindingsByReviewer["council:architecture"]))
	}
}

func TestBuildComparisonArtifactsNormalizesCouncilAndAdvisorReviewerIDs(t *testing.T) {
	artifacts := BuildComparisonArtifacts([]reviewcore.ReviewerArtifact{
		{ReviewerID: "security", ReviewerType: "pack"},
		{ReviewerID: "advisor", ReviewerType: "advisor"},
	})
	if artifacts[0].ReviewerID != "council:security" {
		t.Fatalf("first reviewer id = %q, want council:security", artifacts[0].ReviewerID)
	}
	if artifacts[1].ReviewerID != "advisor:advisor" {
		t.Fatalf("second reviewer id = %q, want advisor:advisor", artifacts[1].ReviewerID)
	}
}

func TestBuildComparisonArtifactsForBundleIncludesAdvisorArtifact(t *testing.T) {
	artifacts := BuildComparisonArtifactsForBundle(reviewcore.ReviewBundle{
		Artifacts: []reviewcore.ReviewerArtifact{
			{ReviewerID: "security", ReviewerType: "pack"},
		},
		AdvisorArtifact: &reviewcore.ReviewerArtifact{
			ReviewerID:   "advisor",
			ReviewerType: "advisor",
		},
	})

	if len(artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(artifacts))
	}
	if artifacts[1].ReviewerID != "advisor:advisor" {
		t.Fatalf("advisor reviewer id = %q, want advisor:advisor", artifacts[1].ReviewerID)
	}
}

func testFinding(locationKey, category, claim string) reviewcore.Finding {
	return reviewcore.Finding{
		Category: category,
		Claim:    claim,
		Identity: &reviewcore.FindingIdentityInput{
			Category:        category,
			NormalizedClaim: claim,
			LocationKey:     locationKey,
			EvidenceKey:     claim,
		},
	}
}
