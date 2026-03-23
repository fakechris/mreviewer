// Package dbtest provides test helpers for spinning up a throwaway MySQL 8.4
// container via testcontainers-go and applying Goose migrations against it.
package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	mysqlcontainer "github.com/testcontainers/testcontainers-go/modules/mysql"
)

type sharedMySQLState struct {
	container *mysqlcontainer.MySQLContainer
	adminDB   *sql.DB
	adminDSN  string
	source    string
}

var (
	sharedMu        sync.Mutex
	sharedState     *sharedMySQLState
	sharedRefCount  int
	sharedDBCounter uint64
)

const adminDSNEnvVar = "MREVIEWER_TEST_ADMIN_DSN"

// New reuses a shared MySQL 8.4 container for the lifetime of the current
// package test process, creates an isolated database for the current test, and
// registers cleanup to drop that database when the test finishes. The shared
// container itself is intentionally left running until the package test process
// exits so later tests can reuse it; Ryuk cleans it up when the process ends.
// Migrations are NOT automatically applied; call MigrateUp to apply them.
func New(t *testing.T) *sql.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test (requires Docker)")
	}

	ctx := context.Background()
	state := acquireSharedState(t, ctx)
	dbName := nextDatabaseName()
	if err := createDatabase(state.adminDB, dbName); err != nil {
		releaseSharedState(t, ctx)
		t.Fatalf("dbtest: create database %s: %v", dbName, err)
	}

	connStr, err := databaseConnectionString(state.adminDSN, dbName)
	if err != nil {
		dropDatabase(t, state.adminDB, dbName)
		releaseSharedState(t, ctx)
		t.Fatalf("dbtest: database connection string: %v", err)
	}

	db, err := sql.Open("mysql", connStr)
	if err != nil {
		dropDatabase(t, state.adminDB, dbName)
		releaseSharedState(t, ctx)
		t.Fatalf("dbtest: open db: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		dropDatabase(t, state.adminDB, dbName)
		releaseSharedState(t, ctx)
		t.Fatalf("dbtest: ping: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Logf("dbtest: close db %s: %v", dbName, err)
		}
		dropDatabase(t, state.adminDB, dbName)
		releaseSharedState(t, ctx)
	})

	return db
}

func acquireSharedState(t *testing.T, ctx context.Context) *sharedMySQLState {
	t.Helper()

	sharedMu.Lock()
	defer sharedMu.Unlock()

	source, adminDSN := sharedStateSource()
	if sharedState != nil && sharedState.source != source {
		if sharedRefCount != 0 {
			t.Fatalf("dbtest: shared mysql source changed from %q to %q while %d references are active", sharedState.source, source, sharedRefCount)
		}
		closeSharedState(t, sharedState)
		sharedState = nil
	}

	if sharedState == nil {
		state, err := newSharedState(ctx, source, adminDSN)
		if err != nil {
			t.Fatalf("dbtest: acquire shared mysql state: %v", err)
		}
		sharedState = state
	}

	sharedRefCount++
	return sharedState
}

func releaseSharedState(t *testing.T, ctx context.Context) {
	t.Helper()

	sharedMu.Lock()
	defer sharedMu.Unlock()

	if sharedRefCount == 0 {
		return
	}
	sharedRefCount--
	if sharedRefCount != 0 {
		return
	}
}

func sharedStateSource() (string, string) {
	if dsn := strings.TrimSpace(os.Getenv(adminDSNEnvVar)); dsn != "" {
		return "external:" + dsn, dsn
	}
	return "container:mysql:8.4", ""
}

func newSharedState(ctx context.Context, source string, adminDSN string) (*sharedMySQLState, error) {
	if strings.HasPrefix(source, "external:") {
		adminDB, err := openAdminDBWithRetry(ctx, adminDSN)
		if err != nil {
			return nil, fmt.Errorf("ping external admin db: %w", err)
		}
		return &sharedMySQLState{
			adminDB:  adminDB,
			adminDSN: adminDSN,
			source:   source,
		}, nil
	}

	ctr, err := mysqlcontainer.Run(ctx,
		"mysql:8.4",
		mysqlcontainer.WithDatabase("mysql"),
		mysqlcontainer.WithUsername("root"),
		mysqlcontainer.WithPassword("test"),
	)
	if err != nil {
		return nil, fmt.Errorf("start mysql container: %w", err)
	}

	connStr, err := ctr.ConnectionString(ctx, "parseTime=true", "loc=UTC", "charset=utf8mb4", "collation=utf8mb4_unicode_ci", "multiStatements=true")
	if err != nil {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			return nil, fmt.Errorf("connection string: %w (terminate mysql: %v)", err, termErr)
		}
		return nil, fmt.Errorf("connection string: %w", err)
	}

	adminDB, err := openAdminDBWithRetry(ctx, connStr)
	if err != nil {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			return nil, fmt.Errorf("ping admin db: %w (terminate mysql: %v)", err, termErr)
		}
		return nil, fmt.Errorf("ping admin db: %w", err)
	}

	return &sharedMySQLState{
		container: ctr,
		adminDB:   adminDB,
		adminDSN:  connStr,
		source:    source,
	}, nil
}

func closeSharedState(t *testing.T, state *sharedMySQLState) {
	t.Helper()
	if state == nil {
		return
	}
	if err := state.adminDB.Close(); err != nil {
		t.Logf("dbtest: close admin db: %v", err)
	}
	if state.container != nil {
		if err := testcontainers.TerminateContainer(state.container); err != nil {
			t.Logf("dbtest: terminate shared mysql container: %v", err)
		}
	}
}

func nextDatabaseName() string {
	return fmt.Sprintf("mreviewer_test_%d_%d", os.Getpid(), atomic.AddUint64(&sharedDBCounter, 1))
}

func createDatabase(adminDB *sql.DB, dbName string) error {
	_, err := adminDB.Exec("CREATE DATABASE `" + dbName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	return err
}

func dropDatabase(t *testing.T, adminDB *sql.DB, dbName string) {
	t.Helper()

	if _, err := adminDB.Exec("DROP DATABASE IF EXISTS `" + dbName + "`"); err != nil {
		t.Logf("dbtest: drop database %s: %v", dbName, err)
	}
}

func databaseConnectionString(adminDSN, dbName string) (string, error) {
	cfg, err := mysqlDriver.ParseDSN(adminDSN)
	if err != nil {
		return "", err
	}
	cfg.DBName = dbName
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["multiStatements"] = "true"
	cfg.Params["parseTime"] = "true"
	cfg.Params["loc"] = "UTC"
	cfg.Params["charset"] = "utf8mb4"
	cfg.Params["collation"] = "utf8mb4_unicode_ci"
	return cfg.FormatDSN(), nil
}

func openAdminDBWithRetry(ctx context.Context, dsn string) (*sql.DB, error) {
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		adminDB, err := sql.Open("mysql", dsn)
		if err != nil {
			lastErr = err
		} else if err := adminDB.PingContext(ctx); err == nil {
			return adminDB, nil
		} else {
			lastErr = err
			_ = adminDB.Close()
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil, lastErr
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
