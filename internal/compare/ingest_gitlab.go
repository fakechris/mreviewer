package compare

import (
	"fmt"
	"strconv"
	"strings"

	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

func IngestGitLabReviewerArtifacts(notes []legacygitlab.MergeRequestNote, discussions []legacygitlab.MergeRequestDiscussion) []reviewcore.ComparisonArtifact {
	artifacts := map[string]*reviewcore.ComparisonArtifact{}

	for _, note := range notes {
		if note.System {
			continue
		}
		reviewerID := gitlabReviewerID(note.Author.Username, note.ID)
		appendGitLabFinding(artifacts, reviewerID, "external_gitlab_note", note.ID, "", note.Body, note.Position)
	}

	for _, discussion := range discussions {
		for _, note := range discussion.Notes {
			if note.System {
				continue
			}
			reviewerID := gitlabReviewerID(note.Author.Username, note.ID)
			appendGitLabFinding(artifacts, reviewerID, "external_gitlab_discussion", note.ID, discussion.ID, note.Body, note.Position)
		}
	}

	return comparisonArtifactsFromMap(artifacts)
}

func appendGitLabFinding(artifacts map[string]*reviewcore.ComparisonArtifact, reviewerID, reviewerType string, noteID int64, discussionID, body string, position *legacygitlab.NotePosition) {
	artifact := artifacts[reviewerID]
	if artifact == nil {
		artifact = &reviewcore.ComparisonArtifact{
			ReviewerID:   reviewerID,
			ReviewerType: reviewerType,
		}
		*artifact = normalizeArtifact(*artifact)
		artifacts[reviewerID] = artifact
	}

	location := gitlabLocation(position, noteID, discussionID)
	artifact.Findings = append(artifact.Findings, reviewcore.Finding{
		Title:    firstLine(body),
		Claim:    strings.TrimSpace(body),
		Category: "external_review",
		Location: location,
		Identity: &reviewcore.FindingIdentityInput{
			Category:        "external_review",
			NormalizedClaim: normalizeClaim(body),
			LocationKey:     gitlabLocationKey(location),
			EvidenceKey:     strconv.FormatInt(noteID, 10),
		},
	})
}

func gitlabLocation(position *legacygitlab.NotePosition, noteID int64, discussionID string) *reviewcore.CanonicalLocation {
	if position == nil {
		return nil
	}
	location := &reviewcore.CanonicalLocation{
		Path: firstNonEmpty(position.NewPath, position.OldPath),
		Line: firstNonZero(position.NewLine, position.OldLine),
		PlatformMetadata: map[string]any{
			"note_id": strconv.FormatInt(noteID, 10),
		},
	}
	if discussionID != "" {
		location.PlatformMetadata["discussion_id"] = discussionID
	}
	if position.OldLine > 0 && position.NewLine == 0 {
		location.Side = reviewcore.LocationSideOld
	} else {
		location.Side = reviewcore.LocationSideNew
	}
	return location
}

func gitlabReviewerID(username string, noteID int64) string {
	username = strings.TrimSpace(username)
	if username != "" {
		return username
	}
	return fmt.Sprintf("gitlab-anonymous-%d", noteID)
}

func gitlabLocationKey(location *reviewcore.CanonicalLocation) string {
	if location == nil {
		return ""
	}
	return fmt.Sprintf("%s:%d:%s", location.Path, location.Line, location.Side)
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
