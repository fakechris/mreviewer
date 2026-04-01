package reviewcore

import (
	"fmt"

	"github.com/mreviewer/mreviewer/internal/llm"
)

func ArtifactFromLegacyResult(reviewerID string, result llm.ReviewResult) ReviewerArtifact {
	artifact := ReviewerArtifact{
		ReviewerID:   reviewerID,
		ReviewerType: "legacy_provider",
		Summary:      result.Summary,
		Verdict:      legacyStatusVerdict(result.Status),
		Findings:     make([]Finding, 0, len(result.Findings)),
	}

	for _, finding := range result.Findings {
		artifact.Findings = append(artifact.Findings, Finding{
			Title:      finding.Title,
			Category:   finding.Category,
			Claim:      finding.BodyMarkdown,
			Severity:   finding.Severity,
			Confidence: finding.Confidence,
			Location:   locationFromLegacyFinding(finding),
			Evidence:   append([]string(nil), finding.Evidence...),
			Identity: &FindingIdentityInput{
				Category:        finding.Category,
				NormalizedClaim: finding.Title,
				LocationKey:     locationKeyFromLegacyFinding(finding),
				EvidenceKey:     finding.CanonicalKey,
			},
		})
	}

	return artifact
}

func legacyStatusVerdict(status string) Verdict {
	switch status {
	case "approved":
		return VerdictApproved
	case "failed":
		return VerdictFailed
	case "requested_changes":
		return VerdictRequestedChanges
	default:
		return VerdictCommentOnly
	}
}

func locationFromLegacyFinding(finding llm.ReviewFinding) *CanonicalLocation {
	if finding.Path == "" {
		return nil
	}
	location := &CanonicalLocation{Path: finding.Path}
	if finding.NewLine != nil {
		location.Side = LocationSideNew
		location.Line = int(*finding.NewLine)
		return location
	}
	if finding.OldLine != nil {
		location.Side = LocationSideOld
		location.Line = int(*finding.OldLine)
		return location
	}
	return location
}

func locationKeyFromLegacyFinding(finding llm.ReviewFinding) string {
	if finding.Path == "" {
		return ""
	}
	if finding.NewLine != nil {
		return finding.Path + ":new:" + itoa(int(*finding.NewLine))
	}
	if finding.OldLine != nil {
		return finding.Path + ":old:" + itoa(int(*finding.OldLine))
	}
	return finding.Path
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	return fmt.Sprintf("%d", value)
}
