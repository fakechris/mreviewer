// Package dbtest provides test helpers for spinning up a throwaway MySQL 8.4
// container via testcontainers-go and applying Goose migrations against it.
package dbtest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	mysqlcontainer "github.com/testcontainers/testcontainers-go/modules/mysql"
)

type sharedMySQLState struct {
	container *mysqlcontainer.MySQLContainer
	adminDB   *sql.DB
	adminDSN  string
}

var (
	sharedMu        sync.Mutex
	sharedState     *sharedMySQLState
	sharedRefCount  int
	sharedDBCounter uint64
)

// New reuses a shared MySQL 8.4 container within the current package test
// process, creates an isolated database for the current test, and registers
// cleanup to drop that database when the test finishes. Migrations are NOT
// automatically applied; call MigrateUp to apply them.
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

	if sharedState == nil {
		ctr, err := mysqlcontainer.Run(ctx,
			"mysql:8.4",
			mysqlcontainer.WithDatabase("mysql"),
			mysqlcontainer.WithUsername("root"),
			mysqlcontainer.WithPassword("test"),
		)
		if err != nil {
			t.Fatalf("dbtest: start mysql container: %v", err)
		}

		connStr, err := ctr.ConnectionString(ctx, "parseTime=true", "loc=UTC", "charset=utf8mb4", "collation=utf8mb4_unicode_ci", "multiStatements=true")
		if err != nil {
			if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
				t.Logf("dbtest: terminate mysql after connection string failure: %v", termErr)
			}
			t.Fatalf("dbtest: connection string: %v", err)
		}

		adminDB, err := sql.Open("mysql", connStr)
		if err != nil {
			if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
				t.Logf("dbtest: terminate mysql after open failure: %v", termErr)
			}
			t.Fatalf("dbtest: open admin db: %v", err)
		}
		if err := adminDB.Ping(); err != nil {
			adminDB.Close()
			if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
				t.Logf("dbtest: terminate mysql after ping failure: %v", termErr)
			}
			t.Fatalf("dbtest: ping admin db: %v", err)
		}

		sharedState = &sharedMySQLState{
			container: ctr,
			adminDB:   adminDB,
			adminDSN:  connStr,
		}
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

	state := sharedState
	sharedState = nil
	if state == nil {
		return
	}
	if err := state.adminDB.Close(); err != nil {
		t.Logf("dbtest: close admin db: %v", err)
	}
	if err := testcontainers.TerminateContainer(state.container); err != nil {
		t.Logf("dbtest: terminate shared mysql container: %v", err)
	}
}

func nextDatabaseName() string {
	return fmt.Sprintf("mreviewer_test_%d", atomic.AddUint64(&sharedDBCounter, 1))
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
