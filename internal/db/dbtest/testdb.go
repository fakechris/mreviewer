// Package dbtest provides test helpers for spinning up a throwaway MySQL 8.4
// container via testcontainers-go and applying Goose migrations against it.
package dbtest

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
)

// New spins up a MySQL 8.4 container, returns a *sql.DB connected to it, and
// registers cleanup to stop the container when the test finishes. The database
// is named "mreviewer_test". Migrations are NOT automatically applied; call
// MigrateUp to apply them.
func New(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test (requires Docker)")
	}

	// Disable the Ryuk reaper to avoid pulling testcontainers/ryuk from Docker
	// Hub; tests clean up containers via t.Cleanup instead.
	t.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	ctx := context.Background()

	ctr, err := mysql.Run(ctx,
		"mysql:8.4",
		mysql.WithDatabase("mreviewer_test"),
		mysql.WithUsername("test"),
		mysql.WithPassword("test"),
	)
	if err != nil {
		t.Fatalf("dbtest: start mysql container: %v", err)
	}
	t.Cleanup(func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			t.Logf("dbtest: terminate container: %v", termErr)
		}
	})

	connStr, err := ctr.ConnectionString(ctx, "parseTime=true", "loc=UTC", "charset=utf8mb4", "collation=utf8mb4_unicode_ci", "multiStatements=true")
	if err != nil {
		t.Fatalf("dbtest: connection string: %v", err)
	}

	db, err := sql.Open("mysql", connStr)
	if err != nil {
		t.Fatalf("dbtest: open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := db.Ping(); err != nil {
		t.Fatalf("dbtest: ping: %v", err)
	}

	return db
}

// MigrateUp runs all Goose migrations from the given directory against db.
func MigrateUp(t *testing.T, db *sql.DB, migrationsDir string) {
	t.Helper()

	if err := goose.SetDialect("mysql"); err != nil {
		t.Fatalf("dbtest: set dialect: %v", err)
	}
	if err := goose.Up(db, migrationsDir); err != nil {
		t.Fatalf("dbtest: goose up: %v", err)
	}
}

// MigrateDown runs goose down-to version 0 (all down) for the given dir.
func MigrateDown(t *testing.T, db *sql.DB, migrationsDir string) {
	t.Helper()

	if err := goose.SetDialect("mysql"); err != nil {
		t.Fatalf("dbtest: set dialect: %v", err)
	}
	if err := goose.DownTo(db, migrationsDir, 0); err != nil {
		t.Fatalf("dbtest: goose down: %v", err)
	}
}

// MigrationsDir returns the path to the project's migrations directory
// relative to the caller's location.
func MigrationsDir() string {
	// When tests are run from different directories we need the project root.
	// The env var is set in CI; locally callers should pass the path explicitly.
	if dir := os.Getenv("MREVIEWER_MIGRATIONS_DIR"); dir != "" {
		return dir
	}
	// Fallback: the standard relative path from a typical test location.
	return "../../../../migrations"
}
