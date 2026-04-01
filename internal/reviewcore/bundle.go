package reviewcore

type PublishCandidate struct {
	Kind     string            `json:"kind"`
	Body     string            `json:"body"`
	Title    string            `json:"title,omitempty"`
	Severity string            `json:"severity,omitempty"`
	Location CanonicalLocation `json:"location,omitempty,omitzero"`
}

type ReviewBundle struct {
	Target            ReviewTarget       `json:"target"`
	Artifacts         []ReviewerArtifact `json:"artifacts,omitempty"`
	AdvisorArtifact   *ReviewerArtifact  `json:"advisor_artifact,omitempty"`
	Verdict           string             `json:"verdict,omitempty"`
	MarkdownSummary   string             `json:"markdown_summary,omitempty"`
	JSONSchemaVersion string             `json:"json_schema_version,omitempty"`
	PublishCandidates []PublishCandidate `json:"publish_candidates,omitempty"`
}
