package database

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenEmptyDSN verifies that an empty DSN is rejected immediately.
func TestOpenEmptyDSN(t *testing.T) {
	_, err := Open("")
	if err == nil {
		t.Fatal("expected error for empty DSN, got nil")
	}
	if !strings.Contains(err.Error(), "empty DSN") {
		t.Errorf("error = %q, want it to mention 'empty DSN'", err)
	}
}

// TestOpenFailsWhenDatabaseUnreachable verifies that Open returns an error
// when MySQL is not listening at the given address. This ensures the service
// fails fast at startup rather than lazily discovering the problem later.
func TestOpenFailsWhenDatabaseUnreachable(t *testing.T) {
	// Use a TCP address that will refuse connections (port 1 is almost
	// certainly not running MySQL).
	dsn := "user:pass@tcp(127.0.0.1:1)/testdb?timeout=1s"

	db, err := Open(dsn)
	if err == nil {
		db.Close()
		t.Fatal("expected Open to fail for unreachable database, got nil error")
	}
	if !strings.Contains(err.Error(), "database: ping") {
		t.Errorf("error = %q, want it to contain 'database: ping'", err)
	}
}

func TestMigrateUpFromDSNInitializesSQLiteSchema(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "mreviewer.db")
	if err := MigrateUpFromDSN(dsn); err != nil {
		t.Fatalf("MigrateUpFromDSN: %v", err)
	}
	db, err := sql.Open("sqlite", strings.TrimPrefix(dsn, "file:"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('review_runs', 'hook_events', 'identity_mappings')`).Scan(&count); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if count != 3 {
		t.Fatalf("table count = %d, want 3", count)
	}
}
