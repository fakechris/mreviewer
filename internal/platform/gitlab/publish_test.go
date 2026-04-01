package gitlab

import (
	"context"
	"errors"
	"testing"

	"github.com/mreviewer/mreviewer/internal/reviewcomment"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type fakePublishClient struct {
	discussions []reviewcomment.CreateDiscussionRequest
	notes       []reviewcomment.CreateNoteRequest
	failBody    string
}

func (f *fakePublishClient) CreateDiscussion(_ context.Context, req reviewcomment.CreateDiscussionRequest) (reviewcomment.Discussion, error) {
	f.discussions = append(f.discussions, req)
	if f.failBody != "" && req.Body == f.failBody {
		return reviewcomment.Discussion{}, errors.New(`gitlab: POST /discussions returned status 400: {"message":"400 Bad request - Note {:line_code=>["must be a valid line code"], :position=>["is incomplete"]}"}`)
	}
	return reviewcomment.Discussion{ID: "discussion"}, nil
}

func (f *fakePublishClient) CreateNote(_ context.Context, req reviewcomment.CreateNoteRequest) (reviewcomment.Discussion, error) {
	f.notes = append(f.notes, req)
	return reviewcomment.Discussion{ID: "note"}, nil
}

func TestPublisherPublishesSummaryAndFindingRequests(t *testing.T) {
	client := &fakePublishClient{}
	publisher := NewPublisher(client)
	bundle := core.ReviewBundle{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitLab,
			URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
			Repository:   "group/repo",
			ProjectID:    77,
			ChangeNumber: 23,
		},
		PublishCandidates: []core.PublishCandidate{
			{Kind: "summary", Body: "judge summary"},
			{
				Kind:     "finding",
				Title:    "Unsafe query",
				Body:     "User input flows into SQL.",
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

	if err := publisher.Publish(context.Background(), bundle); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(client.notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(client.notes))
	}
	if len(client.discussions) != 1 {
		t.Fatalf("discussions = %d, want 1", len(client.discussions))
	}
	if client.notes[0].Body != "judge summary" {
		t.Fatalf("summary body = %q, want judge summary", client.notes[0].Body)
	}
	if client.discussions[0].Body != "User input flows into SQL." {
		t.Fatalf("discussion body = %q", client.discussions[0].Body)
	}
}

func TestPublisherRequiresClient(t *testing.T) {
	if err := NewPublisher(nil).Publish(context.Background(), core.ReviewBundle{}); err == nil {
		t.Fatal("Publish error = nil, want non-nil")
	}
}

func TestPublisherFallsBackToNoteOnPositionFailure(t *testing.T) {
	client := &fakePublishClient{failBody: "User input flows into SQL."}
	publisher := NewPublisher(client)
	bundle := core.ReviewBundle{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitLab,
			URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
			Repository:   "group/repo",
			ProjectID:    77,
			ChangeNumber: 23,
		},
		PublishCandidates: []core.PublishCandidate{{
			Kind:     "finding",
			Title:    "Unsafe query",
			Body:     "User input flows into SQL.",
			Severity: "high",
			Location: core.CanonicalLocation{
				Path:      "internal/db/query.go",
				Side:      core.DiffSideNew,
				StartLine: 44,
				EndLine:   44,
			},
		}},
	}

	if err := publisher.Publish(context.Background(), bundle); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(client.discussions) != 1 {
		t.Fatalf("discussions = %d, want 1 attempted discussion", len(client.discussions))
	}
	if len(client.notes) != 1 {
		t.Fatalf("notes = %d, want 1 fallback note", len(client.notes))
	}
	if client.notes[0].Body != "User input flows into SQL." {
		t.Fatalf("fallback note body = %q", client.notes[0].Body)
	}
}
