package reviewinput

import (
	"context"
	"encoding/json"
	"testing"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
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

type fakeAssembler struct {
	input  ctxpkg.AssembleInput
	result ctxpkg.AssemblyResult
}

func (f *fakeAssembler) Assemble(input ctxpkg.AssembleInput) (ctxpkg.AssemblyResult, error) {
	f.input = input
	return f.result, nil
}

func TestBuilderBuildsReviewInputFromSnapshot(t *testing.T) {
	loader := &fakeRulesLoader{
		result: rules.LoadResult{
			Trusted: internalTrustedRules(),
			EffectivePolicy: rules.EffectivePolicy{
				ProviderRoute: "minimax-review",
			},
			SystemPrompt: "system prompt",
		},
	}
	assembler := &fakeAssembler{
		result: ctxpkg.AssemblyResult{
			Request: ctxpkg.ReviewRequest{
				SchemaVersion: "1.0",
				ReviewRunID:   "rr_123",
			},
		},
	}

	builder := NewBuilder(loader, assembler)
	target := reviewcore.ReviewTarget{
		Platform:   reviewcore.PlatformGitLab,
		Repository: "group/proj",
		Number:     7,
		URL:        "https://gitlab.example.com/group/proj/-/merge_requests/7",
	}
	snapshot := reviewcore.PlatformSnapshot{
		HeadSHA:      "head",
		BaseSHA:      "base",
		SourceBranch: "feat/rules",
		TargetBranch: "main",
		Title:        "Improve rules handling",
		Metadata: map[string]string{
			"project_id": "101",
		},
	}

	input, err := builder.Build(context.Background(), BuildRequest{
		Target:   target,
		Snapshot: snapshot,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if loader.input.HeadSHA != "head" {
		t.Fatalf("expected rules loader to receive head sha, got %q", loader.input.HeadSHA)
	}
	if input.Target.Repository != "group/proj" {
		t.Fatalf("expected review input target repository, got %q", input.Target.Repository)
	}
	if input.ContextText == "" {
		t.Fatal("expected non-empty context text")
	}
	if input.SystemPrompt != "system prompt" {
		t.Fatalf("expected system prompt to be preserved, got %q", input.SystemPrompt)
	}
	if len(input.RequestPayload) == 0 {
		t.Fatal("expected request payload to be populated")
	}
	if input.Metadata["provider_route"] != "minimax-review" {
		t.Fatalf("expected provider route metadata, got %q", input.Metadata["provider_route"])
	}
	if len(input.Sections) == 0 {
		t.Fatal("expected sectionized review input")
	}
	if input.Sections[0].CacheKey == "" {
		t.Fatalf("expected first section to carry cache key, got %#v", input.Sections[0])
	}
	if !hasSection(input.Sections, "policy") {
		t.Fatalf("expected policy section, got %#v", input.Sections)
	}
	if !hasSection(input.Sections, "request_payload") {
		t.Fatalf("expected request_payload section, got %#v", input.Sections)
	}
}

func TestBuilderCarriesGitLabDiffsIntoRequestPayload(t *testing.T) {
	loader := &fakeRulesLoader{
		result: rules.LoadResult{
			Trusted: internalTrustedRules(),
			EffectivePolicy: rules.EffectivePolicy{
				ProviderRoute: "minimax-review",
			},
			SystemPrompt: "system prompt",
		},
	}
	assembler := &fakeAssembler{
		result: ctxpkg.AssemblyResult{
			Request: ctxpkg.ReviewRequest{
				SchemaVersion: "1.0",
				ReviewRunID:   "rr_123",
				Changes: []ctxpkg.Change{
					{Path: "repo/query.go", Status: "modified", ChangedLines: 1},
				},
			},
		},
	}

	builder := NewBuilder(loader, assembler)
	input, err := builder.Build(context.Background(), BuildRequest{
		Target: reviewcore.ReviewTarget{
			Platform:   reviewcore.PlatformGitLab,
			Repository: "group/proj",
			Number:     7,
			URL:        "https://gitlab.example.com/group/proj/-/merge_requests/7",
		},
		Snapshot: reviewcore.PlatformSnapshot{
			HeadSHA: "head",
			BaseSHA: "base",
			Metadata: map[string]string{
				"project_id": "101",
			},
			Opaque: legacygitlab.MergeRequestSnapshot{
				Diffs: []legacygitlab.MergeRequestDiff{
					{OldPath: "repo/query.go", NewPath: "repo/query.go", Diff: "@@ -1 +1 @@\n-old\n+new"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(assembler.input.Diffs) != 1 {
		t.Fatalf("expected gitlab diffs to reach assembler, got %#v", assembler.input.Diffs)
	}

	var request ctxpkg.ReviewRequest
	if err := json.Unmarshal(input.RequestPayload, &request); err != nil {
		t.Fatalf("decode request payload: %v", err)
	}
	if len(request.Changes) != 1 || request.Changes[0].Path != "repo/query.go" {
		t.Fatalf("expected request payload to include assembled changes, got %#v", request.Changes)
	}
}

func internalTrustedRules() ctxpkg.TrustedRules {
	return ctxpkg.TrustedRules{
		PlatformPolicy: "platform",
		ProjectPolicy:  "project",
		ReviewMarkdown: "review",
	}
}

func hasSection(sections []reviewcore.ReviewInputSection, id string) bool {
	for _, section := range sections {
		if section.ID == id {
			return true
		}
	}
	return false
}
