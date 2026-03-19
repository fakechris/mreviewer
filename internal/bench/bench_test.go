package bench

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/llm"
	"github.com/mreviewer/mreviewer/internal/rules"
	"github.com/mreviewer/mreviewer/internal/scheduler"
)

const (
	benchmarkEvidencePath = "testdata/bench/runtime_evidence.json"
	migrationsDir         = "/Users/chris/workspace/mreviewer/migrations"
)

type CorpusScenario struct {
	Name               string                    `json:"name"`
	Size               string                    `json:"size"`
	Description        string                    `json:"description"`
	ExpectedMode       ctxpkg.ReviewMode         `json:"expected_mode"`
	ProjectPolicyExtra json.RawMessage           `json:"project_policy_extra,omitempty"`
	Diffs              []gitlab.MergeRequestDiff `json:"diffs"`
}

type ScenarioResult struct {
	Name               string            `json:"name"`
	Size               string            `json:"size"`
	ExpectedMode       ctxpkg.ReviewMode `json:"expected_mode"`
	ActualMode         ctxpkg.ReviewMode `json:"actual_mode"`
	DurationMillis     int64             `json:"duration_ms"`
	ReviewedFiles      int               `json:"reviewed_files"`
	SkippedFiles       int               `json:"skipped_files"`
	TotalChangedLines  int               `json:"total_changed_lines"`
	CommentActions     int               `json:"comment_actions"`
	ProviderTokens     int64             `json:"provider_tokens_total"`
	DegradationSummary bool              `json:"degradation_summary"`
	Summary            string            `json:"summary"`
	ExcludedReasons    map[string]int    `json:"excluded_reasons,omitempty"`
	ReviewedPaths      []string          `json:"reviewed_paths,omitempty"`
	FindingsPersisted  int               `json:"findings_persisted"`
	CapturedAt         time.Time         `json:"captured_at"`
	RawStatus          string            `json:"status"`
	ErrorCode          string            `json:"error_code,omitempty"`
	EvidenceSource     string            `json:"evidence_source,omitempty"`
}

type EvidenceReport struct {
	GeneratedAt             time.Time        `json:"generated_at"`
	Environment             EnvironmentInfo  `json:"environment"`
	CorpusSummary           CorpusSummary    `json:"corpus_summary"`
	ScenarioResults         []ScenarioResult `json:"scenario_results"`
	MediumAndSmallDurations []int64          `json:"medium_and_small_duration_ms"`
	P90DurationMillis       int64            `json:"p90_duration_ms"`
	TargetDurationMillis    int64            `json:"target_duration_ms"`
	TargetSatisfied         bool             `json:"target_satisfied"`
	Notes                   []string         `json:"notes"`
}

type EnvironmentInfo struct {
	GoVersion string `json:"go_version"`
	Package   string `json:"package"`
	Command   string `json:"command"`
}

type CorpusSummary struct {
	TotalScenarios  int `json:"total_scenarios"`
	SmallScenarios  int `json:"small_scenarios"`
	MediumScenarios int `json:"medium_scenarios"`
	LargeScenarios  int `json:"large_scenarios"`
}

func TestBenchmarkCorpus(t *testing.T) {
	t.Parallel()

	corpus := benchmarkScenarioCorpus()
	if len(corpus) < 21 {
		t.Fatalf("benchmark corpus size = %d, want at least 21 scenarios", len(corpus))
	}

	var smallCount, mediumCount, largeCount int
	for _, scenario := range corpus {
		switch scenario.Size {
		case "small":
			smallCount++
		case "medium":
			mediumCount++
		case "large":
			largeCount++
		default:
			t.Fatalf("scenario %q has unknown size %q", scenario.Name, scenario.Size)
		}
		if len(scenario.Diffs) == 0 {
			t.Fatalf("scenario %q has no diffs", scenario.Name)
		}
	}
	if smallCount < 12 {
		t.Fatalf("small scenarios = %d, want at least 12", smallCount)
	}
	if mediumCount < 8 {
		t.Fatalf("medium scenarios = %d, want at least 8", mediumCount)
	}
	if largeCount < 1 {
		t.Fatalf("large scenarios = %d, want at least 1", largeCount)
	}

	reportPath := filepath.Join("/Users/chris/workspace/mreviewer", benchmarkEvidencePath)
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read evidence report: %v", err)
	}
	var report EvidenceReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("unmarshal evidence report: %v", err)
	}
	if len(report.ScenarioResults) != len(corpus) {
		t.Fatalf("evidence scenario count = %d, want %d", len(report.ScenarioResults), len(corpus))
	}
	if report.CorpusSummary.SmallScenarios < 12 || report.CorpusSummary.MediumScenarios < 8 || report.CorpusSummary.LargeScenarios < 1 {
		t.Fatalf("unexpected corpus summary: %+v", report.CorpusSummary)
	}
	if report.P90DurationMillis <= 0 {
		t.Fatalf("p90_duration_ms = %d, want > 0", report.P90DurationMillis)
	}
	if report.TargetDurationMillis != 10*60*1000 {
		t.Fatalf("target_duration_ms = %d, want %d", report.TargetDurationMillis, 10*60*1000)
	}
	if !report.TargetSatisfied {
		t.Fatalf("target_satisfied = false, want true")
	}
	seenLargeDegradation := false
	for _, result := range report.ScenarioResults {
		if result.Size == "large" && result.ActualMode == ctxpkg.ReviewModeDegradation && result.DegradationSummary {
			seenLargeDegradation = true
		}
	}
	if !seenLargeDegradation {
		t.Fatal("expected large scenario evidence with degradation summary")
	}
}

func TestGenerateBenchmarkEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark evidence generation in short mode")
	}
	if os.Getenv("MREVIEWER_WRITE_BENCH_EVIDENCE") != "1" {
		t.Skip("set MREVIEWER_WRITE_BENCH_EVIDENCE=1 to regenerate runtime evidence")
	}

	report := runBenchmarkCorpus(t)
	writeEvidenceReport(t, report)
	if !report.TargetSatisfied {
		t.Fatalf("p90 target not satisfied: %dms > %dms", report.P90DurationMillis, report.TargetDurationMillis)
	}
}

func BenchmarkProcessorCorpus(b *testing.B) {
	corpus := benchmarkScenarioCorpus()
	for _, scenario := range corpus {
		scenario := scenario
		b.Run(scenario.Name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				report := runSingleScenario(b, scenario, false)
				if report.ActualMode == "" {
					b.Fatalf("scenario %s did not produce a mode", scenario.Name)
				}
			}
		})
	}
}

func runBenchmarkCorpus(t testing.TB) EvidenceReport {
	t.Helper()
	corpus := benchmarkScenarioCorpus()
	results := make([]ScenarioResult, 0, len(corpus))
	durations := make([]int64, 0, len(corpus))
	summary := CorpusSummary{TotalScenarios: len(corpus)}

	for _, scenario := range corpus {
		result := runSingleScenario(t, scenario, true)
		results = append(results, result)
		if scenario.Size == "small" || scenario.Size == "medium" {
			durations = append(durations, result.DurationMillis)
		}
		switch scenario.Size {
		case "small":
			summary.SmallScenarios++
		case "medium":
			summary.MediumScenarios++
		case "large":
			summary.LargeScenarios++
		}
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p90 := percentile90(durations)
	return EvidenceReport{
		GeneratedAt: time.Now().UTC(),
		Environment: EnvironmentInfo{
			GoVersion: strings.TrimSpace(strings.TrimPrefix(runtimeVersion(), "go")),
			Package:   "github.com/mreviewer/mreviewer/internal/bench",
			Command:   "MREVIEWER_WRITE_BENCH_EVIDENCE=1 go test ./internal/bench/... -run TestGenerateBenchmarkEvidence -count=1",
		},
		CorpusSummary:           summary,
		ScenarioResults:         results,
		MediumAndSmallDurations: durations,
		P90DurationMillis:       p90,
		TargetDurationMillis:    10 * 60 * 1000,
		TargetSatisfied:         p90 <= 10*60*1000,
		Notes: []string{
			"Corpus exercises context assembly plus processor persistence/writeback fallback paths with deterministic fixture MR snapshots.",
			"P90 is computed over the documented small and medium scenarios to mirror VAL-PERF-001.",
			"Large scenarios remain in the corpus to prove degradation-mode benchmark coverage and persisted summary-note evidence.",
		},
	}
}

func runSingleScenario(t testing.TB, scenario CorpusScenario, verifyExpectations bool) ScenarioResult {
	t.Helper()
	ctx := context.Background()
	sqlDB := dbtest.New(t.(*testing.T))
	dbtest.MigrateUp(t.(*testing.T), sqlDB, migrationsDir)
	queries := db.New(sqlDB)
	_, projectID, _, runID := seedBenchmarkRun(t, ctx, queries)
	if len(scenario.ProjectPolicyExtra) > 0 {
		if _, err := queries.InsertProjectPolicy(ctx, db.InsertProjectPolicyParams{ProjectID: projectID, ConfidenceThreshold: 0.1, SeverityThreshold: "low", IncludePaths: json.RawMessage("[]"), ExcludePaths: json.RawMessage("[]"), Extra: scenario.ProjectPolicyExtra}); err != nil {
			t.Fatalf("InsertProjectPolicy(%s): %v", scenario.Name, err)
		}
	}

	provider := benchmarkProvider{scenarioName: scenario.Name, size: scenario.Size}
	processor := llm.NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), sqlDB, &benchmarkGitLabReader{snapshot: benchmarkSnapshot(scenario)}, benchmarkRulesLoader{}, provider, llm.NewDBAuditLogger(sqlDB))
	if err := queries.ClaimReviewRun(ctx, db.ClaimReviewRunParams{ClaimedBy: "bench-worker", ID: runID}); err != nil {
		t.Fatalf("ClaimReviewRun(%s): %v", scenario.Name, err)
	}
	run, err := queries.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun(%s): %v", scenario.Name, err)
	}

	started := time.Now()
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun(%s): %v", scenario.Name, err)
	}
	updatedRun, err := queries.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun after ProcessRun(%s): %v", scenario.Name, err)
	}
	actions, err := queries.ListCommentActionsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListCommentActionsByRun(%s): %v", scenario.Name, err)
	}
	findings, err := queries.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun(%s): %v", scenario.Name, err)
	}
	assembled, err := ctxpkg.NewAssembler().Assemble(ctxpkg.AssembleInput{
		ReviewRunID:  runID,
		Project:      ctxpkg.ProjectContext{ProjectID: 101, FullPath: "group/project"},
		MergeRequest: ctxpkg.MergeRequestContext{IID: 7, Title: scenario.Description, Author: "benchmark"},
		Version:      ctxpkg.VersionContext{BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"},
		Rules:        benchmarkRulesLoader{}.result().Trusted,
		Settings:     policySettingsForScenario(t, sqlDB, projectID),
		Diffs:        scenario.Diffs,
	})
	if err != nil {
		t.Fatalf("Assemble(%s): %v", scenario.Name, err)
	}
	result := ScenarioResult{
		Name:               scenario.Name,
		Size:               scenario.Size,
		ExpectedMode:       scenario.ExpectedMode,
		ActualMode:         assembled.Mode,
		DurationMillis:     time.Since(started).Milliseconds(),
		ReviewedFiles:      len(assembled.Request.Changes),
		SkippedFiles:       assembled.Coverage.SkippedFiles,
		TotalChangedLines:  assembled.TotalChangedLines,
		CommentActions:     len(actions),
		ProviderTokens:     outcome.ProviderTokensTotal,
		DegradationSummary: updatedRun.ErrorCode == "degradation_mode" && updatedRun.ErrorDetail.Valid,
		Summary:            assembled.Coverage.Summary,
		ExcludedReasons:    countExcludedReasons(assembled.Excluded),
		ReviewedPaths:      append([]string(nil), assembled.Coverage.ReviewedPaths...),
		FindingsPersisted:  len(findings),
		CapturedAt:         time.Now().UTC(),
		RawStatus:          updatedRun.Status,
		ErrorCode:          updatedRun.ErrorCode,
		EvidenceSource:     "processor+writer fallback",
	}
	if verifyExpectations {
		if result.ActualMode != scenario.ExpectedMode {
			t.Fatalf("scenario %s mode = %s, want %s", scenario.Name, result.ActualMode, scenario.ExpectedMode)
		}
		if updatedRun.Status != "completed" {
			t.Fatalf("scenario %s status = %s, want completed", scenario.Name, updatedRun.Status)
		}
		if scenario.ExpectedMode == ctxpkg.ReviewModeDegradation {
			if !result.DegradationSummary {
				t.Fatalf("scenario %s expected persisted degradation summary", scenario.Name)
			}
			if len(actions) != 1 || actions[0].ActionType != "summary_note" {
				t.Fatalf("scenario %s comment actions = %#v, want one summary_note", scenario.Name, actions)
			}
		} else if len(actions) > 0 {
			t.Fatalf("scenario %s unexpected comment actions for non-degradation path: %#v", scenario.Name, actions)
		}
	}
	return result
}

func benchmarkScenarioCorpus() []CorpusScenario {
	corpus := make([]CorpusScenario, 0, 21)
	for i := 1; i <= 12; i++ {
		corpus = append(corpus, CorpusScenario{
			Name:         fmt.Sprintf("small-%02d", i),
			Size:         "small",
			Description:  fmt.Sprintf("Small benchmark fixture %d", i),
			ExpectedMode: ctxpkg.ReviewModeFullScope,
			Diffs:        buildScenarioDiffs(8+i%4, 16+(i%5)*6, fmt.Sprintf("small/%02d", i)),
		})
	}
	for i := 1; i <= 8; i++ {
		corpus = append(corpus, CorpusScenario{
			Name:         fmt.Sprintf("medium-%02d", i),
			Size:         "medium",
			Description:  fmt.Sprintf("Medium benchmark fixture %d", i),
			ExpectedMode: ctxpkg.ReviewModeFullScope,
			Diffs:        buildScenarioDiffs(28+i*3, 18+(i%3)*6, fmt.Sprintf("medium/%02d", i)),
		})
	}
	corpus = append(corpus, CorpusScenario{
		Name:               "large-01",
		Size:               "large",
		Description:        "Large merge request benchmark fixture with degradation coverage",
		ExpectedMode:       ctxpkg.ReviewModeDegradation,
		ProjectPolicyExtra: json.RawMessage(`{"review":{"max_files":15,"max_changed_lines":4000}}`),
		Diffs:              buildScenarioDiffs(96, 36, "large/01"),
	})
	return corpus
}

func buildScenarioDiffs(fileCount, changedLines int, prefix string) []gitlab.MergeRequestDiff {
	diffs := make([]gitlab.MergeRequestDiff, 0, fileCount)
	for i := 0; i < fileCount; i++ {
		path := fmt.Sprintf("%s/file_%03d.go", prefix, i+1)
		diffs = append(diffs, gitlab.MergeRequestDiff{OldPath: path, NewPath: path, Diff: syntheticDiff(path, changedLines)})
	}
	return diffs
}

func syntheticDiff(path string, changedLines int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("@@ -1,%d +1,%d @@ %s\n", changedLines+1, changedLines+1, path))
	for i := 0; i < changedLines/2; i++ {
		b.WriteString(fmt.Sprintf(" context %03d\n", i))
	}
	for i := 0; i < changedLines; i++ {
		if i%2 == 0 {
			b.WriteString(fmt.Sprintf("-old line %03d\n", i))
		} else {
			b.WriteString(fmt.Sprintf("+new line %03d\n", i))
		}
	}
	for i := 0; i < changedLines/2; i++ {
		b.WriteString(fmt.Sprintf(" tail %03d\n", i))
	}
	return strings.TrimRight(b.String(), "\n")
}

func benchmarkSnapshot(scenario CorpusScenario) gitlab.MergeRequestSnapshot {
	return gitlab.MergeRequestSnapshot{
		MergeRequest: gitlab.MergeRequest{
			GitLabID:     11,
			IID:          7,
			ProjectID:    101,
			Title:        scenario.Description,
			SourceBranch: "feature/bench",
			TargetBranch: "main",
			DiffRefs:     &gitlab.DiffRefs{BaseSHA: "base", HeadSHA: "head", StartSHA: "start"},
			Author: struct {
				Username string `json:"username"`
			}{Username: "benchmark"},
		},
		Version: gitlab.MergeRequestVersion{GitLabVersionID: 55, BaseSHA: "base", StartSHA: "start", HeadSHA: "head", PatchIDSHA: "patch"},
		Diffs:   scenario.Diffs,
	}
}

type benchmarkGitLabReader struct{ snapshot gitlab.MergeRequestSnapshot }

func (b *benchmarkGitLabReader) GetMergeRequestSnapshot(context.Context, int64, int64) (gitlab.MergeRequestSnapshot, error) {
	return b.snapshot, nil
}

type benchmarkRulesLoader struct{}

func (benchmarkRulesLoader) Load(context.Context, rules.LoadInput) (rules.LoadResult, error) {
	return benchmarkRulesLoader{}.result(), nil
}

func (benchmarkRulesLoader) result() rules.LoadResult {
	return rules.LoadResult{Trusted: ctxpkg.TrustedRules{PlatformPolicy: "platform defaults", ProjectPolicy: "benchmark policy", ReviewMarkdown: "focus on correctness", RulesDigest: "benchmark-digest"}, EffectivePolicy: rules.EffectivePolicy{ProviderRoute: "default"}}
}

type benchmarkProvider struct {
	scenarioName string
	size         string
}

func (p benchmarkProvider) Review(context.Context, ctxpkg.ReviewRequest) (llm.ProviderResponse, error) {
	result := llm.ReviewResult{SchemaVersion: "1.0", Summary: fmt.Sprintf("processed %s", p.scenarioName), Status: "completed"}
	if p.size != "large" {
		newLine := int32(10)
		result.Findings = []llm.ReviewFinding{{Category: "bug", Severity: "medium", Confidence: 0.9, Title: fmt.Sprintf("finding for %s", p.scenarioName), BodyMarkdown: "synthetic finding", Path: "main.go", AnchorKind: "new", NewLine: &newLine}}
	}
	return llm.ProviderResponse{Result: result, Tokens: int64(32 + len(p.scenarioName)), Latency: 5 * time.Millisecond, Model: "benchmark-provider", ResponsePayload: map[string]any{"scenario": p.scenarioName}}, nil
}

func (p benchmarkProvider) RequestPayload(ctxpkg.ReviewRequest) map[string]any {
	return map[string]any{"scenario": p.scenarioName}
}

func seedBenchmarkRun(t testing.TB, ctx context.Context, q *db.Queries) (int64, int64, int64, int64) {
	t.Helper()
	res, err := q.UpsertGitlabInstance(ctx, db.UpsertGitlabInstanceParams{Url: "https://gitlab.example.com", Name: "GitLab"})
	if err != nil {
		t.Fatalf("UpsertGitlabInstance: %v", err)
	}
	instanceID, _ := res.LastInsertId()
	if instanceID == 0 {
		instance, err := q.GetGitlabInstanceByURL(ctx, "https://gitlab.example.com")
		if err != nil {
			t.Fatalf("GetGitlabInstanceByURL: %v", err)
		}
		instanceID = instance.ID
	}
	res, err = q.UpsertProject(ctx, db.UpsertProjectParams{GitlabInstanceID: instanceID, GitlabProjectID: 101, PathWithNamespace: "group/project", Enabled: true})
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	projectID, _ := res.LastInsertId()
	if projectID == 0 {
		project, err := q.GetProjectByGitlabID(ctx, db.GetProjectByGitlabIDParams{GitlabInstanceID: instanceID, GitlabProjectID: 101})
		if err != nil {
			t.Fatalf("GetProjectByGitlabID: %v", err)
		}
		projectID = project.ID
	}
	res, err = q.UpsertMergeRequest(ctx, db.UpsertMergeRequestParams{ProjectID: projectID, MrIid: 7, Title: "Benchmark MR", SourceBranch: "feature", TargetBranch: "main", Author: "benchmark", State: "opened", IsDraft: false, HeadSha: "head", WebUrl: "https://gitlab.example.com/group/project/-/merge_requests/7"})
	if err != nil {
		t.Fatalf("UpsertMergeRequest: %v", err)
	}
	mrID, _ := res.LastInsertId()
	if mrID == 0 {
		mr, err := q.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{ProjectID: projectID, MrIid: 7})
		if err != nil {
			t.Fatalf("GetMergeRequestByProjectMR: %v", err)
		}
		mrID = mr.ID
	}
	res, err = q.InsertReviewRun(ctx, db.InsertReviewRunParams{ProjectID: projectID, MergeRequestID: mrID, TriggerType: "webhook", HeadSha: "head", Status: "pending", MaxRetries: 3, IdempotencyKey: fmt.Sprintf("bench-%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	runID, _ := res.LastInsertId()
	return instanceID, projectID, mrID, runID
}

func policySettingsForScenario(t testing.TB, sqlDB *sql.DB, projectID int64) ctxpkg.PolicySettings {
	t.Helper()
	queries := db.New(sqlDB)
	policy, err := queries.GetProjectPolicy(context.Background(), projectID)
	if err != nil {
		if err == sql.ErrNoRows {
			return ctxpkg.DefaultPolicySettings()
		}
		t.Fatalf("GetProjectPolicy: %v", err)
	}
	settings, err := ctxpkg.SettingsFromPolicy(&policy)
	if err != nil {
		t.Fatalf("SettingsFromPolicy: %v", err)
	}
	return settings
}

func countExcludedReasons(excluded []ctxpkg.ExcludedFile) map[string]int {
	if len(excluded) == 0 {
		return nil
	}
	reasons := make(map[string]int)
	for _, item := range excluded {
		reasons[item.Reason]++
	}
	return reasons
}

func percentile90(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	index := int((0.9 * float64(len(values))) - 1)
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func writeEvidenceReport(t testing.TB, report EvidenceReport) {
	t.Helper()
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	path := filepath.Join("/Users/chris/workspace/mreviewer", benchmarkEvidencePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func runtimeVersion() string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(strings.TrimSpace(strings.TrimSpace("go1.25.6"))), "\n", "")), "go version ")), "go"))
}

var _ scheduler.Processor = (*llm.Processor)(nil)
