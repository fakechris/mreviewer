package judge

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type Engine struct{}

type mergedFinding struct {
	finding     reviewcore.Finding
	reviewerIDs []string
}

func NewEngine() *Engine {
	return &Engine{}
}

func (e *Engine) Decide(target reviewcore.ReviewTarget, artifacts []reviewcore.ReviewerArtifact) reviewcore.ReviewBundle {
	merged := mergeFindings(artifacts)
	findings := make([]reviewcore.Finding, 0, len(merged))
	publishCandidates := []reviewcore.PublishCandidate{{
		Type:  "summary",
		Title: "AI review summary",
		Body:  buildSummary(artifacts, merged),
	}}

	for _, entry := range merged {
		findings = append(findings, entry.finding)
		publishCandidates = append(publishCandidates, reviewcore.PublishCandidate{
			Type:        "finding",
			Title:       entry.finding.Title,
			Body:        firstNonEmpty(entry.finding.Recommendation, entry.finding.Claim),
			Severity:    entry.finding.Severity,
			Location:    entry.finding.Location,
			ReviewerIDs: append([]string(nil), entry.reviewerIDs...),
		})
	}

	return reviewcore.ReviewBundle{
		Target:            target,
		Artifacts:         append([]reviewcore.ReviewerArtifact(nil), artifacts...),
		JudgeVerdict:      decideVerdict(artifacts, findings),
		JudgeSummary:      buildSummary(artifacts, merged),
		PublishCandidates: publishCandidates,
		Comparisons:       comparisonArtifacts(artifacts),
	}
}

func mergeFindings(artifacts []reviewcore.ReviewerArtifact) []mergedFinding {
	byIdentity := map[string]mergedFinding{}
	order := make([]string, 0)

	for _, artifact := range artifacts {
		for _, finding := range artifact.Findings {
			key := findingKey(finding)
			existing, ok := byIdentity[key]
			if !ok {
				byIdentity[key] = mergedFinding{
					finding:     finding,
					reviewerIDs: reviewerIDsWith(artifact.ReviewerID),
				}
				order = append(order, key)
				continue
			}

			if severityRank(finding.Severity) > severityRank(existing.finding.Severity) {
				existing.finding.Severity = finding.Severity
			}
			existing.finding.Confidence = max(existing.finding.Confidence, finding.Confidence)
			if existing.finding.Recommendation == "" {
				existing.finding.Recommendation = finding.Recommendation
			}
			existing.reviewerIDs = reviewerIDsWith(append(existing.reviewerIDs, artifact.ReviewerID)...)
			byIdentity[key] = existing
		}
	}

	result := make([]mergedFinding, 0, len(order))
	for _, key := range order {
		result = append(result, byIdentity[key])
	}
	return result
}

func decideVerdict(artifacts []reviewcore.ReviewerArtifact, findings []reviewcore.Finding) reviewcore.Verdict {
	if len(findings) > 0 {
		return reviewcore.VerdictRequestedChanges
	}
	for _, artifact := range artifacts {
		if artifact.Verdict == reviewcore.VerdictApproved {
			return reviewcore.VerdictCommentOnly
		}
	}
	return reviewcore.VerdictCommentOnly
}

func buildSummary(artifacts []reviewcore.ReviewerArtifact, merged []mergedFinding) string {
	return fmt.Sprintf("Reviewed with %d reviewer packs and produced %d merged findings.", len(artifacts), len(merged))
}

func comparisonArtifacts(artifacts []reviewcore.ReviewerArtifact) []reviewcore.ComparisonArtifact {
	result := make([]reviewcore.ComparisonArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		result = append(result, reviewcore.ComparisonArtifact{
			ReviewerID:   artifact.ReviewerID,
			ReviewerType: artifact.ReviewerType,
			Verdict:      artifact.Verdict,
			Findings:     append([]reviewcore.Finding(nil), artifact.Findings...),
		})
	}
	return result
}

func findingKey(finding reviewcore.Finding) string {
	if finding.Identity != nil {
		if data, err := json.Marshal(finding.Identity); err == nil {
			return string(data)
		}
	}
	var parts []string
	parts = append(parts, strings.TrimSpace(finding.Category))
	parts = append(parts, strings.TrimSpace(finding.Claim))
	if finding.Location != nil {
		parts = append(parts, finding.Location.Path)
		parts = append(parts, string(finding.Location.Side))
	}
	return strings.Join(parts, "|")
}

func severityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 5
	case "high":
		return 4
	case "medium":
		return 3
	case "low":
		return 2
	case "info":
		return 1
	default:
		return 0
	}
}

func reviewerIDsWith(values ...string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
