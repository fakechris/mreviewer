package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	comparepkg "github.com/mreviewer/mreviewer/internal/compare"
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
	compare       func(context.Context, string, core.ReviewTarget, core.ReviewBundle, cliOptions) (*comparepkg.Report, error)
	status        func(context.Context, string, core.ReviewTarget, core.ReviewInput, string, int) error
	stdout        io.Writer
	stderr        io.Writer
}

type cliOptions struct {
	target        string
	targets       []string
	configPath    string
	outputMode    OutputMode
	publishMode   PublishMode
	exitMode      string
	reviewerPacks []string
	routeOverride string
	advisorRoute  string
	compareLiveReviewers []string
	compareArtifactPaths []string
}

func main() {
	os.Exit(runWithDeps(os.Args[1:], runtimeDeps{
		resolveTarget: resolveReviewTarget,
		loadInput:     defaultLoadInput,
		newEngine:     defaultReviewEngine,
		publish:       defaultPublish,
		compare:       defaultCompare,
		status:        defaultStatus,
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
	var bundles []core.ReviewBundle
	var comparisons []comparepkg.Report
	for _, rawTarget := range opts.targets {
		target, err := deps.resolveTarget(rawTarget)
		if err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "resolve target failed: %v\n", err)
			return 1
		}
		input, err := deps.loadInput(context.Background(), opts.configPath, target)
		if err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "build input failed: %v\n", err)
			return 1
		}
		if deps.status != nil {
			if err := deps.status(context.Background(), opts.configPath, target, input, "running", 0); err != nil {
				_, _ = fmt.Fprintf(deps.stderr, "status failed: %v\n", err)
				return 1
			}
		}

		bundle, err := deps.newEngine(opts.configPath).Run(context.Background(), input, core.RunOptions{
			OutputMode:    string(opts.outputMode),
			PublishMode:   string(opts.publishMode),
			ReviewerPacks: opts.reviewerPacks,
			RouteOverride: opts.routeOverride,
			AdvisorRoute:  opts.advisorRoute,
		})
		if err != nil {
			if deps.status != nil {
				_ = deps.status(context.Background(), opts.configPath, target, input, "failed", 0)
			}
			_, _ = fmt.Fprintf(deps.stderr, "review failed: %v\n", err)
			return 1
		}
		bundles = append(bundles, bundle)

		if deps.compare != nil && (len(opts.compareLiveReviewers) > 0 || len(opts.compareArtifactPaths) > 0) {
			comparison, err := deps.compare(context.Background(), opts.configPath, target, bundle, opts)
			if err != nil {
				_, _ = fmt.Fprintf(deps.stderr, "compare failed: %v\n", err)
				return 1
			}
			if comparison != nil {
				comparisons = append(comparisons, *comparison)
			}
		}
		if deps.publish != nil && opts.publishMode != PublishModeArtifactOnly {
			publishBundle := bundle
			if opts.publishMode == PublishModeSummaryOnly {
				publishBundle = summaryOnlyBundle(bundle)
			}
			if err := deps.publish(context.Background(), opts.configPath, target, publishBundle); err != nil {
				if deps.status != nil {
					_ = deps.status(context.Background(), opts.configPath, target, input, "failed", 0)
				}
				_, _ = fmt.Fprintf(deps.stderr, "publish failed: %v\n", err)
				return 1
			}
		}
		if deps.status != nil {
			if err := deps.status(context.Background(), opts.configPath, target, input, finalStatusState(bundle), blockingFindings(bundle)); err != nil {
				_, _ = fmt.Fprintf(deps.stderr, "status failed: %v\n", err)
				return 1
			}
		}
	}

	if len(bundles) == 1 {
		bundle := bundles[0]
		var comparison *comparepkg.Report
		if len(comparisons) == 1 {
			comparison = &comparisons[0]
		}
		switch opts.outputMode {
		case OutputModeJSON:
			enc := json.NewEncoder(deps.stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(outputPayload(bundle, comparison)); err != nil {
				_, _ = fmt.Fprintf(deps.stderr, "encode json failed: %v\n", err)
				return 1
			}
		case OutputModeMarkdown:
			_, _ = fmt.Fprint(deps.stdout, renderMarkdownOutput(bundle, comparison))
		case OutputModeBoth:
			_, _ = fmt.Fprint(deps.stdout, renderMarkdownOutput(bundle, comparison))
			enc := json.NewEncoder(deps.stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(outputPayload(bundle, comparison)); err != nil {
				_, _ = fmt.Fprintf(deps.stderr, "encode json failed: %v\n", err)
				return 1
			}
		}
		return exitCodeForResult(opts.exitMode, bundle)
	}

	var aggregate *comparepkg.AggregateReport
	if len(comparisons) > 0 {
		summary := comparepkg.AggregateReports(comparisons)
		aggregate = &summary
	}
	switch opts.outputMode {
	case OutputModeJSON:
		enc := json.NewEncoder(deps.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(outputMultiTargetPayload(bundles, comparisons, aggregate)); err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "encode json failed: %v\n", err)
			return 1
		}
	case OutputModeMarkdown:
		_, _ = fmt.Fprint(deps.stdout, renderMultiTargetMarkdownOutput(bundles, comparisons, aggregate))
	case OutputModeBoth:
		_, _ = fmt.Fprint(deps.stdout, renderMultiTargetMarkdownOutput(bundles, comparisons, aggregate))
		enc := json.NewEncoder(deps.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(outputMultiTargetPayload(bundles, comparisons, aggregate)); err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "encode json failed: %v\n", err)
			return 1
		}
	}

	if len(bundles) > 0 {
		for _, bundle := range bundles {
			if code := exitCodeForResult(opts.exitMode, bundle); code != 0 {
				return code
			}
		}
	}
	return 0
}

func finalStatusState(bundle core.ReviewBundle) string {
	switch strings.ToLower(strings.TrimSpace(bundle.Verdict)) {
	case "failed", "request_changes", "requested_changes":
		return "failed"
	default:
		if blockingFindings(bundle) > 0 {
			return "failed"
		}
		return "success"
	}
}

func blockingFindings(bundle core.ReviewBundle) int {
	count := 0
	for _, candidate := range bundle.PublishCandidates {
		if candidate.Kind == "finding" {
			count++
		}
	}
	return count
}

type runOutput struct {
	Review            core.ReviewBundle                   `json:"review"`
	JudgeVerdict      string                              `json:"judge_verdict,omitempty"`
	AdvisorArtifact   *core.ReviewerArtifact              `json:"advisor_artifact,omitempty"`
	Comparison        *comparepkg.Report                  `json:"comparison,omitempty"`
	DecisionBenchmark *comparepkg.DecisionBenchmarkReport `json:"decision_benchmark,omitempty"`
}

type multiRunOutput struct {
	Reviews             []core.ReviewBundle           `json:"reviews"`
	Comparisons         []comparepkg.Report           `json:"comparisons,omitempty"`
	AggregateComparison *comparepkg.AggregateReport   `json:"aggregate_comparison,omitempty"`
}

func outputPayload(bundle core.ReviewBundle, comparison *comparepkg.Report) any {
	decisionBenchmark := comparepkg.BuildDecisionBenchmarkReportForBundle(bundle, bundle.AdvisorArtifact)
	if comparison == nil && bundle.AdvisorArtifact == nil {
		return bundle
	}
	return runOutput{
		Review:            bundle,
		JudgeVerdict:      bundle.Verdict,
		AdvisorArtifact:   bundle.AdvisorArtifact,
		Comparison:        comparison,
		DecisionBenchmark: &decisionBenchmark,
	}
}

func renderMarkdownOutput(bundle core.ReviewBundle, comparison *comparepkg.Report) string {
	var out strings.Builder
	out.WriteString(bundle.MarkdownSummary)
	out.WriteString("\n")
	if bundle.AdvisorArtifact != nil && strings.TrimSpace(bundle.AdvisorArtifact.Summary) != "" {
		out.WriteString("\n## Advisor\n\n")
		out.WriteString(bundle.AdvisorArtifact.Summary)
		out.WriteString("\n")
	}
	if comparison != nil {
		if summary := comparepkg.RenderMarkdown(*comparison); summary != "" {
			out.WriteString("\n")
			out.WriteString(summary)
			out.WriteString("\n")
		}
	}
	return out.String()
}

func outputMultiTargetPayload(bundles []core.ReviewBundle, comparisons []comparepkg.Report, aggregate *comparepkg.AggregateReport) any {
	return multiRunOutput{
		Reviews:             bundles,
		Comparisons:         comparisons,
		AggregateComparison: aggregate,
	}
}

func renderMultiTargetMarkdownOutput(bundles []core.ReviewBundle, comparisons []comparepkg.Report, aggregate *comparepkg.AggregateReport) string {
	var out strings.Builder
	for i, bundle := range bundles {
		if i > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(bundle.MarkdownSummary)
		out.WriteString("\n")
		if i < len(comparisons) {
			if summary := comparepkg.RenderMarkdown(comparisons[i]); summary != "" {
				out.WriteString("\n")
				out.WriteString(summary)
				out.WriteString("\n")
			}
		}
	}
	if aggregate != nil {
		if summary := comparepkg.RenderAggregateMarkdown(*aggregate); summary != "" {
			out.WriteString("\n")
			out.WriteString(summary)
			out.WriteString("\n")
		}
	}
	return out.String()
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
	opts := cliOptions{configPath: "config.yaml", exitMode: "never"}
	var packs string
	var compareLive string
	var compareArtifacts string
	var targets string
	output := string(OutputModeBoth)
	publish := string(PublishModeFullReviewComments)
	fs.StringVar(&opts.target, "target", "", "GitHub/GitLab PR URL")
	fs.StringVar(&targets, "targets", "", "comma separated GitHub/GitLab PR URLs")
	fs.StringVar(&opts.configPath, "config", "config.yaml", "Path to config file")
	fs.StringVar(&output, "output", output, "markdown|json|both")
	fs.StringVar(&publish, "publish", publish, "full-review-comments|summary-only|artifact-only")
	fs.StringVar(&opts.exitMode, "exit-mode", "never", "never|requested_changes")
	fs.StringVar(&packs, "reviewer-packs", "", "comma separated reviewer packs")
	fs.StringVar(&opts.routeOverride, "route", "", "provider route override")
	fs.StringVar(&opts.advisorRoute, "advisor-route", "", "optional stronger second-opinion provider route")
	fs.StringVar(&compareLive, "compare-live", "", "comma separated live reviewers to compare")
	fs.StringVar(&compareArtifacts, "compare-artifacts", "", "comma separated external artifact json paths")
	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if targets != "" {
		for _, part := range strings.Split(targets, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				opts.targets = append(opts.targets, part)
			}
		}
	}
	if strings.TrimSpace(opts.target) != "" {
		opts.targets = append([]string{strings.TrimSpace(opts.target)}, opts.targets...)
	}
	if len(opts.targets) == 0 {
		return cliOptions{}, fmt.Errorf("--target or --targets is required")
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
	switch strings.TrimSpace(opts.exitMode) {
	case "never", "requested_changes":
	default:
		return cliOptions{}, fmt.Errorf("--exit-mode must be one of never, requested_changes")
	}
	if packs != "" {
		for _, part := range strings.Split(packs, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				opts.reviewerPacks = append(opts.reviewerPacks, part)
			}
		}
	}
	opts.advisorRoute = strings.TrimSpace(opts.advisorRoute)
	if compareLive != "" {
		for _, part := range strings.Split(compareLive, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				opts.compareLiveReviewers = append(opts.compareLiveReviewers, part)
			}
		}
	}
	if compareArtifacts != "" {
		for _, part := range strings.Split(compareArtifacts, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				opts.compareArtifactPaths = append(opts.compareArtifactPaths, part)
			}
		}
	}
	return opts, nil
}

func exitCodeForResult(mode string, bundle core.ReviewBundle) int {
	switch strings.TrimSpace(mode) {
	case "requested_changes":
		switch strings.ToLower(strings.TrimSpace(bundle.Verdict)) {
		case "requested_changes", "request_changes", "failed":
			return 3
		}
	}
	return 0
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
