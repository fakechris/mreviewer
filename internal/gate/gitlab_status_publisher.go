package gate

import (
	"context"
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gitlab"
)

const statusContextName = "mreviewer/ai-review"

type CommitStatusClient interface {
	SetCommitStatus(ctx context.Context, req gitlab.CommitStatusRequest) error
}

type StatusStore interface {
	GetProject(ctx context.Context, id int64) (db.Project, error)
	GetMergeRequest(ctx context.Context, id int64) (db.MergeRequest, error)
}

type GitLabStatusPublisher struct {
	client CommitStatusClient
	store  StatusStore
}

func NewGitLabStatusPublisher(client CommitStatusClient, store StatusStore) *GitLabStatusPublisher {
	return &GitLabStatusPublisher{client: client, store: store}
}

func (p *GitLabStatusPublisher) PublishStatus(ctx context.Context, result Result) error {
	if p == nil || p.client == nil || p.store == nil {
		return nil
	}

	project, err := p.store.GetProject(ctx, result.ProjectID)
	if err != nil {
		return fmt.Errorf("gate: load project %d: %w", result.ProjectID, err)
	}
	if project.GitlabProjectID == 0 {
		return fmt.Errorf("gate: project %d missing gitlab_project_id", result.ProjectID)
	}

	mergeRequest, err := p.store.GetMergeRequest(ctx, result.MergeRequestID)
	if err != nil {
		return fmt.Errorf("gate: load merge request %d: %w", result.MergeRequestID, err)
	}

	req := gitlab.CommitStatusRequest{
		ProjectID:   project.GitlabProjectID,
		SHA:         strings.TrimSpace(result.HeadSHA),
		State:       mapCommitStatusState(result.State),
		Name:        statusContextName,
		Description: commitStatusDescription(result),
		Ref:         strings.TrimSpace(mergeRequest.SourceBranch),
		TargetURL:   strings.TrimSpace(mergeRequest.WebUrl),
	}
	if req.SHA == "" {
		return fmt.Errorf("gate: run %d missing head sha for status publish", result.RunID)
	}
	return p.client.SetCommitStatus(ctx, req)
}

func mapCommitStatusState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "pending":
		return "pending"
	case "running":
		return "running"
	case "passed", "success", "completed":
		return "success"
	default:
		return "failed"
	}
}

func commitStatusDescription(result Result) string {
	switch mapCommitStatusState(result.State) {
	case "pending":
		return "AI review is pending"
	case "running":
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
