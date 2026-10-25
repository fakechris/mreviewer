package reviewcore

import (
	"context"
	"testing"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
)

// fixtureRequest: one file with two hunks.
//
//	hunk 1 (@@ -10,4 +10,5 @@): old 10-13, new 10-14; new 12 and 13 added,
//	old 11 removed (replaced by the additions).
//	hunk 2 (@@ -40,3 +41,2 @@): old 40-42, new 41-42; old 41 deleted with no
//	replacement, so new 41 and 42 are deletion anchors.
func fixtureRequest() ctxpkg.ReviewRequest {
	return ctxpkg.ReviewRequest{Changes: []ctxpkg.Change{{
		Path:    "internal/svc/handler.go",
		OldPath: "internal/svc/handler.go",
		Hunks: []ctxpkg.Hunk{
			{
				OldStart: 10, OldLines: 4, NewStart: 10, NewLines: 5,
				Patch: "@@ -10,4 +10,5 @@\n ctx\n-old := 1\n+new := 1\n+extra := 2\n tail\n end",
			},
			{
				OldStart: 40, OldLines: 3, NewStart: 41, NewLines: 2,
				Patch: "@@ -40,3 +41,2 @@\n keep\n-removed()\n after",
			},
		},
	}, {
		Path: "docs/collapsed.md", Collapsed: true, // no hunks: unknown
	}}}
}

func TestChangedLineIndexVerify(t *testing.T) {
	idx := buildChangedLineIndex(fixtureRequest())
	path := "internal/svc/handler.go"
	cases := []struct {
		name string
		loc  CanonicalLocation
		want locationVerdict
	}{
		{"added line on new side", CanonicalLocation{Path: path, Side: DiffSideNew, StartLine: 11, EndLine: 11}, locationInDiff},
		{"second added line", CanonicalLocation{Path: path, Side: DiffSideNew, StartLine: 12, EndLine: 12}, locationInDiff},
		{"removed line on old side", CanonicalLocation{Path: path, Side: DiffSideOld, StartLine: 11, EndLine: 11}, locationInDiff},
		{"deletion anchor before", CanonicalLocation{Path: path, Side: DiffSideNew, StartLine: 41}, locationInDiff},
		{"deletion anchor after", CanonicalLocation{Path: path, Side: DiffSideNew, StartLine: 42}, locationInDiff},
		{"context line inside hunk is not changed", CanonicalLocation{Path: path, Side: DiffSideNew, StartLine: 10}, locationOutOfDiff},
		{"line far outside any hunk", CanonicalLocation{Path: path, Side: DiffSideNew, StartLine: 300}, locationOutOfDiff},
		{"old side line never touched", CanonicalLocation{Path: path, Side: DiffSideOld, StartLine: 12}, locationOutOfDiff},
		{"range spanning two hunks", CanonicalLocation{Path: path, Side: DiffSideNew, StartLine: 11, EndLine: 42}, locationOutOfDiff},
		{"range inside one hunk", CanonicalLocation{Path: path, Side: DiffSideNew, StartLine: 11, EndLine: 13}, locationInDiff},
		{"file not in request", CanonicalLocation{Path: "other.go", Side: DiffSideNew, StartLine: 1}, locationUnknown},
		{"collapsed file", CanonicalLocation{Path: "docs/collapsed.md", Side: DiffSideNew, StartLine: 1}, locationUnknown},
		{"no line", CanonicalLocation{Path: path, Side: DiffSideNew}, locationUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := idx.verify(tc.loc); got != tc.want {
				t.Fatalf("verify(%+v) = %v, want %v", tc.loc, got, tc.want)
			}
		})
	}
}

func TestChangedLineIndexTruncatedHunkIsSpanOnly(t *testing.T) {
	idx := buildChangedLineIndex(ctxpkg.ReviewRequest{Changes: []ctxpkg.Change{{
		Path: "big.go",
		Hunks: []ctxpkg.Hunk{{
			OldStart: 100, OldLines: 50, NewStart: 100, NewLines: 60, Truncated: true,
			Patch: "@@ -100,50 +100,60 @@\n+only first line kept",
		}},
	}}})
	if got := idx.verify(CanonicalLocation{Path: "big.go", Side: DiffSideNew, StartLine: 150}); got != locationInDiff {
		t.Fatalf("line inside truncated hunk span = %v, want in-diff", got)
	}
	if got := idx.verify(CanonicalLocation{Path: "big.go", Side: DiffSideNew, StartLine: 161}); got != locationOutOfDiff {
		t.Fatalf("line past truncated hunk span = %v, want out-of-diff", got)
	}
}

func TestEngineDowngradesOutOfDiffFindingToSummary(t *testing.T) {
	mk := func(line int) Finding {
		return Finding{
			Category: "bug", Severity: "high", Title: "nil deref", Body: "x may be nil",
			Identity: FindingIdentityInput{
				Category: "bug", NormalizedClaim: "x may be nil", EvidenceFingerprint: "nil-x",
				Location: CanonicalLocation{Path: "internal/svc/handler.go", Side: DiffSideNew, StartLine: line, EndLine: line},
			},
		}
	}
	engine := NewEngine(nil, judgeFunc(func(_ []ReviewerArtifact) JudgeDecision {
		return JudgeDecision{Verdict: "requested_changes", MergedFindings: []Finding{mk(11), mk(300)}}
	}))
	bundle, err := engine.Run(context.Background(), ReviewInput{
		Target:  ReviewTarget{Platform: PlatformGitHub},
		Request: fixtureRequest(),
	}, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(bundle.PublishCandidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(bundle.PublishCandidates))
	}
	inline := bundle.PublishCandidates[0]
	if inline.PublishAsSummary || inline.OutOfDiff {
		t.Fatalf("in-diff finding should stay inline: %+v", inline)
	}
	down := bundle.PublishCandidates[1]
	if !down.PublishAsSummary || !down.OutOfDiff {
		t.Fatalf("out-of-diff finding should be downgraded: %+v", down)
	}
	if down.Location.StartLine != 300 {
		t.Fatalf("original location must be kept for diagnostics, got %+v", down.Location)
	}
	if down.Severity != "high" {
		t.Fatalf("severity must be preserved, got %q", down.Severity)
	}
	if !containsAll(down.Body, "### nil deref", "handler.go:300", "不在本次改动范围内") {
		t.Fatalf("body = %q", down.Body)
	}
}

func TestEngineDoesNotDowngradeWhenRequestHasNoHunks(t *testing.T) {
	engine := NewEngine(nil, judgeFunc(func(_ []ReviewerArtifact) JudgeDecision {
		return JudgeDecision{MergedFindings: []Finding{{
			Title: "t", Body: "b", Severity: "low",
			Identity: FindingIdentityInput{Location: CanonicalLocation{Path: "a.go", Side: DiffSideNew, StartLine: 5, EndLine: 5}},
		}}}
	}))
	bundle, err := engine.Run(context.Background(), ReviewInput{Target: ReviewTarget{Platform: PlatformGitHub}}, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c := bundle.PublishCandidates[0]; c.PublishAsSummary || c.OutOfDiff {
		t.Fatalf("unknown coverage must not downgrade: %+v", c)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
