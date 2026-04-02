package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

	configPath := writeRuntimeConfig(t, "github_base_url: "+server.URL+"\ngithub_token: test-token\n")
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

func TestDefaultCompareSupportsArtifactImports(t *testing.T) {
	imported := core.ReviewerArtifact{
		ReviewerID:   "imported-codex",
		ReviewerKind: "codex",
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitHub,
			URL:          "https://github.com/acme/repo/pull/17",
			Repository:   "acme/repo",
			ChangeNumber: 17,
		},
		Findings: []core.Finding{
			{
				Category: "security",
				Identity: core.FindingIdentityInput{
					Category:        "security",
					NormalizedClaim: "user input is concatenated into sql.",
					Location: core.CanonicalLocation{
						Path:      "internal/db/query.go",
						Side:      core.DiffSideNew,
						StartLine: 42,
						EndLine:   42,
					},
				},
			},
		},
	}
	artifactPath := filepath.Join(t.TempDir(), "codex.json")
	payload, err := json.Marshal(imported)
	if err != nil {
		t.Fatalf("marshal artifact: %v", err)
	}
	if err := os.WriteFile(artifactPath, payload, 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	target := imported.Target
	bundle := core.ReviewBundle{
		Target: target,
		Artifacts: []core.ReviewerArtifact{
			{
				ReviewerID:   "security",
				ReviewerKind: "security",
				Target:       target,
				Findings:     imported.Findings,
			},
		},
	}

	report, err := defaultCompare(context.Background(), "", target, bundle, cliOptions{
		compareArtifactPaths: []string{artifactPath},
	})
	if err != nil {
		t.Fatalf("defaultCompare: %v", err)
	}
	if report == nil {
		t.Fatalf("report is nil")
	}
	if report.ReviewerCount != 2 {
		t.Fatalf("reviewer count = %d, want 2", report.ReviewerCount)
	}
}

func TestDefaultCompareSupportsGitHubLiveReviewers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/repo/issues/17/comments":
			if page := r.URL.Query().Get("page"); page != "" && page != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":1,"body":"Overall summary","user":{"login":"codex-bot"}}]`))
		case "/repos/acme/repo/pulls/17/comments":
			if page := r.URL.Query().Get("page"); page != "" && page != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":2,"body":"SQL built with string concatenation.","path":"internal/db/query.go","line":42,"side":"LEFT","user":{"login":"codex-bot"}}]`))
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
	bundle := core.ReviewBundle{Target: target}

	report, err := defaultCompare(context.Background(), configPath, target, bundle, cliOptions{
		compareLiveReviewers: []string{"codex-bot"},
	})
	if err != nil {
		t.Fatalf("defaultCompare: %v", err)
	}
	if report == nil {
		t.Fatalf("report is nil")
	}
	if report.ReviewerCount != 1 {
		t.Fatalf("reviewer count = %d, want 1", report.ReviewerCount)
	}
	if len(report.UniqueByReviewer["codex-bot"]) != 1 {
		t.Fatalf("unique findings = %#v, want one codex-bot finding", report.UniqueByReviewer)
	}
	if got := report.UniqueByReviewer["codex-bot"][0].Identity.Location.Side; got != core.DiffSideOld {
		t.Fatalf("github compare side = %q, want old", got)
	}
}

func TestDefaultCompareSupportsGitLabLiveReviewers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.RequestURI(), "/api/v4/projects/group%2Frepo/merge_requests/23/notes"):
			_, _ = w.Write([]byte(`[{"id":1,"body":"Overall summary","author":{"username":"gemini-bot"}}]`))
		case strings.HasPrefix(r.URL.RequestURI(), "/api/v4/projects/group%2Frepo/merge_requests/23/discussions"):
			_, _ = w.Write([]byte(`[{
				"id":"discussion-1",
				"notes":[
					{
						"id":2,
						"body":"Missing tenant scope.",
						"author":{"username":"gemini-bot"},
						"position":{"new_path":"internal/db/query.go","new_line":91}
					}
				]
			}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	configPath := writeRuntimeConfig(t, "gitlab_base_url: "+server.URL+"\ngitlab_token: test-token\n")
	target := core.ReviewTarget{
		Platform:     core.PlatformGitLab,
		URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
		BaseURL:      "https://gitlab.example.com",
		Repository:   "group/repo",
		ChangeNumber: 23,
	}
	bundle := core.ReviewBundle{Target: target}

	report, err := defaultCompare(context.Background(), configPath, target, bundle, cliOptions{
		compareLiveReviewers: []string{"gemini-bot"},
	})
	if err != nil {
		t.Fatalf("defaultCompare: %v", err)
	}
	if report == nil {
		t.Fatalf("report is nil")
	}
	if report.ReviewerCount != 1 {
		t.Fatalf("reviewer count = %d, want 1", report.ReviewerCount)
	}
}

func TestDefaultStatusPublishesGitHubCommitStatus(t *testing.T) {
	var state string
	var description string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/repo/statuses/head-sha" {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		state, _ = payload["state"].(string)
		description, _ = payload["description"].(string)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
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
	input := core.ReviewInput{Target: target}
	input.Request.Project.FullPath = "acme/repo"
	input.Request.Version.HeadSHA = "head-sha"

	if err := defaultStatus(context.Background(), configPath, target, input, "running", 0); err != nil {
		t.Fatalf("defaultStatus: %v", err)
	}
	if state != "pending" {
		t.Fatalf("state = %q, want pending", state)
	}
	if description != "AI review is running" {
		t.Fatalf("description = %q, want running description", description)
	}
}

func writeRuntimeConfig(t *testing.T, body string) string {
	t.Helper()
	if !strings.Contains(body, "models:") {
		body += "\nmodels:\n  minimax_default:\n    provider: minimax\n    base_url: https://example.com\n    api_key: test-key\n    model: test-model\n    output_mode: tool_call\n    max_tokens: 4096\nmodel_chains:\n  review_primary:\n    primary: minimax_default\n    fallbacks: []\nreview:\n  model_chain: review_primary\n"
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
