package github

import (
	"context"
	"testing"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type fakeSnapshotReader struct {
	repositoryRef string
	pullNumber    int64
	snapshot      PullRequestSnapshot
}

func (f *fakeSnapshotReader) GetPullRequestSnapshotByRepositoryRef(_ context.Context, repositoryRef string, pullNumber int64) (PullRequestSnapshot, error) {
	f.repositoryRef = repositoryRef
	f.pullNumber = pullNumber
	return f.snapshot, nil
}

func TestAdapterFetchSnapshotPreservesPullRequestAndDiffs(t *testing.T) {
	reader := &fakeSnapshotReader{
		snapshot: PullRequestSnapshot{
			PullRequest: PullRequest{
				ID:          101,
				Number:      17,
				Title:       "Refactor parser",
				Body:        "Makes parser deterministic",
				State:       "open",
				Draft:       false,
				HTMLURL:     "https://github.com/acme/repo/pull/17",
				BaseRefName: "main",
				BaseSHA:     "base",
				HeadRefName: "feat/parser",
				HeadSHA:     "head",
				User: PullRequestUser{
					Login: "chris",
				},
			},
			Files: []PullRequestFile{{
				Filename: "internal/new.go",
				Patch:    "@@ -1 +1 @@\n-old\n+new\n",
				Status:   "modified",
			}},
		},
	}

	adapter := NewAdapter(reader)
	target := core.ReviewTarget{
		Platform:     core.PlatformGitHub,
		URL:          "https://github.com/acme/repo/pull/17",
		Repository:   "acme/repo",
		ChangeNumber: 17,
		BaseURL:      "https://github.com",
	}

	snapshot, err := adapter.FetchSnapshot(context.Background(), target)
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if reader.repositoryRef != "acme/repo" || reader.pullNumber != 17 {
		t.Fatalf("adapter called snapshot reader with (%q,%d), want (acme/repo,17)", reader.repositoryRef, reader.pullNumber)
	}
	if snapshot.Change.Title != "Refactor parser" {
		t.Fatalf("title = %q", snapshot.Change.Title)
	}
	if snapshot.Change.TargetBranch != "main" {
		t.Fatalf("target branch = %q, want main", snapshot.Change.TargetBranch)
	}
	if snapshot.Version.HeadSHA != "head" {
		t.Fatalf("head_sha = %q, want head", snapshot.Version.HeadSHA)
	}
	if len(snapshot.Diffs) != 1 {
		t.Fatalf("diffs len = %d, want 1", len(snapshot.Diffs))
	}
}
