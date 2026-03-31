package compare

import (
	"math"
	"testing"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

func TestNormalizeExternalArtifactBuildsCanonicalReviewerArtifact(t *testing.T) {
	target := core.ReviewTarget{
		Platform:     core.PlatformGitHub,
		URL:          "https://github.com/acme/repo/pull/17",
		Repository:   "acme/repo",
		ChangeNumber: 17,
	}
	artifact := NormalizeExternalArtifact(ExternalArtifactInput{
		ReviewerID:   "coderabbitai",
		ReviewerKind: "coderabbit",
		Target:       target,
		Summary:      "Found two high-signal issues.",
		Comments: []ExternalComment{
			{
				ExternalID: "rvw-1",
				Category:   "security",
				Severity:   "high",
				Title:      "SQL concatenation",
				Body:       "User input is concatenated into SQL.",
				Path:       "internal/db/query.go",
				Side:       core.DiffSideNew,
				StartLine:  42,
				EndLine:    42,
				Standards:  []string{"owasp-a03"},
			},
		},
	})

	if artifact.ReviewerID != "coderabbitai" {
		t.Fatalf("reviewer id = %q, want coderabbitai", artifact.ReviewerID)
	}
	if artifact.ReviewerKind != "coderabbit" {
		t.Fatalf("reviewer kind = %q, want coderabbit", artifact.ReviewerKind)
	}
	if artifact.Target != target {
		t.Fatalf("target = %#v, want %#v", artifact.Target, target)
	}
	if len(artifact.Findings) != 1 {
		t.Fatalf("findings len = %d, want 1", len(artifact.Findings))
	}
	finding := artifact.Findings[0]
	if finding.Identity.Category != "security" {
		t.Fatalf("identity category = %q, want security", finding.Identity.Category)
	}
	if finding.Identity.NormalizedClaim == "" {
		t.Fatalf("normalized claim is empty")
	}
	if finding.Identity.Location.Path != "internal/db/query.go" {
		t.Fatalf("identity location path = %q, want internal/db/query.go", finding.Identity.Location.Path)
	}
	if len(finding.Identity.Standards) != 1 || finding.Identity.Standards[0] != "owasp-a03" {
		t.Fatalf("identity standards = %#v, want [owasp-a03]", finding.Identity.Standards)
	}
}

func TestCompareArtifactsComputesAgreementAndUniqueFindings(t *testing.T) {
	target := core.ReviewTarget{
		Platform:     core.PlatformGitLab,
		URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
		Repository:   "group/repo",
		ChangeNumber: 23,
	}
	shared := NormalizeExternalArtifact(ExternalArtifactInput{
		ReviewerID:   "codex",
		ReviewerKind: "codex",
		Target:       target,
		Comments: []ExternalComment{
			{
				Category:  "security",
				Severity:  "high",
				Body:      "User input is concatenated into SQL.",
				Path:      "internal/db/query.go",
				Side:      core.DiffSideNew,
				StartLine: 42,
				EndLine:   42,
			},
			{
				Category:  "architecture",
				Severity:  "medium",
				Body:      "Handler owns transport and persistence.",
				Path:      "internal/api/handler.go",
				Side:      core.DiffSideNew,
				StartLine: 18,
				EndLine:   18,
			},
		},
	})
	other := NormalizeExternalArtifact(ExternalArtifactInput{
		ReviewerID:   "coderabbit",
		ReviewerKind: "coderabbit",
		Target:       target,
		Comments: []ExternalComment{
			{
				Category:  "security",
				Severity:  "critical",
				Body:      "User input is concatenated into SQL.",
				Path:      "internal/db/query.go",
				Side:      core.DiffSideNew,
				StartLine: 42,
				EndLine:   42,
			},
			{
				Category:  "database",
				Severity:  "medium",
				Body:      "Query scans all rows without a limit.",
				Path:      "internal/db/query.go",
				Side:      core.DiffSideNew,
				StartLine: 91,
				EndLine:   91,
			},
		},
	})

	report := CompareArtifacts([]core.ReviewerArtifact{shared, other})
	if report.Target != target {
		t.Fatalf("target = %#v, want %#v", report.Target, target)
	}
	if report.ReviewerCount != 2 {
		t.Fatalf("reviewer count = %d, want 2", report.ReviewerCount)
	}
	if report.UniqueFindingCount != 3 {
		t.Fatalf("unique finding count = %d, want 3", report.UniqueFindingCount)
	}
	if math.Abs(report.AgreementRate-1.0/3.0) > 1e-9 {
		t.Fatalf("agreement rate = %v, want %v", report.AgreementRate, 1.0/3.0)
	}
	if len(report.SharedFindings) != 1 {
		t.Fatalf("shared findings len = %d, want 1", len(report.SharedFindings))
	}
	if len(report.UniqueByReviewer["codex"]) != 1 {
		t.Fatalf("codex unique findings = %d, want 1", len(report.UniqueByReviewer["codex"]))
	}
	if len(report.UniqueByReviewer["coderabbit"]) != 1 {
		t.Fatalf("coderabbit unique findings = %d, want 1", len(report.UniqueByReviewer["coderabbit"]))
	}
}

func TestCompareArtifactsKeepsAnonymousReviewersDistinct(t *testing.T) {
	target := core.ReviewTarget{
		Platform:     core.PlatformGitHub,
		URL:          "https://github.com/acme/repo/pull/17",
		Repository:   "acme/repo",
		ChangeNumber: 17,
	}

	report := CompareArtifacts([]core.ReviewerArtifact{
		NormalizeExternalArtifact(ExternalArtifactInput{
			Target: target,
			Comments: []ExternalComment{{
				Category:  "security",
				Body:      "SQL built with string concatenation.",
				Path:      "internal/db/query.go",
				Side:      core.DiffSideNew,
				StartLine: 42,
				EndLine:   42,
			}},
		}),
		NormalizeExternalArtifact(ExternalArtifactInput{
			Target: target,
			Comments: []ExternalComment{{
				Category:  "database",
				Body:      "Query scans all rows without a limit.",
				Path:      "internal/db/query.go",
				Side:      core.DiffSideNew,
				StartLine: 91,
				EndLine:   91,
			}},
		}),
	})

	if report.ReviewerCount != 2 {
		t.Fatalf("reviewer count = %d, want 2", report.ReviewerCount)
	}
	if len(report.UniqueByReviewer) != 2 {
		t.Fatalf("unique_by_reviewer len = %d, want 2", len(report.UniqueByReviewer))
	}
}

func TestCompareArtifactsSkipsFindingsWithoutStableIdentity(t *testing.T) {
	target := core.ReviewTarget{
		Platform:     core.PlatformGitHub,
		URL:          "https://github.com/acme/repo/pull/17",
		Repository:   "acme/repo",
		ChangeNumber: 17,
	}
	report := CompareArtifacts([]core.ReviewerArtifact{
		{
			ReviewerID:   "reviewer-a",
			ReviewerKind: "external",
			Target:       target,
			Findings: []core.Finding{
				{Body: "general concern"},
				{Title: "no stable identity either"},
			},
		},
		{
			ReviewerID:   "reviewer-b",
			ReviewerKind: "external",
			Target:       target,
			Findings: []core.Finding{
				{Body: "another general concern"},
			},
		},
	})

	if report.UniqueFindingCount != 0 {
		t.Fatalf("unique finding count = %d, want 0", report.UniqueFindingCount)
	}
	if len(report.SharedFindings) != 0 {
		t.Fatalf("shared findings = %#v, want none", report.SharedFindings)
	}
	if len(report.UniqueByReviewer) != 0 {
		t.Fatalf("unique_by_reviewer = %#v, want none", report.UniqueByReviewer)
	}
}

func TestAggregateReportsAcrossTargets(t *testing.T) {
	reports := []Report{
		{
			Target: core.ReviewTarget{
				Platform:     core.PlatformGitHub,
				URL:          "https://github.com/acme/repo/pull/17",
				Repository:   "acme/repo",
				ChangeNumber: 17,
			},
			ReviewerCount:      2,
			UniqueFindingCount: 2,
			AgreementRate:      0.5,
		},
		{
			Target: core.ReviewTarget{
				Platform:     core.PlatformGitLab,
				URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
				Repository:   "group/repo",
				ChangeNumber: 23,
			},
			ReviewerCount:      3,
			UniqueFindingCount: 4,
			AgreementRate:      1.0,
		},
	}

	aggregate := AggregateReports(reports)
	if aggregate.TargetCount != 2 {
		t.Fatalf("target count = %d, want 2", aggregate.TargetCount)
	}
	if aggregate.TotalReviewerCount != 5 {
		t.Fatalf("total reviewer count = %d, want 5", aggregate.TotalReviewerCount)
	}
	if aggregate.TotalUniqueFindings != 6 {
		t.Fatalf("total unique findings = %d, want 6", aggregate.TotalUniqueFindings)
	}
	if aggregate.AverageAgreementRate != 0.75 {
		t.Fatalf("average agreement rate = %v, want 0.75", aggregate.AverageAgreementRate)
	}
	if len(aggregate.Reports) != 2 {
		t.Fatalf("reports len = %d, want 2", len(aggregate.Reports))
	}
}
