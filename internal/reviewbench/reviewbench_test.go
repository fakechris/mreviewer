package reviewbench

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewinput"
)

func writeCase(t *testing.T, root string, c Case) {
	t.Helper()
	dir := filepath.Join(root, strings.ReplaceAll(c.ID, "/", "__"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(dir, "case.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func sampleCase() Case {
	return Case{
		ID: "aacr/acme__repo__1", Kind: KindGolden, AddedAt: "2026-10-25",
		PR:    CasePR{URL: "https://github.com/acme/repo/pull/1", Owner: "acme", Repo: "repo", Number: 1, BaseSHA: "base", HeadSHA: "head", Language: "Go", ChangedLines: 3},
		Diffs: []CaseDiff{{OldPath: "a.go", NewPath: "a.go", Diff: "@@ -1,3 +1,4 @@\n package a\n+func f() { var p *int; _ = *p }\n x := 1\n y := 2"}},
		Golden: []Comment{
			{Path: "a.go", Side: "right", FromLine: 2, ToLine: 2, Category: "Code Defect", Note: "nil pointer dereference of p"},
			{Path: "a.go", Side: "right", FromLine: 20, ToLine: 20, Category: "Maintainability", Note: "y is unused"},
		},
		Negatives: []Comment{{Path: "a.go", Side: "right", FromLine: 3, ToLine: 3, Note: "x should be const (incorrect)"}},
	}
}

type fakeJudge struct {
	same   func(Finding, Comment) bool
	errOn  string
	scores map[string]int
}

func (f fakeJudge) Model() string { return "fake-judge" }
func (f fakeJudge) SameIssue(_ context.Context, fd Finding, c Comment) (bool, string, error) {
	if f.errOn != "" && strings.Contains(fd.Title, f.errOn) {
		return false, "", errors.New("boom")
	}
	return f.same(fd, c), "fake", nil
}
func (f fakeJudge) Naturalness(_ context.Context, fd Finding) (NaturalnessScore, error) {
	s, ok := f.scores[fd.Title]
	if !ok {
		s = 4
	}
	return NaturalnessScore{Score: s, CodeTermsPreserved: s >= 3}, nil
}

func TestLoadCasesFiltersBySinceAndValidates(t *testing.T) {
	root := t.TempDir()
	old := sampleCase()
	old.ID, old.AddedAt = "aacr/old", "2026-01-01"
	writeCase(t, root, old)
	writeCase(t, root, sampleCase())
	bad := sampleCase()
	bad.ID, bad.Kind = "control/bad", KindControl
	writeCase(t, root, bad)

	if _, err := LoadCases(root, ""); err == nil || !strings.Contains(err.Error(), "control case must not carry golden") {
		t.Fatalf("expected control validation error, got %v", err)
	}
	_ = os.RemoveAll(filepath.Join(root, "control__bad"))
	all, err := LoadCases(root, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("LoadCases all = %d, %v", len(all), err)
	}
	recent, err := LoadCases(root, "2026-06-01")
	if err != nil || len(recent) != 1 || recent[0].ID != "aacr/acme__repo__1" {
		t.Fatalf("LoadCases since = %+v, %v", recent, err)
	}
}

func TestSnapshotMirrorsCase(t *testing.T) {
	s := sampleCase().Snapshot()
	if s.Target.Platform != core.PlatformGitHub || s.Target.Repository != "acme/repo" || s.Target.ChangeNumber != 1 {
		t.Fatalf("target = %+v", s.Target)
	}
	if s.Version.BaseSHA != "base" || s.Version.HeadSHA != "head" || len(s.Diffs) != 1 || s.Diffs[0].NewPath != "a.go" {
		t.Fatalf("snapshot = %+v", s)
	}
}

func TestMatchCaseWindowOnlyAndWithJudge(t *testing.T) {
	c := sampleCase()
	findings := []Finding{
		{Path: "a.go", StartLine: 2, EndLine: 2, Title: "nil deref", Body: "p is nil"},   // TP for golden 0
		{Path: "a.go", StartLine: 3, EndLine: 3, Title: "make x const", Body: "x const"}, // hits negative
		{Path: "a.go", StartLine: 40, EndLine: 40, Title: "far away", Body: "unrelated"}, // FP
		{Path: "b.go", StartLine: 4, EndLine: 4, Title: "wrong file", Body: "nope"},      // FP
	}
	m := matchCase(context.Background(), nil, c, findings, 0)
	if m.TruePositive != 1 || m.NegativeHits != 1 {
		t.Fatalf("window-only tp=%d neg=%d, want 1/1", m.TruePositive, m.NegativeHits)
	}
	outcomes := map[int]string{}
	for _, rec := range m.Records {
		outcomes[rec.FindingIndex] = rec.Outcome
	}
	if outcomes[0] != "true_positive" || outcomes[1] != "negative_hit" || outcomes[2] != "false_positive" || outcomes[3] != "false_positive" {
		t.Fatalf("outcomes = %v", outcomes)
	}

	// A keyword judge that additionally rejects the nil-deref pair: the
	// nil finding becomes a false positive, the const finding still hits the
	// known-incorrect comment, and every candidate pair costs one judge call.
	keyword := func(f Finding, c Comment) bool {
		note := strings.ToLower(c.Note)
		for _, w := range strings.Fields(strings.ToLower(f.Title)) {
			if len(w) > 1 && strings.Contains(note, w) {
				return true
			}
		}
		return false
	}
	judge := fakeJudge{same: func(f Finding, c Comment) bool { return keyword(f, c) && !strings.Contains(f.Title, "nil") }}
	m = matchCase(context.Background(), judge, c, findings, 0)
	if m.TruePositive != 0 || m.NegativeHits != 1 || m.JudgeCalls != 3 {
		t.Fatalf("judged tp=%d neg=%d calls=%d", m.TruePositive, m.NegativeHits, m.JudgeCalls)
	}
	// A judge error on one pair counts as no match for that pair and is
	// recorded; other pairs are still judged.
	judge = fakeJudge{same: keyword, errOn: "nil"}
	m = matchCase(context.Background(), judge, c, findings, 0)
	if m.TruePositive != 0 || m.NegativeHits != 1 || m.JudgeErrors != 1 {
		t.Fatalf("judge error tp=%d neg=%d errors=%d", m.TruePositive, m.NegativeHits, m.JudgeErrors)
	}
}

func TestMatchCaseEachReferenceUsedOnce(t *testing.T) {
	c := sampleCase()
	c.Golden = c.Golden[:1] // one golden at line 2
	findings := []Finding{
		{Path: "a.go", StartLine: 2, Title: "a"},
		{Path: "a.go", StartLine: 2, Title: "b"},
	}
	m := matchCase(context.Background(), nil, c, findings, 0)
	if m.TruePositive != 1 {
		t.Fatalf("two findings on one golden must count once, got %d", m.TruePositive)
	}
}

type fakeEngine struct {
	findings map[string][]core.PublishCandidate
}

func (e fakeEngine) Run(_ context.Context, input core.ReviewInput, opts core.RunOptions) (core.ReviewBundle, error) {
	if opts.RouteOverride != "test-route" {
		return core.ReviewBundle{}, errors.New("route not propagated")
	}
	return core.ReviewBundle{
		Verdict:           "requested_changes",
		Artifacts:         []core.ReviewerArtifact{{ReviewerID: "security", TokensTotal: 1200}, {ReviewerID: "architecture", TokensTotal: 800}},
		PublishCandidates: e.findings[input.Target.URL],
		Coverage:          &core.CoverageSummary{Ratio: 1},
	}, nil
}

type fakeBuilder struct{}

func (fakeBuilder) Build(_ context.Context, in reviewinput.BuildInput) (core.ReviewInput, error) {
	return core.ReviewInput{Target: in.Snapshot.Target}, nil
}

func TestRunnerProducesMetricsAndControlGroup(t *testing.T) {
	golden := sampleCase()
	control := Case{ID: "control/clean", Kind: KindControl, PR: CasePR{URL: "https://github.com/acme/repo/pull/2", Language: "Go"}}
	zh := sampleCase()
	zh.ID, zh.Kind, zh.PR.URL = "zh/one", KindChinese, "https://github.com/acme/repo/pull/3"
	engine := fakeEngine{findings: map[string][]core.PublishCandidate{
		golden.PR.URL: {
			{Kind: "summary", Body: "s"},
			{Kind: "finding", Title: "nil deref", Severity: "high", Location: core.CanonicalLocation{Path: "a.go", Side: core.DiffSideNew, StartLine: 2, EndLine: 2}},
			{Kind: "finding", Title: "noise", Severity: "low", Location: core.CanonicalLocation{Path: "a.go", Side: core.DiffSideNew, StartLine: 300, EndLine: 300}, OutOfDiff: true, PublishAsSummary: true},
		},
		control.PR.URL: {{Kind: "finding", Title: "fp", Location: core.CanonicalLocation{Path: "z.go", StartLine: 1}}},
		zh.PR.URL:      {{Kind: "finding", Title: "翻译腔", Body: "该变量 指针 可能为空", Location: core.CanonicalLocation{Path: "a.go", StartLine: 2}}},
	}}
	judge := fakeJudge{same: func(Finding, Comment) bool { return true }, scores: map[string]int{"翻译腔": 2}}
	runner := &Runner{Engine: engine, Builder: fakeBuilder{}, Judge: judge, Opts: Options{Route: "test-route", NaturalnessPerCase: 5, Concurrency: 2}}
	report, err := runner.Run(context.Background(), []Case{golden, control, zh})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Errors != 0 {
		t.Fatalf("errors = %d: %+v", report.Errors, report.Cases)
	}
	tot := report.Totals
	// golden: 2 findings (1 TP), zh: 1 finding (1 TP); golden refs 2+2.
	if tot.Cases != 2 || tot.Findings != 3 || tot.TruePositive != 2 || tot.Golden != 4 {
		t.Fatalf("totals = %+v", tot)
	}
	if tot.Precision < 0.66 || tot.Precision > 0.67 || tot.Recall != 0.5 {
		t.Fatalf("precision/recall = %v/%v", tot.Precision, tot.Recall)
	}
	if tot.TokensPer != 2000 {
		t.Fatalf("tokens per case = %v, want 2000", tot.TokensPer)
	}
	if report.Control.Cases != 1 || report.Control.Findings != 1 {
		t.Fatalf("control = %+v", report.Control)
	}
	if report.Naturalness == nil || report.Naturalness.Scored != 3 || report.Naturalness.TermsBroken != 1 {
		t.Fatalf("naturalness = %+v", report.Naturalness)
	}
	if report.JudgeModel != "fake-judge" || report.ByLanguage["Go"].Cases != 2 {
		t.Fatalf("report meta = %+v", report)
	}
	var out *CaseResult
	for i := range report.Cases {
		if report.Cases[i].CaseID == golden.ID {
			out = &report.Cases[i]
		}
	}
	if out == nil || out.OutOfDiff != 1 || out.Verdict != "requested_changes" || out.Coverage != 1 {
		t.Fatalf("golden case result = %+v", out)
	}
	md := report.Markdown()
	for _, want := range []string{"## Totals", "| Precision | 66.7% |", "## By language", "control/clean", "Chinese naturalness"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestRunnerScoresExternalFindingsWithoutEngine(t *testing.T) {
	c := sampleCase()
	runner := &Runner{Opts: Options{External: ExternalFindings{c.ID: {{Path: "a.go", StartLine: 20, Title: "unused y"}}}}}
	report, err := runner.Run(context.Background(), []Case{c})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.External || report.Totals.TruePositive != 1 || report.Totals.Findings != 1 {
		t.Fatalf("report = %+v", report.Totals)
	}
	if _, err := (&Runner{}).Run(context.Background(), []Case{c}); err == nil {
		t.Fatal("expected error without engine")
	}
}

func TestExtractJSONAndMessageText(t *testing.T) {
	if got := extractJSON("```json\n{\"same\": true}\n```"); got != `{"same": true}` {
		t.Fatalf("extractJSON = %q", got)
	}
	if got := messageText(json.RawMessage(`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`)); got != "ab" {
		t.Fatalf("messageText parts = %q", got)
	}
	if got := messageText(json.RawMessage(`"plain"`)); got != "plain" {
		t.Fatalf("messageText string = %q", got)
	}
}

func TestVendoredAACRCasesLoadAndAnchorInsideDiff(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "review-bench", "aacr")
	cases, err := LoadCases(root, "")
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	if len(cases) < 30 {
		t.Fatalf("vendored cases = %d, want >= 30", len(cases))
	}
	langs := map[string]bool{}
	golden := 0
	for _, c := range cases {
		langs[c.PR.Language] = true
		golden += len(c.Golden)
		if len(c.Diffs) == 0 || len(c.Golden) == 0 {
			t.Fatalf("case %s has no diffs or golden", c.ID)
		}
	}
	if len(langs) < 5 || golden < 100 {
		t.Fatalf("languages = %d golden = %d", len(langs), golden)
	}
}

func TestLoadExternalFindingsAcceptsPreviousReport(t *testing.T) {
	dir := t.TempDir()
	report := Report{Cases: []CaseResult{{CaseID: "aacr/x", RawFindings: []Finding{{Path: "a.go", StartLine: 2, Title: "t"}}}}}
	data, _ := json.Marshal(report)
	path := filepath.Join(dir, "report.json")
	_ = os.WriteFile(path, data, 0o644)
	ext, err := LoadExternalFindings(path)
	if err != nil || len(ext["aacr/x"]) != 1 || ext["aacr/x"][0].StartLine != 2 {
		t.Fatalf("ext = %+v, err = %v", ext, err)
	}
	plain := filepath.Join(dir, "plain.json")
	_ = os.WriteFile(plain, []byte(`{"aacr/y":[{"path":"b.go","start_line":9}]}`), 0o644)
	ext, err = LoadExternalFindings(plain)
	if err != nil || len(ext["aacr/y"]) != 1 {
		t.Fatalf("plain ext = %+v, err = %v", ext, err)
	}
}
