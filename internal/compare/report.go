package compare

import "github.com/mreviewer/mreviewer/internal/reviewcore"

type AggregateReport struct {
	TargetCount          int     `json:"target_count"`
	ReviewerCount        int     `json:"reviewer_count"`
	UniqueFindingCount   int     `json:"unique_finding_count"`
	AverageAgreementRate float64 `json:"average_agreement_rate"`
}

func BuildAggregateReport(reports []Report) AggregateReport {
	result := AggregateReport{TargetCount: len(reports)}
	if len(reports) == 0 {
		return result
	}
	totalAgreement := 0.0
	reviewerCount := 0
	uniqueFindings := 0
	for _, report := range reports {
		totalAgreement += report.AgreementRate
		reviewerCount += report.ReviewerCount
		uniqueFindings += len(report.SharedFindings) + countUnique(report.UniqueFindingsByReviewer)
	}
	result.ReviewerCount = reviewerCount
	result.UniqueFindingCount = uniqueFindings
	result.AverageAgreementRate = totalAgreement / float64(len(reports))
	return result
}

func BuildComparisonArtifacts(artifacts []reviewcore.ReviewerArtifact) []reviewcore.ComparisonArtifact {
	result := make([]reviewcore.ComparisonArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		result = append(result, normalizeArtifact(reviewcore.ComparisonArtifact{
			ReviewerID:   artifact.ReviewerID,
			ReviewerType: artifact.ReviewerType,
			Verdict:      artifact.Verdict,
			Findings:     append([]reviewcore.Finding(nil), artifact.Findings...),
		}))
	}
	return result
}

func BuildComparisonArtifactsForBundle(bundle reviewcore.ReviewBundle) []reviewcore.ComparisonArtifact {
	artifacts := append([]reviewcore.ReviewerArtifact(nil), bundle.Artifacts...)
	if bundle.AdvisorArtifact != nil {
		artifacts = append(artifacts, *bundle.AdvisorArtifact)
	}
	return BuildComparisonArtifacts(artifacts)
}
