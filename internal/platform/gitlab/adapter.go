package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type SnapshotReader interface {
	GetMergeRequestSnapshotByProjectRef(ctx context.Context, projectRef string, mergeRequestIID int64) (legacygitlab.MergeRequestSnapshot, error)
}

type Adapter struct {
	reader SnapshotReader
}

func NewAdapter(reader SnapshotReader) *Adapter {
	return &Adapter{reader: reader}
}

func ResolveTarget(rawURL string) (core.ReviewTarget, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return core.ReviewTarget{}, fmt.Errorf("gitlab adapter: parse url: %w", err)
	}

	parts := strings.SplitN(strings.Trim(parsed.Path, "/"), "/-/merge_requests/", 2)
	if len(parts) != 2 {
		return core.ReviewTarget{}, fmt.Errorf("gitlab adapter: invalid merge request url: %s", rawURL)
	}

	changeNumber, err := strconv.ParseInt(strings.Trim(parts[1], "/"), 10, 64)
	if err != nil {
		return core.ReviewTarget{}, fmt.Errorf("gitlab adapter: parse merge request iid: %w", err)
	}

	return core.ReviewTarget{
		Platform:     core.PlatformGitLab,
		URL:          strings.TrimSpace(rawURL),
		BaseURL:      strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/"),
		Repository:   strings.Trim(parts[0], "/"),
		ChangeNumber: changeNumber,
	}, nil
}

func (a *Adapter) FetchSnapshot(ctx context.Context, target core.ReviewTarget) (core.PlatformSnapshot, error) {
	if a == nil || a.reader == nil {
		return core.PlatformSnapshot{}, fmt.Errorf("gitlab adapter: snapshot reader is required")
	}
	if target.ChangeNumber <= 0 {
		return core.PlatformSnapshot{}, fmt.Errorf("gitlab adapter: change_number is required")
	}
	projectRef := strings.TrimSpace(target.Repository)
	if target.ProjectID > 0 {
		projectRef = strconv.FormatInt(target.ProjectID, 10)
	}
	if projectRef == "" {
		return core.PlatformSnapshot{}, fmt.Errorf("gitlab adapter: project reference is required")
	}

	snapshot, err := a.reader.GetMergeRequestSnapshotByProjectRef(ctx, projectRef, target.ChangeNumber)
	if err != nil {
		return core.PlatformSnapshot{}, err
	}
	if target.ProjectID <= 0 && snapshot.MergeRequest.ProjectID > 0 {
		target.ProjectID = snapshot.MergeRequest.ProjectID
	}

	changeMetadata, err := json.Marshal(struct {
		DiffRefs *legacygitlab.DiffRefs `json:"diff_refs,omitempty"`
	}{
		DiffRefs: snapshot.MergeRequest.DiffRefs,
	})
	if err != nil {
		return core.PlatformSnapshot{}, fmt.Errorf("gitlab adapter: marshal change metadata: %w", err)
	}

	return core.PlatformSnapshot{
		Target: target,
		Change: core.PlatformChange{
			PlatformID:   strconv.FormatInt(snapshot.MergeRequest.GitLabID, 10),
			ProjectID:    snapshot.MergeRequest.ProjectID,
			Number:       snapshot.MergeRequest.IID,
			Title:        snapshot.MergeRequest.Title,
			Description:  snapshot.MergeRequest.Description,
			State:        snapshot.MergeRequest.State,
			Draft:        snapshot.MergeRequest.Draft,
			SourceBranch: snapshot.MergeRequest.SourceBranch,
			TargetBranch: snapshot.MergeRequest.TargetBranch,
			HeadSHA:      snapshot.MergeRequest.HeadSHA,
			WebURL:       snapshot.MergeRequest.WebURL,
			Author: core.PlatformAuthor{
				Username: snapshot.MergeRequest.Author.Username,
			},
			BaseMetadata: changeMetadata,
		},
		Version: core.PlatformVersion{
			PlatformVersionID: strconv.FormatInt(snapshot.Version.GitLabVersionID, 10),
			BaseSHA:           snapshot.Version.BaseSHA,
			StartSHA:          snapshot.Version.StartSHA,
			HeadSHA:           snapshot.Version.HeadSHA,
			PatchIDSHA:        snapshot.Version.PatchIDSHA,
		},
		HeadCommit: core.PlatformCommit{
			SHA:     snapshot.HeadCommit.SHA,
			Title:   snapshot.HeadCommit.Title,
			Message: snapshot.HeadCommit.Message,
			Author: core.PlatformAuthor{
				Name:  snapshot.HeadCommit.Author.Name,
				Email: snapshot.HeadCommit.Author.Email,
			},
			Committer: core.PlatformAuthor{
				Name:  snapshot.HeadCommit.Committer.Name,
				Email: snapshot.HeadCommit.Committer.Email,
			},
		},
		Diffs: platformDiffs(snapshot.Diffs),
	}, nil
}

func platformDiffs(diffs []legacygitlab.MergeRequestDiff) []core.PlatformDiff {
	if len(diffs) == 0 {
		return nil
	}
	result := make([]core.PlatformDiff, 0, len(diffs))
	for _, diff := range diffs {
		result = append(result, core.PlatformDiff{
			OldPath:       diff.OldPath,
			NewPath:       diff.NewPath,
			Diff:          diff.Diff,
			AMode:         diff.AMode,
			BMode:         diff.BMode,
			NewFile:       diff.NewFile,
			RenamedFile:   diff.RenamedFile,
			DeletedFile:   diff.DeletedFile,
			GeneratedFile: diff.GeneratedFile,
			Collapsed:     diff.Collapsed,
			TooLarge:      diff.TooLarge,
		})
	}
	return result
}
