package reviewcore

type PublishCandidate struct {
	Kind             string `json:"kind"`
	Body             string `json:"body"`
	Title            string `json:"title,omitempty"`
	Severity         string `json:"severity,omitempty"`
	PublishAsSummary bool   `json:"publish_as_summary,omitempty"`
	// OutOfDiff is set when the finding named a line the change did not touch;
	// the candidate is then published at file/summary level instead of inline.
	OutOfDiff bool              `json:"out_of_diff,omitempty"`
	Location  CanonicalLocation `json:"location,omitempty,omitzero"`
}

// VerdictIncomplete is emitted instead of a passing verdict when the reviewer
// did not see enough of the change to vouch for it.
const VerdictIncomplete = "incomplete"

// DefaultCoverageThreshold is the minimum share of changed lines that must
// reach the prompt before a passing verdict is allowed.
const DefaultCoverageThreshold = 0.8

// CoverageSummary is what the bundle tells the user about what was and was
// not reviewed.
type CoverageSummary struct {
	Mode                 string         `json:"mode,omitempty"`
	TotalChangedLines    int            `json:"total_changed_lines"`
	ReviewedChangedLines int            `json:"reviewed_changed_lines"`
	HunksTotal           int            `json:"hunks_total"`
	HunksInPrompt        int            `json:"hunks_in_prompt"`
	Ratio                float64        `json:"ratio"`
	Threshold            float64        `json:"threshold"`
	Incomplete           bool           `json:"incomplete"`
	Skipped              []SkippedScope `json:"skipped,omitempty"`
	Summary              string         `json:"summary,omitempty"`
}

type SkippedScope struct {
	Path         string `json:"path"`
	Reason       string `json:"reason"`
	ChangedLines int    `json:"changed_lines,omitempty"`
}

type ReviewBundle struct {
	Target            ReviewTarget       `json:"target"`
	Artifacts         []ReviewerArtifact `json:"artifacts,omitempty"`
	AdvisorArtifact   *ReviewerArtifact  `json:"advisor_artifact,omitempty"`
	Verdict           string             `json:"verdict,omitempty"`
	MarkdownSummary   string             `json:"markdown_summary,omitempty"`
	JSONSchemaVersion string             `json:"json_schema_version,omitempty"`
	PublishCandidates []PublishCandidate `json:"publish_candidates,omitempty"`
	// Coverage is nil when the input carried no coverage report.
	Coverage *CoverageSummary `json:"coverage,omitempty"`
}
