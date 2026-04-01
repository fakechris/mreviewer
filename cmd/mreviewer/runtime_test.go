package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/mreviewer/mreviewer/internal/compare"
	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewstatus"
)

func TestResolveReviewTargetSupportsGitLabMRURL(t *testing.T) {
	target, err := resolveReviewTarget("https://gitlab.example.com/group/proj/-/merge_requests/17")
	if err != nil {
		t.Fatalf("resolveReviewTarget: %v", err)
	}

	if target.Platform != reviewcore.PlatformGitLab {
		t.Fatalf("expected gitlab platform, got %q", target.Platform)
	}
	if target.Repository != "group/proj" || target.Number != 17 {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestResolveReviewTargetSupportsGitHubPRURL(t *testing.T) {
	target, err := resolveReviewTarget("https://github.com/acme/service/pull/24")
	if err != nil {
		t.Fatalf("resolveReviewTarget: %v", err)
	}

	if target.Platform != reviewcore.PlatformGitHub {
		t.Fatalf("expected github platform, got %q", target.Platform)
	}
	if target.Repository != "acme/service" || target.Number != 24 {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestNewDefaultRunnerSupportsGitHubOnlyConfiguration(t *testing.T) {
	_, err := newDefaultRunner(&config.Config{
		GitHubBaseURL:     "https://github.example.com",
		GitHubToken:       "github-token",
		AnthropicBaseURL:  "https://api.minimaxi.com/anthropic",
		AnthropicAPIKey:   "provider-key",
		AnthropicModel:    "MiniMax-M2.7-highspeed",
	})
	if err != nil {
		t.Fatalf("newDefaultRunner: %v", err)
	}
}

type fakeInputLoader struct {
	target reviewcore.ReviewTarget
	input  reviewcore.ReviewInput
}

func (f *fakeInputLoader) Load(_ context.Context, target reviewcore.ReviewTarget) (reviewcore.ReviewInput, error) {
	f.target = target
	return f.input, nil
}

type fakeBundleEngine struct {
	input    reviewcore.ReviewInput
	packIDs  []string
	response reviewcore.ReviewBundle
	queued   []reviewcore.ReviewBundle
}

func (f *fakeBundleEngine) Run(_ context.Context, input reviewcore.ReviewInput, selectedPackIDs []string) (reviewcore.ReviewBundle, error) {
	f.input = input
	f.packIDs = append([]string(nil), selectedPackIDs...)
	if len(f.queued) > 0 {
		next := f.queued[0]
		f.queued = f.queued[1:]
		return next, nil
	}
	return f.response, nil
}

type fakeBundlePublisher struct {
	input  reviewcore.ReviewInput
	mode   publishMode
	bundle reviewcore.ReviewBundle
}

func (f *fakeBundlePublisher) Publish(_ context.Context, input reviewcore.ReviewInput, mode publishMode, bundle reviewcore.ReviewBundle) error {
	f.input = input
	f.mode = mode
	f.bundle = bundle
	return nil
}

type fakeStatusPublisher struct {
	states []string
	stages []reviewstatus.Stage
}

func (f *fakeStatusPublisher) Publish(_ context.Context, input reviewcore.ReviewInput, update reviewStatusUpdate) error {
	f.states = append(f.states, string(update.State))
	f.stages = append(f.stages, update.Stage)
	return nil
}

type fakeExternalComparisonLoader struct {
	artifacts []reviewcore.ComparisonArtifact
}

func (f *fakeExternalComparisonLoader) Load(_ context.Context, target reviewcore.ReviewTarget, reviewerIDs []string) ([]reviewcore.ComparisonArtifact, error) {
	return append([]reviewcore.ComparisonArtifact(nil), f.artifacts...), nil
}

type fakeAdvisor struct {
	input    reviewcore.ReviewInput
	bundle   reviewcore.ReviewBundle
	route    string
	artifact reviewcore.ReviewerArtifact
}

func (f *fakeAdvisor) Advise(_ context.Context, input reviewcore.ReviewInput, bundle reviewcore.ReviewBundle, route string) (*reviewcore.ReviewerArtifact, error) {
	f.input = input
	f.bundle = bundle
	f.route = route
	artifact := f.artifact
	return &artifact, nil
}

func testFinding(locationKey, category, claim string) reviewcore.Finding {
	return reviewcore.Finding{
		Category: category,
		Claim:    claim,
		Identity: &reviewcore.FindingIdentityInput{
			Category:        category,
			NormalizedClaim: claim,
			LocationKey:     locationKey,
			EvidenceKey:     claim,
		},
	}
}

func TestRuntimeRunnerLoadsInputAndRunsEngine(t *testing.T) {
	loader := &fakeInputLoader{
		input: reviewcore.ReviewInput{
			Target: reviewcore.ReviewTarget{
				Platform:   reviewcore.PlatformGitLab,
				Repository: "group/proj",
				Number:     17,
				URL:        "https://gitlab.example.com/group/proj/-/merge_requests/17",
			},
			ContextText: "assembled context",
		},
	}
	engine := &fakeBundleEngine{
		response: reviewcore.ReviewBundle{
			Target:       reviewcore.ReviewTarget{Platform: reviewcore.PlatformGitLab, Repository: "group/proj", Number: 17},
			JudgeVerdict: reviewcore.VerdictRequestedChanges,
			JudgeSummary: "Merged reviewer judgment",
		},
	}

	runner := runtimeRunner{
		loader: loader,
		engine: engine,
	}

	result, err := runner.Run(context.Background(), RunRequest{
		Target:        reviewcore.ReviewTarget{Platform: reviewcore.PlatformGitLab, Repository: "group/proj", Number: 17},
		ReviewerPacks: []string{"security", "database"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if loader.target.Repository != "group/proj" {
		t.Fatalf("expected loader to receive target, got %#v", loader.target)
	}
	if engine.input.ContextText != "assembled context" {
		t.Fatalf("expected engine to receive built input, got %#v", engine.input)
	}
	if len(engine.packIDs) != 2 || engine.packIDs[0] != "security" {
		t.Fatalf("expected selected packs to reach engine, got %#v", engine.packIDs)
	}
	if result.Markdown == "" {
		t.Fatal("expected markdown rendering from review bundle")
	}
}

func TestRuntimeRunnerRunsAdvisorWhenAdvisorRouteIsSet(t *testing.T) {
	target := reviewcore.ReviewTarget{
		Platform:   reviewcore.PlatformGitHub,
		Repository: "acme/service",
		Number:     24,
		URL:        "https://github.com/acme/service/pull/24",
	}
	advisor := &fakeAdvisor{
		artifact: reviewcore.ReviewerArtifact{
			ReviewerID:   "advisor",
			ReviewerType: "advisor",
			Verdict:      reviewcore.VerdictCommentOnly,
			Summary:      "Advisor agrees with the council.",
		},
	}
	runner := runtimeRunner{
		loaders: map[reviewcore.Platform]InputLoader{
			reviewcore.PlatformGitHub: &fakeInputLoader{input: reviewcore.ReviewInput{Target: target}},
		},
		engine: &fakeBundleEngine{
			response: reviewcore.ReviewBundle{
				Target:       target,
				JudgeVerdict: reviewcore.VerdictRequestedChanges,
				Artifacts: []reviewcore.ReviewerArtifact{
					{
						ReviewerID:   "security",
						ReviewerType: "pack",
						Findings: []reviewcore.Finding{
							testFinding("auth-bypass", "security", "auth bypass"),
						},
					},
				},
			},
		},
		advisor: advisor,
	}

	result, err := runner.Run(context.Background(), RunRequest{
		Target:       target,
		AdvisorRoute: "openai-gpt-5-4",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if advisor.route != "openai-gpt-5-4" {
		t.Fatalf("advisor route = %q, want openai-gpt-5-4", advisor.route)
	}
	if result.AdvisorArtifact == nil || result.AdvisorArtifact.ReviewerType != "advisor" {
		t.Fatalf("expected advisor artifact in result, got %#v", result.AdvisorArtifact)
	}
	if result.Comparison == nil || result.Comparison.ReviewerCount != 2 {
		t.Fatalf("expected advisor to be included in comparison reviewer count, got %#v", result.Comparison)
	}
	if result.DecisionBenchmark == nil || result.DecisionBenchmark.ReviewerCount != 2 {
		t.Fatalf("expected advisor to be included in decision benchmark reviewer count, got %#v", result.DecisionBenchmark)
	}
}

func TestRuntimeRunnerUsesPlatformSpecificLoaderWhenConfigured(t *testing.T) {
	gitHubLoader := &fakeInputLoader{
		input: reviewcore.ReviewInput{
			Target: reviewcore.ReviewTarget{
				Platform:   reviewcore.PlatformGitHub,
				Repository: "acme/service",
				Number:     24,
				URL:        "https://github.com/acme/service/pull/24",
			},
			ContextText: "github context",
		},
	}
	engine := &fakeBundleEngine{
		response: reviewcore.ReviewBundle{
			Target:       reviewcore.ReviewTarget{Platform: reviewcore.PlatformGitHub, Repository: "acme/service", Number: 24},
			JudgeVerdict: reviewcore.VerdictCommentOnly,
			JudgeSummary: "No material issues found",
		},
	}

	runner := runtimeRunner{
		loaders: map[reviewcore.Platform]InputLoader{
			reviewcore.PlatformGitHub: gitHubLoader,
		},
		engine: engine,
	}

	result, err := runner.Run(context.Background(), RunRequest{
		Target: reviewcore.ReviewTarget{
			Platform:   reviewcore.PlatformGitHub,
			Repository: "acme/service",
			Number:     24,
			URL:        "https://github.com/acme/service/pull/24",
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if gitHubLoader.target.Platform != reviewcore.PlatformGitHub {
		t.Fatalf("expected github loader to be used, got %#v", gitHubLoader.target)
	}
	if result.Target.Repository != "acme/service" {
		t.Fatalf("unexpected result target: %#v", result.Target)
	}
}

func TestRuntimeRunnerPublishesBundleWhenPublisherConfigured(t *testing.T) {
	loader := &fakeInputLoader{
		input: reviewcore.ReviewInput{
			Target: reviewcore.ReviewTarget{
				Platform:   reviewcore.PlatformGitLab,
				Repository: "group/proj",
				Number:     17,
				URL:        "https://gitlab.example.com/group/proj/-/merge_requests/17",
			},
			ContextText: "assembled context",
			Metadata:    map[string]string{"project_id": "101"},
		},
	}
	engine := &fakeBundleEngine{
		response: reviewcore.ReviewBundle{
			Target: reviewcore.ReviewTarget{Platform: reviewcore.PlatformGitLab, Repository: "group/proj", Number: 17},
			PublishCandidates: []reviewcore.PublishCandidate{
				{Type: "summary", Body: "Merged reviewer judgment"},
			},
		},
	}
	publisher := &fakeBundlePublisher{}

	runner := runtimeRunner{
		loader: loader,
		engine: engine,
		publishers: map[reviewcore.Platform]BundlePublisher{
			reviewcore.PlatformGitLab: publisher,
		},
	}

	_, err := runner.Run(context.Background(), RunRequest{
		Target:      loader.input.Target,
		PublishMode: publishModeSummaryOnly,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if publisher.mode != publishModeSummaryOnly {
		t.Fatalf("expected publish mode summary-only, got %q", publisher.mode)
	}
	if publisher.bundle.Target.Repository != "group/proj" {
		t.Fatalf("expected published bundle target, got %#v", publisher.bundle.Target)
	}
}

func TestRuntimeRunnerPublishesRunningAndPassedStatuses(t *testing.T) {
	loader := &fakeInputLoader{
		input: reviewcore.ReviewInput{
			Target: reviewcore.ReviewTarget{
				Platform:   reviewcore.PlatformGitHub,
				Repository: "acme/service",
				Number:     24,
				URL:        "https://github.com/acme/service/pull/24",
			},
			Metadata: map[string]string{"head_sha": "head-sha"},
		},
	}
	engine := &fakeBundleEngine{
		response: reviewcore.ReviewBundle{
			Target:       loader.input.Target,
			JudgeVerdict: reviewcore.VerdictCommentOnly,
		},
	}
	status := &fakeStatusPublisher{}

	runner := runtimeRunner{
		loader: loader,
		engine: engine,
		statusPublishers: map[reviewcore.Platform]StatusPublisher{
			reviewcore.PlatformGitHub: status,
		},
	}

	_, err := runner.Run(context.Background(), RunRequest{Target: loader.input.Target})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(status.states) != 2 {
		t.Fatalf("status publish count = %d, want 2", len(status.states))
	}
	if status.stages[0] != reviewstatus.StageRunningPacks {
		t.Fatalf("first stage = %q, want %q", status.stages[0], reviewstatus.StageRunningPacks)
	}
	if status.states[0] != string(reviewStatusRunning) {
		t.Fatalf("first status = %q, want running", status.states[0])
	}
	if status.stages[1] != reviewstatus.StageCompleted {
		t.Fatalf("second stage = %q, want %q", status.stages[1], reviewstatus.StageCompleted)
	}
	if status.states[1] != string(reviewStatusPassed) {
		t.Fatalf("second status = %q, want passed", status.states[1])
	}
}

func TestRuntimeRunnerPublishesFailedStatusForRequestedChanges(t *testing.T) {
	loader := &fakeInputLoader{
		input: reviewcore.ReviewInput{
			Target: reviewcore.ReviewTarget{
				Platform:   reviewcore.PlatformGitHub,
				Repository: "acme/service",
				Number:     24,
				URL:        "https://github.com/acme/service/pull/24",
			},
			Metadata: map[string]string{"head_sha": "head-sha"},
		},
	}
	engine := &fakeBundleEngine{
		response: reviewcore.ReviewBundle{
			Target:       loader.input.Target,
			JudgeVerdict: reviewcore.VerdictRequestedChanges,
		},
	}
	status := &fakeStatusPublisher{}

	runner := runtimeRunner{
		loader: loader,
		engine: engine,
		statusPublishers: map[reviewcore.Platform]StatusPublisher{
			reviewcore.PlatformGitHub: status,
		},
	}

	_, err := runner.Run(context.Background(), RunRequest{Target: loader.input.Target})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(status.states) != 2 {
		t.Fatalf("status publish count = %d, want 2", len(status.states))
	}
	if status.stages[1] != reviewstatus.StageCompleted {
		t.Fatalf("second stage = %q, want %q", status.stages[1], reviewstatus.StageCompleted)
	}
	if status.states[1] != string(reviewStatusFailed) {
		t.Fatalf("second status = %q, want failed", status.states[1])
	}
}

func TestRuntimeRunnerPublishesProgressStagesForAdvisorPublishAndCompare(t *testing.T) {
	target := reviewcore.ReviewTarget{
		Platform:   reviewcore.PlatformGitHub,
		Repository: "acme/service",
		Number:     24,
		URL:        "https://github.com/acme/service/pull/24",
	}
	status := &fakeStatusPublisher{}
	runner := runtimeRunner{
		loaders: map[reviewcore.Platform]InputLoader{
			reviewcore.PlatformGitHub: &fakeInputLoader{input: reviewcore.ReviewInput{Target: target}},
		},
		engine: &fakeBundleEngine{
			response: reviewcore.ReviewBundle{
				Target: target,
				Artifacts: []reviewcore.ReviewerArtifact{
					{ReviewerID: "security", ReviewerType: "pack", Findings: []reviewcore.Finding{testFinding("auth-bypass", "security", "auth bypass")}},
				},
				PublishCandidates: []reviewcore.PublishCandidate{{Type: "summary", Title: "summary"}},
			},
		},
		advisor: &fakeAdvisor{artifact: reviewcore.ReviewerArtifact{ReviewerID: "advisor", ReviewerType: "advisor", Summary: "advisor"}},
		publishers: map[reviewcore.Platform]BundlePublisher{
			reviewcore.PlatformGitHub: &fakeBundlePublisher{},
		},
		statusPublishers: map[reviewcore.Platform]StatusPublisher{
			reviewcore.PlatformGitHub: status,
		},
		externalComparisonLoaders: map[reviewcore.Platform]ExternalComparisonLoader{
			reviewcore.PlatformGitHub: &fakeExternalComparisonLoader{},
		},
	}

	_, err := runner.Run(context.Background(), RunRequest{
		Target:           target,
		AdvisorRoute:     "advisor-route",
		CompareReviewers: []string{"github:coderabbit"},
		CompareTargets:   []reviewcore.ReviewTarget{target},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantStages := []reviewstatus.Stage{
		reviewstatus.StageRunningPacks,
		reviewstatus.StageRunningAdvisor,
		reviewstatus.StagePublishing,
		reviewstatus.StageComparingExternal,
		reviewstatus.StageComparingTargets,
		reviewstatus.StageCompleted,
	}
	if len(status.stages) != len(wantStages) {
		t.Fatalf("stage count = %d, want %d (%v)", len(status.stages), len(wantStages), status.stages)
	}
	for i := range wantStages {
		if status.stages[i] != wantStages[i] {
			t.Fatalf("stage[%d] = %q, want %q", i, status.stages[i], wantStages[i])
		}
	}
}

func TestRuntimeRunnerBuildsComparisonReportsForPrimaryAndCompareTargets(t *testing.T) {
	primaryTarget := reviewcore.ReviewTarget{
		Platform:   reviewcore.PlatformGitLab,
		Repository: "group/proj",
		Number:     17,
		URL:        "https://gitlab.example.com/group/proj/-/merge_requests/17",
	}
	compareTarget := reviewcore.ReviewTarget{
		Platform:   reviewcore.PlatformGitHub,
		Repository: "acme/service",
		Number:     24,
		URL:        "https://github.com/acme/service/pull/24",
	}
	runner := runtimeRunner{
		loaders: map[reviewcore.Platform]InputLoader{
			reviewcore.PlatformGitLab: &fakeInputLoader{
				input: reviewcore.ReviewInput{Target: primaryTarget},
			},
			reviewcore.PlatformGitHub: &fakeInputLoader{
				input: reviewcore.ReviewInput{Target: compareTarget},
			},
		},
		engine: &fakeBundleEngine{
			queued: []reviewcore.ReviewBundle{
				{
					Target: primaryTarget,
					Artifacts: []reviewcore.ReviewerArtifact{
						{
							ReviewerID: "security",
							Findings: []reviewcore.Finding{
								testFinding("auth-bypass", "security", "auth bypass"),
							},
						},
						{
							ReviewerID: "architecture",
							Findings: []reviewcore.Finding{
								testFinding("auth-bypass", "security", "auth bypass"),
								testFinding("n-plus-one", "database", "n+1 query"),
							},
						},
					},
				},
				{
					Target: compareTarget,
					Artifacts: []reviewcore.ReviewerArtifact{
						{
							ReviewerID: "security",
							Findings: []reviewcore.Finding{
								testFinding("nil-deref", "correctness", "nil dereference"),
							},
						},
					},
				},
			},
		},
	}

	result, err := runner.Run(context.Background(), RunRequest{
		Target:         primaryTarget,
		CompareTargets: []reviewcore.ReviewTarget{compareTarget},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Comparison == nil {
		t.Fatal("expected primary comparison report")
	}
	if result.Comparison.AgreementRate != 0.5 {
		t.Fatalf("agreement rate = %v, want 0.5", result.Comparison.AgreementRate)
	}
	if result.AggregateComparison == nil {
		t.Fatal("expected aggregate comparison report")
	}
	if result.AggregateComparison.TargetCount != 2 {
		t.Fatalf("aggregate target count = %d, want 2", result.AggregateComparison.TargetCount)
	}
	if result.AggregateComparison.UniqueFindingCount != 3 {
		t.Fatalf("aggregate unique finding count = %d, want 3", result.AggregateComparison.UniqueFindingCount)
	}
}

func TestRunWithDepsJSONOutputIncludesComparisonBlocks(t *testing.T) {
	stdout := &bytes.Buffer{}
	runner := &fakeRunner{
		result: RunResult{
			Target: reviewcore.ReviewTarget{
				Platform:   reviewcore.PlatformGitLab,
				Repository: "group/proj",
				Number:     7,
				URL:        "https://gitlab.example.com/group/proj/-/merge_requests/7",
			},
			Comparison: &compare.Report{
				ReviewerCount:  2,
				AgreementRate:  0.5,
				SharedFindings: []reviewcore.Finding{testFinding("auth-bypass", "security", "auth bypass")},
			},
			AggregateComparison: &compare.AggregateReport{
				TargetCount:          2,
				ReviewerCount:        3,
				UniqueFindingCount:   4,
				AverageAgreementRate: 0.25,
			},
			DecisionBenchmark: &compare.DecisionBenchmarkReport{
				ReviewerCount:     2,
				ConsensusFindings: 1,
				UniqueFindings:    3,
				JudgeVerdict:      reviewcore.VerdictRequestedChanges,
			},
		},
	}

	exitCode := runWithDeps([]string{
		"--target", "https://gitlab.example.com/group/proj/-/merge_requests/7",
		"--output", "json",
	}, runtimeDeps{
		stdout: stdout,
		runner: runner,
	})
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode json response: %v", err)
	}
	if payload["comparison"] == nil {
		t.Fatal("expected comparison block in json output")
	}
	if payload["aggregate_comparison"] == nil {
		t.Fatal("expected aggregate_comparison block in json output")
	}
	if payload["decision_benchmark"] == nil {
		t.Fatal("expected decision_benchmark block in json output")
	}
}

func TestRuntimeRunnerMergesExternalReviewerArtifactsIntoComparison(t *testing.T) {
	target := reviewcore.ReviewTarget{
		Platform:   reviewcore.PlatformGitHub,
		Repository: "acme/service",
		Number:     24,
		URL:        "https://github.com/acme/service/pull/24",
	}
	runner := runtimeRunner{
		loaders: map[reviewcore.Platform]InputLoader{
			reviewcore.PlatformGitHub: &fakeInputLoader{
				input: reviewcore.ReviewInput{Target: target},
			},
		},
		engine: &fakeBundleEngine{
			response: reviewcore.ReviewBundle{
				Target: target,
				Artifacts: []reviewcore.ReviewerArtifact{
					{ReviewerID: "security", Findings: []reviewcore.Finding{testFinding("auth-bypass", "security", "auth bypass")}},
				},
			},
		},
		externalComparisonLoaders: map[reviewcore.Platform]ExternalComparisonLoader{
			reviewcore.PlatformGitHub: &fakeExternalComparisonLoader{
				artifacts: []reviewcore.ComparisonArtifact{
					{ReviewerID: "coderabbit", Findings: []reviewcore.Finding{testFinding("auth-bypass", "security", "auth bypass")}},
				},
			},
		},
	}

	result, err := runner.Run(context.Background(), RunRequest{
		Target:           target,
		CompareReviewers: []string{"coderabbit"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Comparison == nil {
		t.Fatal("expected comparison report")
	}
	if result.Comparison.ReviewerCount != 2 {
		t.Fatalf("reviewer count = %d, want 2", result.Comparison.ReviewerCount)
	}
	if len(result.Comparison.SharedFindings) != 1 {
		t.Fatalf("shared findings = %d, want 1", len(result.Comparison.SharedFindings))
	}
}
