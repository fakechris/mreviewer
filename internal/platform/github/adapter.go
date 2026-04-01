package github

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type SnapshotReader interface {
	GetPullRequestSnapshotByRepositoryRef(ctx context.Context, repositoryRef string, pullNumber int64) (PullRequestSnapshot, error)
}

type Adapter struct {
	reader SnapshotReader
}

func NewAdapter(reader SnapshotReader) *Adapter {
	return &Adapter{reader: reader}
}

func (a *Adapter) FetchSnapshot(ctx context.Context, target core.ReviewTarget) (core.PlatformSnapshot, error) {
	if a == nil || a.reader == nil {
		return core.PlatformSnapshot{}, fmt.Errorf("github adapter: snapshot reader is required")
	}
	if strings.TrimSpace(target.Repository) == "" {
		return core.PlatformSnapshot{}, fmt.Errorf("github adapter: repository is required")
	}
	if target.ChangeNumber <= 0 {
		return core.PlatformSnapshot{}, fmt.Errorf("github adapter: change_number is required")
	}

	snapshot, err := a.reader.GetPullRequestSnapshotByRepositoryRef(ctx, target.Repository, target.ChangeNumber)
	if err != nil {
		return core.PlatformSnapshot{}, err
	}

	return core.PlatformSnapshot{
		Target: target,
		Change: core.PlatformChange{
			PlatformID:   strconv.FormatInt(snapshot.PullRequest.ID, 10),
			Number:       snapshot.PullRequest.Number,
			Title:        snapshot.PullRequest.Title,
			Description:  snapshot.PullRequest.Body,
			State:        snapshot.PullRequest.State,
			Draft:        snapshot.PullRequest.Draft,
			SourceBranch: snapshot.PullRequest.HeadRefName,
			TargetBranch: snapshot.PullRequest.BaseRefName,
			HeadSHA:      snapshot.PullRequest.HeadSHA,
			WebURL:       snapshot.PullRequest.HTMLURL,
			Author: core.PlatformAuthor{
				UserID:   strconv.FormatInt(snapshot.PullRequest.User.ID, 10),
				Username: snapshot.PullRequest.User.Login,
			},
		},
		Version: core.PlatformVersion{
			BaseSHA: snapshot.PullRequest.BaseSHA,
			HeadSHA: snapshot.PullRequest.HeadSHA,
		},
		HeadCommit: core.PlatformCommit{
			SHA:     snapshot.HeadCommit.SHA,
			Title:   snapshot.HeadCommit.Title,
			Message: snapshot.HeadCommit.Message,
			Author: core.PlatformAuthor{
				UserID:   strconv.FormatInt(snapshot.HeadCommit.Author.ID, 10),
				Username: snapshot.HeadCommit.Author.Login,
				Name:     snapshot.HeadCommit.Author.Name,
				Email:    snapshot.HeadCommit.Author.Email,
			},
			Committer: core.PlatformAuthor{
				UserID:   strconv.FormatInt(snapshot.HeadCommit.Committer.ID, 10),
				Username: snapshot.HeadCommit.Committer.Login,
				Name:     snapshot.HeadCommit.Committer.Name,
				Email:    snapshot.HeadCommit.Committer.Email,
			},
		},
		Diffs: platformDiffs(snapshot.Files),
	}, nil
}

func platformDiffs(files []PullRequestFile) []core.PlatformDiff {
	if len(files) == 0 {
		return nil
	}
	diffs := make([]core.PlatformDiff, 0, len(files))
	for _, file := range files {
		diffs = append(diffs, core.PlatformDiff{
			OldPath:       file.PreviousFilename,
			NewPath:       file.Filename,
			Diff:          file.Patch,
			NewFile:       file.Status == "added",
			RenamedFile:   file.Status == "renamed",
			DeletedFile:   file.Status == "removed",
			GeneratedFile: file.Generated,
			TooLarge:      file.Patch == "" && !file.Removed && !file.Renamed,
		})
	}
	return diffs
}
