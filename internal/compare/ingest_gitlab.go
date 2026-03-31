package compare

import (
	"strings"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type GitLabNote struct {
	Author string
	Body   string
}

type GitLabDiscussion struct {
	Author   string
	Body     string
	Path     string
	OldLine  int
	NewLine  int
	Severity string
	Category string
}

type GitLabCommentSet struct {
	Notes       []GitLabNote
	Discussions []GitLabDiscussion
}

func IngestGitLabComments(target core.ReviewTarget, comments GitLabCommentSet) []core.ReviewerArtifact {
	artifacts := make(map[string]*ExternalArtifactInput)
	for _, note := range comments.Notes {
		input := ensureArtifactInput(artifacts, target, note.Author)
		if input.Summary == "" {
			input.Summary = strings.TrimSpace(note.Body)
		}
	}
	for _, discussion := range comments.Discussions {
		input := ensureArtifactInput(artifacts, target, discussion.Author)
		side := core.DiffSideNew
		line := discussion.NewLine
		if line <= 0 && discussion.OldLine > 0 {
			side = core.DiffSideOld
			line = discussion.OldLine
		}
		input.Comments = append(input.Comments, ExternalComment{
			Category:  discussion.Category,
			Severity:  discussion.Severity,
			Body:      strings.TrimSpace(discussion.Body),
			Claim:     strings.TrimSpace(discussion.Body),
			Path:      strings.TrimSpace(discussion.Path),
			Side:      sideOrDefault(side),
			StartLine: line,
			EndLine:   line,
		})
	}
	return normalizeArtifactInputs(artifacts)
}

func normalizeArtifactInputs(inputs map[string]*ExternalArtifactInput) []core.ReviewerArtifact {
	keys := make([]string, 0, len(inputs))
	for key := range inputs {
		keys = append(keys, key)
	}
	sortStrings(keys)
	out := make([]core.ReviewerArtifact, 0, len(keys))
	for _, key := range keys {
		out = append(out, NormalizeExternalArtifact(*inputs[key]))
	}
	return out
}

func inferReviewerKind(reviewer string) string {
	lowered := strings.ToLower(strings.TrimSpace(reviewer))
	switch {
	case strings.Contains(lowered, "coderabbit"):
		return "coderabbit"
	case strings.Contains(lowered, "codex"):
		return "codex"
	case strings.Contains(lowered, "gemini"):
		return "gemini"
	case strings.Contains(lowered, "devin"):
		return "devin"
	default:
		return "external"
	}
}

func sideOrDefault(side core.DiffSide) core.DiffSide {
	if side == "" {
		return core.DiffSideNew
	}
	return side
}

func lineOrStart(preferred, fallback int) int {
	if preferred > 0 {
		return preferred
	}
	return fallback
}

func sortStrings(values []string) {
	if len(values) < 2 {
		return
	}
	for i := 0; i < len(values)-1; i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}
