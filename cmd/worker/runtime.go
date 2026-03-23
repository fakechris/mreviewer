package main

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/metrics"
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

func newRuntimeDeps(logger *slog.Logger, sqlDB *sql.DB, processor scheduler.Processor) runtimeDeps {
	return newRuntimeDepsWithGatePublishers(logger, sqlDB, processor, gate.NoopStatusPublisher{}, gate.NoopCIGatePublisher{})
}

func newRuntimeDepsWithGatePublishers(logger *slog.Logger, sqlDB *sql.DB, processor scheduler.Processor, status gate.StatusPublisher, ci gate.CIGatePublisher) runtimeDeps {
	return newRuntimeDepsWithWritebackAndGatePublishers(logger, sqlDB, processor, nil, status, ci)
}

func newRuntimeDepsWithWriteback(logger *slog.Logger, sqlDB *sql.DB, processor scheduler.Processor, discussionClient writer.DiscussionClient) runtimeDeps {
	return newRuntimeDepsWithWritebackAndGatePublishers(logger, sqlDB, processor, discussionClient, gate.NoopStatusPublisher{}, gate.NoopCIGatePublisher{})
}

func newRuntimeDepsWithWritebackAndGatePublishers(logger *slog.Logger, sqlDB *sql.DB, processor scheduler.Processor, discussionClient writer.DiscussionClient, status gate.StatusPublisher, ci gate.CIGatePublisher) runtimeDeps {
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
	if discussionClient != nil && sqlDB != nil {
		runtimeWriter = writer.New(discussionClient, writer.NewSQLStore(sqlDB)).WithMetrics(registry).WithTracer(tracer)
	}
	processor = wrapProcessorWithWriteback(sqlDB, processor, runtimeWriter)
	gateSvc := gate.NewService(status, ci, gate.NewDBAuditLogger(db.New(sqlDB)))
	worker := scheduler.NewService(logger, sqlDB, processor,
		scheduler.WithMetrics(registry),
		scheduler.WithTracer(tracer),
		scheduler.WithStatusPublisher(status),
		scheduler.WithGateService(gateSvc),
	)
	return runtimeDeps{GateService: gateSvc, Metrics: registry, Tracer: tracer, Scheduler: worker}
}

func wrapProcessorWithWriteback(sqlDB *sql.DB, processor scheduler.Processor, runtimeWriter *writer.Writer) scheduler.Processor {
	if processor == nil || sqlDB == nil || runtimeWriter == nil {
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
		if err := runtimeWriter.Write(ctx, runForWriteback, outcome.ReviewFindings); err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		return outcome, nil
	})
}
