package ops

import (
	"context"
	"time"

	"github.com/mreviewer/mreviewer/internal/db"
)

type Store interface {
	UpsertWorkerHeartbeat(ctx context.Context, arg db.UpsertWorkerHeartbeatParams) error
	ListActiveWorkerHeartbeats(ctx context.Context, activeSince time.Time) ([]db.WorkerHeartbeat, error)
	ListRunningRunCountsByWorker(ctx context.Context) ([]db.ListRunningRunCountsByWorkerRow, error)
}

type Option func(*Service)

type Service struct {
	store Store
	now   func() time.Time
}

type WorkerIdentity struct {
	WorkerID              string
	Hostname              string
	Version               string
	ConfiguredConcurrency int32
}

type WorkerStatus struct {
	WorkerID              string
	Hostname              string
	Version               string
	ConfiguredConcurrency int32
	StartedAt             time.Time
	LastSeenAt            time.Time
	RunningRuns           int64
}

func NewService(store Store, opts ...Option) *Service {
	svc := &Service{
		store: store,
		now:   time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	if svc.now == nil {
		svc.now = time.Now
	}
	return svc
}

func WithNow(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func (s *Service) Beat(ctx context.Context, identity WorkerIdentity) error {
	ts := s.now().UTC()
	return s.store.UpsertWorkerHeartbeat(ctx, db.UpsertWorkerHeartbeatParams{
		WorkerID:              identity.WorkerID,
		Hostname:              identity.Hostname,
		Version:               identity.Version,
		ConfiguredConcurrency: identity.ConfiguredConcurrency,
		StartedAt:             ts,
		LastSeenAt:            ts,
	})
}

func (s *Service) ListActiveWorkers(ctx context.Context, freshnessWindow time.Duration) ([]WorkerStatus, error) {
	activeSince := s.now().UTC().Add(-freshnessWindow)
	heartbeats, err := s.store.ListActiveWorkerHeartbeats(ctx, activeSince)
	if err != nil {
		return nil, err
	}
	runningCounts, err := s.store.ListRunningRunCountsByWorker(ctx)
	if err != nil {
		return nil, err
	}
	runningByWorker := make(map[string]int64, len(runningCounts))
	for _, item := range runningCounts {
		runningByWorker[item.WorkerID] = item.RunningRuns
	}

	workers := make([]WorkerStatus, 0, len(heartbeats))
	for _, heartbeat := range heartbeats {
		workers = append(workers, WorkerStatus{
			WorkerID:              heartbeat.WorkerID,
			Hostname:              heartbeat.Hostname,
			Version:               heartbeat.Version,
			ConfiguredConcurrency: heartbeat.ConfiguredConcurrency,
			StartedAt:             heartbeat.StartedAt,
			LastSeenAt:            heartbeat.LastSeenAt,
			RunningRuns:           runningByWorker[heartbeat.WorkerID],
		})
	}
	return workers, nil
}

func (s *Service) Run(ctx context.Context, interval time.Duration, identity WorkerIdentity) error {
	if err := s.Beat(ctx, identity); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.Beat(ctx, identity); err != nil {
				return err
			}
		}
	}
}
