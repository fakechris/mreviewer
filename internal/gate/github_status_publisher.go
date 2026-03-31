package gate

import (
	"context"
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
)

type GitHubCommitStatusRequest struct {
	Repository  string
	SHA         string
	State       string
	Context     string
	Description string
	TargetURL   string
}

type GitHubCommitStatusClient interface {
	SetCommitStatus(ctx context.Context, req GitHubCommitStatusRequest) error
}

type GitHubStatusStore interface {
	GetProject(ctx context.Context, id int64) (db.Project, error)
	GetMergeRequest(ctx context.Context, id int64) (db.MergeRequest, error)
}

type GitHubStatusPublisher struct {
	client GitHubCommitStatusClient
	store  GitHubStatusStore
}

func NewGitHubStatusPublisher(client GitHubCommitStatusClient, store GitHubStatusStore) *GitHubStatusPublisher {
	return &GitHubStatusPublisher{client: client, store: store}
}

func (p *GitHubStatusPublisher) PublishStatus(ctx context.Context, result Result) error {
	if p == nil || p.client == nil || p.store == nil {
		return nil
	}
	project, err := p.store.GetProject(ctx, result.ProjectID)
	if err != nil {
		return fmt.Errorf("gate: load project %d: %w", result.ProjectID, err)
	}
	mergeRequest, err := p.store.GetMergeRequest(ctx, result.MergeRequestID)
	if err != nil {
		return fmt.Errorf("gate: load merge request %d: %w", result.MergeRequestID, err)
	}
	if !looksLikeGitHubURL(mergeRequest.WebUrl) {
		return nil
	}
	if strings.TrimSpace(project.PathWithNamespace) == "" {
		return fmt.Errorf("gate: github project %d missing repository path", result.ProjectID)
	}
	req := GitHubCommitStatusRequest{
		Repository:  project.PathWithNamespace,
		SHA:         strings.TrimSpace(result.HeadSHA),
		State:       mapGitHubCommitStatusState(result.State),
		Context:     statusContextName,
		Description: githubCommitStatusDescription(result),
		TargetURL:   strings.TrimSpace(mergeRequest.WebUrl),
	}
	if req.SHA == "" {
		return fmt.Errorf("gate: run %d missing head sha for github status publish", result.RunID)
	}
	return p.client.SetCommitStatus(ctx, req)
}

func mapGitHubCommitStatusState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "pending", "running":
		return "pending"
	case "passed", "success", "completed":
		return "success"
	default:
		return "failure"
	}
}

func githubCommitStatusDescription(result Result) string {
	switch mapGitHubCommitStatusState(result.State) {
	case "pending":
		return "AI review is running"
	case "success":
		return "AI review passed"
	default:
		if result.BlockingFindings > 0 {
			return fmt.Sprintf("AI review found %d blocking findings", result.BlockingFindings)
		}
		return "AI review failed"
	}
}

func looksLikeGitHubURL(raw string) bool {
	raw = strings.ToLower(strings.TrimSpace(raw))
	return strings.Contains(raw, "/pull/")
}
