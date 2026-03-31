package judge

import (
	"strconv"
	"strings"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type MergedFinding struct {
	Finding         core.Finding `json:"finding"`
	SourceReviewers []string     `json:"source_reviewers,omitempty"`
}

type Decision struct {
	MergedFindings []MergedFinding `json:"merged_findings,omitempty"`
	Verdict        string          `json:"verdict,omitempty"`
}

type Engine struct{}

func New() *Engine { return &Engine{} }

func (e *Engine) Decide(artifacts []core.ReviewerArtifact) Decision {
	type aggregate struct {
		finding   core.Finding
		reviewers []string
	}
	byIdentity := map[string]aggregate{}
	for _, artifact := range artifacts {
		for _, finding := range artifact.Findings {
			key := identityKey(finding)
			current, ok := byIdentity[key]
			if !ok {
				byIdentity[key] = aggregate{finding: finding, reviewers: []string{artifact.ReviewerID}}
				continue
			}
			if severityRank(finding.Severity) > severityRank(current.finding.Severity) {
				current.finding = finding
			}
			current.reviewers = appendUnique(current.reviewers, artifact.ReviewerID)
			byIdentity[key] = current
		}
	}

	out := Decision{
		MergedFindings: make([]MergedFinding, 0, len(byIdentity)),
		Verdict:        "pass",
	}
	for _, item := range byIdentity {
		out.MergedFindings = append(out.MergedFindings, MergedFinding{
			Finding:         item.finding,
			SourceReviewers: item.reviewers,
		})
		if strings.EqualFold(item.finding.Severity, "high") || strings.EqualFold(item.finding.Severity, "critical") {
			out.Verdict = "requested_changes"
		}
	}
	if len(out.MergedFindings) == 0 {
		out.Verdict = "pass"
	}
	return out
}

func identityKey(f core.Finding) string {
	loc := f.Identity.Location
	return strings.Join([]string{
		f.Identity.Category,
		f.Identity.NormalizedClaim,
		f.Identity.EvidenceFingerprint,
		loc.Path,
		string(loc.Side),
		intToString(loc.StartLine),
		intToString(loc.EndLine),
	}, "|")
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func intToString(v int) string {
	if v == 0 {
		return ""
	}
	return strconv.Itoa(v)
}

func severityRank(v string) int {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}
