package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type fakeRunner struct {
	request RunRequest
	result  RunResult
}

func (f *fakeRunner) Run(_ context.Context, request RunRequest) (RunResult, error) {
	f.request = request
	return f.result, nil
}

func TestParseCLIOptionsSupportsProductContract(t *testing.T) {
	opts, err := parseCLIOptions([]string{
		"--target", "https://gitlab.example.com/group/proj/-/merge_requests/7",
		"--output", "both",
		"--publish", "full-review-comments",
		"--reviewer-packs", "security,database",
		"--route", "minimax-review",
		"--advisor-route", "openai-gpt-5-4",
		"--targets", "https://gitlab.example.com/group/proj/-/merge_requests/8,https://github.com/acme/service/pull/24",
		"--compare-reviewer", "coderabbit",
		"--compare-reviewer", "gemini",
		"--compare-target", "https://github.com/acme/service/pull/24",
		"--exit-mode", "requested_changes",
	})
	if err != nil {
		t.Fatalf("parseCLIOptions: %v", err)
	}

	if opts.target != "https://gitlab.example.com/group/proj/-/merge_requests/7" {
		t.Fatalf("unexpected target: %q", opts.target)
	}
	if opts.outputMode != outputModeBoth {
		t.Fatalf("expected output mode both, got %q", opts.outputMode)
	}
	if opts.publishMode != publishModeFullReviewComments {
		t.Fatalf("expected full-review-comments publish mode, got %q", opts.publishMode)
	}
	if len(opts.reviewerPacks) != 2 || opts.reviewerPacks[0] != "security" || opts.reviewerPacks[1] != "database" {
		t.Fatalf("unexpected reviewer packs: %#v", opts.reviewerPacks)
	}
	if len(opts.compareReviewers) != 2 {
		t.Fatalf("expected compare reviewers to be collected, got %#v", opts.compareReviewers)
	}
	if len(opts.compareTargets) != 3 {
		t.Fatalf("expected compare targets to be collected, got %#v", opts.compareTargets)
	}
	if opts.advisorRoute != "openai-gpt-5-4" {
		t.Fatalf("expected advisor route, got %q", opts.advisorRoute)
	}
	if opts.exitMode != "requested_changes" {
		t.Fatalf("expected exit mode requested_changes, got %q", opts.exitMode)
	}
}

func TestRunWithDepsJSONOutputPassesCanonicalTargets(t *testing.T) {
	stdout := &bytes.Buffer{}
	runner := &fakeRunner{
		result: RunResult{
			Target: reviewcore.ReviewTarget{
				Platform:   reviewcore.PlatformGitLab,
				Repository: "group/proj",
				Number:     7,
				URL:        "https://gitlab.example.com/group/proj/-/merge_requests/7",
			},
			CompareTargets: []reviewcore.ReviewTarget{
				{
					Platform:   reviewcore.PlatformGitHub,
					Repository: "acme/service",
					Number:     24,
					URL:        "https://github.com/acme/service/pull/24",
				},
			},
			JudgeVerdict: reviewcore.VerdictCommentOnly,
			AdvisorArtifact: &reviewcore.ReviewerArtifact{
				ReviewerID:   "advisor",
				ReviewerType: "advisor",
				Verdict:      reviewcore.VerdictCommentOnly,
				Summary:      "Advisor agrees with the council.",
			},
		},
	}

	exitCode := runWithDeps([]string{
		"--target", "https://gitlab.example.com/group/proj/-/merge_requests/7",
		"--output", "json",
		"--publish", "artifact-only",
		"--reviewer-packs", "security,architecture",
		"--route", "claude-review",
		"--advisor-route", "openai-gpt-5-4",
		"--compare-reviewer", "coderabbit",
		"--compare-target", "https://github.com/acme/service/pull/24",
	}, runtimeDeps{
		stdout: stdout,
		runner: runner,
	})
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if runner.request.Target.Repository != "group/proj" {
		t.Fatalf("expected canonical gitlab repository, got %q", runner.request.Target.Repository)
	}
	if len(runner.request.CompareTargets) != 1 || runner.request.CompareTargets[0].Repository != "acme/service" {
		t.Fatalf("expected canonical compare target, got %#v", runner.request.CompareTargets)
	}
	if runner.request.RouteOverride != "claude-review" {
		t.Fatalf("expected route override, got %q", runner.request.RouteOverride)
	}
	if runner.request.AdvisorRoute != "openai-gpt-5-4" {
		t.Fatalf("expected advisor route override, got %q", runner.request.AdvisorRoute)
	}
	if runner.request.PublishMode != publishModeArtifactOnly {
		t.Fatalf("expected artifact-only publish mode, got %q", runner.request.PublishMode)
	}

	var payload jsonResponse
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode json response: %v", err)
	}
	if payload.Target.Repository != "group/proj" {
		t.Fatalf("unexpected json payload target: %#v", payload.Target)
	}
	if payload.JudgeVerdict != reviewcore.VerdictCommentOnly {
		t.Fatalf("unexpected json payload verdict: %#v", payload.JudgeVerdict)
	}
	if payload.AdvisorArtifact == nil || payload.AdvisorArtifact.ReviewerType != "advisor" {
		t.Fatalf("expected advisor artifact in json payload, got %#v", payload.AdvisorArtifact)
	}
}

func TestRunWithDepsRejectsUnknownPublishMode(t *testing.T) {
	exitCode := runWithDeps([]string{
		"--target", "https://gitlab.example.com/group/proj/-/merge_requests/7",
		"--publish", "ship-it",
	}, runtimeDeps{
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
		runner: &fakeRunner{},
	})
	if exitCode == 0 {
		t.Fatal("expected non-zero exit code for invalid publish mode")
	}
}

func TestRunWithDepsReturnsGateExitCodeWhenRequestedChanges(t *testing.T) {
	stdout := &bytes.Buffer{}
	exitCode := runWithDeps([]string{
		"--target", "https://github.com/acme/service/pull/24",
		"--output", "json",
		"--exit-mode", "requested_changes",
	}, runtimeDeps{
		stdout: stdout,
		stderr: &bytes.Buffer{},
		runner: &fakeRunner{
			result: RunResult{
				Target:       reviewcore.ReviewTarget{Platform: reviewcore.PlatformGitHub, Repository: "acme/service", Number: 24, URL: "https://github.com/acme/service/pull/24"},
				JudgeVerdict: reviewcore.VerdictRequestedChanges,
			},
		},
	})
	if exitCode != 3 {
		t.Fatalf("expected gate exit code 3, got %d", exitCode)
	}
}
