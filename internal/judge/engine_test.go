package judge

import (
	"testing"

	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

func TestJudgeDedupesFindingsAndKeepsHighestSeverity(t *testing.T) {
	engine := NewEngine()
	target := reviewcore.ReviewTarget{
		Platform:   reviewcore.PlatformGitLab,
		Repository: "group/proj",
		Number:     17,
		URL:        "https://gitlab.example.com/group/proj/-/merge_requests/17",
	}

	bundle := engine.Decide(target, []reviewcore.ReviewerArtifact{
		{
			ReviewerID: "security",
			Verdict:    reviewcore.VerdictRequestedChanges,
			Findings: []reviewcore.Finding{
				{
					Title:    "Raw SQL tenant lookup",
					Claim:    "tenant id reaches a string-built SQL query",
					Severity: "medium",
					Identity: &reviewcore.FindingIdentityInput{
						Category:        "security.sql_injection",
						NormalizedClaim: "tenant id reaches raw sql",
						LocationKey:     "repo/query.go:new:91",
					},
				},
			},
		},
		{
			ReviewerID: "architecture",
			Verdict:    reviewcore.VerdictRequestedChanges,
			Findings: []reviewcore.Finding{
				{
					Title:    "Raw SQL tenant lookup",
					Claim:    "tenant id reaches a string-built SQL query",
					Severity: "high",
					Identity: &reviewcore.FindingIdentityInput{
						Category:        "security.sql_injection",
						NormalizedClaim: "tenant id reaches raw sql",
						LocationKey:     "repo/query.go:new:91",
					},
				},
			},
		},
	})

	if bundle.JudgeVerdict != reviewcore.VerdictRequestedChanges {
		t.Fatalf("expected requested_changes verdict, got %q", bundle.JudgeVerdict)
	}
	if len(bundle.PublishCandidates) != 2 {
		t.Fatalf("expected summary + 1 merged finding publish candidate, got %d", len(bundle.PublishCandidates))
	}
	findingCandidate := bundle.PublishCandidates[1]
	if findingCandidate.Severity != "high" {
		t.Fatalf("expected merged highest severity high, got %q", findingCandidate.Severity)
	}
	if len(findingCandidate.ReviewerIDs) != 2 {
		t.Fatalf("expected merged reviewer ids, got %#v", findingCandidate.ReviewerIDs)
	}
}

func TestJudgeReturnsCommentOnlyWhenThereAreNoFindings(t *testing.T) {
	engine := NewEngine()

	bundle := engine.Decide(reviewcore.ReviewTarget{
		Platform:   reviewcore.PlatformGitHub,
		Repository: "acme/service",
		Number:     24,
		URL:        "https://github.com/acme/service/pull/24",
	}, []reviewcore.ReviewerArtifact{
		{
			ReviewerID: "database",
			Verdict:    reviewcore.VerdictApproved,
			Summary:    "No material database risks found.",
		},
	})

	if bundle.JudgeVerdict != reviewcore.VerdictCommentOnly {
		t.Fatalf("expected comment_only verdict, got %q", bundle.JudgeVerdict)
	}
	if len(bundle.PublishCandidates) != 1 || bundle.PublishCandidates[0].Type != "summary" {
		t.Fatalf("expected summary-only publish candidates, got %#v", bundle.PublishCandidates)
	}
}
