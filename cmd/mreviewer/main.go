package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	platformgithub "github.com/mreviewer/mreviewer/internal/platform/github"
	platformgitlab "github.com/mreviewer/mreviewer/internal/platform/gitlab"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type PublishMode string

const (
	PublishModeFullReviewComments PublishMode = "full-review-comments"
	PublishModeSummaryOnly        PublishMode = "summary-only"
	PublishModeArtifactOnly       PublishMode = "artifact-only"
)

type OutputMode string

const (
	OutputModeMarkdown OutputMode = "markdown"
	OutputModeJSON     OutputMode = "json"
	OutputModeBoth     OutputMode = "both"
)

type reviewEngine interface {
	Run(ctx context.Context, input core.ReviewInput, opts core.RunOptions) (core.ReviewBundle, error)
}

type runtimeDeps struct {
	resolveTarget func(string) (core.ReviewTarget, error)
	loadInput     func(context.Context, string, core.ReviewTarget) (core.ReviewInput, error)
	newEngine     func(string) reviewEngine
	publish       func(context.Context, string, core.ReviewTarget, core.ReviewBundle) error
	stdout        io.Writer
	stderr        io.Writer
}

type cliOptions struct {
	target        string
	configPath    string
	outputMode    OutputMode
	publishMode   PublishMode
	reviewerPacks []string
	routeOverride string
}

func main() {
	os.Exit(runWithDeps(os.Args[1:], runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput:     defaultLoadInput,
		newEngine:     defaultReviewEngine,
		publish:       defaultPublish,
		stdout:        os.Stdout,
		stderr:        os.Stderr,
	}))
}

func runWithDeps(args []string, deps runtimeDeps) int {
	if deps.resolveTarget == nil {
		deps.resolveTarget = resolveReviewTarget
	}
	if deps.newEngine == nil {
		deps.newEngine = func(string) reviewEngine { return noopEngine{} }
	}
	if deps.loadInput == nil {
		deps.loadInput = func(_ context.Context, _ string, target core.ReviewTarget) (core.ReviewInput, error) {
			return core.ReviewInput{Target: target}, nil
		}
	}
	if deps.stdout == nil {
		deps.stdout = io.Discard
	}
	if deps.stderr == nil {
		deps.stderr = io.Discard
	}

	opts, err := parseOptions(args, deps.stderr)
	if err != nil {
		return 2
	}
	target, err := deps.resolveTarget(opts.target)
	if err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "resolve target failed: %v\n", err)
		return 1
	}
	input, err := deps.loadInput(context.Background(), opts.configPath, target)
	if err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "build input failed: %v\n", err)
		return 1
	}

	bundle, err := deps.newEngine(opts.configPath).Run(context.Background(), input, core.RunOptions{
		OutputMode:    string(opts.outputMode),
		PublishMode:   string(opts.publishMode),
		ReviewerPacks: opts.reviewerPacks,
		RouteOverride: opts.routeOverride,
	})
	if err != nil {
		_, _ = fmt.Fprintf(deps.stderr, "review failed: %v\n", err)
		return 1
	}
	if deps.publish != nil && opts.publishMode != PublishModeArtifactOnly {
		publishBundle := bundle
		if opts.publishMode == PublishModeSummaryOnly {
			publishBundle = summaryOnlyBundle(bundle)
		}
		if err := deps.publish(context.Background(), opts.configPath, target, publishBundle); err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "publish failed: %v\n", err)
			return 1
		}
	}

	switch opts.outputMode {
	case OutputModeJSON:
		enc := json.NewEncoder(deps.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(bundle); err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "encode json failed: %v\n", err)
			return 1
		}
	case OutputModeMarkdown:
		_, _ = fmt.Fprintln(deps.stdout, bundle.MarkdownSummary)
	case OutputModeBoth:
		_, _ = fmt.Fprintln(deps.stdout, bundle.MarkdownSummary)
		enc := json.NewEncoder(deps.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(bundle); err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "encode json failed: %v\n", err)
			return 1
		}
	}

	return 0
}

func summaryOnlyBundle(bundle core.ReviewBundle) core.ReviewBundle {
	filtered := bundle
	filtered.PublishCandidates = nil
	for _, candidate := range bundle.PublishCandidates {
		if candidate.Kind == "summary" {
			filtered.PublishCandidates = append(filtered.PublishCandidates, candidate)
		}
	}
	return filtered
}

func parseOptions(args []string, stderr io.Writer) (cliOptions, error) {
	fs := flag.NewFlagSet("mreviewer", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opts := cliOptions{configPath: "config.yaml"}
	var packs string
	output := string(OutputModeBoth)
	publish := string(PublishModeFullReviewComments)
	fs.StringVar(&opts.target, "target", "", "GitHub/GitLab PR URL")
	fs.StringVar(&opts.configPath, "config", "config.yaml", "Path to config file")
	fs.StringVar(&output, "output", output, "markdown|json|both")
	fs.StringVar(&publish, "publish", publish, "full-review-comments|summary-only|artifact-only")
	fs.StringVar(&packs, "reviewer-packs", "", "comma separated reviewer packs")
	fs.StringVar(&opts.routeOverride, "route", "", "provider route override")
	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if strings.TrimSpace(opts.target) == "" {
		return cliOptions{}, fmt.Errorf("--target is required")
	}
	opts.outputMode = OutputMode(strings.TrimSpace(output))
	switch opts.outputMode {
	case OutputModeMarkdown, OutputModeJSON, OutputModeBoth:
	default:
		return cliOptions{}, fmt.Errorf("--output must be one of markdown, json, both")
	}
	opts.publishMode = PublishMode(strings.TrimSpace(publish))
	switch opts.publishMode {
	case PublishModeFullReviewComments, PublishModeSummaryOnly, PublishModeArtifactOnly:
	default:
		return cliOptions{}, fmt.Errorf("--publish must be one of full-review-comments, summary-only, artifact-only")
	}
	if packs != "" {
		for _, part := range strings.Split(packs, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				opts.reviewerPacks = append(opts.reviewerPacks, part)
			}
		}
	}
	return opts, nil
}

func resolveReviewTarget(raw string) (core.ReviewTarget, error) {
	if strings.Contains(raw, "/-/merge_requests/") {
		return platformgitlab.ResolveTarget(raw)
	}
	if strings.Contains(raw, "/pull/") {
		return platformgithub.ResolveTarget(raw)
	}
	return core.ReviewTarget{}, fmt.Errorf("unsupported target url: %s", raw)
}

type noopEngine struct{}

func (noopEngine) Run(_ context.Context, input core.ReviewInput, _ core.RunOptions) (core.ReviewBundle, error) {
	return core.ReviewBundle{
		Target:            input.Target,
		MarkdownSummary:   "# Review\n\nNot implemented yet.",
		JSONSchemaVersion: "v1alpha1",
	}, nil
}
