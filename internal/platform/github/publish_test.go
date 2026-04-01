package github

import (
	"context"
	"testing"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type fakePublishClient struct {
	snapshot           PullRequestSnapshot
	snapshotRepository string
	snapshotPullNumber int64
	issueComments      []CreateIssueCommentRequest
	reviewComments     []CreateReviewCommentRequest
}

func (f *fakePublishClient) GetPullRequestSnapshotByRepositoryRef(_ context.Context, repositoryRef string, pullNumber int64) (PullRequestSnapshot, error) {
	f.snapshotRepository = repositoryRef
	f.snapshotPullNumber = pullNumber
	return f.snapshot, nil
}

func (f *fakePublishClient) CreateIssueComment(_ context.Context, req CreateIssueCommentRequest) error {
	f.issueComments = append(f.issueComments, req)
	return nil
}

func (f *fakePublishClient) CreateReviewComment(_ context.Context, req CreateReviewCommentRequest) error {
	f.reviewComments = append(f.reviewComments, req)
	return nil
}

func TestPublisherPublishesSummaryAndFindingComments(t *testing.T) {
	client := &fakePublishClient{
		snapshot: PullRequestSnapshot{
			PullRequest: PullRequest{
				HeadSHA: "head-sha",
			},
		},
	}
	bundle := core.ReviewBundle{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitHub,
			URL:          "https://github.com/acme/repo/pull/17",
			Repository:   "acme/repo",
			ChangeNumber: 17,
		},
		PublishCandidates: []core.PublishCandidate{
			{Kind: "summary", Body: "Summary"},
			{
				Kind:     "finding",
				Title:    "Unsafe SQL",
				Body:     "Use parameterized query",
				Severity: "high",
				Location: core.CanonicalLocation{
					Path:      "internal/db/query.go",
					Side:      core.DiffSideNew,
					StartLine: 44,
					EndLine:   44,
				},
			},
		},
	}

	if err := NewPublisher(client).Publish(context.Background(), bundle); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if client.snapshotRepository != "acme/repo" || client.snapshotPullNumber != 17 {
		t.Fatalf("snapshot lookup = (%q,%d)", client.snapshotRepository, client.snapshotPullNumber)
	}
	if len(client.issueComments) != 1 {
		t.Fatalf("issue comments = %d, want 1", len(client.issueComments))
	}
	if len(client.reviewComments) != 1 {
		t.Fatalf("review comments = %d, want 1", len(client.reviewComments))
	}
	comment := client.reviewComments[0]
	if comment.CommitID != "head-sha" {
		t.Fatalf("commit_id = %q, want head-sha", comment.CommitID)
	}
	if comment.Path != "internal/db/query.go" {
		t.Fatalf("path = %q", comment.Path)
	}
	if comment.Line != 44 || comment.Side != "RIGHT" {
		t.Fatalf("line/side = %d/%q", comment.Line, comment.Side)
	}
}

func TestPublisherFallsBackToTitleForTitleOnlyFindingComment(t *testing.T) {
	client := &fakePublishClient{
		snapshot: PullRequestSnapshot{
			PullRequest: PullRequest{HeadSHA: "head-sha"},
		},
	}
	bundle := core.ReviewBundle{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitHub,
			URL:          "https://github.com/acme/repo/pull/17",
			Repository:   "acme/repo",
			ChangeNumber: 17,
		},
		PublishCandidates: []core.PublishCandidate{{
			Kind:  "finding",
			Title: "Unsafe SQL",
			Location: core.CanonicalLocation{
				Path:      "internal/db/query.go",
				Side:      core.DiffSideNew,
				StartLine: 44,
				EndLine:   44,
			},
		}},
	}

	if err := NewPublisher(client).Publish(context.Background(), bundle); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(client.reviewComments) != 1 {
		t.Fatalf("review comments = %d, want 1", len(client.reviewComments))
	}
	if client.reviewComments[0].Body != "Unsafe SQL" {
		t.Fatalf("review comment body = %q, want title fallback", client.reviewComments[0].Body)
	}
}

func TestPublisherPublishesUnanchoredFindingAsIssueComment(t *testing.T) {
	client := &fakePublishClient{
		snapshot: PullRequestSnapshot{
			PullRequest: PullRequest{HeadSHA: "head-sha"},
		},
	}
	bundle := core.ReviewBundle{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitHub,
			URL:          "https://github.com/acme/repo/pull/17",
			Repository:   "acme/repo",
			ChangeNumber: 17,
		},
		PublishCandidates: []core.PublishCandidate{{
			Kind:             "finding",
			Title:            "PR title does not match the actual change",
			Body:             "### PR title does not match the actual change\n\nThe diff only updates billing.",
			PublishAsSummary: true,
		}},
	}

	if err := NewPublisher(client).Publish(context.Background(), bundle); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(client.issueComments) != 1 {
		t.Fatalf("issue comments = %d, want 1", len(client.issueComments))
	}
	if len(client.reviewComments) != 0 {
		t.Fatalf("review comments = %d, want 0", len(client.reviewComments))
	}
	if client.issueComments[0].Body == "" {
		t.Fatal("issue comment body = empty")
	}
}
