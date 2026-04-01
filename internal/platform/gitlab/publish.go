package gitlab

import (
	"context"
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/reviewcomment"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type PublishMode string

const (
	PublishModeFullReviewComments PublishMode = "full-review-comments"
	PublishModeSummaryOnly        PublishMode = "summary-only"
	PublishModeArtifactOnly       PublishMode = "artifact-only"
)

type PublishRequest struct {
	ProjectID       int64
	MergeRequestIID int64
	Mode            PublishMode
	Bundle          reviewcore.ReviewBundle
}

type PublishedCandidate struct {
	Candidate  reviewcore.PublishCandidate
	Discussion reviewcomment.Discussion
}

type PublishResult struct {
	Published []PublishedCandidate
}

type DiscussionClient interface {
	CreateDiscussion(ctx context.Context, req reviewcomment.CreateDiscussionRequest) (reviewcomment.Discussion, error)
	CreateNote(ctx context.Context, req reviewcomment.CreateNoteRequest) (reviewcomment.Discussion, error)
}

type Publisher struct {
	client DiscussionClient
}

func NewPublisher(client DiscussionClient) *Publisher {
	return &Publisher{client: client}
}

func (p *Publisher) Publish(ctx context.Context, req PublishRequest) error {
	_, err := p.PublishWithResult(ctx, req)
	return err
}

func (p *Publisher) PublishWithResult(ctx context.Context, req PublishRequest) (PublishResult, error) {
	if p == nil || p.client == nil {
		return PublishResult{}, fmt.Errorf("gitlab publisher: client is required")
	}
	if req.Mode == PublishModeArtifactOnly {
		return PublishResult{}, nil
	}

	result := PublishResult{Published: make([]PublishedCandidate, 0, len(req.Bundle.PublishCandidates))}
	for _, candidate := range req.Bundle.PublishCandidates {
		switch candidate.Type {
		case "summary":
			discussion, err := p.publishSummary(ctx, req, candidate)
			if err != nil {
				return PublishResult{}, err
			}
			result.Published = append(result.Published, PublishedCandidate{Candidate: candidate, Discussion: discussion})
		case "finding":
			if req.Mode == PublishModeSummaryOnly {
				continue
			}
			discussion, err := p.publishFinding(ctx, req, candidate)
			if err != nil {
				return PublishResult{}, err
			}
			result.Published = append(result.Published, PublishedCandidate{Candidate: candidate, Discussion: discussion})
		}
	}
	return result, nil
}

func (p *Publisher) publishSummary(ctx context.Context, req PublishRequest, candidate reviewcore.PublishCandidate) (reviewcomment.Discussion, error) {
	discussion, err := p.client.CreateNote(ctx, reviewcomment.CreateNoteRequest{
		ProjectID:       req.ProjectID,
		MergeRequestIID: req.MergeRequestIID,
		Body:            publishBody(candidate),
	})
	return discussion, err
}

func (p *Publisher) publishFinding(ctx context.Context, req PublishRequest, candidate reviewcore.PublishCandidate) (reviewcomment.Discussion, error) {
	discussion, err := p.client.CreateDiscussion(ctx, reviewcomment.CreateDiscussionRequest{
		ProjectID:       req.ProjectID,
		MergeRequestIID: req.MergeRequestIID,
		Body:            publishBody(candidate),
		Position:        positionFromCandidate(candidate),
	})
	return discussion, err
}

func publishBody(candidate reviewcore.PublishCandidate) string {
	body := strings.TrimSpace(candidate.Body)
	if body != "" {
		return body
	}
	return strings.TrimSpace(candidate.Title)
}

func positionFromCandidate(candidate reviewcore.PublishCandidate) reviewcomment.Position {
	position := reviewcomment.Position{
		PositionType: "text",
	}
	if candidate.Location == nil {
		return position
	}
	position.OldPath = candidate.Location.Path
	position.NewPath = candidate.Location.Path
	switch candidate.Location.Side {
	case reviewcore.LocationSideOld:
		line := int32(candidate.Location.Line)
		position.OldLine = &line
	default:
		line := int32(candidate.Location.Line)
		position.NewLine = &line
	}
	return position
}
