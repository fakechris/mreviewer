package reviewcore

type Verdict string

const (
	VerdictCommentOnly      Verdict = "comment_only"
	VerdictApproved         Verdict = "approved"
	VerdictRequestedChanges Verdict = "requested_changes"
	VerdictFailed           Verdict = "failed"
)

type FindingIdentityInput struct {
	Category        string `json:"category,omitempty"`
	NormalizedClaim string `json:"normalized_claim,omitempty"`
	LocationKey     string `json:"location_key,omitempty"`
	EvidenceKey     string `json:"evidence_key,omitempty"`
}

type Finding struct {
	Title          string                `json:"title,omitempty"`
	Category       string                `json:"category,omitempty"`
	Claim          string                `json:"claim,omitempty"`
	Severity       string                `json:"severity,omitempty"`
	Confidence     float64               `json:"confidence,omitempty"`
	Location       *CanonicalLocation    `json:"location,omitempty"`
	Identity       *FindingIdentityInput `json:"identity,omitempty"`
	Evidence       []string              `json:"evidence,omitempty"`
	Recommendation string                `json:"recommendation,omitempty"`
}
