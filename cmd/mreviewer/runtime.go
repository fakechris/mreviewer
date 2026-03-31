package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mreviewer/mreviewer/internal/config"
	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/judge"
	"github.com/mreviewer/mreviewer/internal/llm"
	platformgithub "github.com/mreviewer/mreviewer/internal/platform/github"
	platformgitlab "github.com/mreviewer/mreviewer/internal/platform/gitlab"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewinput"
	"github.com/mreviewer/mreviewer/internal/reviewpack"
	"github.com/mreviewer/mreviewer/internal/rules"
)

type snapshotFetcher interface {
	FetchSnapshot(ctx context.Context, target core.ReviewTarget) (core.PlatformSnapshot, error)
}

type reviewInputBuilder interface {
	Build(ctx context.Context, input reviewinput.BuildInput) (core.ReviewInput, error)
}

type failingEngine struct{ err error }

func (e failingEngine) Run(context.Context, core.ReviewInput, core.RunOptions) (core.ReviewBundle, error) {
	return core.ReviewBundle{}, e.err
}

func defaultLoadInput(ctx context.Context, configPath string, target core.ReviewTarget) (core.ReviewInput, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("load config: %w", err)
	}
	defaultRoute, _, _, err := providerConfigsFromConfig(cfg)
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("resolve provider routes: %w", err)
	}
	switch target.Platform {
	case core.PlatformGitLab:
		if strings.TrimSpace(cfg.GitLabBaseURL) == "" {
			return core.ReviewInput{}, fmt.Errorf("GITLAB_BASE_URL is required")
		}
		if strings.TrimSpace(cfg.GitLabToken) == "" {
			return core.ReviewInput{}, fmt.Errorf("GITLAB_TOKEN is required")
		}
		client, err := legacygitlab.NewClient(cfg.GitLabBaseURL, cfg.GitLabToken)
		if err != nil {
			return core.ReviewInput{}, fmt.Errorf("configure gitlab client: %w", err)
		}
		adapter := platformgitlab.NewAdapter(client)
		builder := reviewinput.NewBuilder(
			rules.NewLoader(client, defaultPlatformDefaults(defaultRoute)),
			ctxpkg.NewAssembler(),
			nil,
		)
		return buildGitLabReviewInput(ctx, target, adapter, builder)
	case core.PlatformGitHub:
		baseURL := strings.TrimSpace(cfg.GitHubBaseURL)
		if baseURL == "" {
			baseURL = githubAPIBaseURL(target.BaseURL)
		}
		if strings.TrimSpace(cfg.GitHubToken) == "" {
			return core.ReviewInput{}, fmt.Errorf("GITHUB_TOKEN is required")
		}
		client, err := platformgithub.NewClient(baseURL, cfg.GitHubToken)
		if err != nil {
			return core.ReviewInput{}, fmt.Errorf("configure github client: %w", err)
		}
		adapter := platformgithub.NewAdapter(client)
		builder := reviewinput.NewBuilder(
			rules.NewLoader(client, defaultPlatformDefaults(defaultRoute)),
			ctxpkg.NewAssembler(),
			nil,
		)
		return buildGitHubReviewInput(ctx, target, adapter, builder)
	default:
		return core.ReviewInput{}, fmt.Errorf("unsupported platform %q", target.Platform)
	}
}

func buildGitLabReviewInput(ctx context.Context, target core.ReviewTarget, fetcher snapshotFetcher, builder reviewInputBuilder) (core.ReviewInput, error) {
	snapshot, err := fetcher.FetchSnapshot(ctx, target)
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("fetch snapshot: %w", err)
	}
	input, err := builder.Build(ctx, reviewinput.BuildInput{
		Snapshot:             snapshot,
		ProjectDefaultBranch: snapshot.Change.TargetBranch,
		ProjectPolicy:        nil,
		MergeRequestID:       0,
	})
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("build review input: %w", err)
	}
	return input, nil
}

func buildGitHubReviewInput(ctx context.Context, target core.ReviewTarget, fetcher snapshotFetcher, builder reviewInputBuilder) (core.ReviewInput, error) {
	snapshot, err := fetcher.FetchSnapshot(ctx, target)
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("fetch snapshot: %w", err)
	}
	input, err := builder.Build(ctx, reviewinput.BuildInput{
		Snapshot:             snapshot,
		ProjectDefaultBranch: snapshot.Change.TargetBranch,
		ProjectPolicy:        nil,
		MergeRequestID:       0,
	})
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("build review input: %w", err)
	}
	return input, nil
}

func defaultReviewEngine(configPath string) reviewEngine {
	cfg, err := config.Load(configPath)
	if err != nil {
		return failingEngine{err: fmt.Errorf("load config: %w", err)}
	}
	logger := slog.New(slog.NewTextHandler(nilDiscardWriter{}, nil))
	defaultRoute, fallbackRoute, providerConfigs, err := providerConfigsFromConfig(cfg)
	if err != nil {
		return failingEngine{err: fmt.Errorf("resolve provider routes: %w", err)}
	}
	registry, err := llm.BuildProviderRegistryFromRouteConfigs(logger, defaultRoute, fallbackRoute, providerConfigs)
	if err != nil {
		return failingEngine{err: fmt.Errorf("build provider registry: %w", err)}
	}
	resolve := func(route string) llm.Provider {
		return registry.ResolveWithFallback(route)
	}
	runners := make([]core.PackRunner, 0, len(reviewpack.DefaultPacks()))
	for _, pack := range reviewpack.DefaultPacks() {
		runners = append(runners, reviewpack.NewLegacyResolverRunner(pack.Contract(), resolve))
	}
	return core.NewEngine(runners, judgeAdapter{inner: judge.New()})
}

func defaultPublish(ctx context.Context, configPath string, target core.ReviewTarget, bundle core.ReviewBundle) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	switch target.Platform {
	case core.PlatformGitLab:
		if strings.TrimSpace(cfg.GitLabBaseURL) == "" {
			return fmt.Errorf("GITLAB_BASE_URL is required")
		}
		if strings.TrimSpace(cfg.GitLabToken) == "" {
			return fmt.Errorf("GITLAB_TOKEN is required")
		}
		client, err := legacygitlab.NewClient(cfg.GitLabBaseURL, cfg.GitLabToken)
		if err != nil {
			return fmt.Errorf("configure gitlab client: %w", err)
		}
		return platformgitlab.NewPublisher(client).Publish(ctx, bundle)
	case core.PlatformGitHub:
		baseURL := strings.TrimSpace(cfg.GitHubBaseURL)
		if baseURL == "" {
			baseURL = githubAPIBaseURL(target.BaseURL)
		}
		if strings.TrimSpace(cfg.GitHubToken) == "" {
			return fmt.Errorf("GITHUB_TOKEN is required")
		}
		client, err := platformgithub.NewClient(baseURL, cfg.GitHubToken)
		if err != nil {
			return fmt.Errorf("configure github client: %w", err)
		}
		return platformgithub.NewPublisher(client).Publish(ctx, bundle)
	default:
		return fmt.Errorf("publish is not implemented for platform %q", target.Platform)
	}
}

type judgeAdapter struct {
	inner *judge.Engine
}

func (a judgeAdapter) Decide(artifacts []core.ReviewerArtifact) core.JudgeDecision {
	if a.inner == nil {
		return core.JudgeDecision{}
	}
	decision := a.inner.Decide(artifacts)
	merged := make([]core.Finding, 0, len(decision.MergedFindings))
	for _, item := range decision.MergedFindings {
		merged = append(merged, item.Finding)
	}
	return core.JudgeDecision{
		Verdict:        decision.Verdict,
		MergedFindings: merged,
		Summary:        renderJudgeSummary(decision),
	}
}

func renderJudgeSummary(decision judge.Decision) string {
	if len(decision.MergedFindings) == 0 {
		return "No review findings detected."
	}
	lines := []string{fmt.Sprintf("Verdict: %s", decision.Verdict), "", "Findings:"}
	for _, item := range decision.MergedFindings {
		title := strings.TrimSpace(item.Finding.Title)
		if title == "" {
			title = strings.TrimSpace(item.Finding.Claim)
		}
		if title == "" {
			title = "Unnamed finding"
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s", strings.ToUpper(strings.TrimSpace(item.Finding.Severity)), title))
	}
	return strings.Join(lines, "\n")
}

type nilDiscardWriter struct{}

func (nilDiscardWriter) Write(p []byte) (int, error) { return len(p), nil }

func defaultPlatformDefaults(defaultRoute string) rules.PlatformDefaults {
	return rules.PlatformDefaults{
		Instructions:        "Platform defaults: prioritize correctness, security, and least-privilege behavior.",
		ConfidenceThreshold: 0.72,
		SeverityThreshold:   "medium",
		IncludePaths:        []string{"src/**"},
		ExcludePaths:        []string{"vendor/**"},
		GateMode:            "threads_resolved",
		ProviderRoute:       defaultRoute,
	}
}

func githubAPIBaseURL(targetBaseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(targetBaseURL), "/")
	switch trimmed {
	case "", "https://github.com":
		return "https://api.github.com"
	default:
		return trimmed + "/api/v3"
	}
}

func providerConfigsFromConfig(cfg *config.Config) (string, string, map[string]llm.ProviderConfig, error) {
	if cfg == nil {
		return "", "", nil, fmt.Errorf("worker: configuration is required")
	}
	routes := make(map[string]llm.ProviderConfig)
	if providerKind := strings.ToLower(strings.TrimSpace(cfg.LLMProvider)); providerKind != "" {
		const quickStartDefaultRoute = "default"
		const quickStartFallbackRoute = "secondary"

		quickStart := llm.ProviderConfig{
			Kind:       providerKind,
			BaseURL:    strings.TrimSpace(cfg.LLMBaseURL),
			APIKey:     strings.TrimSpace(cfg.LLMAPIKey),
			Model:      strings.TrimSpace(cfg.LLMModel),
			MaxTokens:  4096,
			OutputMode: "tool_call",
		}
		if providerKind == llm.ProviderKindOpenAI {
			quickStart.OutputMode = "json_schema"
			quickStart.MaxCompletionTokens = 12000
			quickStart.ReasoningEffort = "medium"
		}

		defaultProvider := quickStart
		defaultProvider.RouteName = quickStartDefaultRoute
		secondaryProvider := quickStart
		secondaryProvider.RouteName = quickStartFallbackRoute
		routes[quickStartDefaultRoute] = defaultProvider
		routes[quickStartFallbackRoute] = secondaryProvider
		return quickStartDefaultRoute, quickStartFallbackRoute, routes, nil
	}
	if len(cfg.LLM.Routes) > 0 {
		defaultRoute := strings.TrimSpace(cfg.LLM.DefaultRoute)
		if defaultRoute == "" {
			return "", "", nil, fmt.Errorf("worker: llm.default_route is required when llm.routes is configured")
		}
		for routeName, route := range cfg.LLM.Routes {
			trimmed := strings.TrimSpace(routeName)
			if trimmed == "" {
				return "", "", nil, fmt.Errorf("worker: llm route name cannot be empty")
			}
			providerKind := strings.TrimSpace(route.Provider)
			if providerKind == "" {
				return "", "", nil, fmt.Errorf("worker: llm.routes.%s.provider is required", trimmed)
			}
			routes[trimmed] = llm.ProviderConfig{
				Kind:                providerKind,
				BaseURL:             strings.TrimSpace(route.BaseURL),
				APIKey:              strings.TrimSpace(route.APIKey),
				Model:               strings.TrimSpace(route.Model),
				RouteName:           trimmed,
				OutputMode:          strings.TrimSpace(route.OutputMode),
				MaxTokens:           route.MaxTokens,
				MaxCompletionTokens: route.MaxCompletionTokens,
				ReasoningEffort:     strings.TrimSpace(route.ReasoningEffort),
				Temperature:         route.Temperature,
			}
		}
		return defaultRoute, strings.TrimSpace(cfg.LLM.FallbackRoute), routes, nil
	}

	const legacyDefaultRoute = "default"
	const legacyFallbackRoute = "secondary"
	legacy := llm.ProviderConfig{
		Kind:       llm.ProviderKindMiniMax,
		BaseURL:    strings.TrimSpace(cfg.AnthropicBaseURL),
		APIKey:     strings.TrimSpace(cfg.AnthropicAPIKey),
		Model:      strings.TrimSpace(cfg.AnthropicModel),
		MaxTokens:  4096,
		OutputMode: "tool_call",
	}
	defaultProvider := legacy
	defaultProvider.RouteName = legacyDefaultRoute
	secondaryProvider := legacy
	secondaryProvider.RouteName = legacyFallbackRoute
	routes[legacyDefaultRoute] = defaultProvider
	routes[legacyFallbackRoute] = secondaryProvider
	return legacyDefaultRoute, legacyFallbackRoute, routes, nil
}
