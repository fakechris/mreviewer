package reviewcore

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/mreviewer/mreviewer/internal/llm"
)

func ArtifactFromLegacyResult(target ReviewTarget, reviewerID string, result llm.ReviewResult) ReviewerArtifact {
	findings := make([]Finding, 0, len(result.Findings))
	for _, finding := range result.Findings {
		findings = append(findings, findingFromLegacy(finding))
	}
	return ReviewerArtifact{
		ReviewerID:   reviewerID,
		ReviewerKind: "legacy_provider",
		Target:       target,
		Summary:      strings.TrimSpace(result.Summary),
		Findings:     findings,
	}
}

func PublishCandidatesFromLegacyResult(result llm.ReviewResult) []PublishCandidate {
	candidates := make([]PublishCandidate, 0, len(result.Findings)+1)
	if result.SummaryNote != nil && strings.TrimSpace(result.SummaryNote.BodyMarkdown) != "" {
		candidates = append(candidates, PublishCandidate{
			Kind: "summary",
			Body: strings.TrimSpace(result.SummaryNote.BodyMarkdown),
		})
	} else if strings.TrimSpace(result.Summary) != "" {
		candidates = append(candidates, PublishCandidate{
			Kind: "summary",
			Body: strings.TrimSpace(result.Summary),
		})
	}

	for _, finding := range result.Findings {
		location := locationFromLegacyFinding(finding)
		if strings.TrimSpace(location.Path) == "" {
			candidates = append(candidates, PublishCandidate{
				Kind:             "finding",
				Title:            strings.TrimSpace(finding.Title),
				Body:             summaryBodyFromLegacyFinding(finding),
				Severity:         strings.TrimSpace(finding.Severity),
				PublishAsSummary: true,
				Location:         location,
			})
			continue
		}
		candidates = append(candidates, PublishCandidate{
			Kind:     "finding",
			Title:    strings.TrimSpace(finding.Title),
			Body:     strings.TrimSpace(finding.BodyMarkdown),
			Severity: strings.TrimSpace(finding.Severity),
			Location: location,
		})
	}
	return candidates
}

func summaryBodyFromLegacyFinding(finding llm.ReviewFinding) string {
	title := strings.TrimSpace(finding.Title)
	body := strings.TrimSpace(finding.BodyMarkdown)
	switch {
	case title != "" && body != "":
		return "### " + title + "\n\n" + body
	case body != "":
		return body
	default:
		return title
	}
}

func findingFromLegacy(finding llm.ReviewFinding) Finding {
	body := strings.TrimSpace(finding.BodyMarkdown)
	title := strings.TrimSpace(finding.Title)
	claim := body
	if claim == "" {
		claim = title
	}
	return Finding{
		Category:   strings.TrimSpace(finding.Category),
		Severity:   strings.TrimSpace(finding.Severity),
		Title:      title,
		Body:       body,
		Claim:      claim,
		Confidence: finding.Confidence,
		Identity: FindingIdentityInput{
			Category:            strings.TrimSpace(finding.Category),
			NormalizedClaim:     normalizeClaim(title, body),
			EvidenceFingerprint: evidenceFingerprint(finding),
			Location:            locationFromLegacyFinding(finding),
		},
	}
}

func locationFromLegacyFinding(finding llm.ReviewFinding) CanonicalLocation {
	oldPath, newPath := resolveLegacyPaths(finding.Path)
	loc := CanonicalLocation{
		Path:    strings.TrimSpace(newPath),
		Snippet: strings.TrimSpace(finding.AnchorSnippet),
	}
	if loc.Path == "" {
		loc.Path = strings.TrimSpace(oldPath)
	}
	switch normalizeAnchorKind(finding.AnchorKind) {
	case "old_line":
		loc.Side = DiffSideOld
		loc.StartLine = intPtr32Value(finding.OldLine)
		loc.EndLine = intPtr32Value(finding.OldLine)
	case "range":
		if line := intPtr32Value(finding.RangeStartNewLine); line > 0 {
			loc.Side = DiffSideNew
			loc.StartLine = line
			loc.EndLine = maxInt(line, intPtr32Value(finding.RangeEndNewLine))
		} else {
			loc.Side = DiffSideOld
			loc.StartLine = intPtr32Value(finding.RangeStartOldLine)
			loc.EndLine = maxInt(loc.StartLine, intPtr32Value(finding.RangeEndOldLine))
		}
	default:
		loc.Side = DiffSideNew
		if line := intPtr32Value(finding.NewLine); line > 0 {
			loc.StartLine = line
			loc.EndLine = line
		}
	}

	metadata := map[string]any{
		"anchor_kind": finding.AnchorKind,
		"old_path":    oldPath,
		"new_path":    newPath,
	}
	if finding.OldLine != nil {
		metadata["old_line"] = *finding.OldLine
	}
	if finding.NewLine != nil {
		metadata["new_line"] = *finding.NewLine
	}
	if finding.RangeStartOldLine != nil || finding.RangeStartNewLine != nil || finding.RangeEndOldLine != nil || finding.RangeEndNewLine != nil {
		metadata["range_start_old_line"] = maybeInt32(finding.RangeStartOldLine)
		metadata["range_start_new_line"] = maybeInt32(finding.RangeStartNewLine)
		metadata["range_end_old_line"] = maybeInt32(finding.RangeEndOldLine)
		metadata["range_end_new_line"] = maybeInt32(finding.RangeEndNewLine)
		if lineRange := legacyLineRangeMetadata(oldPath, newPath, finding); lineRange != nil {
			metadata["line_range"] = lineRange
		}
	}
	if data, err := json.Marshal(metadata); err == nil && len(data) > 2 {
		loc.PlatformMetadata = data
	}

	return loc
}

func normalizeClaim(title, body string) string {
	claim := strings.TrimSpace(body)
	if claim == "" {
		claim = strings.TrimSpace(title)
	}
	claim = strings.ToLower(claim)
	return strings.Join(strings.Fields(claim), " ")
}

func evidenceFingerprint(finding llm.ReviewFinding) string {
	if key := strings.TrimSpace(finding.CanonicalKey); key != "" {
		return key
	}
	for _, evidence := range finding.Evidence {
		evidence = strings.TrimSpace(evidence)
		if evidence != "" {
			return evidence
		}
	}
	return normalizeClaim(finding.Title, finding.BodyMarkdown)
}

func normalizeAnchorKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "old", "old_line", "deleted":
		return "old_line"
	case "range":
		return "range"
	default:
		return "new_line"
	}
}

func intPtr32Value(value *int32) int {
	if value == nil {
		return 0
	}
	return int(*value)
}

func maybeInt32(value *int32) any {
	if value == nil {
		return nil
	}
	return *value
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func resolveLegacyPaths(path string) (string, string) {
	path = strings.TrimSpace(path)
	parts := strings.SplitN(path, " -> ", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return path, path
}

func legacyLineRangeMetadata(oldPath, newPath string, finding llm.ReviewFinding) map[string]any {
	start := legacyRangeLineMetadata(oldPath, newPath, strings.TrimSpace(finding.RangeStartKind), finding.RangeStartOldLine, finding.RangeStartNewLine)
	end := legacyRangeLineMetadata(oldPath, newPath, strings.TrimSpace(finding.RangeEndKind), finding.RangeEndOldLine, finding.RangeEndNewLine)
	if start == nil || end == nil {
		return nil
	}
	return map[string]any{
		"start": start,
		"end":   end,
	}
}

func legacyRangeLineMetadata(oldPath, newPath, kind string, oldLine, newLine *int32) map[string]any {
	lineType := legacyRangeLineType(kind)
	if lineType == "" {
		return nil
	}
	lineCode := legacyLineCode(oldPath, newPath, oldLine, newLine)
	if lineCode == "" {
		return nil
	}
	line := map[string]any{
		"line_code": lineCode,
		"type":      lineType,
	}
	if oldLine != nil {
		line["old_line"] = *oldLine
	}
	if newLine != nil {
		line["new_line"] = *newLine
	}
	return line
}

func legacyRangeLineType(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "old", "old_line", "removed":
		return "old"
	case "new", "new_line", "added":
		return "new"
	case "context", "context_line", "unchanged":
		return "context"
	default:
		return ""
	}
}

func legacyLineCode(oldPath, newPath string, oldLine, newLine *int32) string {
	path := strings.TrimSpace(newPath)
	if path == "" {
		path = strings.TrimSpace(oldPath)
	}
	if path == "" {
		return ""
	}
	oldValue := 0
	if oldLine != nil {
		oldValue = int(*oldLine)
	}
	newValue := 0
	if newLine != nil {
		newValue = int(*newLine)
	}
	return strings.TrimSpace(path) + "_" + strconv.Itoa(oldValue) + "_" + strconv.Itoa(newValue)
}
