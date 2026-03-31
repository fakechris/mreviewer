package github

import (
	"context"
	"fmt"

	"github.com/mreviewer/mreviewer/internal/db"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type RuntimeWriteback struct {
	publisher *Publisher
}

func NewRuntimeWriteback(client PublishClient) *RuntimeWriteback {
	if client == nil {
		return &RuntimeWriteback{}
	}
	return &RuntimeWriteback{publisher: NewPublisher(client)}
}

func (w *RuntimeWriteback) Write(_ context.Context, _ db.ReviewRun, _ []db.ReviewFinding) error {
	if w == nil || w.publisher == nil {
		return fmt.Errorf("github runtime writeback: publisher is required")
	}
	return fmt.Errorf("github runtime writeback: legacy findings write is unsupported; bundle writeback is required")
}

func (w *RuntimeWriteback) WriteBundle(ctx context.Context, _ db.ReviewRun, bundle core.ReviewBundle) error {
	if w == nil || w.publisher == nil {
		return fmt.Errorf("github runtime writeback: publisher is required")
	}
	return w.publisher.Publish(ctx, bundle)
}
