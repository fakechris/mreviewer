package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mreviewer/mreviewer/internal/compare"
	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/reviewcore"
)

type outputMode string

const (
	outputModeMarkdown outputMode = "markdown"
	outputModeJSON     outputMode = "json"
	outputModeBoth     outputMode = "both"
)

type publishMode string

const (
	publishModeFullReviewComments publishMode = "full-review-comments"
	publishModeSummaryOnly        publishMode = "summary-only"
	publishModeArtifactOnly       publishMode = "artifact-only"
)

type cliOptions struct {
	configPath       string
	target           string
	targets          string
	outputMode       outputMode
	publishMode      publishMode
	exitMode         string
	reviewerPacks    []string
	routeOverride    string
	advisorRoute     string
	compareReviewers []string
	compareTargets   []string
}

type runtimeDeps struct {
	loadConfig func(string) (*config.Config, error)
	newRunner  func(*config.Config) (Runner, error)
	stdout     io.Writer
	stderr     io.Writer
	runner     Runner
}

type jsonResponse struct {
	OK                  bool                             `json:"ok"`
	Target              reviewcore.ReviewTarget          `json:"target"`
	JudgeVerdict        reviewcore.Verdict               `json:"judge_verdict,omitempty"`
	AdvisorArtifact     *reviewcore.ReviewerArtifact     `json:"advisor_artifact,omitempty"`
	CompareTargets      []reviewcore.ReviewTarget        `json:"compare_targets,omitempty"`
	Comparison          *compare.Report                  `json:"comparison,omitempty"`
	AggregateComparison *compare.AggregateReport         `json:"aggregate_comparison,omitempty"`
	DecisionBenchmark   *compare.DecisionBenchmarkReport `json:"decision_benchmark,omitempty"`
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	*f = append(*f, value)
	return nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	return runWithDeps(args, runtimeDeps{
		loadConfig: config.Load,
		newRunner:  newDefaultRunner,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
	})
}

func runWithDeps(args []string, deps runtimeDeps) int {
	if deps.loadConfig == nil {
		deps.loadConfig = config.Load
	}
	if deps.newRunner == nil {
		deps.newRunner = newDefaultRunner
	}
	if deps.stdout == nil {
		deps.stdout = io.Discard
	}
	if deps.stderr == nil {
		deps.stderr = io.Discard
	}
	if deps.runner == nil {
		opts, err := parseCLIOptions(args)
		if err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "mreviewer: %v\n", err)
			return 2
		}
		cfg, err := deps.loadConfig(opts.configPath)
		if err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "mreviewer: load config: %v\n", err)
			return 1
		}
		runner, err := deps.newRunner(cfg)
		if err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "mreviewer: build runtime: %v\n", err)
			return 1
		}
		deps.runner = runner
	}

	opts, err := parseCLIOptions(args)
	if err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "mreviewer: %v\n", err)
		return 2
	}

	target, err := resolveReviewTarget(opts.target)
	if err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "mreviewer: resolve target: %v\n", err)
		return 1
	}

	compareTargets := make([]reviewcore.ReviewTarget, 0, len(opts.compareTargets))
	allCompareTargets := append([]string(nil), opts.compareTargets...)
	allCompareTargets = append(allCompareTargets, splitCSV(opts.targets)...)
	for _, raw := range allCompareTargets {
		resolved, err := resolveReviewTarget(raw)
		if err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "mreviewer: resolve compare target: %v\n", err)
			return 1
		}
		compareTargets = append(compareTargets, resolved)
	}

	result, err := deps.runner.Run(context.Background(), RunRequest{
		Target:           target,
		OutputMode:       opts.outputMode,
		PublishMode:      opts.publishMode,
		ReviewerPacks:    append([]string(nil), opts.reviewerPacks...),
		RouteOverride:    opts.routeOverride,
		AdvisorRoute:     opts.advisorRoute,
		CompareReviewers: append([]string(nil), opts.compareReviewers...),
		CompareTargets:   compareTargets,
	})
	if err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "mreviewer: run review: %v\n", err)
		return 1
	}

	if opts.outputMode == outputModeJSON || opts.outputMode == outputModeBoth {
		if err := writeJSONResponse(deps.stdout, jsonResponse{
			OK:                  true,
			Target:              result.Target,
			JudgeVerdict:        result.JudgeVerdict,
			AdvisorArtifact:     result.AdvisorArtifact,
			CompareTargets:      result.CompareTargets,
			Comparison:          result.Comparison,
			AggregateComparison: result.AggregateComparison,
			DecisionBenchmark:   result.DecisionBenchmark,
		}); err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "mreviewer: write json: %v\n", err)
			return 1
		}
		return exitCodeForResult(opts.exitMode, result)
	}

	if result.Markdown != "" {
		_, _ = io.WriteString(deps.stdout, result.Markdown)
		return exitCodeForResult(opts.exitMode, result)
	}
	_, _ = fmt.Fprintf(deps.stdout, "review prepared for %s\n", result.Target.URL)
	return exitCodeForResult(opts.exitMode, result)
}

func parseCLIOptions(args []string) (cliOptions, error) {
	opts := cliOptions{
		configPath:  "config.yaml",
		outputMode:  outputModeMarkdown,
		publishMode: publishModeFullReviewComments,
		exitMode:    "never",
	}
	var reviewerPacks string
	var compareReviewers stringListFlag
	var compareTargets stringListFlag

	fs := flag.NewFlagSet("mreviewer", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.configPath, "config", "config.yaml", "Path to config file")
	fs.StringVar(&opts.target, "target", "", "GitHub pull request or GitLab merge request URL")
	fs.StringVar(&opts.targets, "targets", "", "Comma-separated additional GitHub/GitLab review target URLs")
	fs.StringVar((*string)(&opts.outputMode), "output", string(outputModeMarkdown), "Output mode: markdown, json, both")
	fs.StringVar((*string)(&opts.publishMode), "publish", string(publishModeFullReviewComments), "Publish mode: full-review-comments, summary-only, artifact-only")
	fs.StringVar(&opts.exitMode, "exit-mode", "never", "Exit mode: never, requested_changes")
	fs.StringVar(&reviewerPacks, "reviewer-packs", "", "Comma-separated reviewer packs")
	fs.StringVar(&opts.routeOverride, "route", "", "Provider route override")
	fs.StringVar(&opts.advisorRoute, "advisor-route", "", "Optional stronger second-opinion provider route")
	fs.Var(&compareReviewers, "compare-reviewer", "Reviewer identity to compare against")
	fs.Var(&compareTargets, "compare-target", "Additional GitHub/GitLab review target URL")

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if strings.TrimSpace(opts.target) == "" {
		return cliOptions{}, fmt.Errorf("--target is required")
	}
	if !isValidOutputMode(opts.outputMode) {
		return cliOptions{}, fmt.Errorf("unsupported --output %q", opts.outputMode)
	}
	if !isValidPublishMode(opts.publishMode) {
		return cliOptions{}, fmt.Errorf("unsupported --publish %q", opts.publishMode)
	}
	if !isValidExitMode(opts.exitMode) {
		return cliOptions{}, fmt.Errorf("unsupported --exit-mode %q", opts.exitMode)
	}

	opts.target = strings.TrimSpace(opts.target)
	opts.targets = strings.TrimSpace(opts.targets)
	opts.configPath = strings.TrimSpace(opts.configPath)
	opts.routeOverride = strings.TrimSpace(opts.routeOverride)
	opts.reviewerPacks = splitCSV(reviewerPacks)
	opts.compareReviewers = append([]string(nil), compareReviewers...)
	opts.compareTargets = append([]string(nil), compareTargets...)
	opts.compareTargets = append(opts.compareTargets, splitCSV(opts.targets)...)

	return opts, nil
}

func writeJSONResponse(w io.Writer, payload jsonResponse) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(payload)
}

func isValidOutputMode(mode outputMode) bool {
	switch mode {
	case outputModeMarkdown, outputModeJSON, outputModeBoth:
		return true
	default:
		return false
	}
}

func isValidPublishMode(mode publishMode) bool {
	switch mode {
	case publishModeFullReviewComments, publishModeSummaryOnly, publishModeArtifactOnly:
		return true
	default:
		return false
	}
}

func isValidExitMode(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "never", "requested_changes":
		return true
	default:
		return false
	}
}

func exitCodeForResult(mode string, result RunResult) int {
	switch strings.TrimSpace(mode) {
	case "requested_changes":
		if result.JudgeVerdict == reviewcore.VerdictRequestedChanges || result.JudgeVerdict == reviewcore.VerdictFailed {
			return 3
		}
	}
	return 0
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
