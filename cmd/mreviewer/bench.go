package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mreviewer/mreviewer/internal/config"
	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
	"github.com/mreviewer/mreviewer/internal/reviewbench"
	"github.com/mreviewer/mreviewer/internal/reviewinput"
	"github.com/mreviewer/mreviewer/internal/rules"
)

const defaultBenchCasesDir = "testdata/review-bench"

// runBenchCommand implements `mreviewer bench review`: score a route against
// the offline review corpus and print precision / recall / F1, findings and
// tokens per case, control-group false positives and Chinese naturalness.
func runBenchCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.TrimSpace(args[0]) != "review" {
		if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
			printBenchHelp(stdout)
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "usage: mreviewer bench review [options]")
		return 2
	}
	fs := flag.NewFlagSet("bench review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		configPath  = fs.String("config", "config.yaml", "config file")
		route       = fs.String("route", "", "review route (model or model chain); default from config review.model_chain")
		packs       = fs.String("packs", "", "comma-separated reviewer packs; default all")
		judgeRoute  = fs.String("judge-route", "", "OpenAI-compatible model route used as LLM judge; empty = line-window matching only")
		casesDir    = fs.String("cases-dir", defaultBenchCasesDir, "corpus root containing */case.json")
		since       = fs.String("since", "", "only cases added on or after this date (YYYY-MM-DD)")
		findings    = fs.String("findings", "", "score an external findings JSON ({case_id: [findings]}) instead of running the engine")
		output      = fs.String("output", "markdown", "markdown|json|both")
		outFile     = fs.String("out", "", "also write the JSON report to this file")
		window      = fs.Int("window", reviewbench.DefaultLineWindow, "line window for matching findings to labelled comments")
		concurrency = fs.Int("concurrency", 2, "cases reviewed in parallel")
		naturalness = fs.Int("naturalness", 5, "findings per case scored for Chinese naturalness (needs --judge-route); 0 disables")
	)
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	cases, err := reviewbench.LoadCases(*casesDir, *since)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load cases: %v\n", err)
		return 1
	}
	if len(cases) == 0 {
		_, _ = fmt.Fprintf(stderr, "no cases under %s\n", *casesDir)
		return 1
	}

	runner := &reviewbench.Runner{Opts: reviewbench.Options{
		Route:              strings.TrimSpace(*route),
		ReviewerPacks:      splitCSVArg(*packs),
		LineWindow:         *window,
		Concurrency:        *concurrency,
		NaturalnessPerCase: *naturalness,
	}}

	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*findings) != "" {
		external, err := reviewbench.LoadExternalFindings(*findings)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "load findings: %v\n", err)
			return 1
		}
		runner.Opts.External = external
	} else {
		if runner.Opts.Route == "" {
			runner.Opts.Route = strings.TrimSpace(cfg.Review.ModelChain)
		}
		defaultRoute, _, _, err := providerConfigsFromConfig(cfg)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "resolve provider routes: %v\n", err)
			return 1
		}
		runner.Engine = defaultReviewEngine(*configPath)
		runner.Builder = reviewinput.NewBuilder(
			rules.NewLoader(nil, benchPlatformDefaults(defaultRoute)),
			ctxpkg.NewAssembler(),
			nil,
		)
	}
	if strings.TrimSpace(*judgeRoute) != "" {
		judge, err := judgeFromConfig(cfg, *judgeRoute)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "judge: %v\n", err)
			return 1
		}
		runner.Judge = judge
	}

	_, _ = fmt.Fprintf(stderr, "bench: %d cases, route=%s judge=%s\n", len(cases), orDash(runner.Opts.Route), orDash(*judgeRoute))
	report, err := runner.Run(context.Background(), cases)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "bench: %v\n", err)
		return 1
	}
	if *outFile != "" {
		data, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(*outFile, data, 0o644); err != nil {
			_, _ = fmt.Fprintf(stderr, "write %s: %v\n", *outFile, err)
			return 1
		}
	}
	switch strings.TrimSpace(*output) {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	case "both":
		_, _ = io.WriteString(stdout, report.Markdown())
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	default:
		_, _ = io.WriteString(stdout, report.Markdown())
	}
	if report.Errors > 0 {
		return 1
	}
	return 0
}

// benchPlatformDefaults is the review policy for corpus cases: the platform
// defaults minus the `src/**` include filter, which would silently drop most
// files of a third-party pull request, and with generous size limits so a
// corpus case is never truncated by policy.
func benchPlatformDefaults(defaultRoute string) rules.PlatformDefaults {
	defaults := defaultPlatformDefaults(defaultRoute, "")
	defaults.IncludePaths = nil
	defaults.ExcludePaths = []string{"vendor/**"}
	return defaults
}

// judgeFromConfig builds the LLM judge from a configured model route. Only
// OpenAI-compatible routes are supported because the judge needs a plain
// chat completion, not the structured review contract.
func judgeFromConfig(cfg *config.Config, route string) (reviewbench.Judge, error) {
	model, ok := cfg.Models[strings.TrimSpace(route)]
	if !ok {
		return nil, fmt.Errorf("model route %q is not configured under models:", route)
	}
	switch strings.ToLower(strings.TrimSpace(model.Provider)) {
	case "openai", "zhipuai", "ark_openai", "deepseek", "openai_compatible":
	default:
		return nil, fmt.Errorf("judge route %q uses provider %q; the judge needs an OpenAI-compatible chat route", route, model.Provider)
	}
	if strings.TrimSpace(model.BaseURL) == "" || strings.TrimSpace(model.Model) == "" {
		return nil, fmt.Errorf("judge route %q needs base_url and model", route)
	}
	judge := reviewbench.NewOpenAIJudge(model.BaseURL, model.APIKey, model.Model)
	if strings.EqualFold(strings.TrimSpace(model.Provider), "deepseek") {
		// A DeepSeek thinking model spends max_tokens on reasoning and returns
		// empty content; the judge only needs the JSON answer.
		judge = judge.WithExtraBody(map[string]any{"thinking": map[string]any{"type": "disabled"}})
	}
	return judge, nil
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func printBenchHelp(stdout io.Writer) {
	_, _ = fmt.Fprintln(stdout, "Usage: mreviewer bench review [options]")
	_, _ = fmt.Fprintln(stdout)
	_, _ = fmt.Fprintln(stdout, "Scores a review route against the offline corpus under testdata/review-bench:")
	_, _ = fmt.Fprintln(stdout, "precision, recall, F1, findings and tokens per case, control-group false positives,")
	_, _ = fmt.Fprintln(stdout, "and Chinese naturalness when a judge route is given.")
	_, _ = fmt.Fprintln(stdout)
	_, _ = fmt.Fprintln(stdout, "  --route <ref>         review route; default review.model_chain")
	_, _ = fmt.Fprintln(stdout, "  --judge-route <ref>   OpenAI-compatible route used as judge; omit for line-window matching only")
	_, _ = fmt.Fprintln(stdout, "  --findings <file>     score another tool's findings JSON instead of running the engine")
	_, _ = fmt.Fprintln(stdout, "  --since <date>        only cases added on/after YYYY-MM-DD")
	_, _ = fmt.Fprintln(stdout, "  --output markdown|json|both, --out <file>, --window N, --concurrency N, --naturalness N")
}
