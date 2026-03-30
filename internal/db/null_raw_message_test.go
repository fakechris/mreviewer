package db

import (
	"strings"
	"testing"
)

func TestNullRawMessageScanUnsupportedTypeClearsAndErrors(t *testing.T) {
	msg := NullRawMessage(`{"stale":true}`)

	err := (&msg).Scan(123)
	if err == nil {
		t.Fatal("expected unsupported scan type error")
	}
	if !strings.Contains(err.Error(), "unsupported src type int") {
		t.Fatalf("error = %q, want unsupported src type int", err.Error())
	}
	if len(msg) != 0 {
		t.Fatalf("msg length after unsupported scan = %d, want 0", len(msg))
	}
}
