package reviewcore

type ReviewerArtifact struct {
	ReviewerID   string       `json:"reviewer_id"`
	ReviewerKind string       `json:"reviewer_kind,omitempty"`
	Target       ReviewTarget `json:"target"`
	Summary      string       `json:"summary,omitempty"`
	Findings     []Finding    `json:"findings,omitempty"`
}
