package compare

import (
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

func normalizeComparisonReviewerID(artifact reviewcore.ComparisonArtifact) string {
	id := strings.TrimSpace(artifact.ReviewerID)
	reviewerType := strings.TrimSpace(artifact.ReviewerType)
	switch reviewerType {
	case "pack":
		if id == "" {
			return "council:unknown"
		}
		return "council:" + id
	case "advisor":
		if id == "" {
			return "advisor:unknown"
		}
		return "advisor:" + id
	case "external_github_issue_comment", "external_github_review_comment":
		if id == "" {
			return "github:unknown"
		}
		return "github:" + id
	case "external_gitlab_note", "external_gitlab_discussion":
		if id == "" {
			return "gitlab:unknown"
		}
		return "gitlab:" + id
	default:
		if id == "" {
			return "reviewer:unknown"
		}
		if strings.Contains(id, ":") {
			return id
		}
		if reviewerType == "" {
			return "reviewer:" + id
		}
		return fmt.Sprintf("%s:%s", reviewerType, id)
	}
}

func normalizeArtifact(artifact reviewcore.ComparisonArtifact) reviewcore.ComparisonArtifact {
	artifact.ReviewerID = normalizeComparisonReviewerID(artifact)
	return artifact
}
