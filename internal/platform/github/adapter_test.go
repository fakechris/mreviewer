package github

import (
	"context"
	"testing"

	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type PullRequestSnapshot struct {
	Title            string
	Description      string
	SourceBranch     string
	TargetBranch     string
	BaseSHA          string
	HeadSHA          string
	RepositoryWebURL string
}

type fakePullRequestFetcher struct {
	owner    string
	repo     string
	number   int64
	snapshot PullRequestSnapshot
}

func (f *fakePullRequestFetcher) GetPullRequestSnapshot(_ context.Context, owner, repo string, number int64) (PullRequestSnapshot, error) {
	f.owner = owner
	f.repo = repo
	f.number = number
	return f.snapshot, nil
}

func TestAdapterFetchSnapshotFromGitHubTarget(t *testing.T) {
	fetcher := &fakePullRequestFetcher{
		snapshot: PullRequestSnapshot{
			Title:            "Tighten permission checks",
			Description:      "Adds repository-level policy enforcement",
			SourceBranch:     "feat/permissions",
			TargetBranch:     "main",
			BaseSHA:          "base",
			HeadSHA:          "head",
			RepositoryWebURL: "https://github.com/acme/service",
		},
	}

	adapter := NewAdapter(fetcher)
	target := reviewcore.ReviewTarget{
		Platform:   reviewcore.PlatformGitHub,
		Repository: "acme/service",
		Number:     24,
		URL:        "https://github.com/acme/service/pull/24",
	}

	snapshot, err := adapter.FetchSnapshot(context.Background(), target)
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}

	if fetcher.owner != "acme" || fetcher.repo != "service" {
		t.Fatalf("expected owner/repo acme/service, got %s/%s", fetcher.owner, fetcher.repo)
	}
	if fetcher.number != 24 {
		t.Fatalf("expected number 24, got %d", fetcher.number)
	}
	if snapshot.RepositoryWebURL != "https://github.com/acme/service" {
		t.Fatalf("expected repository url, got %q", snapshot.RepositoryWebURL)
	}
}
