package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	comparepkg "github.com/mreviewer/mreviewer/internal/compare"
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

type reviewEngineFunc func(context.Context, core.ReviewInput, core.RunOptions) (core.ReviewBundle, error)

func (f reviewEngineFunc) Run(ctx context.Context, input core.ReviewInput, opts core.RunOptions) (core.ReviewBundle, error) {
	return f(ctx, input, opts)
}

type publishCall struct {
	target core.ReviewTarget
	bundle core.ReviewBundle
}

type statusCall struct {
	target          core.ReviewTarget
	input           core.ReviewInput
	state           string
	blockingFindings int
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

func TestRunWithDepsJSONOutputIncludesComparisonReport(t *testing.T) {
	engine := &fakeEngine{
		bundle: core.ReviewBundle{
			Target: core.ReviewTarget{
				Platform:     core.PlatformGitHub,
				URL:          "https://github.com/acme/repo/pull/17",
				Repository:   "acme/repo",
				ChangeNumber: 17,
			},
			MarkdownSummary:   "# Review\n\nLooks risky.",
			JSONSchemaVersion: "v1alpha1",
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{
		"--target", "https://github.com/acme/repo/pull/17",
		"--output", "json",
		"--compare-live", "codex,coderabbit",
	}, runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput: func(_ context.Context, _ string, target core.ReviewTarget) (core.ReviewInput, error) {
			return core.ReviewInput{Target: target}, nil
		},
		newEngine: func(string) reviewEngine { return engine },
		compare: func(_ context.Context, _ string, target core.ReviewTarget, bundle core.ReviewBundle, opts cliOptions) (*comparepkg.Report, error) {
			if target.Platform != core.PlatformGitHub {
				t.Fatalf("compare target platform = %q, want github", target.Platform)
			}
			if len(opts.compareLiveReviewers) != 2 {
				t.Fatalf("compare live reviewers = %#v, want 2", opts.compareLiveReviewers)
			}
			report := comparepkg.Report{
				Target:             target,
				ReviewerCount:      3,
				UniqueFindingCount: 4,
				AgreementRate:      0.5,
			}
			return &report, nil
		},
		stdout: &stdout,
		stderr: &stderr,
	})
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json output: %v", err)
	}
	if payload["comparison"] == nil {
		t.Fatalf("comparison payload missing: %s", stdout.String())
	}
}

func TestRunWithDepsMarkdownOutputIncludesComparisonSection(t *testing.T) {
	engine := &fakeEngine{
		bundle: core.ReviewBundle{
			Target: core.ReviewTarget{
				Platform:     core.PlatformGitHub,
				URL:          "https://github.com/acme/repo/pull/17",
				Repository:   "acme/repo",
				ChangeNumber: 17,
			},
			MarkdownSummary: "# Review\n\nLooks risky.",
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{
		"--target", "https://github.com/acme/repo/pull/17",
		"--output", "markdown",
		"--compare-artifacts", "artifacts/codex.json",
	}, runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput: func(_ context.Context, _ string, target core.ReviewTarget) (core.ReviewInput, error) {
			return core.ReviewInput{Target: target}, nil
		},
		newEngine: func(string) reviewEngine { return engine },
		compare: func(_ context.Context, _ string, target core.ReviewTarget, bundle core.ReviewBundle, opts cliOptions) (*comparepkg.Report, error) {
			if len(opts.compareArtifactPaths) != 1 {
				t.Fatalf("compare artifact paths = %#v, want 1", opts.compareArtifactPaths)
			}
			report := comparepkg.Report{
				Target:             target,
				ReviewerCount:      2,
				UniqueFindingCount: 3,
				AgreementRate:      1.0 / 3.0,
			}
			return &report, nil
		},
		stdout: &stdout,
		stderr: &stderr,
	})
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("## Comparison")) {
		t.Fatalf("markdown output missing comparison section: %s", stdout.String())
	}
}

func TestParseOptionsSupportsComparisonFlags(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseOptions([]string{
		"--target", "https://github.com/acme/repo/pull/17",
		"--compare-live", "codex,coderabbit",
		"--compare-artifacts", "artifacts/codex.json,artifacts/coderabbit.json",
	}, &stderr)
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if len(opts.compareLiveReviewers) != 2 {
		t.Fatalf("compare live reviewers = %#v, want 2", opts.compareLiveReviewers)
	}
	if len(opts.compareArtifactPaths) != 2 {
		t.Fatalf("compare artifact paths = %#v, want 2", opts.compareArtifactPaths)
	}
}

func TestParseOptionsSupportsMultipleTargets(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseOptions([]string{
		"--targets", "https://github.com/acme/repo/pull/17,https://gitlab.example.com/group/repo/-/merge_requests/23",
		"--compare-live", "codex",
	}, &stderr)
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if len(opts.targets) != 2 {
		t.Fatalf("targets = %#v, want 2", opts.targets)
	}
	if opts.targets[0] != "https://github.com/acme/repo/pull/17" {
		t.Fatalf("first target = %q", opts.targets[0])
	}
}

func TestParseOptionsSupportsAdvisorAndExitMode(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseOptions([]string{
		"--target", "https://github.com/acme/repo/pull/17",
		"--advisor-route", "openai-gpt-5-4",
		"--exit-mode", "requested_changes",
	}, &stderr)
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.advisorRoute != "openai-gpt-5-4" {
		t.Fatalf("advisor route = %q, want openai-gpt-5-4", opts.advisorRoute)
	}
	if opts.exitMode != "requested_changes" {
		t.Fatalf("exit mode = %q, want requested_changes", opts.exitMode)
	}
}

func TestRunWithDepsJSONOutputSupportsMultiTargetCompare(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{
		"--targets", "https://github.com/acme/repo/pull/17,https://gitlab.example.com/group/repo/-/merge_requests/23",
		"--output", "json",
		"--compare-live", "codex",
	}, runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput: func(_ context.Context, _ string, target core.ReviewTarget) (core.ReviewInput, error) {
			return core.ReviewInput{Target: target}, nil
		},
		newEngine: func(string) reviewEngine {
			return reviewEngineFunc(func(_ context.Context, input core.ReviewInput, _ core.RunOptions) (core.ReviewBundle, error) {
				return core.ReviewBundle{
					Target:          input.Target,
					MarkdownSummary: "# Review",
				}, nil
			})
		},
		compare: func(_ context.Context, _ string, target core.ReviewTarget, _ core.ReviewBundle, _ cliOptions) (*comparepkg.Report, error) {
			if target.Platform == core.PlatformGitHub {
				report := comparepkg.Report{
					Target:             target,
					ReviewerCount:      2,
					UniqueFindingCount: 2,
					AgreementRate:      0.5,
				}
				return &report, nil
			}
			report := comparepkg.Report{
				Target:             target,
				ReviewerCount:      3,
				UniqueFindingCount: 4,
				AgreementRate:      1.0,
			}
			return &report, nil
		},
		stdout: &stdout,
		stderr: &stderr,
	})
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json output: %v", err)
	}
	aggregate, ok := payload["aggregate_comparison"].(map[string]any)
	if !ok {
		t.Fatalf("aggregate_comparison missing: %s", stdout.String())
	}
	if aggregate["target_count"] != float64(2) {
		t.Fatalf("target_count = %#v, want 2", aggregate["target_count"])
	}
	if aggregate["total_reviewer_count"] != float64(5) {
		t.Fatalf("total_reviewer_count = %#v, want 5", aggregate["total_reviewer_count"])
	}
}

func TestRunWithDepsPublishesGitHubStatusTransitions(t *testing.T) {
	engine := &fakeEngine{
		bundle: core.ReviewBundle{
			Target: core.ReviewTarget{
				Platform:     core.PlatformGitHub,
				URL:          "https://github.com/acme/repo/pull/17",
				Repository:   "acme/repo",
				ChangeNumber: 17,
			},
		},
	}
	input := core.ReviewInput{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitHub,
			URL:          "https://github.com/acme/repo/pull/17",
			Repository:   "acme/repo",
			ChangeNumber: 17,
		},
		Request: core.ReviewInput{}.Request,
	}
	input.Request.Project.FullPath = "acme/repo"
	input.Request.Version.HeadSHA = "head-sha"

	var calls []statusCall
	exitCode := runWithDeps([]string{
		"--target", "https://github.com/acme/repo/pull/17",
	}, runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput: func(_ context.Context, _ string, _ core.ReviewTarget) (core.ReviewInput, error) {
			return input, nil
		},
		newEngine: func(string) reviewEngine { return engine },
		status: func(_ context.Context, _ string, target core.ReviewTarget, in core.ReviewInput, state string, blockingFindings int) error {
			calls = append(calls, statusCall{target: target, input: in, state: state, blockingFindings: blockingFindings})
			return nil
		},
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	})
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if len(calls) != 2 {
		t.Fatalf("status calls = %d, want 2", len(calls))
	}
	if calls[0].state != "running" {
		t.Fatalf("first status state = %q, want running", calls[0].state)
	}
	if calls[1].state != "success" {
		t.Fatalf("second status state = %q, want success", calls[1].state)
	}
}

func TestRunWithDepsJSONOutputIncludesAdvisorAndDecisionBenchmark(t *testing.T) {
	engine := &fakeEngine{
		bundle: core.ReviewBundle{
			Target: core.ReviewTarget{
				Platform:     core.PlatformGitHub,
				URL:          "https://github.com/acme/repo/pull/17",
				Repository:   "acme/repo",
				ChangeNumber: 17,
			},
			Verdict:         "requested_changes",
			MarkdownSummary: "judge summary",
			Artifacts: []core.ReviewerArtifact{
				{ReviewerID: "council:security", ReviewerKind: "pack"},
			},
			AdvisorArtifact: &core.ReviewerArtifact{
				ReviewerID:   "advisor:openai-gpt-5-4",
				ReviewerKind: "advisor",
				Summary:      "advisor summary",
			},
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{
		"--target", "https://github.com/acme/repo/pull/17",
		"--output", "json",
		"--advisor-route", "openai-gpt-5-4",
		"--exit-mode", "requested_changes",
	}, runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput: func(_ context.Context, _ string, target core.ReviewTarget) (core.ReviewInput, error) {
			return core.ReviewInput{Target: target}, nil
		},
		newEngine: func(string) reviewEngine { return engine },
		stdout:    &stdout,
		stderr:    &stderr,
	})
	if exitCode != 3 {
		t.Fatalf("exitCode = %d, want 3", exitCode)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json output: %v", err)
	}
	if payload["advisor_artifact"] == nil {
		t.Fatalf("advisor_artifact missing: %s", stdout.String())
	}
	if payload["decision_benchmark"] == nil {
		t.Fatalf("decision_benchmark missing: %s", stdout.String())
	}
	if payload["judge_verdict"] != "requested_changes" {
		t.Fatalf("judge_verdict = %#v, want requested_changes", payload["judge_verdict"])
	}
}

func TestRunWithDepsMultiTargetExitModeUsesAnyBlockingBundle(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{
		"--targets", "https://github.com/acme/repo/pull/17,https://github.com/acme/repo/pull/18",
		"--output", "json",
		"--exit-mode", "requested_changes",
	}, runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput: func(_ context.Context, _ string, target core.ReviewTarget) (core.ReviewInput, error) {
			return core.ReviewInput{Target: target}, nil
		},
		newEngine: func(string) reviewEngine {
			return reviewEngineFunc(func(_ context.Context, input core.ReviewInput, _ core.RunOptions) (core.ReviewBundle, error) {
				verdict := "comment_only"
				if input.Target.ChangeNumber == 18 {
					verdict = "requested_changes"
				}
				return core.ReviewBundle{
					Target:          input.Target,
					Verdict:         verdict,
					MarkdownSummary: "# Review",
				}, nil
			})
		},
		stdout: &stdout,
		stderr: &stderr,
	})
	if exitCode != 3 {
		t.Fatalf("exitCode = %d, want 3 (stderr=%s)", exitCode, stderr.String())
	}
}

func TestRunWithDepsPublishesFailedGitHubStatusOnReviewError(t *testing.T) {
	engine := &fakeEngine{err: context.DeadlineExceeded}
	input := core.ReviewInput{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitHub,
			URL:          "https://github.com/acme/repo/pull/17",
			Repository:   "acme/repo",
			ChangeNumber: 17,
		},
	}
	input.Request.Project.FullPath = "acme/repo"
	input.Request.Version.HeadSHA = "head-sha"

	var calls []statusCall
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{
		"--target", "https://github.com/acme/repo/pull/17",
	}, runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput: func(_ context.Context, _ string, _ core.ReviewTarget) (core.ReviewInput, error) {
			return input, nil
		},
		newEngine: func(string) reviewEngine { return engine },
		status: func(_ context.Context, _ string, target core.ReviewTarget, in core.ReviewInput, state string, blockingFindings int) error {
			calls = append(calls, statusCall{target: target, input: in, state: state, blockingFindings: blockingFindings})
			return nil
		},
		stdout: &bytes.Buffer{},
		stderr: &stderr,
	})
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if len(calls) != 2 {
		t.Fatalf("status calls = %d, want 2", len(calls))
	}
	if calls[0].state != "running" {
		t.Fatalf("first status state = %q, want running", calls[0].state)
	}
	if calls[1].state != "failed" {
		t.Fatalf("second status state = %q, want failed", calls[1].state)
	}
}
