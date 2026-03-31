package reviewinput

import (
	"context"
	"testing"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/db"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/rules"
)

type fakeRulesLoader struct {
	input  rules.LoadInput
	result rules.LoadResult
}

func (f *fakeRulesLoader) Load(_ context.Context, input rules.LoadInput) (rules.LoadResult, error) {
	f.input = input
	return f.result, nil
}

func TestBuilderBuildsReviewInputFromPlatformSnapshot(t *testing.T) {
	loader := &fakeRulesLoader{
		result: rules.LoadResult{
			Trusted: ctxpkg.TrustedRules{
				ReviewMarkdown: "Follow REVIEW.md",
			},
			EffectivePolicy: rules.EffectivePolicy{
				ProviderRoute:  "minimax-review",
				OutputLanguage: "zh-CN",
			},
		},
	}

	builder := NewBuilder(loader, ctxpkg.NewAssembler(), nil)
	snapshot := core.PlatformSnapshot{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitLab,
			URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
			Repository:   "group/repo",
			ProjectID:    77,
			ChangeNumber: 23,
		},
		Change: core.PlatformChange{
			ProjectID:    77,
			Number:       23,
			Title:        "Refactor parser",
			Description:  "Makes parser deterministic",
			SourceBranch: "feat/parser",
			TargetBranch: "main",
			HeadSHA:      "head-sha",
			Author:       core.PlatformAuthor{Username: "chris"},
		},
		Version: core.PlatformVersion{
			BaseSHA:    "base-sha",
			StartSHA:   "start-sha",
			HeadSHA:    "head-sha",
			PatchIDSHA: "patch-sha",
		},
		Diffs: []core.PlatformDiff{{
			OldPath: "internal/old.go",
			NewPath: "internal/new.go",
			Diff:    "@@ -1 +1 @@\n-old\n+new\n",
		}},
	}

	input, err := builder.Build(context.Background(), BuildInput{
		Snapshot:             snapshot,
		ProjectDefaultBranch: "main",
		ProjectPolicy: &db.ProjectPolicy{
			ProviderRoute: "minimax-review",
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if loader.input.ProjectID != 77 {
		t.Fatalf("rules loader project_id = %d, want 77", loader.input.ProjectID)
	}
	if loader.input.RepositoryRef != "group/repo" {
		t.Fatalf("rules loader repository_ref = %q, want group/repo", loader.input.RepositoryRef)
	}
	if len(loader.input.ChangedPaths) != 1 || loader.input.ChangedPaths[0] != "internal/new.go" {
		t.Fatalf("changed paths = %v", loader.input.ChangedPaths)
	}
	if len(loader.input.InstructionConfigPaths) != 1 || loader.input.InstructionConfigPaths[0] != ".gitlab/ai-review.yaml" {
		t.Fatalf("instruction config paths = %v", loader.input.InstructionConfigPaths)
	}
	if input.Target.URL != snapshot.Target.URL {
		t.Fatalf("target url = %q, want %q", input.Target.URL, snapshot.Target.URL)
	}
	if input.Request.Project.FullPath != "group/repo" {
		t.Fatalf("project full_path = %q", input.Request.Project.FullPath)
	}
	if input.Request.MergeRequest.Title != "Refactor parser" {
		t.Fatalf("merge request title = %q", input.Request.MergeRequest.Title)
	}
	if input.EffectivePolicy.ProviderRoute != "minimax-review" {
		t.Fatalf("provider route = %q", input.EffectivePolicy.ProviderRoute)
	}
}
