package context

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gitlab"
)

func TestGeneratedFileFilter(t *testing.T) {
	input := defaultAssembleInput()
	input.Diffs = []gitlab.MergeRequestDiff{
		{
			OldPath:       "src/generated.pb.go",
			NewPath:       "src/generated.pb.go",
			GeneratedFile: true,
			Diff:          sampleDiff("generated"),
		},
		{
			OldPath: "src/app.go",
			NewPath: "src/app.go",
			Diff:    sampleDiff("safe"),
		},
	}

	result, err := NewAssembler().Assemble(input)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(result.Request.Changes) != 1 {
		t.Fatalf("len(changes) = %d, want 1", len(result.Request.Changes))
	}
	if got := result.Request.Changes[0].Path; got != "src/app.go" {
		t.Fatalf("included path = %q, want src/app.go", got)
	}
	if len(result.Excluded) != 1 {
		t.Fatalf("len(excluded) = %d, want 1", len(result.Excluded))
	}
	if result.Excluded[0].Reason != ExcludedReasonGenerated {
		t.Fatalf("excluded reason = %q, want %q", result.Excluded[0].Reason, ExcludedReasonGenerated)
	}
}

func TestBinaryVendorLockFilter(t *testing.T) {
	input := defaultAssembleInput()
	input.Diffs = []gitlab.MergeRequestDiff{
		{
			OldPath: "vendor/github.com/acme/lib.go",
			NewPath: "vendor/github.com/acme/lib.go",
			Diff:    sampleDiff("vendor"),
		},
		{
			OldPath: "assets/logo.png",
			NewPath: "assets/logo.png",
			Diff:    "Binary files a/assets/logo.png and b/assets/logo.png differ",
		},
		{
			OldPath: "go.sum",
			NewPath: "go.sum",
			Diff:    sampleDiff("lock"),
		},
		{
			OldPath:  "data/huge.json",
			NewPath:  "data/huge.json",
			TooLarge: true,
		},
		{
			OldPath: "cmd/worker/main.go",
			NewPath: "cmd/worker/main.go",
			Diff:    sampleDiff("worker"),
		},
	}

	result, err := NewAssembler().Assemble(input)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(result.Request.Changes) != 1 {
		t.Fatalf("len(changes) = %d, want 1", len(result.Request.Changes))
	}
	if got := result.Request.Changes[0].Path; got != "cmd/worker/main.go" {
		t.Fatalf("included path = %q, want cmd/worker/main.go", got)
	}

	gotReasons := map[string]string{}
	for _, excluded := range result.Excluded {
		gotReasons[excluded.Path] = excluded.Reason
	}

	if gotReasons["vendor/github.com/acme/lib.go"] != ExcludedReasonVendor {
		t.Fatalf("vendor reason = %q, want %q", gotReasons["vendor/github.com/acme/lib.go"], ExcludedReasonVendor)
	}
	if gotReasons["assets/logo.png"] != ExcludedReasonBinary {
		t.Fatalf("binary reason = %q, want %q", gotReasons["assets/logo.png"], ExcludedReasonBinary)
	}
	if gotReasons["go.sum"] != ExcludedReasonLockFile {
		t.Fatalf("lock reason = %q, want %q", gotReasons["go.sum"], ExcludedReasonLockFile)
	}
	if gotReasons["data/huge.json"] != ExcludedReasonTooLarge {
		t.Fatalf("too_large reason = %q, want %q", gotReasons["data/huge.json"], ExcludedReasonTooLarge)
	}
}

func TestPathIncludeExclude(t *testing.T) {
	policy := &db.ProjectPolicy{
		IncludePaths: mustRawJSON(t, []string{"src/**"}),
		ExcludePaths: mustRawJSON(t, []string{"src/generated/**", "**/*.md"}),
	}

	settings, err := SettingsFromPolicy(policy)
	if err != nil {
		t.Fatalf("SettingsFromPolicy: %v", err)
	}

	input := defaultAssembleInput()
	input.Settings = settings
	input.Diffs = []gitlab.MergeRequestDiff{
		{OldPath: "src/service/app.go", NewPath: "src/service/app.go", Diff: sampleDiff("service")},
		{OldPath: "src/generated/schema.go", NewPath: "src/generated/schema.go", Diff: sampleDiff("generated")},
		{OldPath: "docs/readme.md", NewPath: "docs/readme.md", Diff: sampleDiff("docs")},
		{OldPath: "internal/runtime.go", NewPath: "internal/runtime.go", Diff: sampleDiff("internal")},
	}

	result, err := NewAssembler().Assemble(input)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(result.Request.Changes) != 1 {
		t.Fatalf("len(changes) = %d, want 1", len(result.Request.Changes))
	}
	if got := result.Request.Changes[0].Path; got != "src/service/app.go" {
		t.Fatalf("included path = %q, want src/service/app.go", got)
	}

	gotReasons := map[string]string{}
	for _, excluded := range result.Excluded {
		gotReasons[excluded.Path] = excluded.Reason
	}
	if gotReasons["src/generated/schema.go"] != ExcludedReasonPathExcluded {
		t.Fatalf("exclude reason = %q, want %q", gotReasons["src/generated/schema.go"], ExcludedReasonPathExcluded)
	}
	if gotReasons["docs/readme.md"] != ExcludedReasonPathNotIncluded {
		t.Fatalf("include reason = %q, want %q", gotReasons["docs/readme.md"], ExcludedReasonPathNotIncluded)
	}
	if gotReasons["internal/runtime.go"] != ExcludedReasonPathNotIncluded {
		t.Fatalf("internal include reason = %q, want %q", gotReasons["internal/runtime.go"], ExcludedReasonPathNotIncluded)
	}
}

func TestHunkContextAssembly(t *testing.T) {
	input := defaultAssembleInput()
	input.Settings.ContextLinesBefore = 2
	input.Settings.ContextLinesAfter = 2
	input.Diffs = []gitlab.MergeRequestDiff{{
		OldPath: "src/auth/session.ts",
		NewPath: "src/auth/session.ts",
		Diff: strings.Join([]string{
			"@@ -10,6 +10,7 @@ function demo() {",
			" context a",
			" context b",
			"-old line",
			"+new line 1",
			"+new line 2",
			" context c",
			" context d",
			" context e",
		}, "\n"),
	}}

	result, err := NewAssembler().Assemble(input)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(result.Request.Changes) != 1 {
		t.Fatalf("len(changes) = %d, want 1", len(result.Request.Changes))
	}
	change := result.Request.Changes[0]
	if len(change.Hunks) != 1 {
		t.Fatalf("len(hunks) = %d, want 1", len(change.Hunks))
	}
	hunk := change.Hunks[0]
	if hunk.OldStart != 10 || hunk.NewStart != 10 {
		t.Fatalf("hunk starts = (%d,%d), want (10,10)", hunk.OldStart, hunk.NewStart)
	}
	if hunk.ChangedLines != 3 {
		t.Fatalf("changed_lines = %d, want 3", hunk.ChangedLines)
	}
	if got, want := hunk.ContextBefore, []string{"context a", "context b"}; !equalStrings(got, want) {
		t.Fatalf("context before = %#v, want %#v", got, want)
	}
	if got, want := hunk.ContextAfter, []string{"context c", "context d"}; !equalStrings(got, want) {
		t.Fatalf("context after = %#v, want %#v", got, want)
	}
	if !strings.Contains(hunk.Patch, "@@ -10,6 +10,7 @@") {
		t.Fatalf("patch = %q, want hunk header", hunk.Patch)
	}
}

func TestHistoricalBotContext(t *testing.T) {
	store := &fakeHistoricalStore{
		findings: []db.ReviewFinding{
			{
				ID:                  11,
				Path:                "src/auth/session.ts",
				Title:               "Missing null guard",
				SemanticFingerprint: "sf_abc",
				BodyMarkdown:        sql.NullString{String: "Handle nil session before dereference.", Valid: true},
			},
		},
		discussions: map[int64]db.GitlabDiscussion{
			11: {
				GitlabDiscussionID: "discussion-123",
				DiscussionType:     "diff",
				Resolved:           false,
			},
		},
	}

	historical, err := LoadHistoricalContext(context.Background(), store, 99)
	if err != nil {
		t.Fatalf("LoadHistoricalContext: %v", err)
	}

	input := defaultAssembleInput()
	input.HistoricalContext = historical
	input.Diffs = []gitlab.MergeRequestDiff{{OldPath: "src/auth/session.ts", NewPath: "src/auth/session.ts", Diff: sampleDiff("safe")}}

	result, err := NewAssembler().Assemble(input)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if len(result.Request.HistoricalContext.ActiveBotFindings) != 1 {
		t.Fatalf("len(active_bot_findings) = %d, want 1", len(result.Request.HistoricalContext.ActiveBotFindings))
	}
	got := result.Request.HistoricalContext.ActiveBotFindings[0]
	if got.SemanticFingerprint != "sf_abc" || got.DiscussionID != "discussion-123" {
		t.Fatalf("historical finding = %+v, want fingerprint/discussion populated", got)
	}
}

func TestLargeDiffTruncation(t *testing.T) {
	input := defaultAssembleInput()
	input.Settings.MaxChangedLines = 3
	input.Diffs = []gitlab.MergeRequestDiff{
		{OldPath: "src/a.go", NewPath: "src/a.go", Diff: sampleDiffWithChangedLines("a", 2)},
		{OldPath: "src/b.go", NewPath: "src/b.go", Diff: sampleDiffWithChangedLines("b", 2)},
	}

	result, err := NewAssembler().Assemble(input)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if !result.Truncated {
		t.Fatal("expected result to be truncated")
	}
	if result.TotalChangedLines != 3 {
		t.Fatalf("total changed lines = %d, want 3", result.TotalChangedLines)
	}
	if len(result.Request.Changes) != 2 {
		t.Fatalf("len(changes) = %d, want 2 with partial second change", len(result.Request.Changes))
	}
	if !result.Request.Changes[1].Truncated {
		t.Fatal("expected second change to be marked truncated")
	}
	if strings.Contains(result.Request.Changes[1].Hunks[0].Patch, "+b-new-2") {
		t.Fatalf("truncated patch still contains over-limit change: %q", result.Request.Changes[1].Hunks[0].Patch)
	}
}

func TestOutboundScopeLimit(t *testing.T) {
	policy := &db.ProjectPolicy{
		IncludePaths: mustRawJSON(t, []string{"src/**"}),
	}
	settings, err := SettingsFromPolicy(policy)
	if err != nil {
		t.Fatalf("SettingsFromPolicy: %v", err)
	}

	input := defaultAssembleInput()
	input.Settings = settings
	input.Rules = TrustedRules{
		PlatformPolicy: "Only review included diffs.",
		ProjectPolicy:  "Focus on correctness and security.",
		ReviewMarkdown: "# Review Guidelines\n- stay in scope",
		RulesDigest:    "sha256:trusted",
	}
	input.Diffs = []gitlab.MergeRequestDiff{
		{OldPath: "src/app.go", NewPath: "src/app.go", Diff: sampleDiff("safe")},
		{OldPath: "secrets/.env", NewPath: "secrets/.env", Diff: strings.Join([]string{
			"@@ -0,0 +1,1 @@",
			"+API_KEY=super-secret-token",
		}, "\n")},
		{OldPath: "vendor/acme/lib.go", NewPath: "vendor/acme/lib.go", Diff: sampleDiff("vendor-secret")},
	}

	result, err := NewAssembler().Assemble(input)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	transport := &captureTransport{}
	if err := transport.Send(result.Request); err != nil {
		t.Fatalf("Send: %v", err)
	}

	payload := string(transport.body)
	for _, want := range []string{"Only review included diffs.", "Focus on correctness and security.", "# Review Guidelines", "src/app.go"} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %q: %s", want, payload)
		}
	}
	for _, banned := range []string{"super-secret-token", "secrets/.env", "vendor/acme/lib.go"} {
		if strings.Contains(payload, banned) {
			t.Fatalf("payload unexpectedly contains %q: %s", banned, payload)
		}
	}
}

func defaultAssembleInput() AssembleInput {
	return AssembleInput{
		ReviewRunID: 42,
		Project: ProjectContext{
			ProjectID: 1001,
			FullPath:  "group/repo",
		},
		MergeRequest: MergeRequestContext{
			IID:         7,
			Title:       "Test MR",
			Description: "desc",
			Author:      "alice",
		},
		Version: VersionContext{
			BaseSHA:    "base-sha",
			StartSHA:   "start-sha",
			HeadSHA:    "head-sha",
			PatchIDSHA: "patch-sha",
		},
		Settings: DefaultPolicySettings(),
		Rules: TrustedRules{
			RulesDigest: "sha256:default",
		},
	}
}

func sampleDiff(label string) string {
	return strings.Join([]string{
		"@@ -1,2 +1,2 @@",
		" context " + label,
		"-old " + label,
		"+new " + label,
	}, "\n")
}

func sampleDiffWithChangedLines(label string, changedLines int) string {
	lines := []string{"@@ -1,4 +1,4 @@", " context " + label}
	for i := 1; i <= changedLines; i++ {
		if i%2 == 1 {
			lines = append(lines, "-"+label+"-old-"+itoa(i))
		} else {
			lines = append(lines, "+"+label+"-new-"+itoa(i))
		}
	}
	lines = append(lines, " context tail "+label)
	return strings.Join(lines, "\n")
}

func mustRawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return data
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type fakeHistoricalStore struct {
	findings    []db.ReviewFinding
	discussions map[int64]db.GitlabDiscussion
	err         error
}

func (f *fakeHistoricalStore) ListActiveFindingsByMR(context.Context, int64) ([]db.ReviewFinding, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]db.ReviewFinding(nil), f.findings...), nil
}

func (f *fakeHistoricalStore) GetGitlabDiscussionByFinding(_ context.Context, reviewFindingID int64) (db.GitlabDiscussion, error) {
	if discussion, ok := f.discussions[reviewFindingID]; ok {
		return discussion, nil
	}
	return db.GitlabDiscussion{}, sql.ErrNoRows
}

type captureTransport struct {
	body []byte
}

func (c *captureTransport) Send(req ReviewRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.body = data
	return nil
}

func itoa(v int) string {
	return strconv.Itoa(v)
}

var _ HistoricalStore = (*fakeHistoricalStore)(nil)

func TestLoadHistoricalContextPropagatesStoreErrors(t *testing.T) {
	store := &fakeHistoricalStore{err: errors.New("boom")}
	_, err := LoadHistoricalContext(context.Background(), store, 1)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("LoadHistoricalContext error = %v, want boom", err)
	}
}
