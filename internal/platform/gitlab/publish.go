package gitlab

import (
	"context"
	"fmt"

	"github.com/mreviewer/mreviewer/internal/reviewcomment"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type PublishClient interface {
	CreateDiscussion(ctx context.Context, req reviewcomment.CreateDiscussionRequest) (reviewcomment.Discussion, error)
	CreateNote(ctx context.Context, req reviewcomment.CreateNoteRequest) (reviewcomment.Discussion, error)
}

type Publisher struct {
	client PublishClient
	writer *Writer
}

func NewPublisher(client PublishClient) *Publisher {
	return &Publisher{
		client: client,
		writer: NewWriter(),
	}
}

func (p *Publisher) Publish(ctx context.Context, bundle core.ReviewBundle) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("gitlab publisher: client is required")
	}
	if p.writer == nil {
		p.writer = NewWriter()
	}
	requests, err := p.writer.BuildRequests(bundle)
	if err != nil {
		return err
	}
	for _, req := range requests.Notes {
		if _, err := p.client.CreateNote(ctx, req); err != nil {
			return fmt.Errorf("gitlab publisher: create note: %w", err)
		}
	}
	for _, req := range requests.Discussions {
		if _, err := p.client.CreateDiscussion(ctx, req); err != nil {
			return fmt.Errorf("gitlab publisher: create discussion: %w", err)
		}
	}
	return nil
}
