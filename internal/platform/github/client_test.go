package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetPullRequestSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.RequestURI() {
		case "/repos/acme/service/pulls/24":
			writeJSON(t, w, http.StatusOK, map[string]any{
				"title":    "Tighten permission checks",
				"body":     "Adds repository-level policy enforcement",
				"html_url": "https://github.com/acme/service/pull/24",
				"head": map[string]any{
					"ref": "feat/permissions",
					"sha": "head-sha",
				},
				"base": map[string]any{
					"ref": "main",
					"sha": "base-sha",
					"repo": map[string]any{
						"html_url": "https://github.com/acme/service",
					},
				},
			})
		case "/repos/acme/service/pulls/24/files?page=1&per_page=100":
			writeJSON(t, w, http.StatusOK, []map[string]any{
				{"filename": "repo/query.go", "status": "modified", "patch": "@@ -1 +1 @@\n-old\n+new"},
			})
		default:
			t.Fatalf("unexpected request uri %q", r.URL.RequestURI())
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	snapshot, err := client.GetPullRequestSnapshot(context.Background(), "acme", "service", 24)
	if err != nil {
		t.Fatalf("GetPullRequestSnapshot: %v", err)
	}

	if snapshot.Title != "Tighten permission checks" {
		t.Fatalf("title = %q", snapshot.Title)
	}
	if len(snapshot.Diffs) != 1 || snapshot.Diffs[0].NewPath != "repo/query.go" {
		t.Fatalf("diffs = %#v", snapshot.Diffs)
	}
}

func TestGetRepositoryFileByRef(t *testing.T) {
	body := "# Review Guidelines\n- Focus on auth boundaries\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/repos/acme/service/contents/REVIEW.md?ref=head-sha" {
			t.Fatalf("unexpected request uri %q", r.URL.RequestURI())
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"encoding": "base64",
			"content":  base64.StdEncoding.EncodeToString([]byte(body)),
		})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	got, err := client.GetRepositoryFileByRef(context.Background(), "acme/service", "REVIEW.md", "head-sha")
	if err != nil {
		t.Fatalf("GetRepositoryFileByRef: %v", err)
	}
	if got != body {
		t.Fatalf("body = %q, want %q", got, body)
	}
}

func TestSetCommitStatus(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.RequestURI() != "/repos/acme/service/statuses/head-sha" {
			t.Fatalf("unexpected request uri %q", r.URL.RequestURI())
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeJSON(t, w, http.StatusCreated, map[string]any{"state": "pending"})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	err = client.SetCommitStatus(context.Background(), CommitStatusRequest{
		Owner:       "acme",
		Repo:        "service",
		SHA:         "head-sha",
		State:       "pending",
		Context:     "mreviewer/ai-review",
		Description: "AI review is running",
		TargetURL:   "https://github.com/acme/service/pull/24",
	})
	if err != nil {
		t.Fatalf("SetCommitStatus: %v", err)
	}

	if requestBody["state"] != "pending" {
		t.Fatalf("state = %#v, want pending", requestBody["state"])
	}
	if requestBody["context"] != "mreviewer/ai-review" {
		t.Fatalf("context = %#v, want mreviewer/ai-review", requestBody["context"])
	}
	if requestBody["description"] != "AI review is running" {
		t.Fatalf("description = %#v, want AI review is running", requestBody["description"])
	}
	if requestBody["target_url"] != "https://github.com/acme/service/pull/24" {
		t.Fatalf("target_url = %#v, want PR URL", requestBody["target_url"])
	}
}

func TestListPullRequestComments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.RequestURI() {
		case "/repos/acme/service/issues/24/comments?page=1&per_page=100":
			writeJSON(t, w, http.StatusOK, []map[string]any{
				{"id": 101, "body": "summary", "html_url": "https://github.com/acme/service/pull/24#issuecomment-101", "user": map[string]any{"login": "coderabbit"}},
			})
		case "/repos/acme/service/pulls/24/comments?page=1&per_page=100":
			writeJSON(t, w, http.StatusOK, []map[string]any{
				{"id": 202, "body": "inline", "html_url": "https://github.com/acme/service/pull/24#discussion_r202", "path": "auth/check.go", "line": 41, "side": "RIGHT", "user": map[string]any{"login": "gemini"}},
			})
		default:
			t.Fatalf("unexpected request uri %q", r.URL.RequestURI())
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	issueComments, err := client.ListIssueComments(context.Background(), "acme", "service", 24)
	if err != nil {
		t.Fatalf("ListIssueComments: %v", err)
	}
	if len(issueComments) != 1 || issueComments[0].User.Login != "coderabbit" {
		t.Fatalf("issue comments = %#v", issueComments)
	}

	reviewComments, err := client.ListReviewComments(context.Background(), "acme", "service", 24)
	if err != nil {
		t.Fatalf("ListReviewComments: %v", err)
	}
	if len(reviewComments) != 1 || reviewComments[0].Path != "auth/check.go" {
		t.Fatalf("review comments = %#v", reviewComments)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}
