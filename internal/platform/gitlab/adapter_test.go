package gitlab

import (
	"context"
	"testing"

	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type fakeSnapshotFetcher struct {
	projectRef string
	iid        int64
	snapshot   legacygitlab.MergeRequestSnapshot
}

func (f *fakeSnapshotFetcher) GetMergeRequestSnapshotByProjectRef(_ context.Context, projectRef string, iid int64) (legacygitlab.MergeRequestSnapshot, error) {
	f.projectRef = projectRef
	f.iid = iid
	return f.snapshot, nil
}

func TestAdapterFetchSnapshotByProjectRef(t *testing.T) {
	fetcher := &fakeSnapshotFetcher{
		snapshot: legacygitlab.MergeRequestSnapshot{
			MergeRequest: legacygitlab.MergeRequest{
				IID:          17,
				ProjectID:    101,
				Title:        "Harden auth flow",
				Description:  "Adds stricter tenant checks",
				SourceBranch: "feat/harden-auth",
				TargetBranch: "main",
				WebURL:       "https://gitlab.example.com/group/proj/-/merge_requests/17",
				DiffRefs: &legacygitlab.DiffRefs{
					BaseSHA:  "base",
					StartSHA: "start",
					HeadSHA:  "head",
				},
			},
			Version: legacygitlab.MergeRequestVersion{
				BaseSHA:  "base",
				StartSHA: "start",
				HeadSHA:  "head",
			},
		},
	}

	adapter := NewAdapter(fetcher)
	target := reviewcore.ReviewTarget{
		Platform:   reviewcore.PlatformGitLab,
		Repository: "group/proj",
		Number:     17,
		URL:        "https://gitlab.example.com/group/proj/-/merge_requests/17",
	}

	snapshot, err := adapter.FetchSnapshot(context.Background(), target)
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}

	if fetcher.projectRef != "group/proj" {
		t.Fatalf("expected project ref group/proj, got %q", fetcher.projectRef)
	}
	if fetcher.iid != 17 {
		t.Fatalf("expected iid 17, got %d", fetcher.iid)
	}
	if snapshot.Title != "Harden auth flow" {
		t.Fatalf("expected title from gitlab snapshot, got %q", snapshot.Title)
	}
	if snapshot.HeadSHA != "head" {
		t.Fatalf("expected head sha, got %q", snapshot.HeadSHA)
	}
}
