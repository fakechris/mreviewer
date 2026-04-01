package reviewrun

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/hooks"
	"github.com/mreviewer/mreviewer/internal/runs"
)

// Service is the shared review-run orchestration layer used by manual trigger,
// webhook ingress, and worker runtime. The first phase reuses the stable
// lifecycle implementation from internal/runs while centralizing the entry
// point for future engine-backed cutovers.
type Service struct {
	lifecycle *runs.Service
}

func NewService(logger *slog.Logger, sqlDB *sql.DB) *Service {
	return &Service{lifecycle: runs.NewService(logger, sqlDB)}
}

func (s *Service) ProcessEvent(ctx context.Context, ev hooks.NormalizedEvent, hookEventID int64) error {
	if s == nil || s.lifecycle == nil {
		return nil
	}
	return s.lifecycle.ProcessEvent(ctx, ev, hookEventID)
}

func (s *Service) ProcessEventWithQuerier(ctx context.Context, q db.Querier, ev hooks.NormalizedEvent, hookEventID int64) error {
	if s == nil || s.lifecycle == nil {
		return nil
	}
	return s.lifecycle.ProcessEventWithQuerier(ctx, q, ev, hookEventID)
}
