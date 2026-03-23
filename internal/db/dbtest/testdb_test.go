package dbtest

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	mysqlcontainer "github.com/testcontainers/testcontainers-go/modules/mysql"
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

func TestNewReusesContainerAcrossSequentialSubtests(t *testing.T) {
	var firstServerUUID string

	t.Run("first", func(t *testing.T) {
		db := New(t)

		if err := db.QueryRow("SELECT @@server_uuid").Scan(&firstServerUUID); err != nil {
			t.Fatalf("inspect first server uuid: %v", err)
		}
		if firstServerUUID == "" {
			t.Fatal("expected first server uuid to be set")
		}
	})

	t.Run("second", func(t *testing.T) {
		db := New(t)

		var secondServerUUID string
		if err := db.QueryRow("SELECT @@server_uuid").Scan(&secondServerUUID); err != nil {
			t.Fatalf("inspect second server uuid: %v", err)
		}
		if secondServerUUID != firstServerUUID {
			t.Fatalf("expected sequential subtests to reuse mysql container, got %q then %q", firstServerUUID, secondServerUUID)
		}
	})
}

func TestNewUsesExternalAdminDSNWhenConfigured(t *testing.T) {
	resetSharedStateForTest(t)

	ctx := context.Background()
	ctr, err := mysqlcontainer.Run(ctx,
		"mysql:8.4",
		mysqlcontainer.WithDatabase("mysql"),
		mysqlcontainer.WithUsername("root"),
		mysqlcontainer.WithPassword("test"),
	)
	if err != nil {
		t.Fatalf("start external mysql: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			t.Logf("terminate external mysql: %v", err)
		}
	})

	adminDSN, err := ctr.ConnectionString(ctx, "parseTime=true", "loc=UTC", "charset=utf8mb4", "collation=utf8mb4_unicode_ci", "multiStatements=true")
	if err != nil {
		t.Fatalf("external mysql connection string: %v", err)
	}
	adminDB, err := sql.Open("mysql", adminDSN)
	if err != nil {
		t.Fatalf("open external admin db: %v", err)
	}
	defer adminDB.Close()

	var externalUUID string
	if err := adminDB.QueryRow("SELECT @@server_uuid").Scan(&externalUUID); err != nil {
		t.Fatalf("inspect external server uuid: %v", err)
	}

	t.Setenv("MREVIEWER_TEST_ADMIN_DSN", adminDSN)

	db := New(t)

	var actualUUID string
	if err := db.QueryRow("SELECT @@server_uuid").Scan(&actualUUID); err != nil {
		t.Fatalf("inspect dbtest server uuid: %v", err)
	}
	if actualUUID != externalUUID {
		t.Fatalf("expected dbtest to use external admin DSN, got %q want %q", actualUUID, externalUUID)
	}
}

func TestNextDatabaseNameIncludesProcessID(t *testing.T) {
	name := nextDatabaseName()
	processID := strconv.Itoa(os.Getpid())
	if !strings.Contains(name, "mreviewer_test_"+processID+"_") {
		t.Fatalf("database name %q does not include current process id %s", name, processID)
	}
}

func resetSharedStateForTest(t *testing.T) {
	t.Helper()

	sharedMu.Lock()
	state := sharedState
	sharedState = nil
	sharedRefCount = 0
	sharedMu.Unlock()

	if state == nil {
		return
	}
	closeSharedState(t, state)
}
