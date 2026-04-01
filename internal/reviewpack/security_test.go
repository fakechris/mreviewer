package reviewpack

import (
	"strings"
	"testing"
)

func TestSecurityPackUsesOWASPAndASVSFraming(t *testing.T) {
	pack, ok := Lookup("security")
	if !ok {
		t.Fatal("expected security pack to exist")
	}

	if !contains(pack.Standards, "OWASP") {
		t.Fatalf("expected security standards to include OWASP, got %#v", pack.Standards)
	}
	if !contains(pack.Standards, "ASVS") {
		t.Fatalf("expected security standards to include ASVS, got %#v", pack.Standards)
	}

	prompt := pack.SystemPrompt()
	if !strings.Contains(prompt, "newly introduced security") {
		t.Fatalf("expected security prompt to focus on newly introduced security issues, got %q", prompt)
	}
	if pack.ConfidenceGate <= 0 {
		t.Fatalf("expected security pack confidence gate, got %v", pack.ConfidenceGate)
	}
	if len(pack.HardExclusions) == 0 {
		t.Fatal("expected hard exclusions")
	}
	if !strings.Contains(prompt, "Hard exclusions:") {
		t.Fatalf("expected prompt to render hard exclusions, got %q", prompt)
	}
	if !pack.NewIssuesOnly {
		t.Fatal("expected security pack to require new issues only")
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
