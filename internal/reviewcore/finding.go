package reviewcore

type FindingIdentityInput struct {
	Category            string            `json:"category,omitempty"`
	NormalizedClaim     string            `json:"normalized_claim,omitempty"`
	EvidenceFingerprint string            `json:"evidence_fingerprint,omitempty"`
	Standards           []string          `json:"standards,omitempty"`
	Location            CanonicalLocation `json:"location"`
}

type Finding struct {
	Category   string               `json:"category,omitempty"`
	Severity   string               `json:"severity,omitempty"`
	Title      string               `json:"title,omitempty"`
	Body       string               `json:"body,omitempty"`
	Claim      string               `json:"claim,omitempty"`
	Confidence float64              `json:"confidence,omitempty"`
	Identity   FindingIdentityInput `json:"identity"`
}
