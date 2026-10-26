package reviewbench

import (
	"context"
	"sort"
	"strings"
)

// DefaultLineWindow is how far (in lines) a finding may sit from a labelled
// comment and still be considered for a match. Reviewers often anchor one
// or two lines away from where the expert did.
const DefaultLineWindow = 5

// MatchRecord explains one scored finding.
type MatchRecord struct {
	FindingIndex int    `json:"finding_index"`
	Path         string `json:"path"`
	StartLine    int    `json:"start_line"`
	Title        string `json:"title,omitempty"`
	Outcome      string `json:"outcome"` // true_positive | false_positive | negative_hit
	// Reference is the index into Golden (true_positive) or Negatives (negative_hit).
	Reference int    `json:"reference,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type matchResult struct {
	Records      []MatchRecord
	TruePositive int
	NegativeHits int
	JudgeCalls   int
	JudgeErrors  int
}

type pair struct {
	finding  int
	ref      int
	distance int
}

// matchCase pairs findings with golden comments (true positives) and with
// known-incorrect comments (negative hits). Rule: same path, compatible
// side, line ranges within window; then the judge, when present, confirms
// the pair describes the same issue. Pairs are taken closest first and each
// finding and each reference is used at most once.
func matchCase(ctx context.Context, judge Judge, c Case, findings []Finding, window int) matchResult {
	if window <= 0 {
		window = DefaultLineWindow
	}
	res := matchResult{}
	usedFinding := map[int]bool{}
	usedGolden := map[int]bool{}
	usedNeg := map[int]bool{}
	outcome := make([]MatchRecord, len(findings))
	for i, f := range findings {
		outcome[i] = MatchRecord{FindingIndex: i, Path: f.Path, StartLine: f.StartLine, Title: f.Title, Outcome: "false_positive"}
	}

	for _, p := range candidatePairs(findings, c.Golden, window) {
		if usedFinding[p.finding] || usedGolden[p.ref] {
			continue
		}
		same, reason := true, "line window"
		if judge != nil {
			res.JudgeCalls++
			ok, why, err := judge.SameIssue(ctx, findings[p.finding], c.Golden[p.ref])
			if err != nil {
				res.JudgeErrors++
				ok, why = false, "judge error: "+err.Error()
			}
			same, reason = ok, why
		}
		if !same {
			continue
		}
		usedFinding[p.finding], usedGolden[p.ref] = true, true
		res.TruePositive++
		outcome[p.finding] = MatchRecord{FindingIndex: p.finding, Path: findings[p.finding].Path, StartLine: findings[p.finding].StartLine, Title: findings[p.finding].Title, Outcome: "true_positive", Reference: p.ref, Reason: reason}
	}

	for _, p := range candidatePairs(findings, c.Negatives, window) {
		if usedFinding[p.finding] || usedNeg[p.ref] {
			continue
		}
		same, reason := true, "line window"
		if judge != nil {
			res.JudgeCalls++
			ok, why, err := judge.SameIssue(ctx, findings[p.finding], c.Negatives[p.ref])
			if err != nil {
				res.JudgeErrors++
				ok, why = false, "judge error: "+err.Error()
			}
			same, reason = ok, why
		}
		if !same {
			continue
		}
		usedFinding[p.finding], usedNeg[p.ref] = true, true
		res.NegativeHits++
		outcome[p.finding] = MatchRecord{FindingIndex: p.finding, Path: findings[p.finding].Path, StartLine: findings[p.finding].StartLine, Title: findings[p.finding].Title, Outcome: "negative_hit", Reference: p.ref, Reason: reason}
	}
	res.Records = outcome
	return res
}

func candidatePairs(findings []Finding, refs []Comment, window int) []pair {
	var pairs []pair
	for fi, f := range findings {
		if f.StartLine <= 0 || strings.TrimSpace(f.Path) == "" {
			continue
		}
		end := f.EndLine
		if end < f.StartLine {
			end = f.StartLine
		}
		for ri, r := range refs {
			if normPath(f.Path) != normPath(r.Path) || !sideCompatible(f.Side, r.Side) {
				continue
			}
			from, to := r.FromLine, r.ToLine
			if to < from {
				to = from
			}
			if f.StartLine > to+window || end < from-window {
				continue
			}
			pairs = append(pairs, pair{finding: fi, ref: ri, distance: abs(f.StartLine - from)})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].distance != pairs[j].distance {
			return pairs[i].distance < pairs[j].distance
		}
		if pairs[i].finding != pairs[j].finding {
			return pairs[i].finding < pairs[j].finding
		}
		return pairs[i].ref < pairs[j].ref
	})
	return pairs
}

func sideCompatible(findingSide, commentSide string) bool {
	fs := strings.ToLower(strings.TrimSpace(findingSide))
	cs := strings.ToLower(strings.TrimSpace(commentSide))
	if fs == "" || fs == "new" {
		fs = "right"
	}
	if cs == "" || cs == "new" {
		cs = "right"
	}
	if fs == "old" {
		fs = "left"
	}
	if cs == "old" {
		cs = "left"
	}
	return fs == cs
}

func normPath(p string) string {
	return strings.TrimPrefix(strings.TrimSpace(p), "./")
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
