package reviewcore

import (
	"encoding/json"
	"testing"
)

func TestReviewTargetSupportsGitHubAndGitLab(t *testing.T) {
	t.Run("github", func(t *testing.T) {
		target := ReviewTarget{
			Platform:     PlatformGitHub,
			URL:          "https://github.com/acme/repo/pull/17",
			Repository:   "acme/repo",
			ChangeNumber: 17,
		}
		if target.Platform != PlatformGitHub {
			t.Fatalf("platform = %q, want %q", target.Platform, PlatformGitHub)
		}
		if target.Repository != "acme/repo" {
			t.Fatalf("repository = %q", target.Repository)
		}
	})

	t.Run("gitlab", func(t *testing.T) {
		target := ReviewTarget{
			Platform:     PlatformGitLab,
			URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
			Repository:   "group/repo",
			ChangeNumber: 23,
			ProjectID:    77,
		}
		if target.Platform != PlatformGitLab {
			t.Fatalf("platform = %q, want %q", target.Platform, PlatformGitLab)
		}
		if target.ProjectID != 77 {
			t.Fatalf("project_id = %d", target.ProjectID)
		}
	})
}

func TestCanonicalLocationSupportsLineRangeAndMetadata(t *testing.T) {
	loc := CanonicalLocation{
		Path:             "internal/llm/processor.go",
		Side:             DiffSideNew,
		StartLine:        101,
		EndLine:          108,
		Snippet:          "if cancelled { return }",
		PlatformMetadata: json.RawMessage(`{"gitlab":{"line_code":"abc_101_108"}}`),
	}

	if loc.Path == "" {
		t.Fatalf("path should not be empty")
	}
	if loc.Side != DiffSideNew {
		t.Fatalf("side = %q, want %q", loc.Side, DiffSideNew)
	}
	if loc.StartLine != 101 || loc.EndLine != 108 {
		t.Fatalf("line range = %d-%d", loc.StartLine, loc.EndLine)
	}
	if len(loc.PlatformMetadata) == 0 {
		t.Fatalf("platform metadata should be preserved")
	}
}

func TestFindingIdentityInputCapturesCanonicalComparisonShape(t *testing.T) {
	finding := Finding{
		Category: "security.sql-injection",
		Severity: "high",
		Claim:    "untrusted user input reaches raw SQL execution without validation",
		Identity: FindingIdentityInput{
			Category:            "security.sql-injection",
			NormalizedClaim:     "untrusted user input reaches raw sql execution without validation",
			EvidenceFingerprint: "sql/raw:user_id",
			Standards:           []string{"owasp-a03", "asvs-v5"},
			Location: CanonicalLocation{
				Path:      "internal/db/query.go",
				Side:      DiffSideNew,
				StartLine: 44,
				EndLine:   44,
			},
		},
	}

	if finding.Identity.NormalizedClaim == "" {
		t.Fatalf("normalized claim should not be empty")
	}
	if finding.Identity.Location.Path == "" {
		t.Fatalf("identity location should not be empty")
	}
	if len(finding.Identity.Standards) != 2 {
		t.Fatalf("standards = %v", finding.Identity.Standards)
	}
}

func TestReviewerArtifactAndReviewBundleRoundTripJSON(t *testing.T) {
	artifact := ReviewerArtifact{
		ReviewerID:   "security",
		ReviewerKind: "specialist_pack",
		Target: ReviewTarget{
			Platform:     PlatformGitLab,
			URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
			Repository:   "group/repo",
			ChangeNumber: 23,
			ProjectID:    77,
		},
		Summary: "Security reviewer found one critical issue.",
		Findings: []Finding{{
			Category: "security.sql-injection",
			Severity: "high",
			Claim:    "raw SQL uses untrusted input",
			Identity: FindingIdentityInput{
				Category:            "security.sql-injection",
				NormalizedClaim:     "raw sql uses untrusted input",
				EvidenceFingerprint: "sql/raw:user_id",
				Location: CanonicalLocation{
					Path:      "internal/db/query.go",
					Side:      DiffSideNew,
					StartLine: 44,
					EndLine:   44,
				},
			},
		}},
	}

	bundle := ReviewBundle{
		Target:            artifact.Target,
		Artifacts:         []ReviewerArtifact{artifact},
		MarkdownSummary:   "# Review\n\nOne issue found.",
		JSONSchemaVersion: "v1alpha1",
		PublishCandidates: []PublishCandidate{{
			Kind: "summary",
			Body: "One issue found.",
		}},
	}

	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}

	var decoded ReviewBundle
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if decoded.Target.URL != bundle.Target.URL {
		t.Fatalf("target url = %q, want %q", decoded.Target.URL, bundle.Target.URL)
	}
	if len(decoded.Artifacts) != 1 {
		t.Fatalf("artifacts len = %d, want 1", len(decoded.Artifacts))
	}
	if len(decoded.PublishCandidates) != 1 {
		t.Fatalf("publish candidates len = %d, want 1", len(decoded.PublishCandidates))
	}
}
