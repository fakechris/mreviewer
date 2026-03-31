package github

import (
	"context"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

func TestRuntimeWritebackPublishesBundle(t *testing.T) {
	client := &fakePublishClient{
		snapshot: PullRequestSnapshot{
			PullRequest: PullRequest{HeadSHA: "head-sha"},
		},
	}
	writeback := NewRuntimeWriteback(client)

	err := writeback.WriteBundle(context.Background(), db.ReviewRun{ID: 7, Status: "requested_changes"}, core.ReviewBundle{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitHub,
			Repository:   "acme/repo",
			ChangeNumber: 18,
		},
		PublishCandidates: []core.PublishCandidate{
			{Kind: "summary", Body: "judge summary"},
			{
				Kind:     "finding",
				Title:    "Unsafe mutation path",
				Body:     "The update path skips validation.",
				Location: core.CanonicalLocation{Path: "internal/legacy.go", StartLine: 12, EndLine: 12, Side: core.DiffSideNew},
			},
		},
	})
	if err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	if len(client.issueComments) != 1 {
		t.Fatalf("issue comments = %d, want 1", len(client.issueComments))
	}
	if len(client.reviewComments) != 1 {
		t.Fatalf("review comments = %d, want 1", len(client.reviewComments))
	}
}
