package compare

import (
	"testing"

	githubplatform "github.com/mreviewer/mreviewer/internal/platform/github"
)

func TestIngestGitHubCommentsPreservesReviewerIdentityAndAnchors(t *testing.T) {
	artifacts := IngestGitHubReviewerArtifacts(
		[]githubplatform.IssueComment{
			{
				ID:      101,
				Body:    "Security summary from CodeRabbit",
				HTMLURL: "https://github.com/acme/service/pull/24#issuecomment-101",
				User:    githubplatform.CommentUser{Login: "coderabbit"},
			},
		},
		[]githubplatform.ReviewComment{
			{
				ID:      202,
				Body:    "Potential auth bypass on this code path",
				HTMLURL: "https://github.com/acme/service/pull/24#discussion_r202",
				Path:    "auth/check.go",
				Line:    41,
				Side:    "RIGHT",
				User:    githubplatform.CommentUser{Login: "coderabbit"},
			},
			{
				ID:      203,
				Body:    "Anonymous second opinion",
				HTMLURL: "https://github.com/acme/service/pull/24#discussion_r203",
				Path:    "auth/check.go",
				Line:    55,
				Side:    "RIGHT",
			},
		},
	)

	if len(artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(artifacts))
	}
	if artifacts[0].ReviewerID != "github:coderabbit" {
		t.Fatalf("first reviewer id = %q, want github:coderabbit", artifacts[0].ReviewerID)
	}
	if len(artifacts[0].Findings) != 2 {
		t.Fatalf("coderabbit findings = %d, want 2", len(artifacts[0].Findings))
	}
	if artifacts[0].Findings[1].Location == nil || artifacts[0].Findings[1].Location.Path != "auth/check.go" {
		t.Fatalf("expected anchored finding, got %#v", artifacts[0].Findings[1].Location)
	}
	if artifacts[1].ReviewerID == "anonymous" || artifacts[1].ReviewerID == "" || artifacts[1].ReviewerID == "github:anonymous" {
		t.Fatalf("expected unique anonymous reviewer identity, got %q", artifacts[1].ReviewerID)
	}
}
