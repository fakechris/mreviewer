// Package database provides MySQL connection setup with production-grade
// connection pool configuration.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

const pingTimeout = 5 * time.Second

// Open creates a new *sql.DB with production connection pool settings.
// It verifies connectivity with a PingContext call so the service fails
// fast when MySQL is unreachable at startup. The caller should defer
// db.Close().
func Open(dsn string) (*sql.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("database: empty DSN")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("database: open: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	// Fail fast: verify that MySQL is actually reachable before returning
	// the pool to the caller.
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("database: ping: %w", err)
	}

	return db, nil
}
