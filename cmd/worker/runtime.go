package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/metrics"
	githubplatform "github.com/mreviewer/mreviewer/internal/platform/github"
	gitlabplatform "github.com/mreviewer/mreviewer/internal/platform/gitlab"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewstatus"
	"github.com/mreviewer/mreviewer/internal/scheduler"
	tracing "github.com/mreviewer/mreviewer/internal/trace"
	"github.com/mreviewer/mreviewer/internal/writer"
)

type runtimeDeps struct {
	GateService *gate.Service
	Metrics     *metrics.Registry
	Tracer      *tracing.Recorder
	Scheduler   *scheduler.Service
}

type reviewFindingWriteback interface {
	Write(ctx context.Context, run db.ReviewRun, findings []db.ReviewFinding) error
}

type reviewBundleWriteback interface {
	WriteBundle(ctx context.Context, run db.ReviewRun, bundle reviewcore.ReviewBundle) error
}

func newRuntimeDeps(logger *slog.Logger, sqlDB *sql.DB, processor scheduler.Processor) runtimeDeps {
	return newRuntimeDepsWithGatePublishers(logger, sqlDB, processor, gate.NoopStatusPublisher{}, gate.NoopCIGatePublisher{})
}

func newRuntimeDepsWithGatePublishers(logger *slog.Logger, sqlDB *sql.DB, processor scheduler.Processor, status gate.StatusPublisher, ci gate.CIGatePublisher) runtimeDeps {
	return newRuntimeDepsWithPlatformClientsAndGatePublishers(logger, sqlDB, processor, nil, nil, status, ci)
}

func newRuntimeDepsWithWriteback(logger *slog.Logger, sqlDB *sql.DB, processor scheduler.Processor, discussionClient writer.DiscussionClient) runtimeDeps {
	return newRuntimeDepsWithPlatformClientsAndGatePublishers(logger, sqlDB, processor, discussionClient, nil, gate.NoopStatusPublisher{}, gate.NoopCIGatePublisher{})
}

func newRuntimeDepsWithWritebackAndGatePublishers(logger *slog.Logger, sqlDB *sql.DB, processor scheduler.Processor, discussionClient writer.DiscussionClient, status gate.StatusPublisher, ci gate.CIGatePublisher) runtimeDeps {
	return newRuntimeDepsWithPlatformClientsAndGatePublishers(logger, sqlDB, processor, discussionClient, nil, status, ci)
}

func newRuntimeDepsWithPlatformClientsAndGatePublishers(logger *slog.Logger, sqlDB *sql.DB, processor scheduler.Processor, discussionClient writer.DiscussionClient, githubPublishClient githubplatform.PublishClient, status gate.StatusPublisher, ci gate.CIGatePublisher) runtimeDeps {
	registry := metrics.NewRegistry()
	tracer := tracing.NewRecorder()
	if configurable, ok := processor.(interface {
		WithMetrics(*metrics.Registry)
		WithTracer(*tracing.Recorder)
	}); ok {
		configurable.WithMetrics(registry)
		configurable.WithTracer(tracer)
	}
	var runtimeWriter *writer.Writer
	var bundleWriter reviewBundleWriteback
	if discussionClient != nil && sqlDB != nil {
		runtimeWriter = writer.New(discussionClient, writer.NewSQLStore(sqlDB)).WithMetrics(registry).WithTracer(tracer)
	}
	if sqlDB != nil {
		bundleWriter = newPlatformBundleWriteback(sqlDB, discussionClient, githubPublishClient)
	}
	processor = wrapProcessorWithWriteback(sqlDB, processor, runtimeWriter, bundleWriter, status)
	gateSvc := gate.NewService(gate.NoopStatusPublisher{}, ci, gate.NewDBAuditLogger(db.New(sqlDB)))
	worker := scheduler.NewService(logger, sqlDB, processor,
		scheduler.WithMetrics(registry),
		scheduler.WithTracer(tracer),
		scheduler.WithStatusPublisher(status),
		scheduler.WithGateService(gateSvc),
	)
	return runtimeDeps{GateService: gateSvc, Metrics: registry, Tracer: tracer, Scheduler: worker}
}

type platformBundleWriteback struct {
	gitlab reviewBundleWriteback
	github reviewBundleWriteback
}

type platformStatusPublisher struct {
	queries *db.Queries
	gitlab gate.StatusPublisher
	github gate.StatusPublisher
}

func newPlatformBundleWriteback(sqlDB *sql.DB, discussionClient writer.DiscussionClient, githubPublishClient githubplatform.PublishClient) reviewBundleWriteback {
	if sqlDB == nil {
		return nil
	}
	return &platformBundleWriteback{
		gitlab: gitlabplatform.NewRuntimeWriteback(sqlDB, discussionClient),
		github: githubplatform.NewRuntimeWriteback(sqlDB, githubPublishClient),
	}
}

func (w *platformBundleWriteback) WriteBundle(ctx context.Context, run db.ReviewRun, bundle reviewcore.ReviewBundle) error {
	switch detectBundlePlatform(bundle) {
	case reviewcore.PlatformGitHub:
		if w == nil || w.github == nil {
			return fmt.Errorf("worker runtime: github bundle writeback is not configured")
		}
		return w.github.WriteBundle(ctx, run, bundle)
	default:
		if w == nil || w.gitlab == nil {
			return fmt.Errorf("worker runtime: gitlab bundle writeback is not configured")
		}
		return w.gitlab.WriteBundle(ctx, run, bundle)
	}
}

func detectBundlePlatform(bundle reviewcore.ReviewBundle) reviewcore.Platform {
	if bundle.Target.Platform != "" {
		return bundle.Target.Platform
	}
	if strings.Contains(bundle.Target.URL, "/pull/") {
		return reviewcore.PlatformGitHub
	}
	return reviewcore.PlatformGitLab
}

func newPlatformStatusPublisher(sqlDB *sql.DB, gitlabPublisher, githubPublisher gate.StatusPublisher) gate.StatusPublisher {
	if sqlDB == nil {
		return gitlabPublisher
	}
	return platformStatusPublisher{
		queries: db.New(sqlDB),
		gitlab: gitlabPublisher,
		github: githubPublisher,
	}
}

func (p platformStatusPublisher) PublishStatus(ctx context.Context, result gate.Result) error {
	switch p.detectPlatform(ctx, result) {
	case reviewcore.PlatformGitHub:
		if p.github == nil {
			return fmt.Errorf("worker runtime: github status publisher is not configured")
		}
		return p.github.PublishStatus(ctx, result)
	default:
		if p.gitlab == nil {
			return fmt.Errorf("worker runtime: gitlab status publisher is not configured")
		}
		return p.gitlab.PublishStatus(ctx, result)
	}
}

func (p platformStatusPublisher) detectPlatform(ctx context.Context, result gate.Result) reviewcore.Platform {
	if p.queries == nil {
		return reviewcore.PlatformGitLab
	}
	if result.MergeRequestID > 0 {
		if mr, err := p.queries.GetMergeRequest(ctx, result.MergeRequestID); err == nil {
			return detectPlatformFromURL(mr.WebUrl)
		}
	}
	if result.ProjectID > 0 {
		if project, err := p.queries.GetProject(ctx, result.ProjectID); err == nil {
			if instance, instanceErr := p.queries.GetGitlabInstance(ctx, project.GitlabInstanceID); instanceErr == nil {
				return detectPlatformFromURL(instance.Url)
			}
		}
	}
	return reviewcore.PlatformGitLab
}

func wrapProcessorWithWriteback(sqlDB *sql.DB, processor scheduler.Processor, runtimeWriter reviewFindingWriteback, bundleWriter reviewBundleWriteback, statusPublisher gate.StatusPublisher) scheduler.Processor {
	if processor == nil || sqlDB == nil || (runtimeWriter == nil && bundleWriter == nil) {
		return processor
	}
	queries := db.New(sqlDB)
	return scheduler.FuncProcessor(func(ctx context.Context, run db.ReviewRun) (scheduler.ProcessOutcome, error) {
		outcome, err := processor.ProcessRun(ctx, run)
		if err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		runForWriteback, loadErr := queries.GetReviewRun(ctx, run.ID)
		if loadErr != nil {
			return scheduler.ProcessOutcome{}, loadErr
		}
		if runForWriteback.ID == 0 {
			runForWriteback = run
		}
		if runForWriteback.Status == "" || runForWriteback.Status == "running" || runForWriteback.Status == "pending" {
			runForWriteback.Status = outcome.Status
		}
		if runForWriteback.Status == "" {
			runForWriteback.Status = "completed"
		}
		if outcome.ReviewBundle != nil && bundleWriter != nil {
			bundle, ok := outcome.ReviewBundle.(reviewcore.ReviewBundle)
			if !ok {
				bundlePtr, ok := outcome.ReviewBundle.(*reviewcore.ReviewBundle)
				if ok && bundlePtr != nil {
					bundle = *bundlePtr
				} else {
					return scheduler.ProcessOutcome{}, fmt.Errorf("worker runtime: unsupported review bundle type %T", outcome.ReviewBundle)
				}
			}
			publishWorkerRuntimeStage(ctx, statusPublisher, runForWriteback, reviewstatus.StagePublishing)
			if err := bundleWriter.WriteBundle(ctx, runForWriteback, bundle); err != nil {
				return scheduler.ProcessOutcome{}, err
			}
			return outcome, nil
		}
		if runtimeWriter != nil {
			if err := runtimeWriter.Write(ctx, runForWriteback, outcome.ReviewFindings); err != nil {
				return scheduler.ProcessOutcome{}, err
			}
		}
		return outcome, nil
	})
}

func publishWorkerRuntimeStage(ctx context.Context, publisher gate.StatusPublisher, run db.ReviewRun, stage reviewstatus.Stage) {
	if publisher == nil {
		return
	}
	_ = publisher.PublishStatus(ctx, gate.Result{
		RunID:          run.ID,
		MergeRequestID: run.MergeRequestID,
		ProjectID:      run.ProjectID,
		HeadSHA:        run.HeadSha,
		State:          "running",
		Stage:          stage,
		Source:         "review_run",
	})
}
