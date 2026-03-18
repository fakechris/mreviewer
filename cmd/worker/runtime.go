package main

import (
	"database/sql"
	"log/slog"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gate"
	"github.com/mreviewer/mreviewer/internal/scheduler"
)

type runtimeDeps struct {
	GateService *gate.Service
	Scheduler   *scheduler.Service
}

func newRuntimeDeps(logger *slog.Logger, sqlDB *sql.DB, processor scheduler.Processor) runtimeDeps {
	return newRuntimeDepsWithGatePublishers(logger, sqlDB, processor, gate.NoopStatusPublisher{}, gate.NoopCIGatePublisher{})
}

func newRuntimeDepsWithGatePublishers(logger *slog.Logger, sqlDB *sql.DB, processor scheduler.Processor, status gate.StatusPublisher, ci gate.CIGatePublisher) runtimeDeps {
	gateSvc := gate.NewService(status, ci, gate.NewDBAuditLogger(db.New(sqlDB)))
	worker := scheduler.NewService(logger, sqlDB, processor, scheduler.WithGateService(gateSvc))
	return runtimeDeps{GateService: gateSvc, Scheduler: worker}
}
