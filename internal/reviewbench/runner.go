package reviewbench

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
	"github.com/mreviewer/mreviewer/internal/reviewinput"
)

type Engine interface {
	Run(ctx context.Context, input core.ReviewInput, opts core.RunOptions) (core.ReviewBundle, error)
}

type InputBuilder interface {
	Build(ctx context.Context, input reviewinput.BuildInput) (core.ReviewInput, error)
}

type Options struct {
	Route         string
	ReviewerPacks []string
	LineWindow    int
	Concurrency   int
	// Naturalness scores at most this many findings per chinese/golden case
	// (0 disables; needs a Judge).
	NaturalnessPerCase int
	// External replaces running the engine: findings per case id.
	External ExternalFindings
}

type CaseResult struct {
	CaseID       string        `json:"case_id"`
	Kind         string        `json:"kind"`
	Language     string        `json:"language,omitempty"`
	ChangedLines int           `json:"changed_lines,omitempty"`
	Golden       int           `json:"golden"`
	Findings     int           `json:"findings"`
	OutOfDiff    int           `json:"out_of_diff,omitempty"`
	TruePositive int           `json:"true_positive"`
	FalsePos     int           `json:"false_positive"`
	NegativeHits int           `json:"negative_hits"`
	TokensTotal  int64         `json:"tokens_total"`
	DurationMs   int64         `json:"duration_ms"`
	Verdict      string        `json:"verdict,omitempty"`
	Coverage     float64       `json:"coverage_ratio,omitempty"`
	JudgeCalls   int           `json:"judge_calls,omitempty"`
	JudgeErrors  int           `json:"judge_errors,omitempty"`
	Naturalness  []int         `json:"naturalness_scores,omitempty"`
	TermsBroken  int           `json:"code_terms_broken,omitempty"`
	Error        string        `json:"error,omitempty"`
	Matches      []MatchRecord `json:"matches,omitempty"`
	// RawFindings keeps what the engine reported so a report can be
	// re-scored offline (other judge, other window) via --findings.
	RawFindings []Finding `json:"raw_findings,omitempty"`
}

type Metrics struct {
	Cases        int     `json:"cases"`
	Golden       int     `json:"golden"`
	Findings     int     `json:"findings"`
	TruePositive int     `json:"true_positive"`
	Precision    float64 `json:"precision"`
	Recall       float64 `json:"recall"`
	F1           float64 `json:"f1"`
	FindingsPer  float64 `json:"findings_per_case"`
	TokensPer    float64 `json:"tokens_per_case"`
	NegativeHits int     `json:"negative_hits"`
}

type ControlMetrics struct {
	Cases       int     `json:"cases"`
	Findings    int     `json:"findings"` // every one is a false positive
	FindingsPer float64 `json:"findings_per_case"`
}

type NaturalnessMetrics struct {
	Scored      int     `json:"scored"`
	Mean        float64 `json:"mean"`
	TermsBroken int     `json:"code_terms_broken"`
}

type Report struct {
	GeneratedAt time.Time           `json:"generated_at"`
	Route       string              `json:"route,omitempty"`
	Packs       []string            `json:"packs,omitempty"`
	JudgeModel  string              `json:"judge_model,omitempty"`
	LineWindow  int                 `json:"line_window"`
	External    bool                `json:"external,omitempty"`
	Totals      Metrics             `json:"totals"`
	ByLanguage  map[string]Metrics  `json:"by_language,omitempty"`
	Control     ControlMetrics      `json:"control"`
	Naturalness *NaturalnessMetrics `json:"naturalness,omitempty"`
	Cases       []CaseResult        `json:"cases"`
	Errors      int                 `json:"errors"`
}

type Runner struct {
	Engine  Engine
	Builder InputBuilder
	Judge   Judge
	Opts    Options
}

func (r *Runner) Run(ctx context.Context, cases []Case) (Report, error) {
	if r.Opts.External == nil && (r.Engine == nil || r.Builder == nil) {
		return Report{}, fmt.Errorf("reviewbench: engine and builder are required unless external findings are given")
	}
	concurrency := r.Opts.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	results := make([]CaseResult, len(cases))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := range cases {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = r.runCase(ctx, cases[i])
		}(i)
	}
	wg.Wait()

	report := Report{
		GeneratedAt: time.Now().UTC(),
		Route:       r.Opts.Route,
		Packs:       r.Opts.ReviewerPacks,
		LineWindow:  r.Opts.LineWindow,
		External:    r.Opts.External != nil,
		ByLanguage:  map[string]Metrics{},
		Cases:       results,
	}
	if r.Opts.LineWindow <= 0 {
		report.LineWindow = DefaultLineWindow
	}
	if named, ok := r.Judge.(interface{ Model() string }); ok && r.Judge != nil {
		report.JudgeModel = named.Model()
	}
	var golden []CaseResult
	for _, res := range results {
		if res.Error != "" {
			report.Errors++
			continue
		}
		if res.Kind == KindControl {
			report.Control.Cases++
			report.Control.Findings += res.Findings
			continue
		}
		golden = append(golden, res)
	}
	report.Totals = aggregate(golden)
	byLang := map[string][]CaseResult{}
	for _, res := range golden {
		lang := res.Language
		if lang == "" {
			lang = "unknown"
		}
		byLang[lang] = append(byLang[lang], res)
	}
	for lang, rs := range byLang {
		report.ByLanguage[lang] = aggregate(rs)
	}
	if report.Control.Cases > 0 {
		report.Control.FindingsPer = float64(report.Control.Findings) / float64(report.Control.Cases)
	}
	var nat NaturalnessMetrics
	sum := 0
	for _, res := range results {
		for _, s := range res.Naturalness {
			nat.Scored++
			sum += s
		}
		nat.TermsBroken += res.TermsBroken
	}
	if nat.Scored > 0 {
		nat.Mean = float64(sum) / float64(nat.Scored)
		report.Naturalness = &nat
	}
	return report, nil
}

func aggregate(rs []CaseResult) Metrics {
	m := Metrics{Cases: len(rs)}
	var tokens int64
	for _, r := range rs {
		m.Golden += r.Golden
		m.Findings += r.Findings
		m.TruePositive += r.TruePositive
		m.NegativeHits += r.NegativeHits
		tokens += r.TokensTotal
	}
	if m.Findings > 0 {
		m.Precision = float64(m.TruePositive) / float64(m.Findings)
	}
	if m.Golden > 0 {
		m.Recall = float64(m.TruePositive) / float64(m.Golden)
	}
	if m.Precision+m.Recall > 0 {
		m.F1 = 2 * m.Precision * m.Recall / (m.Precision + m.Recall)
	}
	if m.Cases > 0 {
		m.FindingsPer = float64(m.Findings) / float64(m.Cases)
		m.TokensPer = float64(tokens) / float64(m.Cases)
	}
	return m
}

func (r *Runner) runCase(ctx context.Context, c Case) CaseResult {
	res := CaseResult{CaseID: c.ID, Kind: c.Kind, Language: c.PR.Language, ChangedLines: c.PR.ChangedLines, Golden: len(c.Golden)}
	start := time.Now()
	var findings []Finding
	if r.Opts.External != nil {
		findings = r.Opts.External[c.ID]
	} else {
		input, err := r.Builder.Build(ctx, reviewinput.BuildInput{Snapshot: c.Snapshot()})
		if err != nil {
			res.Error = "build input: " + err.Error()
			return res
		}
		bundle, err := r.Engine.Run(ctx, input, core.RunOptions{
			OutputMode:    "json",
			PublishMode:   "artifact-only",
			ReviewerPacks: r.Opts.ReviewerPacks,
			RouteOverride: r.Opts.Route,
		})
		if err != nil {
			res.Error = "engine: " + err.Error()
			return res
		}
		findings = FindingsFromBundle(bundle)
		res.Verdict = bundle.Verdict
		if bundle.Coverage != nil {
			res.Coverage = bundle.Coverage.Ratio
		}
		for _, artifact := range bundle.Artifacts {
			res.TokensTotal += artifact.TokensTotal
		}
	}
	res.DurationMs = time.Since(start).Milliseconds()
	res.Findings = len(findings)
	res.RawFindings = findings
	for _, f := range findings {
		if f.OutOfDiff {
			res.OutOfDiff++
		}
	}
	m := matchCase(ctx, r.Judge, c, findings, r.Opts.LineWindow)
	res.TruePositive = m.TruePositive
	res.NegativeHits = m.NegativeHits
	res.FalsePos = res.Findings - res.TruePositive
	res.JudgeCalls, res.JudgeErrors = m.JudgeCalls, m.JudgeErrors
	res.Matches = m.Records

	if r.Judge != nil && r.Opts.NaturalnessPerCase > 0 && c.Kind != KindControl {
		for i, f := range findings {
			if i >= r.Opts.NaturalnessPerCase {
				break
			}
			if strings.TrimSpace(f.Body) == "" && strings.TrimSpace(f.Title) == "" {
				continue
			}
			score, err := r.Judge.Naturalness(ctx, f)
			if err != nil {
				res.JudgeErrors++
				continue
			}
			res.Naturalness = append(res.Naturalness, score.Score)
			if !score.CodeTermsPreserved {
				res.TermsBroken++
			}
		}
	}
	sort.Slice(res.Matches, func(i, j int) bool { return res.Matches[i].FindingIndex < res.Matches[j].FindingIndex })
	return res
}
