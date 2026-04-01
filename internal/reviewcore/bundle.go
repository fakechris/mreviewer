package reviewcore

type PublishCandidate struct {
	Type        string             `json:"type,omitempty"`
	Title       string             `json:"title,omitempty"`
	Body        string             `json:"body,omitempty"`
	Severity    string             `json:"severity,omitempty"`
	Location    *CanonicalLocation `json:"location,omitempty"`
	ReviewerIDs []string           `json:"reviewer_ids,omitempty"`
}

type ComparisonArtifact struct {
	ReviewerID   string    `json:"reviewer_id,omitempty"`
	ReviewerType string    `json:"reviewer_type,omitempty"`
	Verdict      Verdict   `json:"verdict,omitempty"`
	Findings     []Finding `json:"findings,omitempty"`
	Evidence     []string  `json:"evidence,omitempty"`
}

type ReviewBundle struct {
	Target            ReviewTarget         `json:"target"`
	Artifacts         []ReviewerArtifact   `json:"artifacts,omitempty"`
	AdvisorArtifact   *ReviewerArtifact    `json:"advisor_artifact,omitempty"`
	JudgeVerdict      Verdict              `json:"judge_verdict,omitempty"`
	JudgeSummary      string               `json:"judge_summary,omitempty"`
	PublishCandidates []PublishCandidate   `json:"publish_candidates,omitempty"`
	Comparisons       []ComparisonArtifact `json:"comparisons,omitempty"`
}
