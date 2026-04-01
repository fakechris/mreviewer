package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mreviewer/mreviewer/internal/compare"
	"github.com/mreviewer/mreviewer/internal/config"
	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gate"
	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/judge"
	"github.com/mreviewer/mreviewer/internal/llm"
	githubplatform "github.com/mreviewer/mreviewer/internal/platform/github"
	gitlabplatform "github.com/mreviewer/mreviewer/internal/platform/gitlab"
	"github.com/mreviewer/mreviewer/internal/reviewadvisor"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewinput"
	"github.com/mreviewer/mreviewer/internal/reviewpack"
	"github.com/mreviewer/mreviewer/internal/reviewstatus"
	"github.com/mreviewer/mreviewer/internal/rules"
	"github.com/mreviewer/mreviewer/internal/scheduler"
)

type platformDispatchProcessor struct {
	queries *db.Queries
	gitlab  scheduler.Processor
	github  scheduler.Processor
}

func newPlatformDispatchProcessor(sqlDB *sql.DB, gitlabProcessor, githubProcessor scheduler.Processor) scheduler.Processor {
	if gitlabProcessor == nil && githubProcessor == nil {
		return nil
	}
	if sqlDB == nil {
		if gitlabProcessor != nil {
			return gitlabProcessor
		}
		return githubProcessor
	}
	return platformDispatchProcessor{
		queries: db.New(sqlDB),
		gitlab:  gitlabProcessor,
		github:  githubProcessor,
	}
}

func (p platformDispatchProcessor) ProcessRun(ctx context.Context, run db.ReviewRun) (scheduler.ProcessOutcome, error) {
	platform, err := detectRunPlatform(ctx, p.queries, run)
	if err != nil {
		return scheduler.ProcessOutcome{}, err
	}
	switch platform {
	case reviewcore.PlatformGitHub:
		if p.github == nil {
			return scheduler.ProcessOutcome{}, fmt.Errorf("worker runtime: github processor is not configured")
		}
		return p.github.ProcessRun(ctx, run)
	default:
		if p.gitlab == nil {
			return scheduler.ProcessOutcome{}, fmt.Errorf("worker runtime: gitlab processor is not configured")
		}
		return p.gitlab.ProcessRun(ctx, run)
	}
}

type runtimeInputLoader interface {
	Load(ctx context.Context, target reviewcore.ReviewTarget) (reviewcore.ReviewInput, error)
}

type engineBackedProcessor struct {
	queries                   *db.Queries
	platform                  reviewcore.Platform
	loader                    runtimeInputLoader
	engine                    *reviewcore.Engine
	statusPublisher           gate.StatusPublisher
	selectedPackIDs           []string
	advisor                   runtimeAdvisor
	advisorRoute              string
	externalComparisonLoader  runtimeExternalComparisonLoader
	externalReviewerIDs       []string
}

type runtimeAdvisor interface {
	Advise(ctx context.Context, input reviewcore.ReviewInput, bundle reviewcore.ReviewBundle, route string) (*reviewcore.ReviewerArtifact, error)
}

type runtimeExternalComparisonLoader interface {
	Load(ctx context.Context, target reviewcore.ReviewTarget, reviewerIDs []string) ([]reviewcore.ComparisonArtifact, error)
}

type gitLabInputLoader struct {
	adapter *gitlabplatform.Adapter
	builder *reviewinput.Builder
}

func (l gitLabInputLoader) Load(ctx context.Context, target reviewcore.ReviewTarget) (reviewcore.ReviewInput, error) {
	if l.adapter == nil || l.builder == nil {
		return reviewcore.ReviewInput{}, fmt.Errorf("worker runtime: gitlab input loader dependencies are required")
	}
	snapshot, err := l.adapter.FetchSnapshot(ctx, target)
	if err != nil {
		return reviewcore.ReviewInput{}, err
	}
	return l.builder.Build(ctx, reviewinput.BuildRequest{
		Target:   target,
		Snapshot: snapshot,
	})
}

func newGitLabEngineProcessor(sqlDB *sql.DB, cfg *config.Config, defaultRoute string, resolveProvider func(string) llm.Provider) (scheduler.Processor, *legacygitlab.Client, error) {
	if sqlDB == nil {
		return nil, nil, fmt.Errorf("worker runtime: sql db is required")
	}
	if cfg == nil {
		return nil, nil, fmt.Errorf("worker runtime: configuration is required")
	}
	if strings.TrimSpace(cfg.GitLabBaseURL) == "" || strings.TrimSpace(cfg.GitLabToken) == "" {
		return nil, nil, nil
	}
	gitLabLimiter := legacygitlab.NewInMemoryRateLimiter(legacygitlab.RateLimitConfig{Requests: 5, Window: time.Second}, time.Now, nil)
	gitLabLimiter.SetLimit("global", legacygitlab.RateLimitConfig{Requests: 5, Window: time.Second})
	client, err := legacygitlab.NewClient(cfg.GitLabBaseURL, cfg.GitLabToken, legacygitlab.WithRateLimiter(gitLabLimiter))
	if err != nil {
		return nil, nil, err
	}
	loader := gitLabInputLoader{
		adapter: gitlabplatform.NewAdapter(client),
		builder: reviewinput.NewBuilder(
			rules.NewLoader(client, rules.PlatformDefaults{
				Instructions:        "Platform defaults: prioritize correctness, security, and least-privilege behavior.",
				ConfidenceThreshold: 0.72,
				SeverityThreshold:   "medium",
				IncludePaths:        []string{"src/**"},
				ExcludePaths:        []string{"vendor/**"},
				GateMode:            "threads_resolved",
				ProviderRoute:       defaultRoute,
			}),
			ctxpkg.NewAssembler(),
		),
	}
	engine := reviewcore.NewEngine(
		reviewpack.DefaultPacks(),
		reviewcore.NewRouteAwareLegacyProviderPackRunner(resolveProvider),
		judge.NewEngine(),
	)
	statusPublisher := gate.NewGitLabStatusPublisher(client, db.New(sqlDB))
	return engineBackedProcessor{
		queries:                  db.New(sqlDB),
		platform:                 reviewcore.PlatformGitLab,
		loader:                   loader,
		engine:                   engine,
		statusPublisher:          statusPublisher,
		selectedPackIDs:          normalizedReviewPacks(cfg.ReviewPacks),
		advisor:                  reviewadvisor.New(resolveProvider),
		advisorRoute:             strings.TrimSpace(cfg.ReviewAdvisorRoute),
		externalComparisonLoader: workerGitLabExternalComparisonLoader{client: client},
		externalReviewerIDs:      normalizedReviewerIDs(cfg.ReviewCompareReviewers),
	}, client, nil
}

type gitHubInputLoader struct {
	adapter *githubplatform.Adapter[githubplatform.ReviewSnapshot]
	builder *reviewinput.Builder
}

func (l gitHubInputLoader) Load(ctx context.Context, target reviewcore.ReviewTarget) (reviewcore.ReviewInput, error) {
	if l.adapter == nil || l.builder == nil {
		return reviewcore.ReviewInput{}, fmt.Errorf("worker runtime: github input loader dependencies are required")
	}
	snapshot, err := l.adapter.FetchSnapshot(ctx, target)
	if err != nil {
		return reviewcore.ReviewInput{}, err
	}
	return l.builder.Build(ctx, reviewinput.BuildRequest{
		Target:   target,
		Snapshot: snapshot,
	})
}

func newGitHubEngineProcessor(sqlDB *sql.DB, cfg *config.Config, defaultRoute string, resolveProvider func(string) llm.Provider) (scheduler.Processor, *githubplatform.Client, error) {
	if sqlDB == nil {
		return nil, nil, fmt.Errorf("worker runtime: sql db is required")
	}
	if cfg == nil {
		return nil, nil, fmt.Errorf("worker runtime: configuration is required")
	}
	if strings.TrimSpace(cfg.GitHubBaseURL) == "" || strings.TrimSpace(cfg.GitHubToken) == "" {
		return nil, nil, nil
	}
	client, err := githubplatform.NewClient(cfg.GitHubBaseURL, cfg.GitHubToken)
	if err != nil {
		return nil, nil, err
	}
	loader := gitHubInputLoader{
		adapter: githubplatform.NewAdapter[githubplatform.ReviewSnapshot](client),
		builder: reviewinput.NewBuilder(
			rules.NewLoader(client, rules.PlatformDefaults{
				Instructions:        "Platform defaults: prioritize correctness, security, and least-privilege behavior.",
				ConfidenceThreshold: 0.72,
				SeverityThreshold:   "medium",
				IncludePaths:        []string{"src/**"},
				ExcludePaths:        []string{"vendor/**"},
				GateMode:            "external_status",
				ProviderRoute:       defaultRoute,
			}),
			ctxpkg.NewAssembler(),
		),
	}
	engine := reviewcore.NewEngine(
		reviewpack.DefaultPacks(),
		reviewcore.NewRouteAwareLegacyProviderPackRunner(resolveProvider),
		judge.NewEngine(),
	)
	statusPublisher := gate.NewGitHubStatusPublisher(gitHubStatusClientAdapter{client: client}, db.New(sqlDB))
	return engineBackedProcessor{
		queries:                  db.New(sqlDB),
		platform:                 reviewcore.PlatformGitHub,
		loader:                   loader,
		engine:                   engine,
		statusPublisher:          statusPublisher,
		selectedPackIDs:          normalizedReviewPacks(cfg.ReviewPacks),
		advisor:                  reviewadvisor.New(resolveProvider),
		advisorRoute:             strings.TrimSpace(cfg.ReviewAdvisorRoute),
		externalComparisonLoader: workerGitHubExternalComparisonLoader{client: client},
		externalReviewerIDs:      normalizedReviewerIDs(cfg.ReviewCompareReviewers),
	}, client, nil
}

func (p engineBackedProcessor) ProcessRun(ctx context.Context, run db.ReviewRun) (scheduler.ProcessOutcome, error) {
	if p.queries == nil || p.loader == nil || p.engine == nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("worker runtime: github engine processor is not configured")
	}
	project, err := p.queries.GetProject(ctx, run.ProjectID)
	if err != nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("worker runtime: load project: %w", err)
	}
	mr, err := p.queries.GetMergeRequest(ctx, run.MergeRequestID)
	if err != nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("worker runtime: load merge request: %w", err)
	}

	target := reviewcore.ReviewTarget{
		Platform:   p.platform,
		Repository: project.PathWithNamespace,
		Number:     mr.MrIid,
		URL:        mr.WebUrl,
	}
	input, err := p.loader.Load(ctx, target)
	if err != nil {
		return scheduler.ProcessOutcome{}, err
	}
	p.publishRunningStage(ctx, run, reviewstatus.StageRunningPacks)
	bundle, err := p.engine.Run(ctx, input, p.selectedPackIDs)
	if err != nil {
		return scheduler.ProcessOutcome{}, err
	}
	if strings.TrimSpace(p.advisorRoute) != "" && p.advisor != nil {
		p.publishRunningStage(ctx, run, reviewstatus.StageRunningAdvisor)
		advisorArtifact, err := p.advisor.Advise(ctx, input, bundle, p.advisorRoute)
		if err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		bundle.AdvisorArtifact = advisorArtifact
	}
	bundle.Comparisons = compare.BuildComparisonArtifactsForBundle(bundle)
	if len(p.externalReviewerIDs) > 0 && p.externalComparisonLoader != nil {
		p.publishRunningStage(ctx, run, reviewstatus.StageComparingExternal)
		externalArtifacts, err := p.externalComparisonLoader.Load(ctx, target, p.externalReviewerIDs)
		if err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		bundle.Comparisons = append(bundle.Comparisons, externalArtifacts...)
	}

	status := "completed"
	if bundle.JudgeVerdict == reviewcore.VerdictRequestedChanges {
		status = "requested_changes"
	}
	return scheduler.ProcessOutcome{
		Status:         status,
		ReviewBundle:   bundle,
		ReviewFindings: syntheticFindingsForPlatform(run, bundle),
	}, nil
}

func (p engineBackedProcessor) publishRunningStage(ctx context.Context, run db.ReviewRun, stage reviewstatus.Stage) {
	if p.statusPublisher == nil {
		return
	}
	_ = p.statusPublisher.PublishStatus(ctx, gate.Result{
		RunID:          run.ID,
		MergeRequestID: run.MergeRequestID,
		ProjectID:      run.ProjectID,
		HeadSHA:        run.HeadSha,
		State:          "running",
		Stage:          stage,
		Source:         "review_run",
	})
}

func syntheticFindingsForPlatform(run db.ReviewRun, bundle reviewcore.ReviewBundle) []db.ReviewFinding {
	if bundle.Target.Platform == reviewcore.PlatformGitLab {
		return nil
	}
	findings := make([]db.ReviewFinding, 0, len(bundle.PublishCandidates))
	for _, candidate := range bundle.PublishCandidates {
		if candidate.Type != "finding" {
			continue
		}
		finding := db.ReviewFinding{
			ReviewRunID:    run.ID,
			MergeRequestID: run.MergeRequestID,
			Category:       "review",
			Severity:       defaultSeverity(candidate.Severity),
			Confidence:     1.0,
			Title:          candidate.Title,
			BodyMarkdown:   sql.NullString{String: candidate.Body, Valid: strings.TrimSpace(candidate.Body) != ""},
			Path:           "",
			State:          "active",
		}
		if candidate.Location != nil {
			finding.Path = candidate.Location.Path
			if candidate.Location.Line > 0 {
				line := int64(candidate.Location.Line)
				if candidate.Location.Side == reviewcore.LocationSideOld {
					finding.OldLine = sql.NullInt32{Int32: int32(line), Valid: true}
				} else {
					finding.NewLine = sql.NullInt32{Int32: int32(line), Valid: true}
				}
			}
		}
		findings = append(findings, finding)
	}
	return findings
}

func defaultSeverity(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "medium"
	}
	return trimmed
}

func detectRunPlatform(ctx context.Context, queries *db.Queries, run db.ReviewRun) (reviewcore.Platform, error) {
	if queries == nil {
		return reviewcore.PlatformGitLab, nil
	}
	mr, err := queries.GetMergeRequest(ctx, run.MergeRequestID)
	if err == nil {
		return detectPlatformFromURL(mr.WebUrl), nil
	}
	project, projectErr := queries.GetProject(ctx, run.ProjectID)
	if projectErr != nil {
		return reviewcore.PlatformGitLab, fmt.Errorf("worker runtime: detect platform: %w", err)
	}
	instance, instanceErr := queries.GetGitlabInstance(ctx, project.GitlabInstanceID)
	if instanceErr != nil {
		return reviewcore.PlatformGitLab, fmt.Errorf("worker runtime: detect platform: %w", err)
	}
	return detectPlatformFromURL(instance.Url), nil
}

func detectPlatformFromURL(raw string) reviewcore.Platform {
	if strings.Contains(strings.TrimSpace(raw), "/pull/") {
		return reviewcore.PlatformGitHub
	}
	return reviewcore.PlatformGitLab
}

type workerGitHubCommentReader interface {
	ListIssueComments(ctx context.Context, owner, repo string, number int64) ([]githubplatform.IssueComment, error)
	ListReviewComments(ctx context.Context, owner, repo string, number int64) ([]githubplatform.ReviewComment, error)
}

type workerGitHubExternalComparisonLoader struct {
	client workerGitHubCommentReader
}

func (l workerGitHubExternalComparisonLoader) Load(ctx context.Context, target reviewcore.ReviewTarget, reviewerIDs []string) ([]reviewcore.ComparisonArtifact, error) {
	if l.client == nil {
		return nil, fmt.Errorf("worker runtime: github external comparison loader requires a client")
	}
	owner, repo, ok := strings.Cut(strings.TrimSpace(target.Repository), "/")
	if !ok || owner == "" || repo == "" {
		return nil, fmt.Errorf("worker runtime: github repository must be owner/repo")
	}
	issueComments, err := l.client.ListIssueComments(ctx, owner, repo, target.Number)
	if err != nil {
		return nil, err
	}
	reviewComments, err := l.client.ListReviewComments(ctx, owner, repo, target.Number)
	if err != nil {
		return nil, err
	}
	return filterComparisonArtifacts(compare.IngestGitHubReviewerArtifacts(issueComments, reviewComments), reviewerIDs), nil
}

type workerGitLabCommentReader interface {
	ListMergeRequestNotesByProjectRef(ctx context.Context, projectRef string, mergeRequestIID int64) ([]legacygitlab.MergeRequestNote, error)
	ListMergeRequestDiscussionsByProjectRef(ctx context.Context, projectRef string, mergeRequestIID int64) ([]legacygitlab.MergeRequestDiscussion, error)
}

type workerGitLabExternalComparisonLoader struct {
	client workerGitLabCommentReader
}

func (l workerGitLabExternalComparisonLoader) Load(ctx context.Context, target reviewcore.ReviewTarget, reviewerIDs []string) ([]reviewcore.ComparisonArtifact, error) {
	if l.client == nil {
		return nil, fmt.Errorf("worker runtime: gitlab external comparison loader requires a client")
	}
	notes, err := l.client.ListMergeRequestNotesByProjectRef(ctx, target.Repository, target.Number)
	if err != nil {
		return nil, err
	}
	discussions, err := l.client.ListMergeRequestDiscussionsByProjectRef(ctx, target.Repository, target.Number)
	if err != nil {
		return nil, err
	}
	return filterComparisonArtifacts(compare.IngestGitLabReviewerArtifacts(notes, discussions), reviewerIDs), nil
}

func normalizedReviewPacks(values []string) []string {
	return normalizedStringList(values)
}

func normalizedReviewerIDs(values []string) []string {
	return normalizedStringList(values)
}

func normalizedStringList(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			normalized = append(normalized, value)
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func filterComparisonArtifacts(artifacts []reviewcore.ComparisonArtifact, reviewerIDs []string) []reviewcore.ComparisonArtifact {
	if len(reviewerIDs) == 0 {
		return artifacts
	}
	allowed := map[string]struct{}{}
	for _, reviewerID := range reviewerIDs {
		reviewerID = strings.TrimSpace(reviewerID)
		if reviewerID != "" {
			allowed[reviewerID] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return artifacts
	}
	filtered := make([]reviewcore.ComparisonArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if _, ok := allowed[strings.TrimSpace(artifact.ReviewerID)]; ok {
			filtered = append(filtered, artifact)
		}
	}
	return filtered
}
