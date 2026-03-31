// Package dbtest provides test helpers for reusing a shared local MySQL admin
// DSN when possible and otherwise falling back to a throwaway MySQL 8.4
// container plus Goose migrations.
package dbtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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

	openAdminDBWithRetryFunc = openAdminDBWithRetry
	dockerCommandOutputFunc  = dockerCommandOutput
)

const (
	adminDSNEnvVar            = "MREVIEWER_TEST_ADMIN_DSN"
	sharedStateDirEnvVar      = "MREVIEWER_TEST_SHARED_STATE_DIR"
	suiteSharedContainerName  = "mreviewer-test-mysql-shared"
	suiteSharedStateFileName  = "mysql-suite.json"
	suiteSharedLockFileName   = "mysql-suite.lock"
	suiteSharedDefaultImage   = "mysql:8.4"
	suiteSharedDefaultDSNOpts = "parseTime=true&loc=UTC&charset=utf8mb4&collation=utf8mb4_unicode_ci&multiStatements=true"
)

type suiteSharedStateFile struct {
	ContainerName string `json:"container_name"`
	AdminDSN      string `json:"admin_dsn"`
}

// New prefers a shared admin MySQL reachable on 127.0.0.1 via
// MREVIEWER_TEST_ADMIN_DSN or the suite-shared local state file. If neither is
// available, it falls back to a package-local testcontainers MySQL container.
// For each test handle it creates an isolated database and registers cleanup to
// drop that database when the test finishes. Migrations are NOT automatically
// applied; call MigrateUp to apply them.
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

	suiteSharedDSN, suiteSharedErr := ensureSuiteSharedAdminDSN(ctx)
	if suiteSharedErr == nil && strings.TrimSpace(suiteSharedDSN) != "" {
		adminDB, err := openAdminDBWithRetryFunc(ctx, suiteSharedDSN)
		if err != nil {
			return nil, fmt.Errorf("ping suite shared admin db: %w", err)
		}
		return &sharedMySQLState{
			adminDB:  adminDB,
			adminDSN: suiteSharedDSN,
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
		if suiteSharedErr != nil {
			return nil, fmt.Errorf("start mysql container: %w (suite-shared fallback: %v)", err, suiteSharedErr)
		}
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

func ensureSuiteSharedAdminDSN(ctx context.Context) (string, error) {
	statePath := filepath.Join(sharedStateDir(), suiteSharedStateFileName)

	if state, err := readSuiteSharedState(statePath); err == nil && strings.TrimSpace(state.AdminDSN) != "" {
		if err := pingAdminDSN(ctx, state.AdminDSN); err == nil {
			return state.AdminDSN, nil
		}
	}

	var resolved string
	err := withSuiteSharedLock(func() error {
		if state, err := readSuiteSharedState(statePath); err == nil && strings.TrimSpace(state.AdminDSN) != "" {
			if err := pingAdminDSN(ctx, state.AdminDSN); err == nil {
				resolved = state.AdminDSN
				return nil
			}
		}

		dsn, err := startOrDiscoverSuiteSharedAdminDSN(ctx)
		if err != nil {
			return err
		}
		if err := writeSuiteSharedState(statePath, suiteSharedStateFile{
			ContainerName: suiteSharedContainerName,
			AdminDSN:      dsn,
		}); err != nil {
			return err
		}
		resolved = dsn
		return nil
	})
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func startOrDiscoverSuiteSharedAdminDSN(ctx context.Context) (string, error) {
	if dsn, err := adminDSNFromNamedContainer(ctx, suiteSharedContainerName); err == nil {
		if err := pingAdminDSN(ctx, dsn); err == nil {
			return dsn, nil
		}
	}

	if _, err := dockerCommandOutputFunc(ctx,
		"run",
		"-d",
		"--rm",
		"--name", suiteSharedContainerName,
		"-e", "MYSQL_ROOT_PASSWORD=test",
		"-e", "MYSQL_ROOT_HOST=%",
		"-e", "MYSQL_DATABASE=mysql",
		"-p", "127.0.0.1::3306",
		suiteSharedDefaultImage,
	); err != nil {
		if dsn, portErr := adminDSNFromNamedContainer(ctx, suiteSharedContainerName); portErr == nil {
			if pingErr := pingAdminDSN(ctx, dsn); pingErr == nil {
				return dsn, nil
			}
		}
		return "", fmt.Errorf("start suite shared mysql: %w", err)
	}

	dsn, err := adminDSNFromNamedContainer(ctx, suiteSharedContainerName)
	if err != nil {
		return "", err
	}
	if err := pingAdminDSN(ctx, dsn); err != nil {
		return "", fmt.Errorf("ping suite shared mysql: %w", err)
	}
	return dsn, nil
}

func adminDSNFromNamedContainer(ctx context.Context, containerName string) (string, error) {
	output, err := dockerCommandOutputFunc(ctx, "port", containerName, "3306/tcp")
	if err != nil {
		return "", err
	}
	portLine := strings.TrimSpace(output)
	if portLine == "" {
		return "", fmt.Errorf("resolve mapped port for %s: empty output", containerName)
	}
	port := portLine
	if idx := strings.LastIndex(portLine, ":"); idx >= 0 {
		port = portLine[idx+1:]
	}
	if port == "" {
		return "", fmt.Errorf("resolve mapped port for %s: malformed output %q", containerName, portLine)
	}
	return fmt.Sprintf("root:test@tcp(127.0.0.1:%s)/mysql?%s", port, suiteSharedDefaultDSNOpts), nil
}

func pingAdminDSN(ctx context.Context, dsn string) error {
	adminDB, err := openAdminDBWithRetryFunc(ctx, dsn)
	if err != nil {
		return err
	}
	return adminDB.Close()
}

func withSuiteSharedLock(fn func() error) error {
	lockPath := filepath.Join(sharedStateDir(), suiteSharedLockFileName)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("create suite shared state dir: %w", err)
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open suite shared lock: %w", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock suite shared mysql state: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	return fn()
}

func readSuiteSharedState(path string) (suiteSharedStateFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return suiteSharedStateFile{}, err
	}
	var state suiteSharedStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return suiteSharedStateFile{}, fmt.Errorf("decode suite shared state: %w", err)
	}
	return state, nil
}

func writeSuiteSharedState(path string, state suiteSharedStateFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create suite shared state dir: %w", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode suite shared state: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write suite shared state: %w", err)
	}
	return nil
}

func sharedStateDir() string {
	if dir := strings.TrimSpace(os.Getenv(sharedStateDirEnvVar)); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "mreviewer-dbtest")
}

func dockerCommandOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		if trimmed == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, trimmed)
	}
	return trimmed, nil
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
