package compare

import (
	"sort"
	"strings"

	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

func normalizeClaim(body string) string {
	return strings.ToLower(strings.TrimSpace(body))
}

func firstLine(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if idx := strings.IndexByte(body, '\n'); idx >= 0 {
		return strings.TrimSpace(body[:idx])
	}
	return body
}

func comparisonArtifactsFromMap(artifacts map[string]*reviewcore.ComparisonArtifact) []reviewcore.ComparisonArtifact {
	keys := make([]string, 0, len(artifacts))
	for key := range artifacts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]reviewcore.ComparisonArtifact, 0, len(keys))
	for _, key := range keys {
		result = append(result, *artifacts[key])
	}
	return result
}
