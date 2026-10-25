package reviewcore

import (
	"context"
	"strings"
	"testing"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
)

func coverageInput(reviewed, total int) ReviewInput {
	return ReviewInput{
		Target: ReviewTarget{Platform: PlatformGitHub},
		Coverage: ctxpkg.CoverageReport{
			Mode:                 ctxpkg.ReviewModeDegradation,
			TotalChangedLines:    total,
			ReviewedChangedLines: reviewed,
			HunksTotal:           10,
			HunksInPrompt:        4,
			ReviewedPaths:        []string{"a.go"},
			Skipped:              []ctxpkg.SkippedScope{{Path: "big/gen.go", Reason: "scope_limit", ChangedLines: total - reviewed}},
		},
	}
}

func passingJudge(summary string) judgeFunc {
	return judgeFunc(func(_ []ReviewerArtifact) JudgeDecision {
		return JudgeDecision{Verdict: "pass", Summary: summary}
	})
}

func TestEngineMarksCleanReviewIncompleteWhenCoverageBelowThreshold(t *testing.T) {
	bundle, err := NewEngine(nil, passingJudge("looks fine")).Run(context.Background(), coverageInput(40, 100), RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if bundle.Verdict != VerdictIncomplete {
		t.Fatalf("verdict = %q, want incomplete", bundle.Verdict)
	}
	if bundle.Coverage == nil || !bundle.Coverage.Incomplete || bundle.Coverage.Ratio != 0.4 || bundle.Coverage.Threshold != DefaultCoverageThreshold {
		t.Fatalf("coverage = %+v", bundle.Coverage)
	}
	if bundle.Coverage.HunksTotal != 10 || bundle.Coverage.HunksInPrompt != 4 {
		t.Fatalf("hunks = %d/%d, want 4/10", bundle.Coverage.HunksInPrompt, bundle.Coverage.HunksTotal)
	}
	if !strings.Contains(bundle.MarkdownSummary, "looks fine") || !strings.Contains(bundle.MarkdownSummary, "未评审范围") || !strings.Contains(bundle.MarkdownSummary, "big/gen.go") {
		t.Fatalf("summary = %q", bundle.MarkdownSummary)
	}
	if len(bundle.PublishCandidates) != 1 || bundle.PublishCandidates[0].Kind != "summary" || !strings.Contains(bundle.PublishCandidates[0].Body, "40/100") {
		t.Fatalf("summary candidate = %+v", bundle.PublishCandidates)
	}
}

func TestEngineKeepsRequestedChangesWhenCoverageIncomplete(t *testing.T) {
	judge := judgeFunc(func(_ []ReviewerArtifact) JudgeDecision {
		return JudgeDecision{Verdict: "requested_changes", Summary: "found a bug"}
	})
	bundle, err := NewEngine(nil, judge).Run(context.Background(), coverageInput(10, 100), RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if bundle.Verdict != "requested_changes" {
		t.Fatalf("verdict = %q, want requested_changes kept", bundle.Verdict)
	}
	if bundle.Coverage == nil || !bundle.Coverage.Incomplete {
		t.Fatalf("coverage should still be flagged incomplete: %+v", bundle.Coverage)
	}
}

func TestEnginePassesWhenCoverageMeetsThreshold(t *testing.T) {
	bundle, err := NewEngine(nil, passingJudge("")).Run(context.Background(), coverageInput(85, 100), RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if bundle.Verdict != "pass" || bundle.Coverage == nil || bundle.Coverage.Incomplete {
		t.Fatalf("verdict = %q coverage = %+v, want pass", bundle.Verdict, bundle.Coverage)
	}
	if len(bundle.PublishCandidates) != 0 {
		t.Fatalf("no summary expected, got %+v", bundle.PublishCandidates)
	}
}

func TestEngineHonoursCoverageThresholdOption(t *testing.T) {
	bundle, err := NewEngine(nil, passingJudge("")).Run(context.Background(), coverageInput(85, 100), RunOptions{CoverageThreshold: 0.9})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if bundle.Verdict != VerdictIncomplete || bundle.Coverage.Threshold != 0.9 {
		t.Fatalf("verdict = %q threshold = %v, want incomplete at 0.9", bundle.Verdict, bundle.Coverage.Threshold)
	}
}

func TestEngineWithoutCoverageReportLeavesVerdictAlone(t *testing.T) {
	bundle, err := NewEngine(nil, passingJudge("")).Run(context.Background(), ReviewInput{Target: ReviewTarget{Platform: PlatformGitHub}}, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if bundle.Verdict != "pass" || bundle.Coverage != nil {
		t.Fatalf("verdict = %q coverage = %+v, want pass and nil coverage", bundle.Verdict, bundle.Coverage)
	}
}
