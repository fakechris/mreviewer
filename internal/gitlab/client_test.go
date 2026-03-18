package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestGetMergeRequest(t *testing.T) {
	var requestPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "test-token" {
			t.Fatalf("PRIVATE-TOKEN = %q, want test-token", got)
		}

		writeJSON(t, w, http.StatusOK, map[string]any{
			"id":                    101,
			"iid":                   7,
			"project_id":            123,
			"title":                 "Add reader client",
			"description":           "Fetch MR details",
			"state":                 "opened",
			"draft":                 false,
			"source_branch":         "feature/readers",
			"target_branch":         "main",
			"sha":                   "head-sha",
			"detailed_merge_status": "mergeable",
			"has_conflicts":         false,
			"web_url":               "https://gitlab.example.com/group/project/-/merge_requests/7",
			"diff_refs": map[string]any{
				"base_sha":  "base-sha",
				"head_sha":  "head-sha",
				"start_sha": "start-sha",
			},
			"author": map[string]any{"username": "reviewer-bot"},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server)

	mr, err := client.GetMergeRequest(context.Background(), 123, 7)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}

	if requestPath != "/api/v4/projects/123/merge_requests/7" {
		t.Fatalf("request path = %q, want /api/v4/projects/123/merge_requests/7", requestPath)
	}
	if mr.GitLabID != 101 {
		t.Fatalf("GitLabID = %d, want 101", mr.GitLabID)
	}
	if mr.Title != "Add reader client" {
		t.Fatalf("Title = %q, want Add reader client", mr.Title)
	}
	if mr.HeadSHA != "head-sha" {
		t.Fatalf("HeadSHA = %q, want head-sha", mr.HeadSHA)
	}
	if mr.Author.Username != "reviewer-bot" {
		t.Fatalf("Author.Username = %q, want reviewer-bot", mr.Author.Username)
	}
	if mr.DiffRefs == nil || mr.DiffRefs.BaseSHA != "base-sha" || mr.DiffRefs.StartSHA != "start-sha" || mr.DiffRefs.HeadSHA != "head-sha" {
		t.Fatalf("DiffRefs = %+v, want populated SHAs", mr.DiffRefs)
	}
}

func TestGetMergeRequestVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/123/merge_requests/7/versions" {
			t.Fatalf("request path = %q, want /api/v4/projects/123/merge_requests/7/versions", r.URL.Path)
		}

		writeJSON(t, w, http.StatusOK, []map[string]any{
			{
				"id":               22,
				"head_commit_sha":  "new-head",
				"base_commit_sha":  "new-base",
				"start_commit_sha": "new-start",
				"patch_id_sha":     "new-patch",
				"created_at":       "2026-03-17T12:00:00Z",
				"merge_request_id": 101,
				"state":            "collected",
				"real_size":        "4",
			},
			{
				"id":               21,
				"head_commit_sha":  "old-head",
				"base_commit_sha":  "old-base",
				"start_commit_sha": "old-start",
				"patch_id_sha":     "old-patch",
				"created_at":       "2026-03-16T12:00:00Z",
				"merge_request_id": 101,
				"state":            "collected",
				"real_size":        "3",
			},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server)

	version, err := client.GetMergeRequestVersions(context.Background(), 123, 7)
	if err != nil {
		t.Fatalf("GetMergeRequestVersions: %v", err)
	}

	if version.GitLabVersionID != 22 {
		t.Fatalf("GitLabVersionID = %d, want 22", version.GitLabVersionID)
	}
	if version.BaseSHA != "new-base" || version.StartSHA != "new-start" || version.HeadSHA != "new-head" || version.PatchIDSHA != "new-patch" {
		t.Fatalf("version = %+v, want latest version SHAs", version)
	}
	if version.RealSize != "4" {
		t.Fatalf("RealSize = %q, want 4", version.RealSize)
	}
	if version.CreatedAt.UTC().Format(time.RFC3339) != "2026-03-17T12:00:00Z" {
		t.Fatalf("CreatedAt = %s, want 2026-03-17T12:00:00Z", version.CreatedAt.UTC().Format(time.RFC3339))
	}
}

func TestGetMergeRequestVersionsOutOfOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects/123/merge_requests/7/versions" {
			t.Fatalf("request path = %q, want /api/v4/projects/123/merge_requests/7/versions", r.URL.Path)
		}

		writeJSON(t, w, http.StatusOK, []map[string]any{
			{
				"id":               21,
				"head_commit_sha":  "old-head",
				"base_commit_sha":  "old-base",
				"start_commit_sha": "old-start",
				"patch_id_sha":     "old-patch",
				"created_at":       "2026-03-16T12:00:00Z",
				"merge_request_id": 101,
				"state":            "collected",
				"real_size":        "3",
			},
			{
				"id":               22,
				"head_commit_sha":  "new-head",
				"base_commit_sha":  "new-base",
				"start_commit_sha": "new-start",
				"patch_id_sha":     "new-patch",
				"created_at":       "2026-03-17T12:00:00Z",
				"merge_request_id": 101,
				"state":            "collected",
				"real_size":        "4",
			},
			{
				"id":               23,
				"head_commit_sha":  "same-time-head",
				"base_commit_sha":  "same-time-base",
				"start_commit_sha": "same-time-start",
				"patch_id_sha":     "same-time-patch",
				"created_at":       "2026-03-17T12:00:00Z",
				"merge_request_id": 101,
				"state":            "collected",
				"real_size":        "5",
			},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server)

	version, err := client.GetMergeRequestVersions(context.Background(), 123, 7)
	if err != nil {
		t.Fatalf("GetMergeRequestVersions: %v", err)
	}

	if version.GitLabVersionID != 23 {
		t.Fatalf("GitLabVersionID = %d, want 23", version.GitLabVersionID)
	}
	if version.BaseSHA != "same-time-base" || version.StartSHA != "same-time-start" || version.HeadSHA != "same-time-head" || version.PatchIDSHA != "same-time-patch" {
		t.Fatalf("version = %+v, want newest version SHAs from highest ID tie-breaker", version)
	}
	if version.RealSize != "5" {
		t.Fatalf("RealSize = %q, want 5", version.RealSize)
	}
}

func TestGetMergeRequestDiffsPagination(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		page := r.URL.Query().Get("page")
		if r.URL.Path != "/api/v4/projects/123/merge_requests/7/diffs" {
			t.Fatalf("request path = %q, want /api/v4/projects/123/merge_requests/7/diffs", r.URL.Path)
		}
		if r.URL.Query().Get("per_page") != "100" {
			t.Fatalf("per_page = %q, want 100", r.URL.Query().Get("per_page"))
		}

		switch page {
		case "1":
			w.Header().Set("X-Next-Page", "2")
			writeJSON(t, w, http.StatusOK, []map[string]any{
				{"old_path": "a.go", "new_path": "a.go", "diff": "@@ -1 +1 @@"},
				{"old_path": "b.go", "new_path": "b.go", "diff": "@@ -1 +1 @@"},
			})
		case "2":
			writeJSON(t, w, http.StatusOK, []map[string]any{
				{"old_path": "c.go", "new_path": "c.go", "diff": "@@ -1 +1 @@", "generated_file": true},
			})
		default:
			t.Fatalf("unexpected page %q", page)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server)

	diffs, err := client.GetMergeRequestDiffs(context.Background(), 123, 7)
	if err != nil {
		t.Fatalf("GetMergeRequestDiffs: %v", err)
	}

	if len(diffs) != 3 {
		t.Fatalf("len(diffs) = %d, want 3", len(diffs))
	}
	wantPaths := []string{
		"/api/v4/projects/123/merge_requests/7/diffs?page=1&per_page=100",
		"/api/v4/projects/123/merge_requests/7/diffs?page=2&per_page=100",
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", paths, wantPaths)
	}
	if !diffs[2].GeneratedFile {
		t.Fatalf("diffs[2].GeneratedFile = false, want true")
	}
}

func TestDiffNotReadyRetry(t *testing.T) {
	var mu sync.Mutex
	mrCalls := 0
	versionCalls := 0
	diffCalls := 0
	recorder := &sleepRecorder{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.URL.Path {
		case "/api/v4/projects/123/merge_requests/7":
			mrCalls++
			response := map[string]any{
				"id":            101,
				"iid":           7,
				"project_id":    123,
				"title":         "Add reader client",
				"state":         "opened",
				"draft":         false,
				"source_branch": "feature/readers",
				"target_branch": "main",
				"sha":           "head-sha",
				"web_url":       "https://gitlab.example.com/group/project/-/merge_requests/7",
				"author":        map[string]any{"username": "reviewer-bot"},
			}
			if mrCalls > 1 {
				response["diff_refs"] = map[string]any{
					"base_sha":  "base-sha",
					"head_sha":  "head-sha",
					"start_sha": "start-sha",
				}
			}
			writeJSON(t, w, http.StatusOK, response)
		case "/api/v4/projects/123/merge_requests/7/versions":
			versionCalls++
			if versionCalls == 1 {
				writeJSON(t, w, http.StatusOK, []map[string]any{})
				return
			}
			writeJSON(t, w, http.StatusOK, []map[string]any{{
				"id":               22,
				"head_commit_sha":  "head-sha",
				"base_commit_sha":  "base-sha",
				"start_commit_sha": "start-sha",
				"patch_id_sha":     "patch-sha",
				"created_at":       "2026-03-17T12:00:00Z",
			}})
		case "/api/v4/projects/123/merge_requests/7/diffs":
			diffCalls++
			writeJSON(t, w, http.StatusOK, []map[string]any{{
				"old_path": "a.go",
				"new_path": "a.go",
				"diff":     "@@ -1 +1 @@",
			}})
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(
		server.URL,
		"test-token",
		WithHTTPClient(server.Client()),
		WithSleep(recorder.Sleep),
		WithDiffNotReadyMaxRetries(1),
		WithDiffNotReadyBackoff(func(int) time.Duration { return 25 * time.Millisecond }),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	snapshot, err := client.GetMergeRequestSnapshot(context.Background(), 123, 7)
	if err != nil {
		t.Fatalf("GetMergeRequestSnapshot: %v", err)
	}

	if mrCalls != 2 {
		t.Fatalf("mrCalls = %d, want 2", mrCalls)
	}
	if versionCalls != 2 {
		t.Fatalf("versionCalls = %d, want 2", versionCalls)
	}
	if diffCalls != 1 {
		t.Fatalf("diffCalls = %d, want 1", diffCalls)
	}
	wantSleeps := []time.Duration{25 * time.Millisecond}
	if !reflect.DeepEqual(recorder.delays(), wantSleeps) {
		t.Fatalf("sleep delays = %#v, want %#v", recorder.delays(), wantSleeps)
	}
	if len(snapshot.Diffs) != 1 {
		t.Fatalf("len(snapshot.Diffs) = %d, want 1", len(snapshot.Diffs))
	}
	if snapshot.Version.PatchIDSHA != "patch-sha" {
		t.Fatalf("PatchIDSHA = %q, want patch-sha", snapshot.Version.PatchIDSHA)
	}
}

func TestRateLimitRetry(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0
	recorder := &sleepRecorder{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		requestCount++
		if r.URL.Path != "/api/v4/projects/123/merge_requests/7" {
			t.Fatalf("request path = %q, want /api/v4/projects/123/merge_requests/7", r.URL.Path)
		}

		switch requestCount {
		case 1:
			w.Header().Set("Retry-After", "3")
			writeJSON(t, w, http.StatusTooManyRequests, map[string]any{"message": "rate limit"})
		case 2:
			writeJSON(t, w, http.StatusTooManyRequests, map[string]any{"message": "still limited"})
		case 3:
			writeJSON(t, w, http.StatusOK, map[string]any{
				"id":            101,
				"iid":           7,
				"project_id":    123,
				"title":         "Add reader client",
				"state":         "opened",
				"draft":         false,
				"source_branch": "feature/readers",
				"target_branch": "main",
				"sha":           "head-sha",
				"web_url":       "https://gitlab.example.com/group/project/-/merge_requests/7",
				"diff_refs": map[string]any{
					"base_sha":  "base-sha",
					"head_sha":  "head-sha",
					"start_sha": "start-sha",
				},
				"author": map[string]any{"username": "reviewer-bot"},
			})
		default:
			t.Fatalf("unexpected request count %d", requestCount)
		}
	}))
	defer server.Close()

	client, err := NewClient(
		server.URL,
		"test-token",
		WithHTTPClient(server.Client()),
		WithSleep(recorder.Sleep),
		WithRateLimitMaxRetries(2),
		WithRateLimitBackoff(func(attempt int) time.Duration {
			return time.Duration(attempt+1) * time.Second
		}),
		WithRateLimitJitter(func(time.Duration) time.Duration { return 500 * time.Millisecond }),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	mr, err := client.GetMergeRequest(context.Background(), 123, 7)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}

	if requestCount != 3 {
		t.Fatalf("requestCount = %d, want 3", requestCount)
	}
	wantSleeps := []time.Duration{3 * time.Second, 2500 * time.Millisecond}
	if !reflect.DeepEqual(recorder.delays(), wantSleeps) {
		t.Fatalf("sleep delays = %#v, want %#v", recorder.delays(), wantSleeps)
	}
	if mr.Title != "Add reader client" {
		t.Fatalf("Title = %q, want Add reader client", mr.Title)
	}
}

func TestGitLabRateLimiting(t *testing.T) {
	var slept []time.Duration
	current := time.Unix(0, 0)
	limiter := NewInMemoryRateLimiter(RateLimitConfig{Requests: 1, Window: time.Second}, func() time.Time { return current }, func(ctx context.Context, delay time.Duration) error {
		slept = append(slept, delay)
		current = current.Add(delay)
		return nil
	})
	limiter.SetLimit("123", RateLimitConfig{Requests: 1, Window: time.Second})

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		writeJSON(t, w, http.StatusOK, map[string]any{
			"id":            101,
			"iid":           7,
			"project_id":    123,
			"title":         "Add reader client",
			"state":         "opened",
			"draft":         false,
			"source_branch": "feature/readers",
			"target_branch": "main",
			"sha":           "head-sha",
			"web_url":       "https://gitlab.example.com/group/project/-/merge_requests/7",
			"diff_refs": map[string]any{
				"base_sha":  "base-sha",
				"head_sha":  "head-sha",
				"start_sha": "start-sha",
			},
			"author": map[string]any{"username": "reviewer-bot"},
		})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "test-token", WithHTTPClient(server.Client()), WithRateLimiter(limiter))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.GetMergeRequest(context.Background(), 123, 7); err != nil {
		t.Fatalf("first GetMergeRequest: %v", err)
	}
	if _, err := client.GetMergeRequest(context.Background(), 123, 7); err != nil {
		t.Fatalf("second GetMergeRequest: %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("requestCount = %d, want 2", requestCount)
	}
	if len(slept) != 1 || slept[0] != time.Second {
		t.Fatalf("sleep durations = %#v, want [1s]", slept)
	}
}

func newTestClient(t *testing.T, server *httptest.Server, opts ...Option) *Client {
	t.Helper()
	allOpts := append([]Option{WithHTTPClient(server.Client())}, opts...)
	client, err := NewClient(server.URL, "test-token", allOpts...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("Encode: %v", err)
	}
}

type sleepRecorder struct {
	mu         sync.Mutex
	delaysSeen []time.Duration
}

func (r *sleepRecorder) Sleep(_ context.Context, delay time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.delaysSeen = append(r.delaysSeen, delay)
	return nil
}

func (r *sleepRecorder) delays() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	copyOfDelays := make([]time.Duration, len(r.delaysSeen))
	copy(copyOfDelays, r.delaysSeen)
	return copyOfDelays
}
