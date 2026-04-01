package reviewcore

import (
	"context"
	"testing"

	"github.com/mreviewer/mreviewer/internal/llm"
	"github.com/mreviewer/mreviewer/internal/reviewpack"
)

type fakePackRunner struct {
	calls []string
}

func (f *fakePackRunner) Run(_ context.Context, input ReviewInput, pack reviewpack.CapabilityPack) (ReviewerArtifact, error) {
	f.calls = append(f.calls, pack.ID)
	return ReviewerArtifact{
		ReviewerID:   pack.ID,
		ReviewerType: "pack",
		Verdict:      VerdictRequestedChanges,
		Findings: []Finding{
			{
				Title:    pack.ID + " finding",
				Category: pack.ID + ".issue",
				Claim:    "reviewed " + input.Target.Repository,
			},
		},
	}, nil
}

type fakeJudge struct {
	artifacts []ReviewerArtifact
}

func (f *fakeJudge) Decide(target ReviewTarget, artifacts []ReviewerArtifact) ReviewBundle {
	f.artifacts = append([]ReviewerArtifact(nil), artifacts...)
	return ReviewBundle{
		Target:       target,
		Artifacts:    append([]ReviewerArtifact(nil), artifacts...),
		JudgeVerdict: VerdictRequestedChanges,
	}
}

func TestEngineRunExecutesSelectedPacksAndReturnsBundle(t *testing.T) {
	runner := &fakePackRunner{}
	judge := &fakeJudge{}
	engine := NewEngine(reviewpack.DefaultPacks(), runner, judge)

	input := ReviewInput{
		Target: ReviewTarget{
			Platform:   PlatformGitLab,
			Repository: "group/proj",
			Number:     17,
			URL:        "https://gitlab.example.com/group/proj/-/merge_requests/17",
		},
	}

	bundle, err := engine.Run(context.Background(), input, []string{"security", "database"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 pack executions, got %#v", runner.calls)
	}
	if len(judge.artifacts) != 2 {
		t.Fatalf("expected judge to receive 2 artifacts, got %d", len(judge.artifacts))
	}
	if bundle.Target.Repository != "group/proj" {
		t.Fatalf("unexpected bundle target: %#v", bundle.Target)
	}
}

func TestArtifactFromLegacyResultBridgesLegacyFinding(t *testing.T) {
	artifact := ArtifactFromLegacyResult("security", llm.ReviewResult{
		Summary: "Legacy provider summary",
		Findings: []llm.ReviewFinding{
			{
				Category:     "security.sql_injection",
				Severity:     "high",
				Confidence:   0.91,
				Title:        "Raw SQL tenant lookup",
				BodyMarkdown: "User input reaches string-built SQL.",
				Path:         "repo/query.go",
				NewLine:      int32ptr(91),
			},
		},
	})

	if artifact.ReviewerID != "security" {
		t.Fatalf("expected reviewer id security, got %q", artifact.ReviewerID)
	}
	if len(artifact.Findings) != 1 {
		t.Fatalf("expected 1 bridged finding, got %d", len(artifact.Findings))
	}
	if artifact.Findings[0].Location == nil || artifact.Findings[0].Location.Path != "repo/query.go" {
		t.Fatalf("expected bridged location, got %#v", artifact.Findings[0].Location)
	}
	if artifact.Findings[0].Claim == "" {
		t.Fatal("expected bridged claim")
	}
}

func int32ptr(value int32) *int32 {
	return &value
}
