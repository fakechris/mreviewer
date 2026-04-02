package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mreviewer/mreviewer/internal/config"
	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/judge"
	"github.com/mreviewer/mreviewer/internal/llm"
	platformgithub "github.com/mreviewer/mreviewer/internal/platform/github"
	platformgitlab "github.com/mreviewer/mreviewer/internal/platform/gitlab"
	"github.com/mreviewer/mreviewer/internal/reviewadvisor"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewinput"
	"github.com/mreviewer/mreviewer/internal/reviewpack"
	"github.com/mreviewer/mreviewer/internal/reviewrun"
	"github.com/mreviewer/mreviewer/internal/scheduler"
)

type workerReviewInputLoader func(ctx context.Context, target core.ReviewTarget, providerRoute string) (core.ReviewInput, error)

func (f workerReviewInputLoader) Load(ctx context.Context, target core.ReviewTarget, providerRoute string) (core.ReviewInput, error) {
	return f(ctx, target, providerRoute)
}

type workerAdvisorAwareEngine struct {
	base                *core.Engine
	advisor             *reviewadvisor.Advisor
	defaultAdvisorRef   string
}

func (e workerAdvisorAwareEngine) Run(ctx context.Context, input core.ReviewInput, opts core.RunOptions) (core.ReviewBundle, error) {
	if e.base == nil {
		return core.ReviewBundle{}, fmt.Errorf("worker runtime: review engine is required")
	}
	bundle, err := e.base.Run(ctx, input, opts)
	if err != nil {
		return core.ReviewBundle{}, err
	}
	ref := strings.TrimSpace(opts.AdvisorRoute)
	if ref == "" {
		ref = strings.TrimSpace(e.defaultAdvisorRef)
	}
	if ref == "" || e.advisor == nil {
		return bundle, nil
	}
	artifact, err := e.advisor.Advise(ctx, input, bundle, ref)
	if err != nil {
		slog.Default().WarnContext(ctx, "worker runtime advisor failed; continuing with council result",
			"route", ref,
			"error", err,
		)
		return bundle, nil
	}
	bundle.AdvisorArtifact = artifact
	return bundle, nil
}

func newReviewRunProcessor(cfg *config.Config, sqlDB *sql.DB, gitlabClient *gitlab.Client, githubClient *platformgithub.Client, rulesLoader reviewinput.RulesLoader, providerRegistry *llm.ProviderRegistry) (scheduler.Processor, error) {
	if cfg == nil {
		return nil, fmt.Errorf("worker: configuration is required")
	}
	if sqlDB == nil {
		return nil, fmt.Errorf("worker: database is required")
	}
	if rulesLoader == nil {
		return nil, fmt.Errorf("worker: rules loader is required")
	}
	if providerRegistry == nil {
		return nil, fmt.Errorf("worker: provider registry is required")
	}
	if gitlabClient == nil && githubClient == nil {
		return nil, fmt.Errorf("worker: at least one platform client is required")
	}

	builder := reviewinput.NewBuilder(rulesLoader, ctxpkg.NewAssembler(), llm.NewSQLProcessorStore(sqlDB))
	inputLoader := workerReviewInputLoader(func(ctx context.Context, target core.ReviewTarget, providerRoute string) (core.ReviewInput, error) {
		input, err := buildWorkerReviewInput(ctx, target, gitlabClient, githubClient, builder)
		if err != nil {
			return core.ReviewInput{}, err
		}
		if trimmed := strings.TrimSpace(providerRoute); trimmed != "" {
			input.EffectivePolicy.ProviderRoute = trimmed
		}
		return input, nil
	})
	defaultRoute, fallbackRoutes, _, err := config.ResolveReviewCatalog(cfg)
	if err != nil {
		return nil, fmt.Errorf("worker: resolve review model chain: %w", err)
	}
	resolve := func(ref string) llm.Provider {
		return config.ResolveProvider(cfg, providerRegistry, defaultRoute, fallbackRoutes, ref)
	}
	runners := make([]core.PackRunner, 0, len(reviewpack.DefaultPacks()))
	for _, pack := range reviewpack.DefaultPacks() {
		runners = append(runners, reviewpack.NewLegacyResolverRunner(pack.Contract(), resolve))
	}
	engine := workerAdvisorAwareEngine{
		base:                core.NewEngine(runners, workerJudgeAdapter{inner: judge.New()}),
		advisor:             reviewadvisor.New(resolve),
		defaultAdvisorRef:   strings.TrimSpace(cfg.Review.AdvisorChain),
	}
	return reviewrun.NewEngineProcessor(sqlDB, inputLoader, engine).
		WithDefaultReviewerPacks(normalizedReviewPacks(cfg.Review.Packs)).
		WithDefaultAdvisorRoute(strings.TrimSpace(cfg.Review.AdvisorChain)), nil
}

func buildWorkerReviewInput(ctx context.Context, target core.ReviewTarget, gitlabClient *gitlab.Client, githubClient *platformgithub.Client, builder *reviewinput.Builder) (core.ReviewInput, error) {
	var (
		snapshot core.PlatformSnapshot
		err      error
	)
	switch target.Platform {
	case core.PlatformGitHub:
		if githubClient == nil {
			return core.ReviewInput{}, fmt.Errorf("build review input: github client is required")
		}
		snapshot, err = platformgithub.NewAdapter(githubClient).FetchSnapshot(ctx, target)
	case core.PlatformGitLab:
		if gitlabClient == nil {
			return core.ReviewInput{}, fmt.Errorf("build review input: gitlab client is required")
		}
		snapshot, err = platformgitlab.NewAdapter(gitlabClient).FetchSnapshot(ctx, target)
	default:
		return core.ReviewInput{}, fmt.Errorf("build review input: unsupported platform: %s", target.Platform)
	}
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

type workerJudgeAdapter struct {
	inner *judge.Engine
}

func (a workerJudgeAdapter) Decide(artifacts []core.ReviewerArtifact) core.JudgeDecision {
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
		Summary:        renderWorkerJudgeSummary(decision),
	}
}

func renderWorkerJudgeSummary(decision judge.Decision) string {
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

func normalizedReviewPacks(packs []string) []string {
	if len(packs) == 0 {
		return nil
	}
	result := make([]string, 0, len(packs))
	seen := make(map[string]struct{}, len(packs))
	for _, pack := range packs {
		token := strings.ToLower(strings.TrimSpace(pack))
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		result = append(result, token)
	}
	return result
}
