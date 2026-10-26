package reviewbench

import (
	"encoding/json"
	"os"
	"strings"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

// Finding is an engine-agnostic review finding, so the same scorer can rate
// mreviewer bundles and the JSON output of other tools.
type Finding struct {
	Path      string `json:"path"`
	Side      string `json:"side,omitempty"` // old | new; empty means new
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line,omitempty"`
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Category  string `json:"category,omitempty"`
	// OutOfDiff mirrors the engine's downgrade flag; such findings are still
	// scored (they are still shown to the user) but reported separately.
	OutOfDiff bool `json:"out_of_diff,omitempty"`
}

// FindingsFromBundle extracts the finding publish candidates of a bundle.
func FindingsFromBundle(bundle core.ReviewBundle) []Finding {
	var out []Finding
	for _, candidate := range bundle.PublishCandidates {
		if candidate.Kind != "finding" {
			continue
		}
		out = append(out, Finding{
			Path:      strings.TrimSpace(candidate.Location.Path),
			Side:      string(candidate.Location.Side),
			StartLine: candidate.Location.StartLine,
			EndLine:   candidate.Location.EndLine,
			Title:     strings.TrimSpace(candidate.Title),
			Body:      strings.TrimSpace(candidate.Body),
			Severity:  strings.TrimSpace(candidate.Severity),
			OutOfDiff: candidate.OutOfDiff,
		})
	}
	return out
}

// ExternalFindings maps a case id to findings produced by another tool, so
// the corpus can score e.g. `ocr review --format json` after conversion.
type ExternalFindings map[string][]Finding

// LoadExternalFindings reads either a plain {case_id: [findings]} map or a
// previous bench report (its cases' raw_findings), so a run can be re-scored
// without calling the review model again.
func LoadExternalFindings(path string) (ExternalFindings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var report struct {
		Cases []struct {
			CaseID      string    `json:"case_id"`
			RawFindings []Finding `json:"raw_findings"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &report); err == nil && len(report.Cases) > 0 {
		out := ExternalFindings{}
		for _, c := range report.Cases {
			if c.CaseID != "" {
				out[c.CaseID] = c.RawFindings
			}
		}
		return out, nil
	}
	var out ExternalFindings
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}
