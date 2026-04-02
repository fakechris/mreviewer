package reviewruntime

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

func TestIsGitHubRuntimeRunLogsMalformedScopeJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	if IsGitHubRuntimeRun(db.ReviewRun{ScopeJson: []byte("{bad-json")}, logger) {
		t.Fatal("malformed scope json should not be treated as github")
	}
	if got := buf.String(); !strings.Contains(got, "runtime scope json") {
		t.Fatalf("log output = %q, want runtime scope json warning", got)
	}
}

func TestNormalizeReviewPacks(t *testing.T) {
	got := NormalizeReviewPacks([]string{" Security ", "database", "security", ""})
	want := []string{"security", "database"}
	if len(got) != len(want) {
		t.Fatalf("NormalizeReviewPacks len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("NormalizeReviewPacks[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestJudgeSummary(t *testing.T) {
	summary := JudgeSummary(core.JudgeDecision{
		Verdict: "requested_changes",
		MergedFindings: []core.Finding{{
			Severity: "high",
			Title:    "Broken authz boundary",
		}},
	})
	if !strings.Contains(summary, "Verdict: requested_changes") {
		t.Fatalf("summary = %q, want verdict", summary)
	}
	if !strings.Contains(summary, "[HIGH] Broken authz boundary") {
		t.Fatalf("summary = %q, want merged finding", summary)
	}
}

func TestJudgeSummaryIncludesVerdictWithoutFindings(t *testing.T) {
	summary := JudgeSummary(core.JudgeDecision{Verdict: "approve"})
	if !strings.Contains(summary, "Verdict: approve") {
		t.Fatalf("summary = %q, want verdict", summary)
	}
	if !strings.Contains(summary, "No review findings detected.") {
		t.Fatalf("summary = %q, want empty finding message", summary)
	}
}
