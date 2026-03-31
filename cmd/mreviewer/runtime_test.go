package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewinput"
)

type fakeSnapshotFetcher struct {
	target   core.ReviewTarget
	snapshot core.PlatformSnapshot
}

func (f *fakeSnapshotFetcher) FetchSnapshot(_ context.Context, target core.ReviewTarget) (core.PlatformSnapshot, error) {
	f.target = target
	return f.snapshot, nil
}

type fakeReviewInputBuilder struct {
	input  reviewinput.BuildInput
	output core.ReviewInput
}

func (f *fakeReviewInputBuilder) Build(_ context.Context, input reviewinput.BuildInput) (core.ReviewInput, error) {
	f.input = input
	return f.output, nil
}

func TestBuildGitLabReviewInputBuildsFromSnapshot(t *testing.T) {
	target := core.ReviewTarget{
		Platform:     core.PlatformGitLab,
		URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
		Repository:   "group/repo",
		ChangeNumber: 23,
	}
	snapshot := core.PlatformSnapshot{
		Target: target,
		Change: core.PlatformChange{
			PlatformID:   "101",
			ProjectID:    77,
			Number:       23,
			Title:        "Refactor parser",
			TargetBranch: "main",
			SourceBranch: "feat/parser",
			Description:  "Makes parser deterministic",
			HeadSHA:      "head-sha",
			WebURL:       target.URL,
			Author:       core.PlatformAuthor{Username: "chris"},
		},
		Version: core.PlatformVersion{
			PlatformVersionID: "123",
			BaseSHA:           "base",
			StartSHA:          "start",
			HeadSHA:           "head",
		},
	}
	fetcher := &fakeSnapshotFetcher{snapshot: snapshot}
	builder := &fakeReviewInputBuilder{
		output: core.ReviewInput{Target: target},
	}

	input, err := buildGitLabReviewInput(context.Background(), target, fetcher, builder)
	if err != nil {
		t.Fatalf("buildGitLabReviewInput: %v", err)
	}
	if input.Target.URL != target.URL {
		t.Fatalf("target url = %q, want %q", input.Target.URL, target.URL)
	}
	if fetcher.target.URL != target.URL {
		t.Fatalf("fetcher target = %q, want %q", fetcher.target.URL, target.URL)
	}
	if builder.input.Snapshot.Change.Title != "Refactor parser" {
		t.Fatalf("builder snapshot title = %q", builder.input.Snapshot.Change.Title)
	}
	if builder.input.ProjectDefaultBranch != "main" {
		t.Fatalf("project default branch = %q, want main", builder.input.ProjectDefaultBranch)
	}
	if builder.input.ProjectPolicy != nil {
		t.Fatalf("project policy = %#v, want nil", builder.input.ProjectPolicy)
	}
	if builder.input.MergeRequestID != 0 {
		t.Fatalf("merge request id = %d, want 0 until db-backed lookup exists", builder.input.MergeRequestID)
	}
}

func TestBuildGitHubReviewInputBuildsFromSnapshot(t *testing.T) {
	target := core.ReviewTarget{
		Platform:     core.PlatformGitHub,
		URL:          "https://github.com/acme/repo/pull/17",
		Repository:   "acme/repo",
		ChangeNumber: 17,
	}
	snapshot := core.PlatformSnapshot{
		Target: target,
		Change: core.PlatformChange{
			PlatformID:   "101",
			Number:       17,
			Title:        "Refactor parser",
			TargetBranch: "main",
			SourceBranch: "feat/parser",
			Description:  "Makes parser deterministic",
			HeadSHA:      "head-sha",
			WebURL:       target.URL,
			Author:       core.PlatformAuthor{Username: "chris"},
		},
		Version: core.PlatformVersion{
			BaseSHA: "base",
			HeadSHA: "head",
		},
	}
	fetcher := &fakeSnapshotFetcher{snapshot: snapshot}
	builder := &fakeReviewInputBuilder{
		output: core.ReviewInput{Target: target},
	}

	input, err := buildGitHubReviewInput(context.Background(), target, fetcher, builder)
	if err != nil {
		t.Fatalf("buildGitHubReviewInput: %v", err)
	}
	if input.Target.URL != target.URL {
		t.Fatalf("target url = %q, want %q", input.Target.URL, target.URL)
	}
	if fetcher.target.URL != target.URL {
		t.Fatalf("fetcher target = %q, want %q", fetcher.target.URL, target.URL)
	}
	if builder.input.Snapshot.Change.Title != "Refactor parser" {
		t.Fatalf("builder snapshot title = %q", builder.input.Snapshot.Change.Title)
	}
	if builder.input.ProjectDefaultBranch != "main" {
		t.Fatalf("project default branch = %q, want main", builder.input.ProjectDefaultBranch)
	}
}

func TestDefaultLoadInputSupportsGitHubTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/repo/pulls/17":
			_, _ = w.Write([]byte(`{
				"id": 101,
				"number": 17,
				"title": "Refactor parser",
				"body": "Makes parser deterministic",
				"state": "open",
				"draft": false,
				"html_url": "https://github.com/acme/repo/pull/17",
				"user": {"login": "chris"},
				"base": {"ref": "main", "sha": "base"},
				"head": {"ref": "feat/parser", "sha": "head"}
			}`))
		case "/repos/acme/repo/pulls/17/files":
			if page := r.URL.Query().Get("page"); page != "" && page != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{
				"filename": "internal/new.go",
				"status": "modified",
				"patch": "@@ -1 +1 @@\n-old\n+new\n"
			}]`))
		case "/repos/acme/repo/contents/.github/ai-review.yaml":
			http.NotFound(w, r)
		case "/repos/acme/repo/contents/REVIEW.md":
			_, _ = w.Write([]byte(`{
				"type": "file",
				"encoding": "base64",
				"content": "IyBSZXZpZXcKLSBGb2N1cyBvbiBjb3JyZWN0bmVzcy4K"
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	configPath := writeRuntimeConfig(t, "github_base_url: "+server.URL+"\ngithub_token: test-token\nllm_provider: minimax\nllm_api_key: test-key\nllm_base_url: https://example.com\nllm_model: test-model\n")
	target := core.ReviewTarget{
		Platform:     core.PlatformGitHub,
		URL:          "https://github.com/acme/repo/pull/17",
		BaseURL:      "https://github.com",
		Repository:   "acme/repo",
		ChangeNumber: 17,
	}

	input, err := defaultLoadInput(context.Background(), configPath, target)
	if err != nil {
		t.Fatalf("defaultLoadInput: %v", err)
	}
	if input.Target.URL != target.URL {
		t.Fatalf("target url = %q, want %q", input.Target.URL, target.URL)
	}
	if input.Request.MergeRequest.Title != "Refactor parser" {
		t.Fatalf("title = %q", input.Request.MergeRequest.Title)
	}
	if input.Request.Project.FullPath != "acme/repo" {
		t.Fatalf("project full_path = %q", input.Request.Project.FullPath)
	}
}

func TestDefaultPublishSupportsGitHubTarget(t *testing.T) {
	var issueComments int
	var reviewComments int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/repo/pulls/17":
			_, _ = w.Write([]byte(`{
				"id": 101,
				"number": 17,
				"title": "Refactor parser",
				"body": "Makes parser deterministic",
				"state": "open",
				"draft": false,
				"html_url": "https://github.com/acme/repo/pull/17",
				"user": {"login": "chris"},
				"base": {"ref": "main", "sha": "base"},
				"head": {"ref": "feat/parser", "sha": "head-sha"}
			}`))
		case "/repos/acme/repo/pulls/17/files":
			if page := r.URL.Query().Get("page"); page != "" && page != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case "/repos/acme/repo/issues/17/comments":
			issueComments++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
		case "/repos/acme/repo/pulls/17/comments":
			reviewComments++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":2}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	configPath := writeRuntimeConfig(t, "github_base_url: "+server.URL+"\ngithub_token: test-token\n")
	target := core.ReviewTarget{
		Platform:     core.PlatformGitHub,
		URL:          "https://github.com/acme/repo/pull/17",
		BaseURL:      "https://github.com",
		Repository:   "acme/repo",
		ChangeNumber: 17,
	}
	bundle := core.ReviewBundle{
		Target: target,
		PublishCandidates: []core.PublishCandidate{
			{Kind: "summary", Body: "Summary"},
			{
				Kind: "finding",
				Body: "Use parameterized query",
				Location: core.CanonicalLocation{
					Path:      "internal/db/query.go",
					Side:      core.DiffSideNew,
					StartLine: 44,
					EndLine:   44,
				},
			},
		},
	}

	if err := defaultPublish(context.Background(), configPath, target, bundle); err != nil {
		t.Fatalf("defaultPublish: %v", err)
	}
	if issueComments != 1 {
		t.Fatalf("issue comments = %d, want 1", issueComments)
	}
	if reviewComments != 1 {
		t.Fatalf("review comments = %d, want 1", reviewComments)
	}
}

func writeRuntimeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
