package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/mreviewer/mreviewer/internal/config"
	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/judge"
	"github.com/mreviewer/mreviewer/internal/llm"
	"github.com/mreviewer/mreviewer/internal/manualtrigger"
	platformgitlab "github.com/mreviewer/mreviewer/internal/platform/gitlab"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewinput"
	"github.com/mreviewer/mreviewer/internal/reviewpack"
	"github.com/mreviewer/mreviewer/internal/reviewrun"
	"github.com/mreviewer/mreviewer/internal/rules"
	"github.com/mreviewer/mreviewer/internal/writer"
)

type reviewInputLoaderFunc func(ctx context.Context, target core.ReviewTarget, providerRoute string) (core.ReviewInput, error)

func (f reviewInputLoaderFunc) Load(ctx context.Context, target core.ReviewTarget, providerRoute string) (core.ReviewInput, error) {
	return f(ctx, target, providerRoute)
}

func newDefaultRunProcessor(cfg *config.Config, sqlDB *sql.DB, client *legacygitlab.Client) (manualtrigger.RunProcessor, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration is required")
	}
	if sqlDB == nil {
		return nil, fmt.Errorf("database is required")
	}
	if client == nil {
		return nil, fmt.Errorf("gitlab client is required")
	}

	defaultRoute, fallbackRoute, providerConfigs, err := providerConfigsFromManualConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve provider routes: %w", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry, err := llm.BuildProviderRegistryFromRouteConfigs(logger, defaultRoute, fallbackRoute, providerConfigs)
	if err != nil {
		return nil, fmt.Errorf("build provider registry: %w", err)
	}

	adapter := platformgitlab.NewAdapter(client)
	inputBuilder := reviewinput.NewBuilder(
		rules.NewLoader(client, manualPlatformDefaults(defaultRoute)),
		ctxpkg.NewAssembler(),
		nil,
	)
	inputLoader := reviewInputLoaderFunc(func(ctx context.Context, target core.ReviewTarget, providerRoute string) (core.ReviewInput, error) {
		input, err := buildManualGitLabReviewInput(ctx, target, adapter, inputBuilder)
		if err != nil {
			return core.ReviewInput{}, err
		}
		if trimmed := strings.TrimSpace(providerRoute); trimmed != "" {
			input.EffectivePolicy.ProviderRoute = trimmed
		}
		return input, nil
	})

	runners := make([]core.PackRunner, 0, len(reviewpack.DefaultPacks()))
	resolve := func(route string) llm.Provider {
		return registry.ResolveWithFallback(route)
	}
	for _, pack := range reviewpack.DefaultPacks() {
		runners = append(runners, reviewpack.NewLegacyResolverRunner(pack.Contract(), resolve))
	}
	engine := core.NewEngine(runners, manualJudgeAdapter{inner: judge.New()})
	runtimeWriter := platformgitlab.NewRuntimeWriteback(client, writer.NewSQLStore(sqlDB))

	return reviewrun.NewEngineProcessor(sqlDB, inputLoader, engine).WithWriteback(runtimeWriter), nil
}

func buildManualGitLabReviewInput(ctx context.Context, target core.ReviewTarget, fetcher *platformgitlab.Adapter, builder *reviewinput.Builder) (core.ReviewInput, error) {
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

type manualJudgeAdapter struct {
	inner *judge.Engine
}

func (a manualJudgeAdapter) Decide(artifacts []core.ReviewerArtifact) core.JudgeDecision {
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
		Summary:        renderManualJudgeSummary(decision),
	}
}

func renderManualJudgeSummary(decision judge.Decision) string {
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

func manualPlatformDefaults(defaultRoute string) rules.PlatformDefaults {
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

func providerConfigsFromManualConfig(cfg *config.Config) (string, string, map[string]llm.ProviderConfig, error) {
	if cfg == nil {
		return "", "", nil, fmt.Errorf("configuration is required")
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
			return "", "", nil, fmt.Errorf("llm.default_route is required when llm.routes is configured")
		}
		for routeName, route := range cfg.LLM.Routes {
			trimmed := strings.TrimSpace(routeName)
			if trimmed == "" {
				return "", "", nil, fmt.Errorf("llm route name cannot be empty")
			}
			providerKind := strings.TrimSpace(route.Provider)
			if providerKind == "" {
				return "", "", nil, fmt.Errorf("llm.routes.%s.provider is required", trimmed)
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
