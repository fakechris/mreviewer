package reviewinput

import (
	"context"
	"fmt"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/db"
	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/rules"
)

type RulesLoader interface {
	Load(ctx context.Context, input rules.LoadInput) (rules.LoadResult, error)
}

type BuildInput struct {
	Snapshot             core.PlatformSnapshot
	ProjectDefaultBranch string
	ProjectPolicy        *db.ProjectPolicy
	MergeRequestID       int64
}

type Builder struct {
	rulesLoader RulesLoader
	assembler   *ctxpkg.Assembler
	store       ctxpkg.HistoricalStore
}

func NewBuilder(rulesLoader RulesLoader, assembler *ctxpkg.Assembler, store ctxpkg.HistoricalStore) *Builder {
	if assembler == nil {
		assembler = ctxpkg.NewAssembler()
	}
	return &Builder{
		rulesLoader: rulesLoader,
		assembler:   assembler,
		store:       store,
	}
}

func (b *Builder) Build(ctx context.Context, input BuildInput) (core.ReviewInput, error) {
	if b == nil || b.rulesLoader == nil || b.assembler == nil {
		return core.ReviewInput{}, fmt.Errorf("reviewinput: builder dependencies are not configured")
	}

	changedPaths := make([]string, 0, len(input.Snapshot.Diffs))
	for _, diff := range input.Snapshot.Diffs {
		if diff.NewPath != "" {
			changedPaths = append(changedPaths, diff.NewPath)
			continue
		}
		if diff.OldPath != "" {
			changedPaths = append(changedPaths, diff.OldPath)
		}
	}

	loadResult, err := b.rulesLoader.Load(ctx, rules.LoadInput{
		ProjectID:              input.Snapshot.Change.ProjectID,
		RepositoryRef:          input.Snapshot.Target.Repository,
		HeadSHA:                input.Snapshot.Version.HeadSHA,
		ProjectPolicy:          input.ProjectPolicy,
		ChangedPaths:           changedPaths,
		InstructionConfigPaths: instructionConfigPaths(input.Snapshot.Target.Platform),
	})
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("reviewinput: load rules: %w", err)
	}

	settings, err := ctxpkg.SettingsFromPolicy(input.ProjectPolicy)
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("reviewinput: policy settings: %w", err)
	}

	historical, err := ctxpkg.LoadHistoricalContext(ctx, b.store, input.MergeRequestID)
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("reviewinput: historical context: %w", err)
	}

	assembled, err := b.assembler.Assemble(ctxpkg.AssembleInput{
		ReviewRunID: 0,
		Project: ctxpkg.ProjectContext{
			ProjectID:     input.Snapshot.Change.ProjectID,
			FullPath:      input.Snapshot.Target.Repository,
			DefaultBranch: input.ProjectDefaultBranch,
		},
		MergeRequest: ctxpkg.MergeRequestContext{
			IID:         input.Snapshot.Change.Number,
			Title:       input.Snapshot.Change.Title,
			Description: input.Snapshot.Change.Description,
			Author:      input.Snapshot.Change.Author.Username,
		},
		Version: ctxpkg.VersionContext{
			BaseSHA:    input.Snapshot.Version.BaseSHA,
			StartSHA:   input.Snapshot.Version.StartSHA,
			HeadSHA:    input.Snapshot.Version.HeadSHA,
			PatchIDSHA: input.Snapshot.Version.PatchIDSHA,
		},
		Rules:             loadResult.Trusted,
		Settings:          settings,
		Diffs:             legacyDiffsFromSnapshot(input.Snapshot.Diffs),
		HistoricalContext: historical,
	})
	if err != nil {
		return core.ReviewInput{}, fmt.Errorf("reviewinput: assemble request: %w", err)
	}

	return core.ReviewInput{
		Target:          input.Snapshot.Target,
		Request:         assembled.Request,
		EffectivePolicy: loadResult.EffectivePolicy,
		Warnings:        loadResult.Warnings,
	}, nil
}

func legacyDiffsFromSnapshot(diffs []core.PlatformDiff) []legacygitlab.MergeRequestDiff {
	if len(diffs) == 0 {
		return nil
	}
	legacyDiffs := make([]legacygitlab.MergeRequestDiff, 0, len(diffs))
	for _, diff := range diffs {
		legacyDiffs = append(legacyDiffs, legacygitlab.MergeRequestDiff{
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
	return legacyDiffs
}

func instructionConfigPaths(platform core.Platform) []string {
	switch platform {
	case core.PlatformGitHub:
		return []string{".github/ai-review.yaml"}
	default:
		return []string{".gitlab/ai-review.yaml"}
	}
}
