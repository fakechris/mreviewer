package dbtest

import (
	"os"
	"testing"
)

func TestNewKeepsRyukEnabled(t *testing.T) {
	t.Setenv("TESTCONTAINERS_RYUK_DISABLED", "")

	db := New(t)

	if err := db.Ping(); err != nil {
		t.Fatalf("ping shared db: %v", err)
	}
	if got := os.Getenv("TESTCONTAINERS_RYUK_DISABLED"); got == "true" {
		t.Fatalf("TESTCONTAINERS_RYUK_DISABLED should not be forced to true")
	}
}

func TestNewReusesContainerWithIsolatedDatabases(t *testing.T) {
	db1 := New(t)
	db2 := New(t)

	var serverUUID1, database1 string
	if err := db1.QueryRow("SELECT @@server_uuid, DATABASE()").Scan(&serverUUID1, &database1); err != nil {
		t.Fatalf("inspect first database: %v", err)
	}

	var serverUUID2, database2 string
	if err := db2.QueryRow("SELECT @@server_uuid, DATABASE()").Scan(&serverUUID2, &database2); err != nil {
		t.Fatalf("inspect second database: %v", err)
	}

	if serverUUID1 != serverUUID2 {
		t.Fatalf("expected shared mysql container, got different server_uuid values %q and %q", serverUUID1, serverUUID2)
	}
	if database1 == database2 {
		t.Fatalf("expected isolated databases per test handle, both used %q", database1)
	}

	if _, err := db1.Exec("CREATE TABLE shared_container_check (id INT PRIMARY KEY)"); err != nil {
		t.Fatalf("create table in first database: %v", err)
	}

	var tableCount int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'shared_container_check'`).Scan(&tableCount); err != nil {
		t.Fatalf("check second database isolation: %v", err)
	}
	if tableCount != 0 {
		t.Fatalf("expected second database to be isolated, found table shared_container_check")
	}
}
