package reviewcore

type ReviewerArtifact struct {
	ReviewerID   string         `json:"reviewer_id,omitempty"`
	ReviewerType string         `json:"reviewer_type,omitempty"`
	Verdict      Verdict        `json:"verdict,omitempty"`
	Summary      string         `json:"summary,omitempty"`
	Findings     []Finding      `json:"findings,omitempty"`
	Metrics      map[string]any `json:"metrics,omitempty"`
}
