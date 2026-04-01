package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type PublishMode string

const (
	PublishModeFullReviewComments PublishMode = "full-review-comments"
	PublishModeSummaryOnly        PublishMode = "summary-only"
	PublishModeArtifactOnly       PublishMode = "artifact-only"
)

type PublishRequest struct {
	Owner  string
	Repo   string
	Number int64
	Mode   PublishMode
	Bundle reviewcore.ReviewBundle
}

type IssueCommentRequest struct {
	Owner  string
	Repo   string
	Number int64
	Body   string
}

type ReviewCommentRequest struct {
	Owner  string
	Repo   string
	Number int64
	Body   string
	Path   string
	Line   int
	Side   string
}

type PublishClient interface {
	CreateIssueComment(ctx context.Context, req IssueCommentRequest) error
	CreateReviewComment(ctx context.Context, req ReviewCommentRequest) error
}

type Publisher struct {
	client PublishClient
}

func NewPublisher(client PublishClient) *Publisher {
	return &Publisher{client: client}
}

func (p *Publisher) Publish(ctx context.Context, req PublishRequest) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("github publisher: client is required")
	}
	if req.Mode == PublishModeArtifactOnly {
		return nil
	}

	for _, candidate := range req.Bundle.PublishCandidates {
		switch candidate.Type {
		case "summary":
			if err := p.client.CreateIssueComment(ctx, IssueCommentRequest{
				Owner:  req.Owner,
				Repo:   req.Repo,
				Number: req.Number,
				Body:   publishCandidateBody(candidate),
			}); err != nil {
				return err
			}
		case "finding":
			if req.Mode == PublishModeSummaryOnly {
				continue
			}
			comment := ReviewCommentRequest{
				Owner:  req.Owner,
				Repo:   req.Repo,
				Number: req.Number,
				Body:   publishCandidateBody(candidate),
			}
			if candidate.Location != nil {
				comment.Path = candidate.Location.Path
				comment.Line = candidate.Location.Line
				if candidate.Location.Side == reviewcore.LocationSideOld {
					comment.Side = "LEFT"
				} else {
					comment.Side = "RIGHT"
				}
			}
			if err := p.client.CreateReviewComment(ctx, comment); err != nil {
				return err
			}
		}
	}
	return nil
}

func publishCandidateBody(candidate reviewcore.PublishCandidate) string {
	body := strings.TrimSpace(candidate.Body)
	if body != "" {
		return body
	}
	return strings.TrimSpace(candidate.Title)
}
