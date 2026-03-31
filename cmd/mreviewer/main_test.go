package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type fakeEngine struct {
	input  core.ReviewInput
	opts   core.RunOptions
	bundle core.ReviewBundle
	err    error
}

func (f *fakeEngine) Run(_ context.Context, input core.ReviewInput, opts core.RunOptions) (core.ReviewBundle, error) {
	f.input = input
	f.opts = opts
	return f.bundle, f.err
}

type publishCall struct {
	target core.ReviewTarget
	bundle core.ReviewBundle
}

func TestRunWithDepsJSONOutputArtifactOnly(t *testing.T) {
	engine := &fakeEngine{
		bundle: core.ReviewBundle{
			Target: core.ReviewTarget{
				Platform:     core.PlatformGitLab,
				URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
				Repository:   "group/repo",
				ChangeNumber: 23,
				ProjectID:    77,
			},
			MarkdownSummary:   "# Review\n\nLooks good.",
			JSONSchemaVersion: "v1alpha1",
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{
		"--target", "https://gitlab.example.com/group/repo/-/merge_requests/23",
		"--output", "json",
		"--publish", "artifact-only",
		"--reviewer-packs", "security,architecture",
		"--route", "openai-gpt-5-4",
	}, runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput: func(_ context.Context, _ string, target core.ReviewTarget) (core.ReviewInput, error) {
			return core.ReviewInput{Target: target}, nil
		},
		newEngine:     func(string) reviewEngine { return engine },
		stdout:        &stdout,
		stderr:        &stderr,
	})

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}
	if engine.input.Target.Platform != core.PlatformGitLab {
		t.Fatalf("engine target platform = %q", engine.input.Target.Platform)
	}
	if engine.opts.PublishMode != string(PublishModeArtifactOnly) {
		t.Fatalf("publish mode = %q", engine.opts.PublishMode)
	}
	if len(engine.opts.ReviewerPacks) != 2 {
		t.Fatalf("reviewer packs = %v", engine.opts.ReviewerPacks)
	}
	if engine.opts.RouteOverride != "openai-gpt-5-4" {
		t.Fatalf("route override = %q", engine.opts.RouteOverride)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json output: %v", err)
	}
	if payload["markdown_summary"] != "# Review\n\nLooks good." {
		t.Fatalf("markdown_summary = %#v", payload["markdown_summary"])
	}
}

func TestRunWithDepsRejectsUnknownPublishMode(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{
		"--target", "https://gitlab.example.com/group/repo/-/merge_requests/23",
		"--publish", "invalid-mode",
	}, runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput: func(_ context.Context, _ string, target core.ReviewTarget) (core.ReviewInput, error) {
			return core.ReviewInput{Target: target}, nil
		},
		newEngine:     func(string) reviewEngine { return &fakeEngine{} },
		stdout:        &stdout,
		stderr:        &stderr,
	})
	if exitCode != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode)
	}
}

func TestRunWithDepsFailsWhenInputLoadingFails(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{
		"--target", "https://gitlab.example.com/group/repo/-/merge_requests/23",
	}, runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput: func(_ context.Context, _ string, _ core.ReviewTarget) (core.ReviewInput, error) {
			return core.ReviewInput{}, context.DeadlineExceeded
		},
		newEngine: func(string) reviewEngine { return &fakeEngine{} },
		stdout:    &stdout,
		stderr:    &stderr,
	})
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("build input failed")) {
		t.Fatalf("stderr = %q, want build input failed", stderr.String())
	}
}

func TestRunWithDepsPublishesSummaryOnlyBundle(t *testing.T) {
	engine := &fakeEngine{
		bundle: core.ReviewBundle{
			Target: core.ReviewTarget{
				Platform:     core.PlatformGitLab,
				URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
				Repository:   "group/repo",
				ChangeNumber: 23,
				ProjectID:    77,
			},
			MarkdownSummary: "judge summary",
			PublishCandidates: []core.PublishCandidate{
				{Kind: "summary", Body: "judge summary"},
				{Kind: "finding", Title: "Unsafe query", Body: "body"},
			},
		},
	}
	var published []publishCall
	exitCode := runWithDeps([]string{
		"--target", "https://gitlab.example.com/group/repo/-/merge_requests/23",
		"--publish", "summary-only",
	}, runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput: func(_ context.Context, _ string, target core.ReviewTarget) (core.ReviewInput, error) {
			return core.ReviewInput{Target: target}, nil
		},
		newEngine: func(string) reviewEngine { return engine },
		publish: func(_ context.Context, _ string, target core.ReviewTarget, bundle core.ReviewBundle) error {
			published = append(published, publishCall{target: target, bundle: bundle})
			return nil
		},
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	})
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if len(published) != 1 {
		t.Fatalf("publish calls = %d, want 1", len(published))
	}
	if len(published[0].bundle.PublishCandidates) != 1 {
		t.Fatalf("published candidates = %d, want 1", len(published[0].bundle.PublishCandidates))
	}
	if published[0].bundle.PublishCandidates[0].Kind != "summary" {
		t.Fatalf("published candidate kind = %q, want summary", published[0].bundle.PublishCandidates[0].Kind)
	}
}

func TestRunWithDepsPublishesFullReviewCommentsBundle(t *testing.T) {
	engine := &fakeEngine{
		bundle: core.ReviewBundle{
			Target: core.ReviewTarget{
				Platform:     core.PlatformGitLab,
				URL:          "https://gitlab.example.com/group/repo/-/merge_requests/23",
				Repository:   "group/repo",
				ChangeNumber: 23,
				ProjectID:    77,
			},
			MarkdownSummary: "judge summary",
			PublishCandidates: []core.PublishCandidate{
				{Kind: "summary", Body: "judge summary"},
				{Kind: "finding", Title: "Unsafe query", Body: "body"},
			},
		},
	}
	var published []publishCall
	exitCode := runWithDeps([]string{
		"--target", "https://gitlab.example.com/group/repo/-/merge_requests/23",
		"--publish", "full-review-comments",
	}, runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput: func(_ context.Context, _ string, target core.ReviewTarget) (core.ReviewInput, error) {
			return core.ReviewInput{Target: target}, nil
		},
		newEngine: func(string) reviewEngine { return engine },
		publish: func(_ context.Context, _ string, target core.ReviewTarget, bundle core.ReviewBundle) error {
			published = append(published, publishCall{target: target, bundle: bundle})
			return nil
		},
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	})
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if len(published) != 1 {
		t.Fatalf("publish calls = %d, want 1", len(published))
	}
	if len(published[0].bundle.PublishCandidates) != 2 {
		t.Fatalf("published candidates = %d, want 2", len(published[0].bundle.PublishCandidates))
	}
}

func TestResolveReviewTargetSupportsGitHubPRURL(t *testing.T) {
	target, err := resolveReviewTarget("https://github.com/acme/repo/pull/17")
	if err != nil {
		t.Fatalf("resolveReviewTarget: %v", err)
	}
	if target.Platform != core.PlatformGitHub {
		t.Fatalf("platform = %q, want %q", target.Platform, core.PlatformGitHub)
	}
	if target.Repository != "acme/repo" {
		t.Fatalf("repository = %q, want acme/repo", target.Repository)
	}
	if target.ChangeNumber != 17 {
		t.Fatalf("change number = %d, want 17", target.ChangeNumber)
	}
}
