package reviewcore

import (
	"context"
	"testing"
)

type packRunnerFunc func(context.Context, ReviewTarget, RunOptions) (ReviewerArtifact, error)

func (f packRunnerFunc) Run(ctx context.Context, input ReviewInput, opts RunOptions) (ReviewerArtifact, error) {
	return f(ctx, input.Target, opts)
}

type judgeFunc func([]ReviewerArtifact) JudgeDecision

func (f judgeFunc) Decide(artifacts []ReviewerArtifact) JudgeDecision { return f(artifacts) }

func TestEngineBuildsBundleFromArtifactsAndJudge(t *testing.T) {
	engine := NewEngine([]PackRunner{
		packRunnerFunc(func(_ context.Context, target ReviewTarget, _ RunOptions) (ReviewerArtifact, error) {
			return ReviewerArtifact{
				ReviewerID:   "security",
				ReviewerKind: "specialist_pack",
				Target:       target,
				Summary:      "security summary",
				Findings: []Finding{{
					Category: "security.sql-injection",
					Severity: "high",
					Title:    "Raw SQL uses untrusted input",
					Body:     "The query concatenates user input directly into SQL.",
					Claim:    "raw sql uses untrusted input",
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
			}, nil
		}),
	}, judgeFunc(func(artifacts []ReviewerArtifact) JudgeDecision {
		return JudgeDecision{
			Verdict:        "requested_changes",
			MergedFindings: artifacts[0].Findings,
			Summary:        "judge summary",
		}
	}))

	target := ReviewTarget{
		Platform:     PlatformGitLab,
		URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
		Repository:   "group/repo",
		ProjectID:    77,
		ChangeNumber: 23,
	}
	bundle, err := engine.Run(context.Background(), ReviewInput{Target: target}, RunOptions{
		OutputMode:    "json",
		PublishMode:   "artifact-only",
		ReviewerPacks: []string{"security"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(bundle.Artifacts) != 1 {
		t.Fatalf("artifacts len = %d, want 1", len(bundle.Artifacts))
	}
	if bundle.MarkdownSummary != "judge summary" {
		t.Fatalf("markdown summary = %q", bundle.MarkdownSummary)
	}
	if len(bundle.PublishCandidates) != 2 {
		t.Fatalf("publish candidates len = %d, want 2", len(bundle.PublishCandidates))
	}
}

func TestEngineAddsFindingPublishCandidatesFromJudgeDecision(t *testing.T) {
	engine := NewEngine([]PackRunner{
		packRunnerFunc(func(_ context.Context, target ReviewTarget, _ RunOptions) (ReviewerArtifact, error) {
			return ReviewerArtifact{
				ReviewerID: "architecture",
				Target:     target,
			}, nil
		}),
	}, judgeFunc(func(_ []ReviewerArtifact) JudgeDecision {
		return JudgeDecision{
			Verdict: "requested_changes",
			Summary: "judge summary",
			MergedFindings: []Finding{{
				Category: "architecture.error-handling",
				Severity: "medium",
				Title:    "Dropped storage error",
				Body:     "The returned storage error is ignored and the request still reports success.",
				Identity: FindingIdentityInput{
					Category:            "architecture.error-handling",
					NormalizedClaim:     "the returned storage error is ignored and the request still reports success.",
					EvidenceFingerprint: "storage-error-dropped",
					Location: CanonicalLocation{
						Path:      "internal/service/handler.go",
						Side:      DiffSideNew,
						StartLine: 19,
						EndLine:   19,
					},
				},
			}},
		}
	}))

	bundle, err := engine.Run(context.Background(), ReviewInput{
		Target: ReviewTarget{
			Platform:     PlatformGitLab,
			URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
			Repository:   "group/repo",
			ProjectID:    77,
			ChangeNumber: 23,
		},
	}, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(bundle.PublishCandidates) != 2 {
		t.Fatalf("publish candidates len = %d, want 2", len(bundle.PublishCandidates))
	}
	if bundle.PublishCandidates[1].Kind != "finding" {
		t.Fatalf("candidate kind = %q, want finding", bundle.PublishCandidates[1].Kind)
	}
	if bundle.PublishCandidates[1].Title != "Dropped storage error" {
		t.Fatalf("candidate title = %q", bundle.PublishCandidates[1].Title)
	}
	if bundle.PublishCandidates[1].Body != "The returned storage error is ignored and the request still reports success." {
		t.Fatalf("candidate body = %q", bundle.PublishCandidates[1].Body)
	}
	if bundle.PublishCandidates[1].Location.Path != "internal/service/handler.go" {
		t.Fatalf("candidate path = %q", bundle.PublishCandidates[1].Location.Path)
	}
}
