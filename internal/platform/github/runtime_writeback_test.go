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
	if got := client.issueComments[0].Body; got != "judge summary" {
		t.Fatalf("issue comment body = %q, want judge summary", got)
	}
	if got := client.reviewComments[0].Body; got != "The update path skips validation." {
		t.Fatalf("review comment body = %q, want finding body", got)
	}
	if got := client.reviewComments[0].Path; got != "internal/legacy.go" {
		t.Fatalf("review comment path = %q, want internal/legacy.go", got)
	}
	if got := client.reviewComments[0].Line; got != 12 {
		t.Fatalf("review comment line = %d, want 12", got)
	}
}

func TestRuntimeWritebackRejectsLegacyFindingWrites(t *testing.T) {
	writeback := NewRuntimeWriteback(&fakePublishClient{})

	err := writeback.Write(context.Background(), db.ReviewRun{}, nil)
	if err == nil || err.Error() != "github runtime writeback: legacy findings write is unsupported; bundle writeback is required" {
		t.Fatalf("Write error = %v, want unsupported legacy write", err)
	}
}
