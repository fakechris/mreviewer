package gate_test

import (
	"context"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/gate"
)

type fakeGitHubStatusClient struct {
	requests []gate.GitHubCommitStatusRequest
}

func (f *fakeGitHubStatusClient) SetCommitStatus(_ context.Context, req gate.GitHubCommitStatusRequest) error {
	f.requests = append(f.requests, req)
	return nil
}

type fakeGitHubStatusStore struct {
	project  db.Project
	mr       db.MergeRequest
	instance db.GitlabInstance
}

func (f fakeGitHubStatusStore) GetProject(_ context.Context, id int64) (db.Project, error) {
	if f.project.ID != id {
		return db.Project{}, nil
	}
	return f.project, nil
}

func (f fakeGitHubStatusStore) GetMergeRequest(_ context.Context, id int64) (db.MergeRequest, error) {
	if f.mr.ID != id {
		return db.MergeRequest{}, nil
	}
	return f.mr, nil
}

func (f fakeGitHubStatusStore) GetGitlabInstance(_ context.Context, id int64) (db.GitlabInstance, error) {
	if f.instance.ID != id {
		return db.GitlabInstance{}, nil
	}
	return f.instance, nil
}

func TestGitHubStatusPublisherPublishesRunningStatus(t *testing.T) {
	client := &fakeGitHubStatusClient{}
	publisher := gate.NewGitHubStatusPublisher(client, fakeGitHubStatusStore{
		project:  db.Project{ID: 1, GitlabInstanceID: 3, GitlabProjectID: 101, PathWithNamespace: "acme/repo"},
		mr:       db.MergeRequest{ID: 7, MrIid: 17, SourceBranch: "feature", WebUrl: "https://github.com/acme/repo/pull/17"},
		instance: db.GitlabInstance{ID: 3, Url: "https://github.com"},
	})

	err := publisher.PublishStatus(context.Background(), gate.Result{
		RunID:          11,
		MergeRequestID: 7,
		ProjectID:      1,
		HeadSHA:        "head-sha",
		State:          "running",
	})
	if err != nil {
		t.Fatalf("PublishStatus: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("status requests = %d, want 1", len(client.requests))
	}
	req := client.requests[0]
	if req.Repository != "acme/repo" {
		t.Fatalf("repository = %q, want acme/repo", req.Repository)
	}
	if req.State != "pending" {
		t.Fatalf("state = %q, want pending", req.State)
	}
	if req.Context != "mreviewer/ai-review" {
		t.Fatalf("context = %q, want mreviewer/ai-review", req.Context)
	}
}
