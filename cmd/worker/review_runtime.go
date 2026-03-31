package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/config"
	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/judge"
	"github.com/mreviewer/mreviewer/internal/llm"
	platformgitlab "github.com/mreviewer/mreviewer/internal/platform/gitlab"
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

func newReviewRunProcessor(cfg *config.Config, sqlDB *sql.DB, gitlabClient *gitlab.Client, rulesLoader reviewinput.RulesLoader, providerRegistry *llm.ProviderRegistry) (scheduler.Processor, error) {
	if cfg == nil {
		return nil, fmt.Errorf("worker: configuration is required")
	}
	if sqlDB == nil {
		return nil, fmt.Errorf("worker: database is required")
	}
	if gitlabClient == nil {
		return nil, fmt.Errorf("worker: gitlab client is required")
	}
	if rulesLoader == nil {
		return nil, fmt.Errorf("worker: rules loader is required")
	}
	if providerRegistry == nil {
		return nil, fmt.Errorf("worker: provider registry is required")
	}

	adapter := platformgitlab.NewAdapter(gitlabClient)
	builder := reviewinput.NewBuilder(rulesLoader, ctxpkg.NewAssembler(), llm.NewSQLProcessorStore(sqlDB))
	inputLoader := workerReviewInputLoader(func(ctx context.Context, target core.ReviewTarget, providerRoute string) (core.ReviewInput, error) {
		input, err := buildWorkerGitLabReviewInput(ctx, target, adapter, builder)
		if err != nil {
			return core.ReviewInput{}, err
		}
		if trimmed := strings.TrimSpace(providerRoute); trimmed != "" {
			input.EffectivePolicy.ProviderRoute = trimmed
		}
		return input, nil
	})
	resolve := func(route string) llm.Provider {
		return providerRegistry.ResolveWithFallback(route)
	}
	runners := make([]core.PackRunner, 0, len(reviewpack.DefaultPacks()))
	for _, pack := range reviewpack.DefaultPacks() {
		runners = append(runners, reviewpack.NewLegacyResolverRunner(pack.Contract(), resolve))
	}
	engine := core.NewEngine(runners, workerJudgeAdapter{inner: judge.New()})
	return reviewrun.NewEngineProcessor(sqlDB, inputLoader, engine), nil
}

func buildWorkerGitLabReviewInput(ctx context.Context, target core.ReviewTarget, fetcher *platformgitlab.Adapter, builder *reviewinput.Builder) (core.ReviewInput, error) {
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
