package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientGetPullRequestSnapshotByRepositoryRef(t *testing.T) {
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
				"previous_filename": "internal/old.go",
				"status": "modified",
				"patch": "@@ -1 +1 @@\n-old\n+new\n"
			}]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	snapshot, err := client.GetPullRequestSnapshotByRepositoryRef(context.Background(), "acme/repo", 17)
	if err != nil {
		t.Fatalf("GetPullRequestSnapshotByRepositoryRef: %v", err)
	}
	if snapshot.PullRequest.Title != "Refactor parser" {
		t.Fatalf("title = %q", snapshot.PullRequest.Title)
	}
	if snapshot.PullRequest.BaseRefName != "main" || snapshot.PullRequest.HeadRefName != "feat/parser" {
		t.Fatalf("base/head refs = %q/%q", snapshot.PullRequest.BaseRefName, snapshot.PullRequest.HeadRefName)
	}
	if len(snapshot.Files) != 1 || snapshot.Files[0].Filename != "internal/new.go" {
		t.Fatalf("files = %#v", snapshot.Files)
	}
}

func TestClientGetRepositoryFileByRepositoryRef(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/repos/acme/repo/contents/REVIEW.md" {
			t.Fatalf("path = %q", got)
		}
		if got := r.URL.Query().Get("ref"); got != "head-sha" {
			t.Fatalf("ref = %q, want head-sha", got)
		}
		_, _ = w.Write([]byte(`{
			"type": "file",
			"encoding": "base64",
			"content": "IyBSZXZpZXcKRm9jdXMgb24gdHJ1c3QgYm91bmRhcmllcy4K"
		}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	body, err := client.GetRepositoryFileByRepositoryRef(context.Background(), "acme/repo", "REVIEW.md", "head-sha")
	if err != nil {
		t.Fatalf("GetRepositoryFileByRepositoryRef: %v", err)
	}
	if body != "# Review\nFocus on trust boundaries.\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestClientGetPullRequestSnapshotByRepositoryRefPaginatesFiles(t *testing.T) {
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
			switch r.URL.Query().Get("page") {
			case "", "1":
				_, _ = w.Write([]byte(`[{
					"filename": "internal/first.go",
					"status": "modified",
					"patch": "@@ -1 +1 @@\n-old\n+new\n"
				}]`))
			case "2":
				_, _ = w.Write([]byte(`[{
					"filename": "internal/second.go",
					"status": "modified",
					"patch": "@@ -1 +1 @@\n-old\n+new\n"
				}]`))
			default:
				_, _ = w.Write([]byte(`[]`))
			}
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	snapshot, err := client.GetPullRequestSnapshotByRepositoryRef(context.Background(), "acme/repo", 17)
	if err != nil {
		t.Fatalf("GetPullRequestSnapshotByRepositoryRef: %v", err)
	}
	if len(snapshot.Files) != 2 {
		t.Fatalf("files len = %d, want 2", len(snapshot.Files))
	}
	if snapshot.Files[1].Filename != "internal/second.go" {
		t.Fatalf("second filename = %q", snapshot.Files[1].Filename)
	}
}

func TestClientSetCommitStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/repos/acme/repo/statuses/head-sha" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if payload["state"] != "pending" {
			t.Fatalf("state = %#v, want pending", payload["state"])
		}
		if payload["context"] != "mreviewer/ai-review" {
			t.Fatalf("context = %#v, want mreviewer/ai-review", payload["context"])
		}
		if payload["description"] != "AI review is running" {
			t.Fatalf("description = %#v, want running description", payload["description"])
		}
		if payload["target_url"] != "https://github.com/acme/repo/pull/17" {
			t.Fatalf("target_url = %#v", payload["target_url"])
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = client.SetCommitStatus(context.Background(), CommitStatusRequest{
		Repository:  "acme/repo",
		SHA:         "head-sha",
		State:       "pending",
		Context:     "mreviewer/ai-review",
		Description: "AI review is running",
		TargetURL:   "https://github.com/acme/repo/pull/17",
	})
	if err != nil {
		t.Fatalf("SetCommitStatus: %v", err)
	}
}
