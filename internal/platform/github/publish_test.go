package github

import (
	"context"
	"testing"

	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type fakeGitHubPublishClient struct {
	summaryComments []IssueCommentRequest
	reviewComments  []ReviewCommentRequest
}

func (f *fakeGitHubPublishClient) CreateIssueComment(_ context.Context, req IssueCommentRequest) error {
	f.summaryComments = append(f.summaryComments, req)
	return nil
}

func (f *fakeGitHubPublishClient) CreateReviewComment(_ context.Context, req ReviewCommentRequest) error {
	f.reviewComments = append(f.reviewComments, req)
	return nil
}

func TestPublisherPublishesSummaryAndReviewComments(t *testing.T) {
	client := &fakeGitHubPublishClient{}
	publisher := NewPublisher(client)

	err := publisher.Publish(context.Background(), PublishRequest{
		Owner:  "acme",
		Repo:   "service",
		Number: 24,
		Mode:   PublishModeFullReviewComments,
		Bundle: reviewcore.ReviewBundle{
			PublishCandidates: []reviewcore.PublishCandidate{
				{Type: "summary", Title: "AI review summary", Body: "Merged reviewer judgment"},
				{
					Type:     "finding",
					Title:    "Unsafe tenant lookup",
					Body:     "User-controlled tenant id reaches string-built SQL.",
					Severity: "high",
					Location: &reviewcore.CanonicalLocation{Path: "repo/query.go", Side: reviewcore.LocationSideNew, Line: 91},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(client.summaryComments) != 1 {
		t.Fatalf("expected 1 summary comment, got %d", len(client.summaryComments))
	}
	if len(client.reviewComments) != 1 {
		t.Fatalf("expected 1 review comment, got %d", len(client.reviewComments))
	}
	if client.reviewComments[0].Path != "repo/query.go" || client.reviewComments[0].Line != 91 {
		t.Fatalf("unexpected review comment request: %#v", client.reviewComments[0])
	}
}

func TestPublisherFallsBackToTitleWhenBodyIsEmpty(t *testing.T) {
	client := &fakeGitHubPublishClient{}
	publisher := NewPublisher(client)

	err := publisher.Publish(context.Background(), PublishRequest{
		Owner:  "acme",
		Repo:   "service",
		Number: 24,
		Mode:   PublishModeFullReviewComments,
		Bundle: reviewcore.ReviewBundle{
			PublishCandidates: []reviewcore.PublishCandidate{
				{
					Type:     "finding",
					Title:    "Only title available",
					Location: &reviewcore.CanonicalLocation{Path: "repo/query.go", Side: reviewcore.LocationSideNew, Line: 91},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(client.reviewComments) != 1 || client.reviewComments[0].Body != "Only title available" {
		t.Fatalf("expected title fallback body, got %#v", client.reviewComments)
	}
}
