package compare

import core "github.com/mreviewer/mreviewer/internal/reviewcore"

func BuildAggregateReport(reports []Report) AggregateReport {
	return AggregateReports(reports)
}

func BuildArtifactsForBundle(bundle core.ReviewBundle, advisor *core.ReviewerArtifact) []core.ReviewerArtifact {
	artifacts := append([]core.ReviewerArtifact(nil), bundle.Artifacts...)
	if advisor != nil {
		artifacts = append(artifacts, *advisor)
	}
	return artifacts
}
