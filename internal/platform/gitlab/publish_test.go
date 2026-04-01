package gitlab

import (
	"context"
	"testing"

	"github.com/mreviewer/mreviewer/internal/reviewcomment"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type fakeGitLabDiscussionClient struct {
	notes       []reviewcomment.CreateNoteRequest
	discussions []reviewcomment.CreateDiscussionRequest
}

func (f *fakeGitLabDiscussionClient) CreateNote(_ context.Context, req reviewcomment.CreateNoteRequest) (reviewcomment.Discussion, error) {
	f.notes = append(f.notes, req)
	return reviewcomment.Discussion{ID: "note-1"}, nil
}

func (f *fakeGitLabDiscussionClient) CreateDiscussion(_ context.Context, req reviewcomment.CreateDiscussionRequest) (reviewcomment.Discussion, error) {
	f.discussions = append(f.discussions, req)
	return reviewcomment.Discussion{ID: "discussion-1"}, nil
}

func TestPublisherPublishesSummaryAndFindings(t *testing.T) {
	client := &fakeGitLabDiscussionClient{}
	publisher := NewPublisher(client)

	err := publisher.Publish(context.Background(), PublishRequest{
		ProjectID:       101,
		MergeRequestIID: 17,
		Mode:            PublishModeFullReviewComments,
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

	if len(client.notes) != 1 {
		t.Fatalf("expected 1 summary note, got %d", len(client.notes))
	}
	if len(client.discussions) != 1 {
		t.Fatalf("expected 1 inline discussion, got %d", len(client.discussions))
	}
	if client.discussions[0].Position.NewPath != "repo/query.go" {
		t.Fatalf("unexpected position: %#v", client.discussions[0].Position)
	}
}

func TestPublisherSummaryOnlySkipsFindingComments(t *testing.T) {
	client := &fakeGitLabDiscussionClient{}
	publisher := NewPublisher(client)

	err := publisher.Publish(context.Background(), PublishRequest{
		ProjectID:       101,
		MergeRequestIID: 17,
		Mode:            PublishModeSummaryOnly,
		Bundle: reviewcore.ReviewBundle{
			PublishCandidates: []reviewcore.PublishCandidate{
				{Type: "summary", Title: "AI review summary", Body: "Merged reviewer judgment"},
				{Type: "finding", Title: "Should be skipped", Body: "Body"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(client.notes) != 1 || len(client.discussions) != 0 {
		t.Fatalf("expected summary-only publish, got notes=%d discussions=%d", len(client.notes), len(client.discussions))
	}
}
