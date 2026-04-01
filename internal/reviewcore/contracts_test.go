package reviewcore

import (
	"encoding/json"
	"testing"
)

func TestReviewTargetCanonicalFields(t *testing.T) {
	target := ReviewTarget{
		Platform:   PlatformGitHub,
		Repository: "acme/service",
		Number:     42,
		URL:        "https://github.com/acme/service/pull/42",
	}

	if target.Platform != PlatformGitHub {
		t.Fatalf("expected github platform, got %q", target.Platform)
	}
	if target.Repository != "acme/service" {
		t.Fatalf("expected canonical repository, got %q", target.Repository)
	}
	if target.Number != 42 {
		t.Fatalf("expected canonical number, got %d", target.Number)
	}
}

func TestCanonicalLocationOmitsEmptyPlatformMetadata(t *testing.T) {
	location := CanonicalLocation{
		Path: "internal/service/auth.go",
		Side: LocationSideNew,
		Line: 27,
	}

	data, err := json.Marshal(location)
	if err != nil {
		t.Fatalf("marshal location: %v", err)
	}

	if string(data) == "" {
		t.Fatal("expected non-empty json")
	}
	if containsJSONField(data, "platform_metadata") {
		t.Fatalf("expected empty platform_metadata to be omitted, got %s", string(data))
	}
}

func TestFindingIdentityInputJSONIsDeterministic(t *testing.T) {
	identity := FindingIdentityInput{
		Category:        "security.sql_injection",
		NormalizedClaim: "user-controlled tenant id reaches raw SQL",
		LocationKey:     "internal/repo/query.go:new:91",
		EvidenceKey:     "tenant_id->fmt.Sprintf->db.Query",
	}

	first, err := json.Marshal(identity)
	if err != nil {
		t.Fatalf("marshal identity first: %v", err)
	}
	second, err := json.Marshal(identity)
	if err != nil {
		t.Fatalf("marshal identity second: %v", err)
	}

	if string(first) != string(second) {
		t.Fatalf("expected deterministic json, got %s vs %s", string(first), string(second))
	}
}

func TestReviewBundleMarshalsArtifactsAndJudgeVerdict(t *testing.T) {
	bundle := ReviewBundle{
		Target: ReviewTarget{
			Platform:   PlatformGitLab,
			Repository: "group/project",
			Number:     7,
			URL:        "https://gitlab.example.com/group/project/-/merge_requests/7",
		},
		Artifacts: []ReviewerArtifact{
			{
				ReviewerID:   "security",
				ReviewerType: "pack",
				Verdict:      VerdictRequestedChanges,
				Findings: []Finding{
					{
						Title:    "SQL injection through tenant lookup",
						Category: "security.sql_injection",
						Claim:    "user input reaches raw SQL",
					},
				},
			},
		},
		JudgeVerdict: VerdictRequestedChanges,
	}

	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}

	if !containsJSONField(data, "artifacts") {
		t.Fatalf("expected artifacts in bundle json, got %s", string(data))
	}
	if !containsJSONField(data, "judge_verdict") {
		t.Fatalf("expected judge_verdict in bundle json, got %s", string(data))
	}
}

func containsJSONField(data []byte, field string) bool {
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return false
	}
	_, ok := decoded[field]
	return ok
}
