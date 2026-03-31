package dbtest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
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

func TestEnsureSuiteSharedAdminDSNUsesPersistedStateWhenReachable(t *testing.T) {
	resetSharedStateForTest(t)
	t.Setenv(sharedStateDirEnvVar, t.TempDir())

	statePath := filepath.Join(os.Getenv(sharedStateDirEnvVar), suiteSharedStateFileName)
	wantDSN := "root:test@tcp(127.0.0.1:3307)/mysql?parseTime=true"
	if err := writeSuiteSharedState(statePath, suiteSharedStateFile{
		ContainerName: suiteSharedContainerName,
		AdminDSN:      wantDSN,
	}); err != nil {
		t.Fatalf("write suite state: %v", err)
	}

	originalOpen := openAdminDBWithRetryFunc
	originalDocker := dockerCommandOutputFunc
	t.Cleanup(func() {
		openAdminDBWithRetryFunc = originalOpen
		dockerCommandOutputFunc = originalDocker
	})

	openAdminDBWithRetryFunc = func(ctx context.Context, dsn string) (*sql.DB, error) {
		if dsn != wantDSN {
			t.Fatalf("openAdminDBWithRetry called with %q, want %q", dsn, wantDSN)
		}
		return sql.Open("mysql", wantDSN)
	}
	dockerCommandOutputFunc = func(ctx context.Context, args ...string) (string, error) {
		t.Fatalf("docker should not be called when persisted suite state is reachable: %v", args)
		return "", nil
	}

	gotDSN, err := ensureSuiteSharedAdminDSN(context.Background())
	if err != nil {
		t.Fatalf("ensureSuiteSharedAdminDSN returned error: %v", err)
	}
	if gotDSN != wantDSN {
		t.Fatalf("ensureSuiteSharedAdminDSN=%q, want %q", gotDSN, wantDSN)
	}
}

func TestEnsureSuiteSharedAdminDSNStartsAndPersistsSharedContainer(t *testing.T) {
	resetSharedStateForTest(t)
	t.Setenv(sharedStateDirEnvVar, t.TempDir())

	var dockerCalls [][]string
	wantDSN := "root:test@tcp(127.0.0.1:44001)/mysql?parseTime=true&loc=UTC&charset=utf8mb4&collation=utf8mb4_unicode_ci&multiStatements=true"
	portLookups := 0

	originalOpen := openAdminDBWithRetryFunc
	originalDocker := dockerCommandOutputFunc
	t.Cleanup(func() {
		openAdminDBWithRetryFunc = originalOpen
		dockerCommandOutputFunc = originalDocker
	})

	openAdminDBWithRetryFunc = func(ctx context.Context, dsn string) (*sql.DB, error) {
		if dsn != wantDSN {
			t.Fatalf("openAdminDBWithRetry called with %q, want %q", dsn, wantDSN)
		}
		return sql.Open("mysql", wantDSN)
	}
	dockerCommandOutputFunc = func(ctx context.Context, args ...string) (string, error) {
		dockerCalls = append(dockerCalls, append([]string(nil), args...))
		if len(args) >= 3 && args[0] == "port" && args[1] == suiteSharedContainerName && args[2] == "3306/tcp" {
			portLookups++
			if portLookups == 1 {
				return "", os.ErrNotExist
			}
			return "127.0.0.1:44001\n", nil
		}
		if len(args) >= 4 && args[0] == "run" && args[1] == "-d" {
			return "new-container-id\n", nil
		}
		t.Fatalf("unexpected docker call: %v", args)
		return "", nil
	}

	gotDSN, err := ensureSuiteSharedAdminDSN(context.Background())
	if err != nil {
		t.Fatalf("ensureSuiteSharedAdminDSN returned error: %v", err)
	}
	if gotDSN != wantDSN {
		t.Fatalf("ensureSuiteSharedAdminDSN=%q, want %q", gotDSN, wantDSN)
	}
	if len(dockerCalls) != 3 {
		t.Fatalf("expected docker port probe + run + docker port, got %d calls: %v", len(dockerCalls), dockerCalls)
	}

	statePath := filepath.Join(os.Getenv(sharedStateDirEnvVar), suiteSharedStateFileName)
	state, err := readSuiteSharedState(statePath)
	if err != nil {
		t.Fatalf("readSuiteSharedState: %v", err)
	}
	if state.ContainerName != suiteSharedContainerName {
		t.Fatalf("persisted container name=%q, want %q", state.ContainerName, suiteSharedContainerName)
	}
	if state.AdminDSN != wantDSN {
		t.Fatalf("persisted admin DSN=%q, want %q", state.AdminDSN, wantDSN)
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
