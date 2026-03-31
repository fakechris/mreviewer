package compare

import (
	"strings"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type GitHubIssueComment struct {
	Author string
	Body   string
}

type GitHubReviewComment struct {
	Author    string
	Body      string
	Path      string
	Line      int
	StartLine int
	Side      core.DiffSide
}

type GitHubCommentSet struct {
	IssueComments  []GitHubIssueComment
	ReviewComments []GitHubReviewComment
}

func IngestGitHubComments(target core.ReviewTarget, comments GitHubCommentSet) []core.ReviewerArtifact {
	artifacts := make(map[string]*ExternalArtifactInput)
	for _, comment := range comments.IssueComments {
		input := ensureArtifactInput(artifacts, target, comment.Author)
		if input.Summary == "" {
			input.Summary = strings.TrimSpace(comment.Body)
		}
	}
	for _, comment := range comments.ReviewComments {
		input := ensureArtifactInput(artifacts, target, comment.Author)
		input.Comments = append(input.Comments, ExternalComment{
			Body:      strings.TrimSpace(comment.Body),
			Claim:     strings.TrimSpace(comment.Body),
			Path:      strings.TrimSpace(comment.Path),
			Side:      sideOrDefault(comment.Side),
			StartLine: lineOrStart(comment.StartLine, comment.Line),
			EndLine:   lineOrStart(comment.Line, comment.StartLine),
		})
	}
	return normalizeArtifactInputs(artifacts)
}

func ensureArtifactInput(artifacts map[string]*ExternalArtifactInput, target core.ReviewTarget, reviewer string) *ExternalArtifactInput {
	key := strings.TrimSpace(reviewer)
	if key == "" {
		key = "unknown"
	}
	if existing, ok := artifacts[key]; ok {
		return existing
	}
	input := &ExternalArtifactInput{
		ReviewerID:   key,
		ReviewerKind: inferReviewerKind(key),
		Target:       target,
	}
	artifacts[key] = input
	return input
}
