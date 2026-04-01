package compare

import core "github.com/mreviewer/mreviewer/internal/reviewcore"

type DecisionBenchmarkReport struct {
	ReviewerCount     int    `json:"reviewer_count"`
	ConsensusFindings int    `json:"consensus_findings"`
	UniqueFindings    int    `json:"unique_findings"`
	JudgeVerdict      string `json:"judge_verdict,omitempty"`
}

func BuildDecisionBenchmarkReport(artifacts []core.ReviewerArtifact, judgeVerdict string) DecisionBenchmarkReport {
	report := CompareArtifacts(artifacts)
	return DecisionBenchmarkReport{
		ReviewerCount:     len(artifacts),
		ConsensusFindings: len(report.SharedFindings),
		UniqueFindings:    report.UniqueFindingCount,
		JudgeVerdict:      judgeVerdict,
	}
}

func BuildDecisionBenchmarkReportForBundle(bundle core.ReviewBundle, advisor *core.ReviewerArtifact) DecisionBenchmarkReport {
	return BuildDecisionBenchmarkReport(BuildArtifactsForBundle(bundle, advisor), bundle.Verdict)
}
