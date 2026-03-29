// Package database provides database connection setup with production-grade
// connection pool configuration. Supports MySQL and SQLite backends.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

const pingTimeout = 5 * time.Second

// Open creates a new *sql.DB with production connection pool settings.
// It verifies connectivity with a PingContext call so the service fails
// fast when the database is unreachable at startup. The caller should defer
// db.Close().
func Open(dsn string) (*sql.DB, error) {
	conn, _, err := OpenWithDialect(dsn)
	return conn, err
}

// OpenWithDialect creates a new *sql.DB and returns the detected dialect.
// SQLite DSNs are detected by prefix ("sqlite://", "file:") or suffix (".db", ".sqlite").
// All other DSNs are treated as MySQL.
func OpenWithDialect(dsn string) (*sql.DB, Dialect, error) {
	if dsn == "" {
		return nil, DialectMySQL, fmt.Errorf("database: empty DSN")
	}

	dialect := DetectDialect(dsn)
	switch dialect {
	case DialectSQLite:
		return openSQLite(dsn)
	default:
		return openMySQL(dsn)
	}
}

func openMySQL(dsn string) (*sql.DB, Dialect, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, DialectMySQL, fmt.Errorf("database: open mysql: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, DialectMySQL, fmt.Errorf("database: ping mysql: %w", err)
	}

	return db, DialectMySQL, nil
}

func openSQLite(dsn string) (*sql.DB, Dialect, error) {
	// Normalize DSN: strip "sqlite://" prefix if present.
	sqliteDSN := dsn
	if strings.HasPrefix(strings.ToLower(dsn), "sqlite://") {
		sqliteDSN = dsn[len("sqlite://"):]
	}

	db, err := sql.Open("sqlite", sqliteDSN)
	if err != nil {
		return nil, DialectSQLite, fmt.Errorf("database: open sqlite: %w", err)
	}

	// SQLite optimal settings for single-writer mode.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, DialectSQLite, fmt.Errorf("database: ping sqlite: %w", err)
	}

	// Enable WAL mode and foreign keys.
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			db.Close()
			return nil, DialectSQLite, fmt.Errorf("database: sqlite pragma %q: %w", p, err)
		}
	}

	return db, DialectSQLite, nil
}
