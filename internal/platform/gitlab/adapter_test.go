package gitlab

import (
	"context"
	"testing"

	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type fakeSnapshotReader struct {
	projectRef string
	mrIID      int64
	snapshot   legacygitlab.MergeRequestSnapshot
}

func (f *fakeSnapshotReader) GetMergeRequestSnapshotByProjectRef(_ context.Context, projectRef string, mergeRequestIID int64) (legacygitlab.MergeRequestSnapshot, error) {
	f.projectRef = projectRef
	f.mrIID = mergeRequestIID
	return f.snapshot, nil
}

func TestResolveTargetParsesGitLabMergeRequestURL(t *testing.T) {
	target, err := ResolveTarget("https://gitlab.example.com/group/sub/repo/-/merge_requests/23")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}

	if target.Platform != core.PlatformGitLab {
		t.Fatalf("platform = %q, want %q", target.Platform, core.PlatformGitLab)
	}
	if target.Repository != "group/sub/repo" {
		t.Fatalf("repository = %q", target.Repository)
	}
	if target.ChangeNumber != 23 {
		t.Fatalf("change_number = %d, want 23", target.ChangeNumber)
	}
	if target.BaseURL != "https://gitlab.example.com" {
		t.Fatalf("base_url = %q", target.BaseURL)
	}
}

func TestAdapterFetchSnapshotPreservesMergeRequestAndDiffs(t *testing.T) {
	reader := &fakeSnapshotReader{
		snapshot: legacygitlab.MergeRequestSnapshot{
			MergeRequest: legacygitlab.MergeRequest{
				ProjectID:    77,
				IID:          23,
				Title:        "Refactor parser",
				SourceBranch: "feat/parser",
				TargetBranch: "main",
				WebURL:       "https://gitlab.example.com/group/repo/-/merge_requests/23",
				Author: struct {
					Username string `json:"username"`
				}{Username: "chris"},
			},
			Version: legacygitlab.MergeRequestVersion{
				GitLabVersionID: 123,
				BaseSHA:         "base",
				StartSHA:        "start",
				HeadSHA:         "head",
				PatchIDSHA:      "patch",
			},
			Diffs: []legacygitlab.MergeRequestDiff{{
				OldPath: "internal/old.go",
				NewPath: "internal/new.go",
				Diff:    "@@ -1 +1 @@\n-old\n+new\n",
			}},
		},
	}

	adapter := NewAdapter(reader)
	target := core.ReviewTarget{
		Platform:     core.PlatformGitLab,
		URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
		Repository:   "group/repo",
		ProjectID:    77,
		ChangeNumber: 23,
		BaseURL:      "https://gitlab.example.com",
	}

	snapshot, err := adapter.FetchSnapshot(context.Background(), target)
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}

	if reader.projectRef != "77" || reader.mrIID != 23 {
		t.Fatalf("adapter called snapshot reader with (%q,%d), want (77,23)", reader.projectRef, reader.mrIID)
	}
	if snapshot.Target.URL != target.URL {
		t.Fatalf("target url = %q, want %q", snapshot.Target.URL, target.URL)
	}
	if snapshot.Change.Title != "Refactor parser" {
		t.Fatalf("title = %q", snapshot.Change.Title)
	}
	if len(snapshot.Diffs) != 1 {
		t.Fatalf("diffs len = %d, want 1", len(snapshot.Diffs))
	}
	if snapshot.Version.HeadSHA != "head" {
		t.Fatalf("head_sha = %q, want head", snapshot.Version.HeadSHA)
	}
}

func TestAdapterFetchSnapshotFallsBackToRepositoryPathWhenProjectIDMissing(t *testing.T) {
	reader := &fakeSnapshotReader{
		snapshot: legacygitlab.MergeRequestSnapshot{
			MergeRequest: legacygitlab.MergeRequest{
				ProjectID: 77,
				IID:       23,
			},
		},
	}
	adapter := NewAdapter(reader)
	target := core.ReviewTarget{
		Platform:     core.PlatformGitLab,
		URL:          "https://gitlab.example.com/group/sub/repo/-/merge_requests/23",
		Repository:   "group/sub/repo",
		ChangeNumber: 23,
		BaseURL:      "https://gitlab.example.com",
	}

	if _, err := adapter.FetchSnapshot(context.Background(), target); err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if reader.projectRef != "group/sub/repo" {
		t.Fatalf("project ref = %q, want repository path", reader.projectRef)
	}
	if target.ProjectID != 0 {
		t.Fatalf("target project_id mutated unexpectedly = %d", target.ProjectID)
	}
	if reader.snapshot.MergeRequest.ProjectID != 77 {
		t.Fatalf("fixture project id = %d, want 77", reader.snapshot.MergeRequest.ProjectID)
	}
	if reader.snapshot.MergeRequest.ProjectID == 0 {
		t.Fatal("fixture project id should be non-zero")
	}
	if target.ProjectID != 0 {
		t.Fatalf("target project_id = %d, want original zero", target.ProjectID)
	}
	if snapshot, err := adapter.FetchSnapshot(context.Background(), target); err != nil {
		t.Fatalf("FetchSnapshot second call: %v", err)
	} else if snapshot.Target.ProjectID != 77 {
		t.Fatalf("snapshot target project_id = %d, want 77", snapshot.Target.ProjectID)
	}
}
