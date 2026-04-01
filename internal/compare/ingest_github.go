package compare

import (
	"fmt"
	"strconv"
	"strings"

	githubplatform "github.com/mreviewer/mreviewer/internal/platform/github"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

func IngestGitHubReviewerArtifacts(issueComments []githubplatform.IssueComment, reviewComments []githubplatform.ReviewComment) []reviewcore.ComparisonArtifact {
	artifacts := map[string]*reviewcore.ComparisonArtifact{}

	for _, comment := range issueComments {
		reviewerID := githubReviewerID(comment.User.Login, comment.ID)
		appendGitHubFinding(artifacts, reviewerID, "external_github_issue_comment", reviewcore.Finding{
			Title:    firstLine(comment.Body),
			Claim:    strings.TrimSpace(comment.Body),
			Category: "external_review",
			Identity: &reviewcore.FindingIdentityInput{
				Category:        "external_review",
				NormalizedClaim: normalizeClaim(comment.Body),
				LocationKey:     "issue_comment",
				EvidenceKey:     strconv.FormatInt(comment.ID, 10),
			},
			Evidence: []string{strings.TrimSpace(comment.HTMLURL)},
		})
	}

	for _, comment := range reviewComments {
		reviewerID := githubReviewerID(comment.User.Login, comment.ID)
		location := &reviewcore.CanonicalLocation{
			Path: comment.Path,
			Line: comment.Line,
			PlatformMetadata: map[string]any{
				"comment_id": strconv.FormatInt(comment.ID, 10),
				"url":        strings.TrimSpace(comment.HTMLURL),
			},
		}
		if strings.EqualFold(strings.TrimSpace(comment.Side), "LEFT") {
			location.Side = reviewcore.LocationSideOld
		} else {
			location.Side = reviewcore.LocationSideNew
		}
		appendGitHubFinding(artifacts, reviewerID, "external_github_review_comment", reviewcore.Finding{
			Title:    firstLine(comment.Body),
			Claim:    strings.TrimSpace(comment.Body),
			Category: "external_review",
			Location: location,
			Identity: &reviewcore.FindingIdentityInput{
				Category:        "external_review",
				NormalizedClaim: normalizeClaim(comment.Body),
				LocationKey:     fmt.Sprintf("%s:%d:%s", comment.Path, comment.Line, location.Side),
				EvidenceKey:     strconv.FormatInt(comment.ID, 10),
			},
			Evidence: []string{strings.TrimSpace(comment.HTMLURL)},
		})
	}

	return comparisonArtifactsFromMap(artifacts)
}

func appendGitHubFinding(artifacts map[string]*reviewcore.ComparisonArtifact, reviewerID, reviewerType string, finding reviewcore.Finding) {
	artifact := artifacts[reviewerID]
	if artifact == nil {
		artifact = &reviewcore.ComparisonArtifact{
			ReviewerID:   reviewerID,
			ReviewerType: reviewerType,
		}
		*artifact = normalizeArtifact(*artifact)
		artifacts[reviewerID] = artifact
	}
	artifact.Findings = append(artifact.Findings, finding)
}

func githubReviewerID(login string, commentID int64) string {
	login = strings.TrimSpace(login)
	if login != "" {
		return login
	}
	return fmt.Sprintf("github-anonymous-%d", commentID)
}
