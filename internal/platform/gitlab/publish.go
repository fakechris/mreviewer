package gitlab

import (
	"context"
	"fmt"
	"strings"

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
			if isPublishPositionFailure(err) {
				if _, noteErr := p.client.CreateNote(ctx, reviewcomment.CreateNoteRequest{
					ProjectID:       req.ProjectID,
					MergeRequestIID: req.MergeRequestIID,
					Body:            req.Body,
				}); noteErr != nil {
					return fmt.Errorf("gitlab publisher: create fallback note after discussion failure: %w", noteErr)
				}
				continue
			}
			return fmt.Errorf("gitlab publisher: create discussion: %w", err)
		}
	}
	return nil
}

func isPublishPositionFailure(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "400") || strings.Contains(message, "position") || strings.Contains(message, "line_code") || strings.Contains(message, "invalid line")
}
