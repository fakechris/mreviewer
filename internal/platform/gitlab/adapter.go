package gitlab

import (
	"context"
	"fmt"
	"strconv"

	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type SnapshotFetcher interface {
	GetMergeRequestSnapshotByProjectRef(ctx context.Context, projectRef string, iid int64) (legacygitlab.MergeRequestSnapshot, error)
}

type Adapter struct {
	fetcher SnapshotFetcher
}

func NewAdapter(fetcher SnapshotFetcher) *Adapter {
	return &Adapter{fetcher: fetcher}
}

func (a *Adapter) FetchSnapshot(ctx context.Context, target reviewcore.ReviewTarget) (reviewcore.PlatformSnapshot, error) {
	if a == nil || a.fetcher == nil {
		return reviewcore.PlatformSnapshot{}, fmt.Errorf("gitlab adapter: snapshot fetcher is required")
	}
	if target.Repository == "" {
		return reviewcore.PlatformSnapshot{}, fmt.Errorf("gitlab adapter: target repository is required")
	}

	snapshot, err := a.fetcher.GetMergeRequestSnapshotByProjectRef(ctx, target.Repository, target.Number)
	if err != nil {
		return reviewcore.PlatformSnapshot{}, err
	}

	metadata := map[string]string{}
	if snapshot.MergeRequest.ProjectID > 0 {
		metadata["project_id"] = strconv.FormatInt(snapshot.MergeRequest.ProjectID, 10)
	}

	return reviewcore.PlatformSnapshot{
		BaseSHA:          firstNonEmpty(snapshot.Version.BaseSHA, diffRefBaseSHA(snapshot.MergeRequest.DiffRefs)),
		HeadSHA:          firstNonEmpty(snapshot.Version.HeadSHA, diffRefHeadSHA(snapshot.MergeRequest.DiffRefs)),
		StartSHA:         firstNonEmpty(snapshot.Version.StartSHA, diffRefStartSHA(snapshot.MergeRequest.DiffRefs)),
		Title:            snapshot.MergeRequest.Title,
		Description:      snapshot.MergeRequest.Description,
		SourceBranch:     snapshot.MergeRequest.SourceBranch,
		TargetBranch:     snapshot.MergeRequest.TargetBranch,
		RepositoryWebURL: snapshot.MergeRequest.WebURL,
		Metadata:         metadata,
		Opaque:           snapshot,
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func diffRefBaseSHA(refs *legacygitlab.DiffRefs) string {
	if refs == nil {
		return ""
	}
	return refs.BaseSHA
}

func diffRefHeadSHA(refs *legacygitlab.DiffRefs) string {
	if refs == nil {
		return ""
	}
	return refs.HeadSHA
}

func diffRefStartSHA(refs *legacygitlab.DiffRefs) string {
	if refs == nil {
		return ""
	}
	return refs.StartSHA
}
