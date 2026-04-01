package reviewpack

import "testing"

func TestDefaultPacksExposeCapabilityContracts(t *testing.T) {
	packs := DefaultPacks()
	if len(packs) != 3 {
		t.Fatalf("expected 3 default packs, got %d", len(packs))
	}

	for _, pack := range packs {
		if pack.ID == "" {
			t.Fatal("expected pack id")
		}
		if pack.Scope == "" {
			t.Fatalf("expected scope for pack %q", pack.ID)
		}
		if len(pack.Rubric) == 0 {
			t.Fatalf("expected rubric for pack %q", pack.ID)
		}
		if len(pack.EvidenceRequirements) == 0 {
			t.Fatalf("expected evidence requirements for pack %q", pack.ID)
		}
		if len(pack.OutputSchema) == 0 {
			t.Fatalf("expected output schema for pack %q", pack.ID)
		}
		if pack.SystemPrompt() == "" {
			t.Fatalf("expected non-empty system prompt for pack %q", pack.ID)
		}
	}
}

func TestLookupBuiltinPackReturnsArchitecturePack(t *testing.T) {
	pack, ok := Lookup("architecture")
	if !ok {
		t.Fatal("expected architecture pack to exist")
	}
	if pack.ID != "architecture" {
		t.Fatalf("expected architecture pack, got %q", pack.ID)
	}
}
