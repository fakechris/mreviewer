package reviewrun

import (
	"context"
	"database/sql"
	"testing"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/dbtest"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type fakeReviewInputLoader struct {
	input    core.ReviewInput
	target   core.ReviewTarget
	route    string
	loadErr  error
	loadCall int
}

func (f *fakeReviewInputLoader) Load(_ context.Context, target core.ReviewTarget, providerRoute string) (core.ReviewInput, error) {
	f.loadCall++
	f.target = target
	f.route = providerRoute
	return f.input, f.loadErr
}

type fakeReviewEngine struct {
	bundle  core.ReviewBundle
	runOpts core.RunOptions
	input   core.ReviewInput
	runErr  error
	runCall int
}

func (f *fakeReviewEngine) Run(_ context.Context, input core.ReviewInput, opts core.RunOptions) (core.ReviewBundle, error) {
	f.runCall++
	f.input = input
	f.runOpts = opts
	return f.bundle, f.runErr
}

type fakeBundleWriteback struct {
	run      db.ReviewRun
	bundle   core.ReviewBundle
	calls    int
	writeErr error
}

func (f *fakeBundleWriteback) Write(_ context.Context, _ db.ReviewRun, _ []db.ReviewFinding) error {
	return nil
}

func (f *fakeBundleWriteback) WriteBundle(_ context.Context, run db.ReviewRun, bundle core.ReviewBundle) error {
	f.calls++
	f.run = run
	f.bundle = bundle
	return f.writeErr
}

func setupEngineProcessorDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := dbtest.New(t)
	dbtest.MigrateUp(t, sqlDB, "../../migrations")
	return sqlDB
}

func seedEngineProcessorRunEntities(t *testing.T, sqlDB *sql.DB, projectGitLabID, mrIID int64, headSHA string) (instanceID, projectID, mrID int64) {
	t.Helper()
	ctx := context.Background()
	queries := db.New(sqlDB)

	instRes, err := sqlDB.Exec(`INSERT INTO gitlab_instances (url, name) VALUES ('https://gitlab.example.com', 'test')`)
	if err != nil {
		t.Fatalf("insert gitlab_instances: %v", err)
	}
	instanceID, _ = instRes.LastInsertId()

	projRes, err := sqlDB.Exec(`INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled) VALUES (?, ?, 'group/repo', TRUE)`,
		instanceID, projectGitLabID,
	)
	if err != nil {
		t.Fatalf("insert projects: %v", err)
	}
	projectID, _ = projRes.LastInsertId()

	if _, err := queries.UpsertMergeRequest(ctx, db.UpsertMergeRequestParams{
		ProjectID:    projectID,
		MrIid:        mrIID,
		Title:        "Engine processor MR",
		State:        "opened",
		SourceBranch: "feature",
		TargetBranch: "main",
		HeadSha:      headSHA,
		WebUrl:       "https://gitlab.example.com/group/repo/-/merge_requests/11",
		Author:       "tester",
	}); err != nil {
		t.Fatalf("UpsertMergeRequest: %v", err)
	}
	mr, err := queries.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{
		ProjectID: projectID,
		MrIid:     mrIID,
	})
	if err != nil {
		t.Fatalf("GetMergeRequestByProjectMR: %v", err)
	}
	return instanceID, projectID, mr.ID
}

func TestEngineProcessorProcessRunPersistsBundleAsRequestedChanges(t *testing.T) {
	sqlDB := setupEngineProcessorDB(t)
	ctx := context.Background()
	queries := db.New(sqlDB)
	_, projectID, mrID := seedEngineProcessorRunEntities(t, sqlDB, 301, 11, "head-sha-engine-processor")

	runResult, err := queries.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mrID,
		TriggerType:    "manual",
		HeadSha:        "head-sha-engine-processor",
		Status:         "pending",
		MaxRetries:     3,
		IdempotencyKey: "engine-processor-run",
	})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	runID, _ := runResult.LastInsertId()

	run, err := queries.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}

	inputLoader := &fakeReviewInputLoader{
		input: core.ReviewInput{
			Target: core.ReviewTarget{
				Platform:     core.PlatformGitLab,
				URL:          "https://gitlab.example.com/group/repo/-/merge_requests/11",
				Repository:   "group/repo",
				ProjectID:    301,
				ChangeNumber: 11,
				BaseURL:      "https://gitlab.example.com",
			},
			Request: ctxpkg.ReviewRequest{
				ReviewRunID: "11",
				Changes: []ctxpkg.Change{
					{Path: "src/service/foo.go", Status: "modified"},
				},
			},
		},
	}
	engine := &fakeReviewEngine{
		bundle: core.ReviewBundle{
			MarkdownSummary: "Verdict: requested_changes",
			Verdict:         "requested_changes",
			PublishCandidates: []core.PublishCandidate{
				{Kind: "summary", Body: "Verdict: requested_changes"},
				{
					Kind:     "finding",
					Title:    "Off-by-one bug",
					Body:     "Loop excludes the last record.",
					Severity: "high",
					Location: core.CanonicalLocation{
						Path:      "src/service/foo.go",
						Side:      core.DiffSideNew,
						StartLine: 21,
						EndLine:   21,
					},
				},
			},
		},
	}

	processor := NewEngineProcessor(sqlDB, inputLoader, engine)
	run.ScopeJson = db.NullRawMessage([]byte(`{"provider_route":"claude-opus-4-1"}`))
	outcome, err := processor.ProcessRun(ctx, run)
	if err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}

	updatedRun, err := queries.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun(updated): %v", err)
	}
	if updatedRun.Status != "requested_changes" {
		t.Fatalf("run status = %q, want requested_changes", updatedRun.Status)
	}

	findings, err := queries.ListFindingsByRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListFindingsByRun: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings len = %d, want 1", len(findings))
	}
	if findings[0].Title != "Off-by-one bug" {
		t.Fatalf("finding title = %q, want Off-by-one bug", findings[0].Title)
	}
	if inputLoader.loadCall != 1 {
		t.Fatalf("input loader calls = %d, want 1", inputLoader.loadCall)
	}
	if inputLoader.route != "claude-opus-4-1" {
		t.Fatalf("input loader route = %q, want claude-opus-4-1", inputLoader.route)
	}
	if engine.runCall != 1 {
		t.Fatalf("engine run calls = %d, want 1", engine.runCall)
	}
	if engine.runOpts.RouteOverride != "claude-opus-4-1" {
		t.Fatalf("engine route override = %q, want claude-opus-4-1", engine.runOpts.RouteOverride)
	}
	bundle, ok := outcome.ReviewBundle.(core.ReviewBundle)
	if !ok {
		t.Fatalf("outcome review bundle type = %T, want reviewcore.ReviewBundle", outcome.ReviewBundle)
	}
	if bundle.Verdict != "requested_changes" {
		t.Fatalf("outcome review bundle verdict = %q, want requested_changes", bundle.Verdict)
	}
	if len(bundle.PublishCandidates) != 2 {
		t.Fatalf("outcome review bundle publish candidates = %d, want 2", len(bundle.PublishCandidates))
	}
}

func TestNormalizePlatformAcceptsCommonGitHubVariants(t *testing.T) {
	tests := map[core.Platform]core.Platform{
		"github":  core.PlatformGitHub,
		"GitHub":  core.PlatformGitHub,
		"git hub": core.PlatformGitHub,
		"gitlab":  core.PlatformGitLab,
		"GitLab":  core.PlatformGitLab,
		"git lab": core.PlatformGitLab,
	}
	for input, want := range tests {
		if got := normalizePlatform(input); got != want {
			t.Fatalf("normalizePlatform(%q) = %q, want %q", input, got, want)
		}
	}
	if got := normalizePlatform("bitbucket"); got != "" {
		t.Fatalf("normalizePlatform(bitbucket) = %q, want empty", got)
	}
}

func TestEngineProcessorProcessRunRejectsMissingDependencies(t *testing.T) {
	processor := NewEngineProcessor(nil, nil, nil)
	_, err := processor.ProcessRun(context.Background(), db.ReviewRun{})
	if err == nil {
		t.Fatal("ProcessRun error = nil, want non-nil")
	}
}

func TestEngineProcessorPrefersBundleWritebackWhenAvailable(t *testing.T) {
	sqlDB := setupEngineProcessorDB(t)
	ctx := context.Background()
	queries := db.New(sqlDB)
	_, projectID, mrID := seedEngineProcessorRunEntities(t, sqlDB, 301, 11, "head-sha-bundle-writeback")

	runResult, err := queries.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mrID,
		TriggerType:    "manual",
		HeadSha:        "head-sha-bundle-writeback",
		Status:         "pending",
		MaxRetries:     3,
		IdempotencyKey: "engine-processor-bundle-writeback",
	})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	runID, _ := runResult.LastInsertId()
	run, err := queries.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}

	inputLoader := &fakeReviewInputLoader{
		input: core.ReviewInput{
			Target: core.ReviewTarget{
				Platform:     core.PlatformGitLab,
				URL:          "https://gitlab.example.com/group/repo/-/merge_requests/11",
				Repository:   "group/repo",
				ProjectID:    301,
				ChangeNumber: 11,
				BaseURL:      "https://gitlab.example.com",
			},
			Request: ctxpkg.ReviewRequest{
				ReviewRunID: "11",
				Changes: []ctxpkg.Change{
					{Path: "src/service/foo.go", Status: "modified"},
				},
			},
		},
	}
	engine := &fakeReviewEngine{
		bundle: core.ReviewBundle{
			Verdict: "requested_changes",
			PublishCandidates: []core.PublishCandidate{
				{Kind: "summary", Body: "Verdict: requested_changes"},
			},
		},
	}
	writeback := &fakeBundleWriteback{}

	processor := NewEngineProcessor(sqlDB, inputLoader, engine).WithWriteback(writeback)
	if _, err := processor.ProcessRun(ctx, run); err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if writeback.calls != 1 {
		t.Fatalf("bundle writeback calls = %d, want 1", writeback.calls)
	}
	if writeback.bundle.Verdict != "requested_changes" {
		t.Fatalf("bundle writeback verdict = %q, want requested_changes", writeback.bundle.Verdict)
	}
}

func TestEngineProcessorLoadsGitHubTargetFromScope(t *testing.T) {
	sqlDB := setupEngineProcessorDB(t)
	ctx := context.Background()
	queries := db.New(sqlDB)

	instRes, err := sqlDB.Exec(`INSERT INTO gitlab_instances (url, name) VALUES ('https://github.com', 'github')`)
	if err != nil {
		t.Fatalf("insert gitlab_instances: %v", err)
	}
	instanceID, _ := instRes.LastInsertId()

	projRes, err := sqlDB.Exec(`INSERT INTO projects (gitlab_instance_id, gitlab_project_id, path_with_namespace, enabled) VALUES (?, ?, 'acme/repo', TRUE)`,
		instanceID, 101,
	)
	if err != nil {
		t.Fatalf("insert projects: %v", err)
	}
	projectID, _ := projRes.LastInsertId()

	if _, err := queries.UpsertMergeRequest(ctx, db.UpsertMergeRequestParams{
		ProjectID:    projectID,
		MrIid:        17,
		Title:        "GitHub runtime PR",
		State:        "opened",
		SourceBranch: "feature/github",
		TargetBranch: "main",
		HeadSha:      "github-head-sha",
		WebUrl:       "https://github.com/acme/repo/pull/17",
		Author:       "octocat",
	}); err != nil {
		t.Fatalf("UpsertMergeRequest: %v", err)
	}
	mr, err := queries.GetMergeRequestByProjectMR(ctx, db.GetMergeRequestByProjectMRParams{
		ProjectID: projectID,
		MrIid:     17,
	})
	if err != nil {
		t.Fatalf("GetMergeRequestByProjectMR: %v", err)
	}
	runResult, err := queries.InsertReviewRun(ctx, db.InsertReviewRunParams{
		ProjectID:      projectID,
		MergeRequestID: mr.ID,
		TriggerType:    "webhook",
		HeadSha:        "github-head-sha",
		Status:         "pending",
		MaxRetries:     3,
		IdempotencyKey: "engine-processor-github",
		ScopeJson:      db.NullRawMessage([]byte(`{"platform":"github"}`)),
	})
	if err != nil {
		t.Fatalf("InsertReviewRun: %v", err)
	}
	runID, _ := runResult.LastInsertId()
	run, err := queries.GetReviewRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetReviewRun: %v", err)
	}

	inputLoader := &fakeReviewInputLoader{
		input: core.ReviewInput{
			Target: core.ReviewTarget{
				Platform:     core.PlatformGitHub,
				URL:          "https://github.com/acme/repo/pull/17",
				Repository:   "acme/repo",
				ProjectID:    101,
				ChangeNumber: 17,
				BaseURL:      "https://github.com",
			},
			Request: ctxpkg.ReviewRequest{},
		},
	}
	engine := &fakeReviewEngine{
		bundle: core.ReviewBundle{
			Target:          inputLoader.input.Target,
			Verdict:         "approved",
			MarkdownSummary: "Verdict: approved",
		},
	}

	processor := NewEngineProcessor(sqlDB, inputLoader, engine)
	if _, err := processor.ProcessRun(ctx, run); err != nil {
		t.Fatalf("ProcessRun: %v", err)
	}
	if inputLoader.target.Platform != core.PlatformGitHub {
		t.Fatalf("loader target platform = %q, want github", inputLoader.target.Platform)
	}
	if inputLoader.target.Repository != "acme/repo" {
		t.Fatalf("loader target repository = %q, want acme/repo", inputLoader.target.Repository)
	}
	if inputLoader.target.BaseURL != "https://github.com" {
		t.Fatalf("loader target base url = %q, want https://github.com", inputLoader.target.BaseURL)
	}
}

func TestPersistMRVersionFromInputUsesPlatformVersionID(t *testing.T) {
	sqlDB := setupEngineProcessorDB(t)
	ctx := context.Background()
	queries := db.New(sqlDB)
	_, _, mrID := seedEngineProcessorRunEntities(t, sqlDB, 401, 22, "head-sha-version-id")

	err := persistMRVersionFromInput(ctx, queries, mrID, core.PlatformVersion{PlatformVersionID: "17"}, ctxpkg.VersionContext{
		BaseSHA:    "base-sha",
		StartSHA:   "start-sha",
		HeadSHA:    "head-sha-version-id",
		PatchIDSHA: "patch-sha",
	})
	if err != nil {
		t.Fatalf("persistMRVersionFromInput: %v", err)
	}

	version, err := queries.GetLatestMRVersion(ctx, mrID)
	if err != nil {
		t.Fatalf("GetLatestMRVersion: %v", err)
	}
	if version.GitlabVersionID != 17 {
		t.Fatalf("GitlabVersionID = %d, want 17", version.GitlabVersionID)
	}
}

func TestPersistMRVersionFromInputSkipsDuplicateZeroVersion(t *testing.T) {
	sqlDB := setupEngineProcessorDB(t)
	ctx := context.Background()
	queries := db.New(sqlDB)
	_, _, mrID := seedEngineProcessorRunEntities(t, sqlDB, 402, 23, "head-sha-duplicate-zero")

	if _, err := queries.InsertMRVersion(ctx, db.InsertMRVersionParams{
		MergeRequestID:  mrID,
		GitlabVersionID: 0,
		BaseSha:         "base-sha",
		StartSha:        "start-sha",
		HeadSha:         "head-sha-duplicate-zero",
		PatchIDSha:      "patch-sha",
	}); err != nil {
		t.Fatalf("InsertMRVersion: %v", err)
	}

	err := persistMRVersionFromInput(ctx, queries, mrID, core.PlatformVersion{}, ctxpkg.VersionContext{
		BaseSHA:    "base-sha",
		StartSHA:   "start-sha",
		HeadSHA:    "head-sha-duplicate-zero",
		PatchIDSHA: "patch-sha",
	})
	if err != nil {
		t.Fatalf("persistMRVersionFromInput: %v", err)
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM mr_versions WHERE merge_request_id = ?`, mrID).Scan(&count); err != nil {
		t.Fatalf("count mr_versions: %v", err)
	}
	if count != 1 {
		t.Fatalf("mr_versions count = %d, want 1", count)
	}

	version, err := queries.GetLatestMRVersion(ctx, mrID)
	if err != nil {
		t.Fatalf("GetLatestMRVersion: %v", err)
	}
	if version.GitlabVersionID != 0 {
		t.Fatalf("GitlabVersionID = %d, want 0", version.GitlabVersionID)
	}
}

func TestPersistMRVersionFromInputSkipsDuplicatePlatformVersionID(t *testing.T) {
	sqlDB := setupEngineProcessorDB(t)
	ctx := context.Background()
	queries := db.New(sqlDB)
	_, _, mrID := seedEngineProcessorRunEntities(t, sqlDB, 403, 24, "head-sha-duplicate-version-id")

	if _, err := queries.InsertMRVersion(ctx, db.InsertMRVersionParams{
		MergeRequestID:  mrID,
		GitlabVersionID: 6563,
		BaseSha:         "base-sha",
		StartSha:        "start-sha",
		HeadSha:         "head-sha-duplicate-version-id",
		PatchIDSha:      "patch-sha",
	}); err != nil {
		t.Fatalf("InsertMRVersion: %v", err)
	}

	err := persistMRVersionFromInput(ctx, queries, mrID, core.PlatformVersion{PlatformVersionID: "6563"}, ctxpkg.VersionContext{
		BaseSHA:    "base-sha",
		StartSHA:   "start-sha",
		HeadSHA:    "head-sha-duplicate-version-id",
		PatchIDSHA: "patch-sha",
	})
	if err != nil {
		t.Fatalf("persistMRVersionFromInput: %v", err)
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM mr_versions WHERE merge_request_id = ?`, mrID).Scan(&count); err != nil {
		t.Fatalf("count mr_versions: %v", err)
	}
	if count != 1 {
		t.Fatalf("mr_versions count = %d, want 1", count)
	}
}

func TestPersistMRVersionFromInputSkipsDuplicateKnownIDWhenSnapshotOmitsVersionID(t *testing.T) {
	sqlDB := setupEngineProcessorDB(t)
	ctx := context.Background()
	queries := db.New(sqlDB)
	_, _, mrID := seedEngineProcessorRunEntities(t, sqlDB, 404, 25, "head-sha-known-id")

	if _, err := queries.InsertMRVersion(ctx, db.InsertMRVersionParams{
		MergeRequestID:  mrID,
		GitlabVersionID: 123,
		BaseSha:         "base-sha",
		StartSha:        "start-sha",
		HeadSha:         "head-sha-known-id",
		PatchIDSha:      "patch-sha",
	}); err != nil {
		t.Fatalf("InsertMRVersion: %v", err)
	}

	err := persistMRVersionFromInput(ctx, queries, mrID, core.PlatformVersion{}, ctxpkg.VersionContext{
		BaseSHA:    "base-sha",
		StartSHA:   "start-sha",
		HeadSHA:    "head-sha-known-id",
		PatchIDSHA: "patch-sha",
	})
	if err != nil {
		t.Fatalf("persistMRVersionFromInput: %v", err)
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM mr_versions WHERE merge_request_id = ?`, mrID).Scan(&count); err != nil {
		t.Fatalf("count mr_versions: %v", err)
	}
	if count != 1 {
		t.Fatalf("mr_versions count = %d, want 1", count)
	}

	version, err := queries.GetLatestMRVersion(ctx, mrID)
	if err != nil {
		t.Fatalf("GetLatestMRVersion: %v", err)
	}
	if version.GitlabVersionID != 123 {
		t.Fatalf("GitlabVersionID = %d, want 123", version.GitlabVersionID)
	}
}
