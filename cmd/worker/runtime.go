package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/metrics"
	"github.com/mreviewer/mreviewer/internal/ops"
	platformgitlab "github.com/mreviewer/mreviewer/internal/platform/gitlab"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/scheduler"
	tracing "github.com/mreviewer/mreviewer/internal/trace"
	"github.com/mreviewer/mreviewer/internal/writer"
)

type runtimeDeps struct {
	GateService       *gate.Service
	Metrics           *metrics.Registry
	Tracer            *tracing.Recorder
	Scheduler         *scheduler.Service
	Heartbeat         *ops.Service
	HeartbeatIdentity ops.WorkerIdentity
}

var defaultRuntimeNewStore = func(conn db.DBTX) db.Store { return db.New(conn) }
var workerVersion = "dev"

type runtimeWriteback interface {
	Write(ctx context.Context, run db.ReviewRun, findings []db.ReviewFinding) error
}

type runtimeBundleWriteback interface {
	WriteBundle(ctx context.Context, run db.ReviewRun, bundle core.ReviewBundle) error
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
	return newRuntimeDepsWithStoreFactory(logger, sqlDB, processor, discussionClient, status, ci, defaultRuntimeNewStore)
}

func newRuntimeDepsWithStoreFactory(logger *slog.Logger, sqlDB *sql.DB, processor scheduler.Processor, discussionClient writer.DiscussionClient, status gate.StatusPublisher, ci gate.CIGatePublisher, newStore func(db.DBTX) db.Store) runtimeDeps {
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
		_ = runtimeWriter
	}
	var writeback runtimeWriteback
	if discussionClient != nil && sqlDB != nil {
		writeback = platformgitlab.NewRuntimeWriteback(discussionClient, writer.NewSQLStoreWithStore(newStore(sqlDB))).WithMetrics(registry).WithTracer(tracer)
	}
	processor = wrapProcessorWithWriteback(sqlDB, processor, writeback, newStore)
	var auditLogger gate.AuditLogger
	if sqlDB != nil {
		auditLogger = gate.NewDBAuditLogger(newStore(sqlDB))
	}
	gateSvc := gate.NewService(gate.NoopStatusPublisher{}, ci, auditLogger)
	worker := scheduler.NewService(logger, sqlDB, processor,
		scheduler.WithMetrics(registry),
		scheduler.WithTracer(tracer),
		scheduler.WithStatusPublisher(status),
		scheduler.WithGateService(gateSvc),
		scheduler.WithStoreFactory(newStore),
	)
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "worker"
	}
	heartbeatIdentity := ops.WorkerIdentity{
		WorkerID:              worker.WorkerID(),
		Hostname:              hostname,
		Version:               workerVersion,
		ConfiguredConcurrency: int32(worker.ConfiguredConcurrency()),
	}
	var heartbeatSvc *ops.Service
	if sqlDB != nil {
		heartbeatSvc = ops.NewService(newStore(sqlDB))
	}
	return runtimeDeps{
		GateService:       gateSvc,
		Metrics:           registry,
		Tracer:            tracer,
		Scheduler:         worker,
		Heartbeat:         heartbeatSvc,
		HeartbeatIdentity: heartbeatIdentity,
	}
}

func wrapProcessorWithWriteback(sqlDB *sql.DB, processor scheduler.Processor, runtimeWriter runtimeWriteback, newStore func(db.DBTX) db.Store) scheduler.Processor {
	if processor == nil || sqlDB == nil || runtimeWriter == nil {
		return processor
	}
	queries := newStore(sqlDB)
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
		if bundleWriter, ok := runtimeWriter.(runtimeBundleWriteback); ok {
			if bundle, ok := outcome.ReviewBundle.(core.ReviewBundle); ok {
				if err := bundleWriter.WriteBundle(ctx, runForWriteback, bundle); err != nil {
					return scheduler.ProcessOutcome{}, err
				}
				return outcome, nil
			}
		}
		if err := runtimeWriter.Write(ctx, runForWriteback, outcome.ReviewFindings); err != nil {
			return scheduler.ProcessOutcome{}, err
		}
		return outcome, nil
	})
}
