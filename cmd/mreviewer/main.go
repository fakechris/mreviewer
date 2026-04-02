package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	comparepkg "github.com/mreviewer/mreviewer/internal/compare"
	platformgithub "github.com/mreviewer/mreviewer/internal/platform/github"
	platformgitlab "github.com/mreviewer/mreviewer/internal/platform/gitlab"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/textutil"
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
	target               string
	targets              []string
	configPath           string
	outputMode           OutputMode
	publishMode          PublishMode
	exitMode             string
	reviewerPacks        []string
	routeOverride        string
	advisorRoute         string
	compareLiveReviewers []string
	compareArtifactPaths []string
	dryRun               bool
	verbose              int
}

func main() {
	os.Exit(runCLI(os.Args[1:], runtimeDeps{
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

func runCLI(args []string, reviewDeps runtimeDeps) int {
	if reviewDeps.stdout == nil {
		reviewDeps.stdout = os.Stdout
	}
	if reviewDeps.stderr == nil {
		reviewDeps.stderr = os.Stderr
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printTopLevelHelp(reviewDeps.stdout)
		return 0
	}
	if args[0] == "--version" {
		printVersion(reviewDeps.stdout)
		return 0
	}
	if strings.HasPrefix(args[0], "-") {
		return runWithDeps(args, reviewDeps)
	}
	switch strings.TrimSpace(args[0]) {
	case "review":
		return runWithDeps(args[1:], reviewDeps)
	case "init":
		return runInitCommand(args[1:], reviewDeps.stdout, reviewDeps.stderr)
	case "doctor":
		return runDoctorCommand(args[1:], reviewDeps.stdout, reviewDeps.stderr)
	case "serve":
		return runServeCommand(args[1:], reviewDeps.stdout, reviewDeps.stderr)
	case "version":
		printVersion(reviewDeps.stdout)
		return 0
	case "help":
		return runHelpCommand(args[1:], reviewDeps)
	default:
		return runWithDeps(args, reviewDeps)
	}
}

func runHelpCommand(args []string, deps runtimeDeps) int {
	if len(args) == 0 {
		printTopLevelHelp(deps.stdout)
		return 0
	}
	switch strings.TrimSpace(args[0]) {
	case "review":
		return runWithDeps([]string{"--help"}, deps)
	case "init":
		return runInitCommand([]string{"--help"}, deps.stdout, deps.stderr)
	case "doctor":
		return runDoctorCommand([]string{"--help"}, deps.stdout, deps.stderr)
	case "serve":
		return runServeCommand([]string{"--help"}, deps.stdout, deps.stderr)
	case "version":
		printVersion(deps.stdout)
		return 0
	default:
		_, _ = fmt.Fprintf(deps.stderr, "unknown help topic %q\n", strings.TrimSpace(args[0]))
		return 2
	}
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
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if opts.dryRun {
		cliTracef(deps.stderr, opts.verbose, 1, "cli: dry-run enabled; publish and status updates are disabled")
	}
	var bundles []core.ReviewBundle
	var comparisons []comparepkg.Report
	for _, rawTarget := range opts.targets {
		target, err := deps.resolveTarget(rawTarget)
		if err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "resolve target failed: %v\n", err)
			return 1
		}
		cliTracef(deps.stderr, opts.verbose, 2, "cli: resolved target %s (%s #%d)", target.URL, target.Platform, target.ChangeNumber)
		input, err := deps.loadInput(context.Background(), opts.configPath, target)
		if err != nil {
			_, _ = fmt.Fprintf(deps.stderr, "build input failed: %v\n", err)
			return 1
		}
		cliTracef(deps.stderr, opts.verbose, 3, "cli: loaded input for %s using config %s", target.URL, opts.configPath)
		if deps.status != nil && !opts.dryRun {
			if err := deps.status(context.Background(), opts.configPath, target, input, "running", 0); err != nil {
				_, _ = fmt.Fprintf(deps.stderr, "status failed: %v\n", err)
			}
		}

		cliTracef(deps.stderr, opts.verbose, 2, "cli: running review engine (route=%s advisor=%s packs=%s publish=%s)", opts.routeOverride, opts.advisorRoute, strings.Join(opts.reviewerPacks, ","), opts.publishMode)
		bundle, err := deps.newEngine(opts.configPath).Run(context.Background(), input, core.RunOptions{
			OutputMode:    string(opts.outputMode),
			PublishMode:   string(opts.publishMode),
			ReviewerPacks: opts.reviewerPacks,
			RouteOverride: opts.routeOverride,
			AdvisorRoute:  opts.advisorRoute,
		})
		if err != nil {
			if deps.status != nil && !opts.dryRun {
				if statusErr := deps.status(context.Background(), opts.configPath, target, input, "failed", 0); statusErr != nil {
					_, _ = fmt.Fprintf(deps.stderr, "status failed: %v\n", statusErr)
				}
			}
			_, _ = fmt.Fprintf(deps.stderr, "review failed: %v\n", err)
			return 1
		}
		bundles = append(bundles, bundle)

		if deps.compare != nil && (len(opts.compareLiveReviewers) > 0 || len(opts.compareArtifactPaths) > 0) {
			cliTracef(deps.stderr, opts.verbose, 3, "cli: running comparison for %s", target.URL)
			comparison, err := deps.compare(context.Background(), opts.configPath, target, bundle, opts)
			if err != nil {
				_, _ = fmt.Fprintf(deps.stderr, "compare failed: %v\n", err)
				return 1
			}
			if comparison != nil {
				comparisons = append(comparisons, *comparison)
			}
		}
		if deps.publish != nil && opts.publishMode != PublishModeArtifactOnly && !opts.dryRun {
			publishBundle := bundle
			if opts.publishMode == PublishModeSummaryOnly {
				publishBundle = summaryOnlyBundle(bundle)
			}
			if err := deps.publish(context.Background(), opts.configPath, target, publishBundle); err != nil {
				if deps.status != nil && !opts.dryRun {
					if statusErr := deps.status(context.Background(), opts.configPath, target, input, "failed", 0); statusErr != nil {
						_, _ = fmt.Fprintf(deps.stderr, "status failed: %v\n", statusErr)
					}
				}
				_, _ = fmt.Fprintf(deps.stderr, "publish failed: %v\n", err)
				return 1
			}
		}
		if deps.status != nil && !opts.dryRun {
			if err := deps.status(context.Background(), opts.configPath, target, input, finalStatusState(bundle), blockingFindings(bundle)); err != nil {
				_, _ = fmt.Fprintf(deps.stderr, "status failed: %v\n", err)
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
	Reviews             []core.ReviewBundle         `json:"reviews"`
	Comparisons         []comparepkg.Report         `json:"comparisons,omitempty"`
	AggregateComparison *comparepkg.AggregateReport `json:"aggregate_comparison,omitempty"`
}

type reviewBrief struct {
	Verdict           string           `json:"verdict,omitempty"`
	ActionItems       []briefAction    `json:"action_items,omitempty"`
	SpecialistSignals []briefSignal    `json:"specialist_signals,omitempty"`
	Comparison        *briefComparison `json:"comparison,omitempty"`
}

type briefAction struct {
	Title    string `json:"title,omitempty"`
	Severity string `json:"severity,omitempty"`
	Body     string `json:"body,omitempty"`
}

type briefSignal struct {
	ReviewerID string `json:"reviewer_id,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

type briefComparison struct {
	ReviewerCount      int     `json:"reviewer_count,omitempty"`
	UniqueFindingCount int     `json:"unique_finding_count,omitempty"`
	AgreementRate      float64 `json:"agreement_rate,omitempty"`
}

type aggregateReviewBrief struct {
	TargetCount          int     `json:"target_count,omitempty"`
	RequestedChanges     int     `json:"requested_changes,omitempty"`
	AverageAgreementRate float64 `json:"average_agreement_rate,omitempty"`
}

func outputPayload(bundle core.ReviewBundle, comparison *comparepkg.Report) any {
	payload := bundlePayloadMap(bundle)
	payload["review_brief"] = buildReviewBrief(bundle, comparison)
	if strings.TrimSpace(bundle.Verdict) != "" {
		payload["judge_verdict"] = bundle.Verdict
	}
	decisionBenchmark := comparepkg.BuildDecisionBenchmarkReportForBundle(bundle, bundle.AdvisorArtifact)
	payload["decision_benchmark"] = decisionBenchmark
	if bundle.AdvisorArtifact != nil {
		payload["advisor_artifact"] = bundle.AdvisorArtifact
	}
	if comparison != nil {
		payload["comparison"] = comparison
	}
	return payload
}

func renderMarkdownOutput(bundle core.ReviewBundle, comparison *comparepkg.Report) string {
	var out strings.Builder
	out.WriteString(renderDecisionBriefMarkdown(bundle, comparison))
	out.WriteString("\n")
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
	payload := multiRunOutput{
		Reviews:             bundles,
		Comparisons:         comparisons,
		AggregateComparison: aggregate,
	}
	out := bundlePayloadMap(core.ReviewBundle{})
	delete(out, "target")
	delete(out, "markdown_summary")
	delete(out, "json_schema_version")
	delete(out, "publish_candidates")
	delete(out, "artifacts")
	delete(out, "advisor_artifact")
	delete(out, "verdict")
	for k := range out {
		delete(out, k)
	}
	out["reviews"] = payload.Reviews
	if len(payload.Comparisons) > 0 {
		out["comparisons"] = payload.Comparisons
	}
	if payload.AggregateComparison != nil {
		out["aggregate_comparison"] = payload.AggregateComparison
		out["aggregate_review_brief"] = buildAggregateReviewBrief(bundles, payload.AggregateComparison)
	}
	return out
}

func renderMultiTargetMarkdownOutput(bundles []core.ReviewBundle, comparisons []comparepkg.Report, aggregate *comparepkg.AggregateReport) string {
	var out strings.Builder
	if aggregate != nil {
		out.WriteString(renderAggregateDecisionBriefMarkdown(bundles, aggregate))
		out.WriteString("\n\n")
	}
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
		if candidate.Kind == "summary" || candidate.PublishAsSummary {
			filtered.PublishCandidates = append(filtered.PublishCandidates, candidate)
		}
	}
	return filtered
}

func parseOptions(args []string, stderr io.Writer) (cliOptions, error) {
	cleanedArgs, commonFlags, err := extractCommonCLIFlags(args)
	if err != nil {
		return cliOptions{}, err
	}
	fs := flag.NewFlagSet("mreviewer", flag.ContinueOnError)
	fs.SetOutput(stderr)
	setFlagSetUsage(fs, `
Usage: mreviewer review --target <url> [options]

Review a GitHub pull request or GitLab merge request and emit markdown/json artifacts.

Agent-friendly flags: --dry-run (alias: --dryrun), --verbose, -vv, -vvv, -vvvv

Examples:
  mreviewer review --target https://github.com/acme/repo/pull/17
  mreviewer review --target https://gitlab.example.com/group/repo/-/merge_requests/23 --output json
  mreviewer review --target https://github.com/acme/repo/pull/17 --dry-run -vv
`)
	opts := cliOptions{configPath: "config.yaml", exitMode: "never", verbose: commonFlags.verbose}
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
	fs.StringVar(&opts.routeOverride, "route", "", "model or model-chain override")
	fs.StringVar(&opts.advisorRoute, "advisor-route", "", "optional stronger second-opinion model or model-chain override")
	fs.StringVar(&compareLive, "compare-live", "", "comma separated live reviewers to compare")
	fs.StringVar(&compareArtifacts, "compare-artifacts", "", "comma separated external artifact json paths")
	fs.BoolVar(&opts.dryRun, "dry-run", false, "Resolve and render without publish or status side effects")
	fs.BoolVar(&opts.dryRun, "dryrun", false, "Alias for --dry-run")
	fs.Bool("verbose", false, "Increase detail; repeat -vv/-vvv/-vvvv for debug traces")
	if err := fs.Parse(cleanedArgs); err != nil {
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
	if opts.dryRun {
		opts.publishMode = PublishModeArtifactOnly
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

func bundlePayloadMap(bundle core.ReviewBundle) map[string]any {
	data, _ := json.Marshal(bundle)
	var payload map[string]any
	_ = json.Unmarshal(data, &payload)
	if payload == nil {
		payload = map[string]any{}
	}
	return payload
}

func buildReviewBrief(bundle core.ReviewBundle, comparison *comparepkg.Report) reviewBrief {
	brief := reviewBrief{Verdict: bundle.Verdict}
	for _, candidate := range bundle.PublishCandidates {
		if candidate.Kind != "finding" {
			continue
		}
		brief.ActionItems = append(brief.ActionItems, briefAction{
			Title:    strings.TrimSpace(candidate.Title),
			Severity: strings.TrimSpace(candidate.Severity),
			Body:     strings.TrimSpace(candidate.Body),
		})
		if len(brief.ActionItems) >= 5 {
			break
		}
	}
	for _, artifact := range bundle.Artifacts {
		if strings.TrimSpace(artifact.Summary) == "" {
			continue
		}
		brief.SpecialistSignals = append(brief.SpecialistSignals, briefSignal{
			ReviewerID: strings.TrimSpace(artifact.ReviewerID),
			Summary:    strings.TrimSpace(artifact.Summary),
		})
	}
	if bundle.AdvisorArtifact != nil && strings.TrimSpace(bundle.AdvisorArtifact.Summary) != "" {
		brief.SpecialistSignals = append(brief.SpecialistSignals, briefSignal{
			ReviewerID: strings.TrimSpace(bundle.AdvisorArtifact.ReviewerID),
			Summary:    strings.TrimSpace(bundle.AdvisorArtifact.Summary),
		})
	}
	if comparison != nil {
		brief.Comparison = &briefComparison{
			ReviewerCount:      comparison.ReviewerCount,
			UniqueFindingCount: comparison.UniqueFindingCount,
			AgreementRate:      comparison.AgreementRate,
		}
	}
	return brief
}

func buildAggregateReviewBrief(bundles []core.ReviewBundle, aggregate *comparepkg.AggregateReport) aggregateReviewBrief {
	brief := aggregateReviewBrief{TargetCount: len(bundles)}
	if aggregate != nil {
		brief.AverageAgreementRate = aggregate.AverageAgreementRate
	}
	for _, bundle := range bundles {
		switch strings.ToLower(strings.TrimSpace(bundle.Verdict)) {
		case "requested_changes", "request_changes", "failed":
			brief.RequestedChanges++
		}
	}
	return brief
}

func renderDecisionBriefMarkdown(bundle core.ReviewBundle, comparison *comparepkg.Report) string {
	brief := buildReviewBrief(bundle, comparison)
	var out strings.Builder
	out.WriteString("# Review Decision Brief\n\n")
	out.WriteString("## Final Verdict\n\n")
	if strings.TrimSpace(brief.Verdict) == "" {
		out.WriteString("unknown\n")
	} else {
		out.WriteString(brief.Verdict)
		out.WriteString("\n")
	}
	out.WriteString("\n## What To Fix First\n\n")
	if len(brief.ActionItems) == 0 {
		out.WriteString("- No actionable findings from the current council run.\n")
	} else {
		for _, item := range brief.ActionItems {
			line := "- "
			if item.Severity != "" {
				line += "[" + item.Severity + "] "
			}
			line += sanitizeActionLabel(item.Title, item.Body)
			out.WriteString(line)
			out.WriteString("\n")
		}
	}
	if len(brief.SpecialistSignals) > 0 {
		out.WriteString("\n## Specialist Signals\n\n")
		for _, signal := range brief.SpecialistSignals {
			out.WriteString("- ")
			out.WriteString(textutil.FirstNonEmpty(signal.ReviewerID, "reviewer"))
			out.WriteString(": ")
			out.WriteString(signal.Summary)
			out.WriteString("\n")
		}
	}
	if brief.Comparison != nil {
		out.WriteString("\n## Reviewer Overlap\n\n")
		out.WriteString(fmt.Sprintf("- Reviewers: %d\n", brief.Comparison.ReviewerCount))
		out.WriteString(fmt.Sprintf("- Unique findings: %d\n", brief.Comparison.UniqueFindingCount))
		out.WriteString(fmt.Sprintf("- Agreement rate: %.2f\n", brief.Comparison.AgreementRate))
	}
	return strings.TrimSpace(out.String())
}

func renderAggregateDecisionBriefMarkdown(bundles []core.ReviewBundle, aggregate *comparepkg.AggregateReport) string {
	brief := buildAggregateReviewBrief(bundles, aggregate)
	var out strings.Builder
	out.WriteString("# Portfolio Review Brief\n\n")
	out.WriteString(fmt.Sprintf("- Targets: %d\n", brief.TargetCount))
	out.WriteString(fmt.Sprintf("- Requested changes: %d\n", brief.RequestedChanges))
	out.WriteString(fmt.Sprintf("- Average agreement rate: %.2f\n", brief.AverageAgreementRate))
	return strings.TrimSpace(out.String())
}

func sanitizeActionLabel(title, body string) string {
	label := strings.TrimSpace(title)
	if label == "" {
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) != "" {
				label = strings.TrimSpace(line)
				break
			}
		}
	}
	if label == "" {
		return ""
	}
	label = regexp.MustCompile(`^[#>\-\*\s`+"`"+`]+`).ReplaceAllString(label, "")
	label = strings.Join(strings.Fields(label), " ")
	return strings.TrimSpace(label)
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
