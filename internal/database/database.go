// Package database provides MySQL connection setup with production-grade
// connection pool configuration.
package database

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Open creates a new *sql.DB with production connection pool settings.
// The caller should defer db.Close().
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

	return db, nil
}
