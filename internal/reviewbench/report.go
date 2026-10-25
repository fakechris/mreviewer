package reviewbench

import (
	"fmt"
	"sort"
	"strings"
)

// Markdown renders the report the way the acceptance docs record it.
func (r Report) Markdown() string {
	var b strings.Builder
	b.WriteString("# Review bench\n\n")
	fmt.Fprintf(&b, "- Generated: %s\n", r.GeneratedAt.Format("2006-01-02 15:04 UTC"))
	if r.External {
		b.WriteString("- Source: external findings\n")
	} else {
		fmt.Fprintf(&b, "- Route: %s\n", orDash(r.Route))
		if len(r.Packs) > 0 {
			fmt.Fprintf(&b, "- Packs: %s\n", strings.Join(r.Packs, ", "))
		}
	}
	fmt.Fprintf(&b, "- Judge: %s\n", orDash(r.JudgeModel))
	fmt.Fprintf(&b, "- Line window: ±%d\n", r.LineWindow)
	if r.Errors > 0 {
		fmt.Fprintf(&b, "- Cases with errors: %d\n", r.Errors)
	}
	b.WriteString("\n## Totals\n\n")
	b.WriteString("| Metric | Value |\n|---|---|\n")
	t := r.Totals
	fmt.Fprintf(&b, "| Cases | %d |\n", t.Cases)
	fmt.Fprintf(&b, "| Golden findings | %d |\n", t.Golden)
	fmt.Fprintf(&b, "| Reported findings | %d |\n", t.Findings)
	fmt.Fprintf(&b, "| True positives | %d |\n", t.TruePositive)
	fmt.Fprintf(&b, "| Precision | %.1f%% |\n", t.Precision*100)
	fmt.Fprintf(&b, "| Recall | %.1f%% |\n", t.Recall*100)
	fmt.Fprintf(&b, "| F1 | %.1f%% |\n", t.F1*100)
	fmt.Fprintf(&b, "| Findings per case | %.1f |\n", t.FindingsPer)
	fmt.Fprintf(&b, "| Tokens per case | %.0f |\n", t.TokensPer)
	fmt.Fprintf(&b, "| Hits on known-incorrect comments | %d |\n", t.NegativeHits)
	if r.Control.Cases > 0 {
		fmt.Fprintf(&b, "| Control cases | %d |\n", r.Control.Cases)
		fmt.Fprintf(&b, "| Control false positives | %d (%.1f per case) |\n", r.Control.Findings, r.Control.FindingsPer)
	}
	if r.Naturalness != nil {
		fmt.Fprintf(&b, "| Chinese naturalness (1-5) | %.2f over %d findings |\n", r.Naturalness.Mean, r.Naturalness.Scored)
		fmt.Fprintf(&b, "| Code terms translated | %d |\n", r.Naturalness.TermsBroken)
	}
	if len(r.ByLanguage) > 0 {
		b.WriteString("\n## By language\n\n| Language | Cases | Golden | Findings | TP | Precision | Recall | F1 |\n|---|---|---|---|---|---|---|---|\n")
		langs := make([]string, 0, len(r.ByLanguage))
		for l := range r.ByLanguage {
			langs = append(langs, l)
		}
		sort.Strings(langs)
		for _, l := range langs {
			m := r.ByLanguage[l]
			fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %.1f%% | %.1f%% | %.1f%% |\n", l, m.Cases, m.Golden, m.Findings, m.TruePositive, m.Precision*100, m.Recall*100, m.F1*100)
		}
	}
	b.WriteString("\n## Cases\n\n| Case | Kind | Lang | Golden | Findings | TP | Neg hits | Tokens | ms | Verdict | Error |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, c := range r.Cases {
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d | %d | %d | %d | %d | %s | %s |\n", c.CaseID, c.Kind, orDash(c.Language), c.Golden, c.Findings, c.TruePositive, c.NegativeHits, c.TokensTotal, c.DurationMs, orDash(c.Verdict), c.Error)
	}
	return b.String()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
