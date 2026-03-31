package reviewrun

import (
	"context"
	"fmt"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/hooks"
	"github.com/mreviewer/mreviewer/internal/scheduler"
)

type EventProcessor interface {
	ProcessEventWithQuerier(ctx context.Context, q db.Querier, ev hooks.NormalizedEvent, hookEventID int64) error
}

type RunProcessor interface {
	ProcessRun(ctx context.Context, run db.ReviewRun) (scheduler.ProcessOutcome, error)
}

type Service struct {
	eventProcessor EventProcessor
	runProcessor   RunProcessor
}

func NewService(eventProcessor EventProcessor, runProcessor RunProcessor) *Service {
	return &Service{
		eventProcessor: eventProcessor,
		runProcessor:   runProcessor,
	}
}

func (s *Service) ProcessEventWithQuerier(ctx context.Context, q db.Querier, ev hooks.NormalizedEvent, hookEventID int64) error {
	if s == nil || s.eventProcessor == nil {
		return fmt.Errorf("reviewrun: event processor is not configured")
	}
	return s.eventProcessor.ProcessEventWithQuerier(ctx, q, ev, hookEventID)
}

func (s *Service) ProcessRun(ctx context.Context, run db.ReviewRun) (scheduler.ProcessOutcome, error) {
	if s == nil || s.runProcessor == nil {
		return scheduler.ProcessOutcome{}, fmt.Errorf("reviewrun: run processor is not configured")
	}
	return s.runProcessor.ProcessRun(ctx, run)
}
