package github

import (
	"context"
	"fmt"
	"strings"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type CreateIssueCommentRequest struct {
	Repository string `json:"repository"`
	PullNumber int64  `json:"pull_number"`
	Body       string `json:"body"`
}

type CreateReviewCommentRequest struct {
	Repository string `json:"repository"`
	PullNumber int64  `json:"pull_number"`
	CommitID   string `json:"commit_id"`
	Body       string `json:"body"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Side       string `json:"side"`
	StartLine  int    `json:"start_line,omitempty"`
	StartSide  string `json:"start_side,omitempty"`
}

type PublishClient interface {
	GetPullRequestSnapshotByRepositoryRef(ctx context.Context, repositoryRef string, pullNumber int64) (PullRequestSnapshot, error)
	CreateIssueComment(ctx context.Context, req CreateIssueCommentRequest) error
	CreateReviewComment(ctx context.Context, req CreateReviewCommentRequest) error
}

type Publisher struct {
	client PublishClient
}

func NewPublisher(client PublishClient) *Publisher {
	return &Publisher{client: client}
}

func (p *Publisher) Publish(ctx context.Context, bundle core.ReviewBundle) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("github publisher: client is required")
	}
	if strings.TrimSpace(bundle.Target.Repository) == "" || bundle.Target.ChangeNumber <= 0 {
		return fmt.Errorf("github publisher: repository and change number are required")
	}
	snapshot, err := p.client.GetPullRequestSnapshotByRepositoryRef(ctx, bundle.Target.Repository, bundle.Target.ChangeNumber)
	if err != nil {
		return fmt.Errorf("github publisher: load pull request snapshot: %w", err)
	}
	for _, candidate := range bundle.PublishCandidates {
		switch candidate.Kind {
		case "summary":
			if strings.TrimSpace(candidate.Body) == "" {
				continue
			}
			if err := p.client.CreateIssueComment(ctx, CreateIssueCommentRequest{
				Repository: bundle.Target.Repository,
				PullNumber: bundle.Target.ChangeNumber,
				Body:       candidate.Body,
			}); err != nil {
				return fmt.Errorf("github publisher: create issue comment: %w", err)
			}
		case "finding":
			if candidate.PublishAsSummary {
				body := strings.TrimSpace(candidate.Body)
				if body == "" {
					body = strings.TrimSpace(candidate.Title)
				}
				if body == "" {
					continue
				}
				if err := p.client.CreateIssueComment(ctx, CreateIssueCommentRequest{
					Repository: bundle.Target.Repository,
					PullNumber: bundle.Target.ChangeNumber,
					Body:       body,
				}); err != nil {
					return fmt.Errorf("github publisher: create issue comment for unanchored finding: %w", err)
				}
				continue
			}
			req, ok := reviewCommentRequest(bundle.Target, snapshot.PullRequest.HeadSHA, candidate)
			if !ok {
				continue
			}
			if err := p.client.CreateReviewComment(ctx, req); err != nil {
				return fmt.Errorf("github publisher: create review comment: %w", err)
			}
		}
	}
	return nil
}

func reviewCommentRequest(target core.ReviewTarget, commitID string, candidate core.PublishCandidate) (CreateReviewCommentRequest, bool) {
	if strings.TrimSpace(commitID) == "" || strings.TrimSpace(candidate.Location.Path) == "" || candidate.Location.StartLine <= 0 {
		return CreateReviewCommentRequest{}, false
	}
	body := strings.TrimSpace(candidate.Body)
	if body == "" {
		body = strings.TrimSpace(candidate.Title)
	}
	if body == "" {
		return CreateReviewCommentRequest{}, false
	}
	side := "RIGHT"
	if candidate.Location.Side == core.DiffSideOld {
		side = "LEFT"
	}
	req := CreateReviewCommentRequest{
		Repository: target.Repository,
		PullNumber: target.ChangeNumber,
		CommitID:   commitID,
		Body:       body,
		Path:       candidate.Location.Path,
		Line:       candidate.Location.EndLine,
		Side:       side,
	}
	if req.Line <= 0 {
		req.Line = candidate.Location.StartLine
	}
	if candidate.Location.EndLine > candidate.Location.StartLine {
		req.StartLine = candidate.Location.StartLine
		req.StartSide = side
	}
	return req, true
}
