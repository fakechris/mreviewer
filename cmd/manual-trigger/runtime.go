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

	defaultRoute, fallbackRoutes, providerConfigs, err := providerConfigsFromManualConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve provider routes: %w", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry, err := llm.BuildProviderRegistryFromRouteConfigs(logger, defaultRoute, fallbackRoutes, providerConfigs)
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
	resolve := func(ref string) llm.Provider {
		return config.ResolveProvider(cfg, registry, defaultRoute, fallbackRoutes, ref)
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

func providerConfigsFromManualConfig(cfg *config.Config) (string, []string, map[string]llm.ProviderConfig, error) {
	if cfg == nil {
		return "", nil, nil, fmt.Errorf("configuration is required")
	}
	return config.ResolveReviewCatalog(cfg)
}
