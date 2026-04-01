package compare

import (
	"testing"

	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

func TestBuildReportCalculatesAgreementRate(t *testing.T) {
	report := BuildReport([]reviewcore.ComparisonArtifact{
		{
			ReviewerID: "security",
			Findings: []reviewcore.Finding{
				testFinding("auth-bypass", "security", "auth bypass"),
				testFinding("nil-deref", "correctness", "nil dereference"),
			},
		},
		{
			ReviewerID: "database",
			Findings: []reviewcore.Finding{
				testFinding("auth-bypass", "security", "auth bypass"),
				testFinding("index-miss", "database", "missing index"),
			},
		},
	})

	if report.AgreementRate != 1.0/3.0 {
		t.Fatalf("agreement rate = %v, want %v", report.AgreementRate, 1.0/3.0)
	}
}

func TestBuildDecisionBenchmarkReportUsesCouncilArtifacts(t *testing.T) {
	report := BuildDecisionBenchmarkReport([]reviewcore.ReviewerArtifact{
		{
			ReviewerID: "security",
			Verdict:    reviewcore.VerdictRequestedChanges,
			Findings: []reviewcore.Finding{
				testFinding("auth-bypass", "security", "auth bypass"),
				testFinding("nil-deref", "correctness", "nil dereference"),
			},
		},
		{
			ReviewerID: "architecture",
			Verdict:    reviewcore.VerdictCommentOnly,
			Findings: []reviewcore.Finding{
				testFinding("auth-bypass", "security", "auth bypass"),
			},
		},
	}, reviewcore.VerdictRequestedChanges)

	if report.ReviewerCount != 2 {
		t.Fatalf("reviewer count = %d, want 2", report.ReviewerCount)
	}
	if report.ConsensusFindings != 1 {
		t.Fatalf("consensus findings = %d, want 1", report.ConsensusFindings)
	}
	if report.UniqueFindings != 2 {
		t.Fatalf("unique findings = %d, want 2", report.UniqueFindings)
	}
	if report.JudgeVerdict != reviewcore.VerdictRequestedChanges {
		t.Fatalf("judge verdict = %q, want requested_changes", report.JudgeVerdict)
	}
}

func TestBuildDecisionBenchmarkReportForBundleIncludesAdvisorArtifact(t *testing.T) {
	report := BuildDecisionBenchmarkReportForBundle(reviewcore.ReviewBundle{
		Artifacts: []reviewcore.ReviewerArtifact{
			{
				ReviewerID:   "security",
				ReviewerType: "pack",
				Findings: []reviewcore.Finding{
					testFinding("auth-bypass", "security", "auth bypass"),
				},
			},
		},
		AdvisorArtifact: &reviewcore.ReviewerArtifact{
			ReviewerID:   "advisor",
			ReviewerType: "advisor",
			Findings: []reviewcore.Finding{
				testFinding("auth-bypass", "security", "auth bypass"),
				testFinding("nil-deref", "correctness", "nil dereference"),
			},
		},
		JudgeVerdict: reviewcore.VerdictRequestedChanges,
	})

	if report.ReviewerCount != 2 {
		t.Fatalf("reviewer count = %d, want 2", report.ReviewerCount)
	}
	if report.ConsensusFindings != 1 {
		t.Fatalf("consensus findings = %d, want 1", report.ConsensusFindings)
	}
	if report.UniqueFindings != 2 {
		t.Fatalf("unique findings = %d, want 2", report.UniqueFindings)
	}
}
