package compare

import "github.com/mreviewer/mreviewer/internal/reviewcore"

type DecisionBenchmarkReport struct {
	ReviewerCount     int                `json:"reviewer_count"`
	ConsensusFindings int                `json:"consensus_findings"`
	UniqueFindings    int                `json:"unique_findings"`
	JudgeVerdict      reviewcore.Verdict `json:"judge_verdict,omitempty"`
}

func BuildDecisionBenchmarkReport(artifacts []reviewcore.ReviewerArtifact, judgeVerdict reviewcore.Verdict) DecisionBenchmarkReport {
	comparisonArtifacts := make([]reviewcore.ComparisonArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		comparisonArtifacts = append(comparisonArtifacts, normalizeArtifact(reviewcore.ComparisonArtifact{
			ReviewerID:   artifact.ReviewerID,
			ReviewerType: artifact.ReviewerType,
			Verdict:      artifact.Verdict,
			Findings:     append([]reviewcore.Finding(nil), artifact.Findings...),
		}))
	}
	report := BuildReport(comparisonArtifacts)
	return DecisionBenchmarkReport{
		ReviewerCount:     len(artifacts),
		ConsensusFindings: len(report.SharedFindings),
		UniqueFindings:    len(report.SharedFindings) + countUnique(report.UniqueFindingsByReviewer),
		JudgeVerdict:      judgeVerdict,
	}
}

func BuildDecisionBenchmarkReportForBundle(bundle reviewcore.ReviewBundle) DecisionBenchmarkReport {
	artifacts := append([]reviewcore.ReviewerArtifact(nil), bundle.Artifacts...)
	if bundle.AdvisorArtifact != nil {
		artifacts = append(artifacts, *bundle.AdvisorArtifact)
	}
	return BuildDecisionBenchmarkReport(artifacts, bundle.JudgeVerdict)
}

func countUnique(findingsByReviewer map[string][]reviewcore.Finding) int {
	total := 0
	for _, findings := range findingsByReviewer {
		total += len(findings)
	}
	return total
}
