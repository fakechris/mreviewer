package textutil

import "testing"

func TestFirstNonEmpty(t *testing.T) {
	if got := FirstNonEmpty("", "  ", "\nfoo\t", "bar"); got != "foo" {
		t.Fatalf("FirstNonEmpty returned %q, want foo", got)
	}
	if got := FirstNonEmpty("", "  "); got != "" {
		t.Fatalf("FirstNonEmpty returned %q, want empty string", got)
	}
}
