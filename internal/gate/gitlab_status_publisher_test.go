package gate

import (
	"context"
	"testing"

	"github.com/mreviewer/mreviewer/internal/db"
)

type fakeCommitStatusClient struct {
	requests []CommitStatusRequest
	err      error
}

func (f *fakeCommitStatusClient) SetCommitStatus(_ context.Context, req CommitStatusRequest) error {
	f.requests = append(f.requests, req)
	return f.err
}

func TestGitLabStatusPublisherPublishesRunningStatus(t *testing.T) {
	client := &fakeCommitStatusClient{}
	publisher := NewGitLabStatusPublisher(client, fakeStatusStore{
		project: db.Project{ID: 10, GitlabProjectID: 101},
		mr:      db.MergeRequest{ID: 20, MrIid: 7, SourceBranch: "feature/status", WebUrl: "https://gitlab.example.com/group/project/-/merge_requests/7"},
	})

	err := publisher.PublishStatus(context.Background(), Result{
		RunID:          1,
		ProjectID:      10,
		MergeRequestID: 20,
		HeadSHA:        "head-sha",
		State:          "running",
	})
	if err != nil {
		t.Fatalf("PublishStatus: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(client.requests))
	}
	req := client.requests[0]
	if req.ProjectID != 101 {
		t.Fatalf("project id = %d, want 101", req.ProjectID)
	}
	if req.SHA != "head-sha" {
		t.Fatalf("sha = %q, want head-sha", req.SHA)
	}
	if req.State != "running" {
		t.Fatalf("state = %q, want running", req.State)
	}
	if req.Name != "mreviewer/ai-review" {
		t.Fatalf("name = %q, want mreviewer/ai-review", req.Name)
	}
	if req.Ref != "feature/status" {
		t.Fatalf("ref = %q, want feature/status", req.Ref)
	}
	if req.TargetURL != "https://gitlab.example.com/group/project/-/merge_requests/7" {
		t.Fatalf("target_url = %q, want MR URL", req.TargetURL)
	}
}

func TestGitLabStatusPublisherPublishesFailureForBlockingFindings(t *testing.T) {
	client := &fakeCommitStatusClient{}
	publisher := NewGitLabStatusPublisher(client, fakeStatusStore{
		project: db.Project{ID: 10, GitlabProjectID: 101},
		mr:      db.MergeRequest{ID: 20, MrIid: 7, SourceBranch: "feature/status"},
	})

	err := publisher.PublishStatus(context.Background(), Result{
		RunID:            1,
		ProjectID:        10,
		MergeRequestID:   20,
		HeadSHA:          "head-sha",
		State:            "failed",
		BlockingFindings: 2,
	})
	if err != nil {
		t.Fatalf("PublishStatus: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(client.requests))
	}
	req := client.requests[0]
	if req.State != "failed" {
		t.Fatalf("state = %q, want failed", req.State)
	}
	if req.Description == "" {
		t.Fatal("expected description to be populated")
	}
}

type fakeStatusStore struct {
	project db.Project
	mr      db.MergeRequest
	err     error
}

func (f fakeStatusStore) GetProject(_ context.Context, id int64) (db.Project, error) {
	if f.err != nil {
		return db.Project{}, f.err
	}
	if f.project.ID != id {
		return db.Project{}, nil
	}
	return f.project, nil
}

func (f fakeStatusStore) GetMergeRequest(_ context.Context, id int64) (db.MergeRequest, error) {
	if f.err != nil {
		return db.MergeRequest{}, f.err
	}
	if f.mr.ID != id {
		return db.MergeRequest{}, nil
	}
	return f.mr, nil
}
