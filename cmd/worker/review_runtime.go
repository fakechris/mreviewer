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
	platformgithub "github.com/mreviewer/mreviewer/internal/platform/github"
	platformgitlab "github.com/mreviewer/mreviewer/internal/platform/gitlab"
	"github.com/mreviewer/mreviewer/internal/reviewadvisor"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewinput"
	"github.com/mreviewer/mreviewer/internal/reviewruntime"
	"github.com/mreviewer/mreviewer/internal/scheduler"
)

type workerAdvisorAwareEngine struct {
	base              *core.Engine
	advisor           *reviewadvisor.Advisor
	defaultAdvisorRef string
}

func (e workerAdvisorAwareEngine) Run(ctx context.Context, input core.ReviewInput, opts core.RunOptions) (core.ReviewBundle, error) {
	return reviewruntime.AdvisorAwareEngine{
		Base:               e.base,
		Advisor:            e.advisor,
		DefaultAdvisorRef:  e.defaultAdvisorRef,
		AdvisorWarnMessage: "worker runtime advisor failed; continuing with council result",
	}.Run(ctx, input, opts)
}

type workerJudgeAdapter struct {
	inner *judge.Engine
}

func (a workerJudgeAdapter) Decide(artifacts []core.ReviewerArtifact) core.JudgeDecision {
	return reviewruntime.JudgeAdapter{Inner: a.inner}.Decide(artifacts)
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
	inputLoader := reviewruntime.InputLoaderFunc(func(ctx context.Context, target core.ReviewTarget, providerRoute string) (core.ReviewInput, error) {
		input, err := buildWorkerReviewInput(ctx, target, gitlabClient, githubClient, builder)
		if err != nil {
			return core.ReviewInput{}, err
		}
		if trimmed := strings.TrimSpace(providerRoute); trimmed != "" {
			input.EffectivePolicy.ProviderRoute = trimmed
		}
		return input, nil
	})
	return reviewruntime.NewProcessor(cfg, sqlDB, inputLoader, providerRegistry, "worker runtime advisor failed; continuing with council result")
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
