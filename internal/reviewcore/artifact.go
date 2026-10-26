package reviewcore

type ReviewerArtifact struct {
	ReviewerID   string       `json:"reviewer_id"`
	ReviewerKind string       `json:"reviewer_kind,omitempty"`
	Target       ReviewTarget `json:"target"`
	Summary      string       `json:"summary,omitempty"`
	Findings     []Finding    `json:"findings,omitempty"`
	// TokensTotal and LatencyMillis are the provider usage for this artifact,
	// zero when the runner did not report them.
	TokensTotal   int64 `json:"tokens_total,omitempty"`
	LatencyMillis int64 `json:"latency_ms,omitempty"`
}
