package compare

import (
	"testing"

	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
)

func TestIngestGitLabCommentsPreservesReviewerIdentityAndDiscussionMetadata(t *testing.T) {
	artifacts := IngestGitLabReviewerArtifacts(
		[]legacygitlab.MergeRequestNote{
			{
				ID:   301,
				Body: "Architecture summary from gemini",
				Author: legacygitlab.NoteAuthor{
					Username: "gemini",
				},
			},
		},
		[]legacygitlab.MergeRequestDiscussion{
			{
				ID: "discussion-1",
				Notes: []legacygitlab.MergeRequestDiscussionNote{
					{
						ID:   302,
						Body: "This query likely causes an n+1 pattern",
						Author: legacygitlab.NoteAuthor{
							Username: "gemini",
						},
						Position: &legacygitlab.NotePosition{
							NewPath: "repo/query.go",
							NewLine: 18,
						},
					},
					{
						ID:   303,
						Body: "Anonymous comment",
						Position: &legacygitlab.NotePosition{
							NewPath: "repo/query.go",
							NewLine: 27,
						},
					},
				},
			},
		},
	)

	if len(artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2", len(artifacts))
	}
	if artifacts[0].ReviewerID != "gitlab:gemini" {
		t.Fatalf("first reviewer id = %q, want gitlab:gemini", artifacts[0].ReviewerID)
	}
	if len(artifacts[0].Findings) != 2 {
		t.Fatalf("gemini findings = %d, want 2", len(artifacts[0].Findings))
	}
	if artifacts[0].Findings[1].Location == nil || artifacts[0].Findings[1].Location.Path != "repo/query.go" {
		t.Fatalf("expected anchored gitlab finding, got %#v", artifacts[0].Findings[1].Location)
	}
	if artifacts[0].Findings[1].Location.PlatformMetadata["discussion_id"] != "discussion-1" {
		t.Fatalf("expected discussion metadata to survive, got %#v", artifacts[0].Findings[1].Location.PlatformMetadata)
	}
	if artifacts[1].ReviewerID == "anonymous" || artifacts[1].ReviewerID == "" || artifacts[1].ReviewerID == "gitlab:anonymous" {
		t.Fatalf("expected unique anonymous reviewer identity, got %q", artifacts[1].ReviewerID)
	}
}
