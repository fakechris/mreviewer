package gate

import (
	"context"
	"fmt"
	"strings"
)

type GitHubCommitStatusRequest struct {
	Owner       string
	Repo        string
	SHA         string
	State       string
	Context     string
	Description string
	TargetURL   string
}

type GitHubCommitStatusClient interface {
	SetCommitStatus(ctx context.Context, req GitHubCommitStatusRequest) error
}

type GitHubStatusPublisher struct {
	client GitHubCommitStatusClient
	store  StatusStore
}

func NewGitHubStatusPublisher(client GitHubCommitStatusClient, store StatusStore) *GitHubStatusPublisher {
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
	owner, repo, ok := strings.Cut(strings.TrimSpace(project.PathWithNamespace), "/")
	if !ok || owner == "" || repo == "" {
		return fmt.Errorf("gate: project %d missing owner/repo path", result.ProjectID)
	}

	mergeRequest, err := p.store.GetMergeRequest(ctx, result.MergeRequestID)
	if err != nil {
		return fmt.Errorf("gate: load merge request %d: %w", result.MergeRequestID, err)
	}
	headSHA := strings.TrimSpace(result.HeadSHA)
	if headSHA == "" {
		headSHA = strings.TrimSpace(mergeRequest.HeadSha)
	}
	if headSHA == "" {
		return fmt.Errorf("gate: run %d missing head sha for github status publish", result.RunID)
	}

	return p.client.SetCommitStatus(ctx, GitHubCommitStatusRequest{
		Owner:       owner,
		Repo:        repo,
		SHA:         headSHA,
		State:       mapGitHubCommitStatusState(result.State),
		Context:     statusContextName,
		Description: commitStatusDescription(result),
		TargetURL:   strings.TrimSpace(mergeRequest.WebUrl),
	})
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
