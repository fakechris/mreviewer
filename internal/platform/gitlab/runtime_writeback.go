package gitlab

import (
	"context"
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/metrics"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	tracing "github.com/mreviewer/mreviewer/internal/trace"
	legacywriter "github.com/mreviewer/mreviewer/internal/writer"
)

type RuntimeWriteback struct {
	legacy    *legacywriter.Writer
	store     legacywriter.Store
	publisher *Publisher
}

func NewRuntimeWriteback(client legacywriter.DiscussionClient, store legacywriter.Store) *RuntimeWriteback {
	var legacy *legacywriter.Writer
	if client != nil && store != nil {
		legacy = legacywriter.New(client, store)
	}
	var publisher *Publisher
	if client != nil {
		publisher = NewPublisher(client)
	}
	return &RuntimeWriteback{
		legacy:    legacy,
		store:     store,
		publisher: publisher,
	}
}

func (w *RuntimeWriteback) WithMetrics(registry *metrics.Registry) *RuntimeWriteback {
	if w != nil && w.legacy != nil {
		w.legacy = w.legacy.WithMetrics(registry)
	}
	return w
}

func (w *RuntimeWriteback) WithTracer(recorder *tracing.Recorder) *RuntimeWriteback {
	if w != nil && w.legacy != nil {
		w.legacy = w.legacy.WithTracer(recorder)
	}
	return w
}

func (w *RuntimeWriteback) Write(ctx context.Context, run db.ReviewRun, findings []db.ReviewFinding) error {
	if w == nil || w.legacy == nil {
		return fmt.Errorf("gitlab runtime writeback: legacy writer is required")
	}
	return w.legacy.Write(ctx, run, findings)
}

func (w *RuntimeWriteback) WriteBundle(ctx context.Context, run db.ReviewRun, bundle core.ReviewBundle) error {
	if w == nil {
		return fmt.Errorf("gitlab runtime writeback: writeback is required")
	}
	if strings.EqualFold(strings.TrimSpace(run.Status), "parser_error") {
		if w.legacy == nil {
			return fmt.Errorf("gitlab runtime writeback: legacy writer is required for parser_error")
		}
		return w.legacy.Write(ctx, run, nil)
	}
	if w.legacy != nil && w.store != nil && run.ID != 0 {
		findings, err := w.store.ListFindingsByRun(ctx, run.ID)
		if err != nil {
			return fmt.Errorf("gitlab runtime writeback: load persisted findings: %w", err)
		}
		return w.legacy.Write(ctx, run, findings)
	}
	if w.publisher == nil {
		return fmt.Errorf("gitlab runtime writeback: publisher is required")
	}
	return w.publisher.Publish(ctx, bundle)
}
