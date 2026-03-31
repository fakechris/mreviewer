package compare

import (
	"testing"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

func TestIngestGitHubCommentsGroupsArtifactsByReviewer(t *testing.T) {
	target := core.ReviewTarget{
		Platform:     core.PlatformGitHub,
		URL:          "https://github.com/acme/repo/pull/17",
		Repository:   "acme/repo",
		ChangeNumber: 17,
	}

	artifacts := IngestGitHubComments(target, GitHubCommentSet{
		IssueComments: []GitHubIssueComment{
			{Author: "codex-bot", Body: "Overall this looks safe."},
			{Author: "coderabbitai", Body: "Found one risky pattern."},
		},
		ReviewComments: []GitHubReviewComment{
			{Author: "codex-bot", Body: "SQL built with string concatenation.", Path: "internal/db/query.go", Line: 42},
			{Author: "coderabbitai", Body: "Handler owns too many responsibilities.", Path: "internal/api/handler.go", Line: 18},
		},
	})

	if len(artifacts) != 2 {
		t.Fatalf("artifacts len = %d, want 2", len(artifacts))
	}
	if artifacts[0].ReviewerID == artifacts[1].ReviewerID {
		t.Fatalf("reviewers were not grouped separately: %#v", artifacts)
	}
}

func TestIngestGitLabCommentsGroupsArtifactsByReviewer(t *testing.T) {
	target := core.ReviewTarget{
		Platform:     core.PlatformGitLab,
		URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
		Repository:   "group/repo",
		ChangeNumber: 23,
	}

	artifacts := IngestGitLabComments(target, GitLabCommentSet{
		Notes: []GitLabNote{
			{Author: "gemini-bot", Body: "Overall review summary."},
		},
		Discussions: []GitLabDiscussion{
			{Author: "gemini-bot", Body: "Query does not apply tenant scope.", Path: "internal/db/query.go", NewLine: 91},
			{Author: "devin-ai", Body: "Missing transaction boundary.", Path: "internal/db/query.go", NewLine: 112},
		},
	})

	if len(artifacts) != 2 {
		t.Fatalf("artifacts len = %d, want 2", len(artifacts))
	}
}
