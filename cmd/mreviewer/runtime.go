package main

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/mreviewer/mreviewer/internal/compare"
	"github.com/mreviewer/mreviewer/internal/config"
	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
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
)

type RunRequest struct {
	Target           reviewcore.ReviewTarget
	OutputMode       outputMode
	PublishMode      publishMode
	ReviewerPacks    []string
	RouteOverride    string
	AdvisorRoute     string
	CompareReviewers []string
	CompareTargets   []reviewcore.ReviewTarget
}

type RunResult struct {
	Target              reviewcore.ReviewTarget
	JudgeVerdict        reviewcore.Verdict
	AdvisorArtifact     *reviewcore.ReviewerArtifact
	CompareTargets      []reviewcore.ReviewTarget
	Markdown            string
	Comparison          *compare.Report
	AggregateComparison *compare.AggregateReport
	DecisionBenchmark   *compare.DecisionBenchmarkReport
}

type Runner interface {
	Run(ctx context.Context, request RunRequest) (RunResult, error)
}

type InputLoader interface {
	Load(ctx context.Context, target reviewcore.ReviewTarget) (reviewcore.ReviewInput, error)
}

type BundleEngine interface {
	Run(ctx context.Context, input reviewcore.ReviewInput, selectedPackIDs []string) (reviewcore.ReviewBundle, error)
}

type BundlePublisher interface {
	Publish(ctx context.Context, input reviewcore.ReviewInput, mode publishMode, bundle reviewcore.ReviewBundle) error
}

type reviewStatusState string

const (
	reviewStatusRunning reviewStatusState = "running"
	reviewStatusPassed  reviewStatusState = "passed"
	reviewStatusFailed  reviewStatusState = "failed"
)

type StatusPublisher interface {
	Publish(ctx context.Context, input reviewcore.ReviewInput, update reviewStatusUpdate) error
}

type reviewStatusUpdate struct {
	State  reviewStatusState
	Stage  reviewstatus.Stage
	Bundle *reviewcore.ReviewBundle
}

type ExternalComparisonLoader interface {
	Load(ctx context.Context, target reviewcore.ReviewTarget, reviewerIDs []string) ([]reviewcore.ComparisonArtifact, error)
}

type Advisor interface {
	Advise(ctx context.Context, input reviewcore.ReviewInput, bundle reviewcore.ReviewBundle, route string) (*reviewcore.ReviewerArtifact, error)
}

type unimplementedRunner struct{}

func (unimplementedRunner) Run(_ context.Context, request RunRequest) (RunResult, error) {
	return RunResult{
		Target:         request.Target,
		CompareTargets: append([]reviewcore.ReviewTarget(nil), request.CompareTargets...),
	}, fmt.Errorf("portable review runtime is not implemented yet")
}

type runtimeRunner struct {
	loader                    InputLoader
	loaders                   map[reviewcore.Platform]InputLoader
	engine                    BundleEngine
	advisor                   Advisor
	publishers                map[reviewcore.Platform]BundlePublisher
	statusPublishers          map[reviewcore.Platform]StatusPublisher
	externalComparisonLoaders map[reviewcore.Platform]ExternalComparisonLoader
}

func (r runtimeRunner) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	loader := r.loader
	if r.loaders != nil {
		if platformLoader, ok := r.loaders[request.Target.Platform]; ok {
			loader = platformLoader
		}
	}
	if loader == nil {
		return RunResult{}, fmt.Errorf("portable review runtime: input loader is required")
	}
	if r.engine == nil {
		return RunResult{}, fmt.Errorf("portable review runtime: engine is required")
	}

	input, err := loader.Load(ctx, request.Target)
	if err != nil {
		return RunResult{}, err
	}
	statusPublisher := r.statusPublishers[request.Target.Platform]
	if err := publishRuntimeStatus(ctx, statusPublisher, input, reviewStatusUpdate{
		State: reviewStatusRunning,
		Stage: reviewstatus.StageRunningPacks,
	}); err != nil {
		return RunResult{}, err
	}
	if request.RouteOverride != "" {
		if input.Metadata == nil {
			input.Metadata = map[string]string{}
		}
		input.Metadata["provider_route"] = request.RouteOverride
	}

	bundle, err := r.engine.Run(ctx, input, request.ReviewerPacks)
	if err != nil {
		_ = publishRuntimeStatus(ctx, statusPublisher, input, reviewStatusUpdate{
			State: reviewStatusFailed,
			Stage: reviewstatus.StageRunningPacks,
		})
		return RunResult{}, err
	}
	if strings.TrimSpace(request.AdvisorRoute) != "" && r.advisor != nil {
		if err := publishRuntimeStatus(ctx, statusPublisher, input, reviewStatusUpdate{
			State:  reviewStatusRunning,
			Stage:  reviewstatus.StageRunningAdvisor,
			Bundle: &bundle,
		}); err != nil {
			return RunResult{}, err
		}
		advisorArtifact, err := r.advisor.Advise(ctx, input, bundle, request.AdvisorRoute)
		if err != nil {
			return RunResult{}, err
		}
		bundle.AdvisorArtifact = advisorArtifact
	}
	if request.PublishMode != publishModeArtifactOnly && r.publishers != nil {
		if publisher, ok := r.publishers[request.Target.Platform]; ok && publisher != nil {
			if err := publishRuntimeStatus(ctx, statusPublisher, input, reviewStatusUpdate{
				State:  reviewStatusRunning,
				Stage:  reviewstatus.StagePublishing,
				Bundle: &bundle,
			}); err != nil {
				return RunResult{}, err
			}
			if err := publisher.Publish(ctx, input, request.PublishMode, bundle); err != nil {
				_ = publishRuntimeStatus(ctx, statusPublisher, input, reviewStatusUpdate{
					State:  reviewStatusFailed,
					Stage:  reviewstatus.StagePublishing,
					Bundle: &bundle,
				})
				return RunResult{}, err
			}
		}
	}
	bundle.Comparisons = compare.BuildComparisonArtifactsForBundle(bundle)
	if len(request.CompareReviewers) > 0 && r.externalComparisonLoaders != nil {
		if comparisonLoader, ok := r.externalComparisonLoaders[request.Target.Platform]; ok && comparisonLoader != nil {
			if err := publishRuntimeStatus(ctx, statusPublisher, input, reviewStatusUpdate{
				State:  reviewStatusRunning,
				Stage:  reviewstatus.StageComparingExternal,
				Bundle: &bundle,
			}); err != nil {
				return RunResult{}, err
			}
			externalArtifacts, err := comparisonLoader.Load(ctx, request.Target, request.CompareReviewers)
			if err != nil {
				return RunResult{}, err
			}
			bundle.Comparisons = append(bundle.Comparisons, externalArtifacts...)
		}
	}
	comparisonReport := compare.BuildReport(bundle.Comparisons)

	aggregateReports := []compare.Report{comparisonReport}
	for _, compareTarget := range request.CompareTargets {
		if err := publishRuntimeStatus(ctx, statusPublisher, input, reviewStatusUpdate{
			State:  reviewStatusRunning,
			Stage:  reviewstatus.StageComparingTargets,
			Bundle: &bundle,
		}); err != nil {
			return RunResult{}, err
		}
		compareLoader := loader
		if r.loaders != nil {
			if platformLoader, ok := r.loaders[compareTarget.Platform]; ok {
				compareLoader = platformLoader
			}
		}
		if compareLoader == nil {
			return RunResult{}, fmt.Errorf("portable review runtime: input loader is required for compare target %s", compareTarget.URL)
		}
		compareInput, err := compareLoader.Load(ctx, compareTarget)
		if err != nil {
			return RunResult{}, err
		}
		if request.RouteOverride != "" {
			if compareInput.Metadata == nil {
				compareInput.Metadata = map[string]string{}
			}
			compareInput.Metadata["provider_route"] = request.RouteOverride
		}
		compareBundle, err := r.engine.Run(ctx, compareInput, request.ReviewerPacks)
		if err != nil {
			return RunResult{}, err
		}
		compareBundle.Comparisons = compare.BuildComparisonArtifactsForBundle(compareBundle)
		aggregateReports = append(aggregateReports, compare.BuildReport(compareBundle.Comparisons))
	}
	aggregateReport := compare.BuildAggregateReport(aggregateReports)
	decisionBenchmark := compare.BuildDecisionBenchmarkReportForBundle(bundle)
	finalState := reviewStatusPassed
	if bundle.JudgeVerdict == reviewcore.VerdictRequestedChanges {
		finalState = reviewStatusFailed
	}
	if err := publishRuntimeStatus(ctx, statusPublisher, input, reviewStatusUpdate{
		State:  finalState,
		Stage:  reviewstatus.StageCompleted,
		Bundle: &bundle,
	}); err != nil {
		return RunResult{}, err
	}

	return RunResult{
		Target:              bundle.Target,
		JudgeVerdict:        bundle.JudgeVerdict,
		AdvisorArtifact:     bundle.AdvisorArtifact,
		CompareTargets:      append([]reviewcore.ReviewTarget(nil), request.CompareTargets...),
		Markdown:            renderBundleMarkdown(bundle),
		Comparison:          &comparisonReport,
		AggregateComparison: &aggregateReport,
		DecisionBenchmark:   &decisionBenchmark,
	}, nil
}

func publishRuntimeStatus(ctx context.Context, publisher StatusPublisher, input reviewcore.ReviewInput, update reviewStatusUpdate) error {
	if publisher == nil {
		return nil
	}
	return publisher.Publish(ctx, input, update)
}

type gitlabInputLoader struct {
	adapter *gitlabplatform.Adapter
	builder *reviewinput.Builder
}

func (l gitlabInputLoader) Load(ctx context.Context, target reviewcore.ReviewTarget) (reviewcore.ReviewInput, error) {
	if l.adapter == nil || l.builder == nil {
		return reviewcore.ReviewInput{}, fmt.Errorf("gitlab input loader: dependencies are required")
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

func newDefaultRunner(cfg *config.Config) (Runner, error) {
	if cfg == nil {
		return nil, fmt.Errorf("portable review runtime: configuration is required")
	}

	defaultRoute, providerConfigs, err := cliProviderConfigsFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	providerRegistry, err := buildCLIProviderRegistry(defaultRoute, providerConfigs)
	if err != nil {
		return nil, err
	}

	engine := reviewcore.NewEngine(
		reviewpack.DefaultPacks(),
		reviewcore.NewRouteAwareLegacyProviderPackRunner(providerRegistry.ResolveWithFallback),
		newJudgeAdapter(),
	)

	loaders := map[reviewcore.Platform]InputLoader{}
	publishers := map[reviewcore.Platform]BundlePublisher{}
	statusPublishers := map[reviewcore.Platform]StatusPublisher{}
	externalComparisonLoaders := map[reviewcore.Platform]ExternalComparisonLoader{}

	if strings.TrimSpace(cfg.GitLabBaseURL) != "" && strings.TrimSpace(cfg.GitLabToken) != "" {
		client, err := legacygitlab.NewClient(cfg.GitLabBaseURL, cfg.GitLabToken)
		if err != nil {
			return nil, err
		}
		loader := reviewinput.NewBuilder(
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
		)
		loaders[reviewcore.PlatformGitLab] = gitlabInputLoader{
			adapter: gitlabplatform.NewAdapter(client),
			builder: loader,
		}
		publishers[reviewcore.PlatformGitLab] = gitLabBundlePublisher{
			publisher: gitlabplatform.NewPublisher(client),
		}
		statusPublishers[reviewcore.PlatformGitLab] = gitLabStatusPublisher{
			client: client,
		}
		externalComparisonLoaders[reviewcore.PlatformGitLab] = gitLabExternalComparisonLoader{client: client}
	}

	if githubLoader, err := newGitHubInputLoader(cfg, defaultRoute); err == nil {
		loaders[reviewcore.PlatformGitHub] = githubLoader
	} else if strings.TrimSpace(cfg.GitHubBaseURL) != "" || strings.TrimSpace(cfg.GitHubToken) != "" {
		return nil, err
	}
	if strings.TrimSpace(cfg.GitHubBaseURL) != "" && strings.TrimSpace(cfg.GitHubToken) != "" {
		if githubClient, err := githubplatform.NewClient(cfg.GitHubBaseURL, cfg.GitHubToken); err == nil {
			publishers[reviewcore.PlatformGitHub] = gitHubBundlePublisher{
				publisher: githubplatform.NewPublisher(githubClient),
			}
			statusPublishers[reviewcore.PlatformGitHub] = gitHubStatusPublisher{
				client: githubClient,
			}
			externalComparisonLoaders[reviewcore.PlatformGitHub] = gitHubExternalComparisonLoader{client: githubClient}
		} else {
			return nil, err
		}
	}
	if len(loaders) == 0 {
		return nil, fmt.Errorf("portable review runtime: at least one complete GitHub or GitLab configuration is required")
	}

	return runtimeRunner{
		loaders:                   loaders,
		engine:                    engine,
		advisor:                   newRuntimeAdvisor(providerRegistry.ResolveWithFallback),
		publishers:                publishers,
		statusPublishers:          statusPublishers,
		externalComparisonLoaders: externalComparisonLoaders,
	}, nil
}

type judgeAdapter struct{}

func newJudgeAdapter() judgeAdapter {
	return judgeAdapter{}
}

func (judgeAdapter) Decide(target reviewcore.ReviewTarget, artifacts []reviewcore.ReviewerArtifact) reviewcore.ReviewBundle {
	// Avoid importing internal/judge into tests through extra indirection here.
	// The production runtime reuses the same decision semantics as the package-level judge engine.
	engine := judge.NewEngine()
	return engine.Decide(target, artifacts)
}

func cliProviderConfigsFromConfig(cfg *config.Config) (string, map[string]llm.ProviderConfig, error) {
	if cfg == nil {
		return "", nil, fmt.Errorf("portable review runtime: configuration is required")
	}
	if len(cfg.LLM.Routes) > 0 {
		defaultRoute := strings.TrimSpace(cfg.LLM.DefaultRoute)
		if defaultRoute == "" {
			return "", nil, fmt.Errorf("portable review runtime: llm.default_route is required when llm.routes is configured")
		}
		routes := make(map[string]llm.ProviderConfig, len(cfg.LLM.Routes))
		for routeName, route := range cfg.LLM.Routes {
			routes[strings.TrimSpace(routeName)] = llm.ProviderConfig{
				Kind:                strings.TrimSpace(route.Provider),
				BaseURL:             strings.TrimSpace(route.BaseURL),
				APIKey:              strings.TrimSpace(route.APIKey),
				Model:               strings.TrimSpace(route.Model),
				RouteName:           strings.TrimSpace(routeName),
				OutputMode:          strings.TrimSpace(route.OutputMode),
				MaxTokens:           route.MaxTokens,
				MaxCompletionTokens: route.MaxCompletionTokens,
				ReasoningEffort:     strings.TrimSpace(route.ReasoningEffort),
				Temperature:         route.Temperature,
			}
		}
		return defaultRoute, routes, nil
	}

	const legacyDefaultRoute = "default"
	return legacyDefaultRoute, map[string]llm.ProviderConfig{
		legacyDefaultRoute: {
			Kind:       llm.ProviderKindMiniMax,
			BaseURL:    strings.TrimSpace(cfg.AnthropicBaseURL),
			APIKey:     strings.TrimSpace(cfg.AnthropicAPIKey),
			Model:      strings.TrimSpace(cfg.AnthropicModel),
			RouteName:  legacyDefaultRoute,
			MaxTokens:  4096,
			OutputMode: "tool_call",
		},
	}, nil
}

func buildCLIProviderRegistry(defaultRoute string, providerConfigs map[string]llm.ProviderConfig) (*llm.ProviderRegistry, error) {
	defaultCfg, ok := providerConfigs[defaultRoute]
	if !ok {
		return nil, fmt.Errorf("portable review runtime: missing default provider route %q", defaultRoute)
	}
	defaultProvider, err := llm.NewProviderFromConfig(defaultCfg)
	if err != nil {
		return nil, err
	}
	registry := llm.NewProviderRegistry(nil, defaultRoute, defaultProvider)
	for routeName, routeCfg := range providerConfigs {
		if strings.TrimSpace(routeName) == strings.TrimSpace(defaultRoute) {
			continue
		}
		provider, err := llm.NewProviderFromConfig(routeCfg)
		if err != nil {
			return nil, err
		}
		registry.Register(routeName, provider)
	}
	return registry, nil
}

func newRuntimeAdvisor(resolve func(string) llm.Provider) Advisor {
	return reviewadvisor.New(resolve)
}

func renderBundleMarkdown(bundle reviewcore.ReviewBundle) string {
	lines := []string{
		fmt.Sprintf("# Review for %s", bundle.Target.Repository),
		fmt.Sprintf("Verdict: %s", bundle.JudgeVerdict),
	}
	if bundle.JudgeSummary != "" {
		lines = append(lines, "", bundle.JudgeSummary)
	}
	if bundle.AdvisorArtifact != nil && strings.TrimSpace(bundle.AdvisorArtifact.Summary) != "" {
		lines = append(lines, "", "## Advisor", "", bundle.AdvisorArtifact.Summary)
	}
	return strings.Join(lines, "\n")
}

type gitHubInputLoader struct {
	adapter *githubplatform.Adapter[githubplatform.ReviewSnapshot]
	builder *reviewinput.Builder
}

func (l gitHubInputLoader) Load(ctx context.Context, target reviewcore.ReviewTarget) (reviewcore.ReviewInput, error) {
	if l.adapter == nil || l.builder == nil {
		return reviewcore.ReviewInput{}, fmt.Errorf("github input loader: dependencies are required")
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

func newGitHubInputLoader(cfg *config.Config, defaultRoute string) (InputLoader, error) {
	if cfg == nil {
		return nil, fmt.Errorf("github input loader: configuration is required")
	}
	if strings.TrimSpace(cfg.GitHubBaseURL) == "" || strings.TrimSpace(cfg.GitHubToken) == "" {
		return nil, fmt.Errorf("github input loader: github configuration is incomplete")
	}

	client, err := githubplatform.NewClient(cfg.GitHubBaseURL, cfg.GitHubToken)
	if err != nil {
		return nil, err
	}
	return gitHubInputLoader{
		adapter: githubplatform.NewAdapter[githubplatform.ReviewSnapshot](client),
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
	}, nil
}

type gitLabBundlePublisher struct {
	publisher *gitlabplatform.Publisher
}

func (p gitLabBundlePublisher) Publish(ctx context.Context, input reviewcore.ReviewInput, mode publishMode, bundle reviewcore.ReviewBundle) error {
	if p.publisher == nil {
		return fmt.Errorf("gitlab bundle publisher: publisher is required")
	}
	projectID, err := parseProjectIDString(input.Metadata["project_id"])
	if err != nil {
		return err
	}
	return p.publisher.Publish(ctx, gitlabplatform.PublishRequest{
		ProjectID:       projectID,
		MergeRequestIID: input.Target.Number,
		Mode:            gitlabplatform.PublishMode(mode),
		Bundle:          bundle,
	})
}

type gitHubBundlePublisher struct {
	publisher *githubplatform.Publisher
}

func (p gitHubBundlePublisher) Publish(ctx context.Context, input reviewcore.ReviewInput, mode publishMode, bundle reviewcore.ReviewBundle) error {
	if p.publisher == nil {
		return fmt.Errorf("github bundle publisher: publisher is required")
	}
	owner, repo, ok := strings.Cut(input.Target.Repository, "/")
	if !ok || owner == "" || repo == "" {
		return fmt.Errorf("github bundle publisher: repository must be owner/repo")
	}
	return p.publisher.Publish(ctx, githubplatform.PublishRequest{
		Owner:  owner,
		Repo:   repo,
		Number: input.Target.Number,
		Mode:   githubplatform.PublishMode(mode),
		Bundle: bundle,
	})
}

type gitLabStatusSetter interface {
	SetCommitStatus(ctx context.Context, req legacygitlab.CommitStatusRequest) error
}

type gitLabStatusPublisher struct {
	client gitLabStatusSetter
}

func (p gitLabStatusPublisher) Publish(ctx context.Context, input reviewcore.ReviewInput, update reviewStatusUpdate) error {
	if p.client == nil {
		return fmt.Errorf("gitlab status publisher: client is required")
	}
	projectID, err := strconv.ParseInt(strings.TrimSpace(input.Metadata["project_id"]), 10, 64)
	if err != nil || projectID == 0 {
		return fmt.Errorf("gitlab status publisher: missing project_id")
	}
	headSHA := strings.TrimSpace(input.Snapshot.HeadSHA)
	if headSHA == "" {
		return fmt.Errorf("gitlab status publisher: missing head sha")
	}
	return p.client.SetCommitStatus(ctx, legacygitlab.CommitStatusRequest{
		ProjectID:   projectID,
		SHA:         headSHA,
		State:       mapCLICommitStatusState(update.State),
		Name:        "mreviewer/ai-review",
		Description: commitStatusDescriptionForUpdate(update),
		Ref:         strings.TrimSpace(input.Snapshot.SourceBranch),
		TargetURL:   strings.TrimSpace(input.Target.URL),
	})
}

type gitHubStatusSetter interface {
	SetCommitStatus(ctx context.Context, req githubplatform.CommitStatusRequest) error
}

type gitHubStatusPublisher struct {
	client gitHubStatusSetter
}

func (p gitHubStatusPublisher) Publish(ctx context.Context, input reviewcore.ReviewInput, update reviewStatusUpdate) error {
	if p.client == nil {
		return fmt.Errorf("github status publisher: client is required")
	}
	owner, repo, ok := strings.Cut(strings.TrimSpace(input.Target.Repository), "/")
	if !ok || owner == "" || repo == "" {
		return fmt.Errorf("github status publisher: repository must be owner/repo")
	}
	headSHA := strings.TrimSpace(input.Snapshot.HeadSHA)
	if headSHA == "" {
		return fmt.Errorf("github status publisher: missing head sha")
	}
	return p.client.SetCommitStatus(ctx, githubplatform.CommitStatusRequest{
		Owner:       owner,
		Repo:        repo,
		SHA:         headSHA,
		State:       mapCLICommitStatusState(update.State),
		Context:     "mreviewer/ai-review",
		Description: commitStatusDescriptionForUpdate(update),
		TargetURL:   strings.TrimSpace(input.Target.URL),
	})
}

func mapCLICommitStatusState(state reviewStatusState) string {
	switch state {
	case reviewStatusRunning:
		return "pending"
	case reviewStatusPassed:
		return "success"
	default:
		return "failure"
	}
}

func commitStatusDescriptionForUpdate(update reviewStatusUpdate) string {
	switch update.State {
	case reviewStatusRunning:
		if description := update.Stage.Description(); description != "" {
			return description
		}
		return "AI review is running"
	case reviewStatusPassed:
		return "AI review passed"
	default:
		if update.Bundle != nil {
			blocking := 0
			for _, candidate := range update.Bundle.PublishCandidates {
				if candidate.Type == "finding" {
					blocking++
				}
			}
			if blocking > 0 {
				return fmt.Sprintf("AI review found %d blocking findings", blocking)
			}
		}
		return "AI review failed"
	}
}

type gitHubCommentReader interface {
	ListIssueComments(ctx context.Context, owner, repo string, number int64) ([]githubplatform.IssueComment, error)
	ListReviewComments(ctx context.Context, owner, repo string, number int64) ([]githubplatform.ReviewComment, error)
}

type gitHubExternalComparisonLoader struct {
	client gitHubCommentReader
}

func (l gitHubExternalComparisonLoader) Load(ctx context.Context, target reviewcore.ReviewTarget, reviewerIDs []string) ([]reviewcore.ComparisonArtifact, error) {
	if l.client == nil {
		return nil, fmt.Errorf("github external comparison loader: client is required")
	}
	owner, repo, ok := strings.Cut(strings.TrimSpace(target.Repository), "/")
	if !ok || owner == "" || repo == "" {
		return nil, fmt.Errorf("github external comparison loader: repository must be owner/repo")
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

type gitLabCommentReader interface {
	ListMergeRequestNotesByProjectRef(ctx context.Context, projectRef string, mergeRequestIID int64) ([]legacygitlab.MergeRequestNote, error)
	ListMergeRequestDiscussionsByProjectRef(ctx context.Context, projectRef string, mergeRequestIID int64) ([]legacygitlab.MergeRequestDiscussion, error)
}

type gitLabExternalComparisonLoader struct {
	client gitLabCommentReader
}

func (l gitLabExternalComparisonLoader) Load(ctx context.Context, target reviewcore.ReviewTarget, reviewerIDs []string) ([]reviewcore.ComparisonArtifact, error) {
	if l.client == nil {
		return nil, fmt.Errorf("gitlab external comparison loader: client is required")
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

func parseProjectIDString(raw string) (int64, error) {
	projectID, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || projectID <= 0 {
		return 0, fmt.Errorf("gitlab bundle publisher: valid project_id metadata is required")
	}
	return projectID, nil
}

func resolveReviewTarget(raw string) (reviewcore.ReviewTarget, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return reviewcore.ReviewTarget{}, fmt.Errorf("parse target url: %w", err)
	}

	switch strings.ToLower(parsed.Host) {
	case "github.com":
		return resolveGitHubTarget(parsed)
	default:
		return resolveGitLabTarget(parsed)
	}
}

func resolveGitHubTarget(parsed *url.URL) (reviewcore.ReviewTarget, error) {
	segments := trimPathSegments(parsed.Path)
	if len(segments) != 4 || segments[2] != "pull" {
		return reviewcore.ReviewTarget{}, fmt.Errorf("unsupported github pull request url: %s", parsed.String())
	}
	number, err := strconv.ParseInt(segments[3], 10, 64)
	if err != nil {
		return reviewcore.ReviewTarget{}, fmt.Errorf("parse github pull request number: %w", err)
	}

	return reviewcore.ReviewTarget{
		Platform:   reviewcore.PlatformGitHub,
		Repository: segments[0] + "/" + segments[1],
		Number:     number,
		URL:        parsed.String(),
	}, nil
}

func resolveGitLabTarget(parsed *url.URL) (reviewcore.ReviewTarget, error) {
	parts := strings.SplitN(strings.Trim(parsed.Path, "/"), "/-/merge_requests/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return reviewcore.ReviewTarget{}, fmt.Errorf("unsupported gitlab merge request url: %s", parsed.String())
	}
	number, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return reviewcore.ReviewTarget{}, fmt.Errorf("parse gitlab merge request number: %w", err)
	}

	return reviewcore.ReviewTarget{
		Platform:   reviewcore.PlatformGitLab,
		Repository: parts[0],
		Number:     number,
		URL:        parsed.String(),
	}, nil
}

func trimPathSegments(path string) []string {
	rawSegments := strings.Split(strings.Trim(path, "/"), "/")
	segments := make([]string, 0, len(rawSegments))
	for _, segment := range rawSegments {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}
