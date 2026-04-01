package main

import (
	"context"
	"database/sql"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/reviewstatus"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewpack"
)

type recordingInputLoader struct {
	targets []reviewcore.ReviewTarget
}

func (l *recordingInputLoader) Load(_ context.Context, target reviewcore.ReviewTarget) (reviewcore.ReviewInput, error) {
	l.targets = append(l.targets, target)
	return reviewcore.ReviewInput{Target: target}, nil
}

type staticPackRunner struct{}

func (staticPackRunner) Run(context.Context, reviewcore.ReviewInput, reviewpack.CapabilityPack) (reviewcore.ReviewerArtifact, error) {
	return reviewcore.ReviewerArtifact{ReviewerID: "security", ReviewerType: "capability_pack"}, nil
}

type echoJudge struct{}

func (echoJudge) Decide(target reviewcore.ReviewTarget, _ []reviewcore.ReviewerArtifact) reviewcore.ReviewBundle {
	return reviewcore.ReviewBundle{Target: target, JudgeVerdict: reviewcore.VerdictCommentOnly}
}

type recordingPackRunner struct {
	packIDs []string
}

func (r *recordingPackRunner) Run(_ context.Context, _ reviewcore.ReviewInput, pack reviewpack.CapabilityPack) (reviewcore.ReviewerArtifact, error) {
	r.packIDs = append(r.packIDs, pack.ID)
	return reviewcore.ReviewerArtifact{
		ReviewerID:   pack.ID,
		ReviewerType: "capability_pack",
		Summary:      pack.ID,
	}, nil
}

type staticAdvisor struct {
	routes []string
}

func (a *staticAdvisor) Advise(_ context.Context, _ reviewcore.ReviewInput, bundle reviewcore.ReviewBundle, route string) (*reviewcore.ReviewerArtifact, error) {
	a.routes = append(a.routes, route)
	return &reviewcore.ReviewerArtifact{
		ReviewerID:   "advisor:" + route,
		ReviewerType: "advisor",
		Summary:      "advisor summary for " + bundle.Target.Repository,
	}, nil
}

type recordingExternalComparisonLoader struct {
	targets      []reviewcore.ReviewTarget
	reviewerSets [][]string
}

func (l *recordingExternalComparisonLoader) Load(_ context.Context, target reviewcore.ReviewTarget, reviewerIDs []string) ([]reviewcore.ComparisonArtifact, error) {
	l.targets = append(l.targets, target)
	l.reviewerSets = append(l.reviewerSets, append([]string(nil), reviewerIDs...))
	return []reviewcore.ComparisonArtifact{
		{
			ReviewerID:   reviewerIDs[0],
			ReviewerType: "external_gitlab_note",
			Findings: []reviewcore.Finding{
				{
					Title:      "external finding",
					Category:   "security",
					Claim:      "external claim",
					Severity:   "high",
					Confidence: 0.91,
					Location: &reviewcore.CanonicalLocation{
						Path: "app/service.go",
					},
				},
			},
		},
	}, nil
}

type recordingGateStatusPublisher struct {
	results []gate.Result
}

func (p *recordingGateStatusPublisher) PublishStatus(_ context.Context, result gate.Result) error {
	p.results = append(p.results, result)
	return nil
}

func insertReviewRuntimeProject(t *testing.T, sqlDB *sql.DB, instanceURL, path string, gitlabProjectID int64) (int64, int64) {
	t.Helper()
	res, err := sqlDB.Exec("INSERT INTO gitlab_instances (url, name) VALUES (?, 'test')", instanceURL)
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	instanceID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("instance last insert id: %v", err)
	}
	res, err = sqlDB.Exec(`INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled)
		VALUES (?, ?, ?, TRUE)`, instanceID, gitlabProjectID, path)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
	projectID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project last insert id: %v", err)
	}
	return instanceID, projectID
}

func insertReviewRuntimeMR(t *testing.T, sqlDB *sql.DB, projectID, iid int64, headSHA, webURL string) int64 {
	t.Helper()
	res, err := sqlDB.Exec(`INSERT INTO merge_requests (project_id, mr_iid, title, state, target_branch, source_branch, head_sha, web_url)
		VALUES (?, ?, ?, 'opened', 'main', 'feature', ?, ?)`, projectID, iid, "Runtime engine test", headSHA, webURL)
	if err != nil {
		t.Fatalf("insert merge request: %v", err)
	}
	mrID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("merge request last insert id: %v", err)
	}
	return mrID
}

func TestEngineBackedProcessorUsesGitLabTargetForGitLabRuns(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	_, projectID := insertReviewRuntimeProject(t, sqlDB, "https://gitlab.example.com", "group/project", 101)
	mrID := insertReviewRuntimeMR(t, sqlDB, projectID, 17, "sha-gitlab-engine", "https://gitlab.example.com/group/project/-/merge_requests/17")
	runID := insertTestRun(t, sqlDB, projectID, mrID, "pending", "engine-backed-gitlab", "sha-gitlab-engine")

	loader := &recordingInputLoader{}
	processor := engineBackedProcessor{
		queries:  db.New(sqlDB),
		platform: reviewcore.PlatformGitLab,
		loader:   loader,
		engine:   reviewcore.NewEngine([]reviewpack.CapabilityPack{{ID: "security"}}, staticPackRunner{}, echoJudge{}),
	}

	run, err := db.New(sqlDB).GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if len(loader.targets) != 1 {
		t.Fatalf("loaded targets = %d, want 1", len(loader.targets))
	}
	if loader.targets[0].Platform != reviewcore.PlatformGitLab {
		t.Fatalf("loader target platform = %q, want gitlab", loader.targets[0].Platform)
	}
	bundle, ok := outcome.ReviewBundle.(reviewcore.ReviewBundle)
	if !ok {
		t.Fatalf("outcome bundle type = %T, want reviewcore.ReviewBundle", outcome.ReviewBundle)
	}
	if bundle.Target.Platform != reviewcore.PlatformGitLab {
		t.Fatalf("bundle target platform = %q, want gitlab", bundle.Target.Platform)
	}
}

func TestEngineBackedProcessorRunsConfiguredPacksAdvisorAndExternalComparison(t *testing.T) {
	ctx := context.Background()
	sqlDB := setupTestDB(t)
	_, projectID := insertReviewRuntimeProject(t, sqlDB, "https://gitlab.example.com", "group/project", 202)
	mrID := insertReviewRuntimeMR(t, sqlDB, projectID, 23, "sha-gitlab-runtime-advanced", "https://gitlab.example.com/group/project/-/merge_requests/23")
	runID := insertTestRun(t, sqlDB, projectID, mrID, "pending", "engine-backed-gitlab-advanced", "sha-gitlab-runtime-advanced")

	loader := &recordingInputLoader{}
	packRunner := &recordingPackRunner{}
	advisor := &staticAdvisor{}
	comparisonLoader := &recordingExternalComparisonLoader{}
	statusPublisher := &recordingGateStatusPublisher{}
	processor := engineBackedProcessor{
		queries: db.New(sqlDB),
		platform: reviewcore.PlatformGitLab,
		loader:   loader,
		engine: reviewcore.NewEngine([]reviewpack.CapabilityPack{
			{ID: "security"},
			{ID: "architecture"},
		}, packRunner, echoJudge{}),
		statusPublisher:           statusPublisher,
		selectedPackIDs:          []string{"security"},
		advisor:                  advisor,
		advisorRoute:             "openai-gpt-5-4",
		externalComparisonLoader: comparisonLoader,
		externalReviewerIDs:      []string{"gitlab:coderabbit"},
	}

	run, err := db.New(sqlDB).GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if len(loader.targets) != 1 {
		t.Fatalf("loaded targets = %d, want 1", len(loader.targets))
	}
	if got := packRunner.packIDs; len(got) != 1 || got[0] != "security" {
		t.Fatalf("pack runner ids = %#v, want [security]", got)
	}
	if got := advisor.routes; len(got) != 1 || got[0] != "openai-gpt-5-4" {
		t.Fatalf("advisor routes = %#v, want [openai-gpt-5-4]", got)
	}
	if got := comparisonLoader.reviewerSets; len(got) != 1 || len(got[0]) != 1 || got[0][0] != "gitlab:coderabbit" {
		t.Fatalf("comparison reviewer sets = %#v, want [[gitlab:coderabbit]]", got)
	}
	bundle, ok := outcome.ReviewBundle.(reviewcore.ReviewBundle)
	if !ok {
		t.Fatalf("outcome bundle type = %T, want reviewcore.ReviewBundle", outcome.ReviewBundle)
	}
	if bundle.AdvisorArtifact == nil {
		t.Fatal("expected advisor artifact to be populated")
	}
	if len(bundle.Comparisons) != 2 {
		t.Fatalf("comparison artifacts = %d, want 2", len(bundle.Comparisons))
	}
	gotStages := make([]reviewstatus.Stage, 0, len(statusPublisher.results))
	for _, result := range statusPublisher.results {
		gotStages = append(gotStages, result.Stage)
	}
	wantStages := []reviewstatus.Stage{
		reviewstatus.StageRunningPacks,
		reviewstatus.StageRunningAdvisor,
		reviewstatus.StageComparingExternal,
	}
	if len(gotStages) != len(wantStages) {
		t.Fatalf("status stages = %#v, want %#v", gotStages, wantStages)
	}
	for i := range wantStages {
		if gotStages[i] != wantStages[i] {
			t.Fatalf("status stage %d = %q, want %q", i, gotStages[i], wantStages[i])
		}
	}
}
