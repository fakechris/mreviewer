package compare

import (
	"strings"

	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type Report struct {
	ReviewerCount            int                             `json:"reviewer_count"`
	SharedFindings           []reviewcore.Finding            `json:"shared_findings,omitempty"`
	UniqueFindingsByReviewer map[string][]reviewcore.Finding `json:"unique_findings_by_reviewer,omitempty"`
	AgreementRate            float64                         `json:"agreement_rate"`
}

func BuildReport(artifacts []reviewcore.ComparisonArtifact) Report {
	report := Report{
		ReviewerCount:            len(artifacts),
		UniqueFindingsByReviewer: map[string][]reviewcore.Finding{},
	}
	if len(artifacts) == 0 {
		return report
	}

	type occurrence struct {
		finding     reviewcore.Finding
		reviewerIDs map[string]struct{}
	}
	byIdentity := map[string]*occurrence{}
	totalUnique := 0

	for _, artifact := range artifacts {
		reviewerID := normalizeComparisonReviewerID(artifact)
		for _, finding := range artifact.Findings {
			identity := findingIdentityKey(finding)
			if identity == "" {
				continue
			}
			entry, ok := byIdentity[identity]
			if !ok {
				entry = &occurrence{
					finding:     finding,
					reviewerIDs: map[string]struct{}{},
				}
				byIdentity[identity] = entry
				totalUnique++
			}
			entry.reviewerIDs[reviewerID] = struct{}{}
		}
	}

	shared := 0
	for _, artifact := range artifacts {
		reviewerID := normalizeComparisonReviewerID(artifact)
		for _, finding := range artifact.Findings {
			identity := findingIdentityKey(finding)
			entry := byIdentity[identity]
			if entry == nil {
				continue
			}
			if len(entry.reviewerIDs) > 1 {
				if !containsFinding(report.SharedFindings, identity) {
					report.SharedFindings = append(report.SharedFindings, entry.finding)
					shared++
				}
				continue
			}
			report.UniqueFindingsByReviewer[reviewerID] = append(report.UniqueFindingsByReviewer[reviewerID], finding)
		}
	}

	if totalUnique > 0 {
		report.AgreementRate = float64(shared) / float64(totalUnique)
	}
	return report
}

func containsFinding(findings []reviewcore.Finding, identity string) bool {
	for _, finding := range findings {
		if findingIdentityKey(finding) == identity {
			return true
		}
	}
	return false
}

func findingIdentityKey(finding reviewcore.Finding) string {
	if finding.Identity == nil {
		return ""
	}
	parts := []string{
		strings.TrimSpace(finding.Identity.Category),
		strings.TrimSpace(finding.Identity.NormalizedClaim),
		strings.TrimSpace(finding.Identity.LocationKey),
		strings.TrimSpace(finding.Identity.EvidenceKey),
	}
	if strings.Join(parts, "") == "" {
		return ""
	}
	return strings.Join(parts, "::")
}
