package main

import (
	"database/sql"
	"log/slog"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/metrics"
	"github.com/mreviewer/mreviewer/internal/scheduler"
	tracing "github.com/mreviewer/mreviewer/internal/trace"
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
	registry := metrics.NewRegistry()
	tracer := tracing.NewRecorder()
	if configurable, ok := processor.(interface {
		WithMetrics(*metrics.Registry)
		WithTracer(*tracing.Recorder)
	}); ok {
		configurable.WithMetrics(registry)
		configurable.WithTracer(tracer)
	}
	gateSvc := gate.NewService(status, ci, gate.NewDBAuditLogger(db.New(sqlDB)))
	worker := scheduler.NewService(logger, sqlDB, processor, scheduler.WithMetrics(registry), scheduler.WithTracer(tracer), scheduler.WithGateService(gateSvc))
	return runtimeDeps{GateService: gateSvc, Metrics: registry, Tracer: tracer, Scheduler: worker}
}
