package github

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type RuntimeWriteback struct {
	queries   *db.Queries
	publisher *Publisher
}

func NewRuntimeWriteback(sqlDB *sql.DB, client PublishClient) *RuntimeWriteback {
	if sqlDB == nil || client == nil {
		return nil
	}
	return &RuntimeWriteback{
		queries:   db.New(sqlDB),
		publisher: NewPublisher(client),
	}
}

func (w *RuntimeWriteback) WriteBundle(ctx context.Context, run db.ReviewRun, bundle reviewcore.ReviewBundle) error {
	if w == nil || w.queries == nil || w.publisher == nil {
		return fmt.Errorf("github runtime writeback: dependencies are required")
	}

	project, err := w.queries.GetProject(ctx, run.ProjectID)
	if err != nil {
		return fmt.Errorf("github runtime writeback: load project: %w", err)
	}
	mergeRequest, err := w.queries.GetMergeRequest(ctx, run.MergeRequestID)
	if err != nil {
		return fmt.Errorf("github runtime writeback: load merge request: %w", err)
	}

	owner, repo, ok := splitRepository(project.PathWithNamespace)
	if !ok {
		return fmt.Errorf("github runtime writeback: invalid repository path %q", project.PathWithNamespace)
	}

	return w.publisher.Publish(ctx, PublishRequest{
		Owner:  owner,
		Repo:   repo,
		Number: mergeRequest.MrIid,
		Mode:   PublishModeFullReviewComments,
		Bundle: bundle,
	})
}

func splitRepository(repository string) (string, string, bool) {
	owner, repo, ok := strings.Cut(strings.TrimSpace(repository), "/")
	if !ok || owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}
