package database

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed embeddedmigrations/mysql/*.sql embeddedmigrations/sqlite/*.sql
var embeddedMigrations embed.FS

// MigrateUp opens the DSN, applies embedded migrations, and closes the DB.
func MigrateUpFromDSN(dsn string) error {
	db, dialect, err := OpenWithDialect(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	return MigrateUp(db, dialect)
}

// MigrateUp applies embedded migrations for the detected dialect.
func MigrateUp(db *sql.DB, dialect Dialect) error {
	if db == nil {
		return fmt.Errorf("database: db is required for migrations")
	}
	goose.SetBaseFS(embeddedMigrations)

	dir := "embeddedmigrations/mysql"
	gooseDialect := "mysql"
	if dialect == DialectSQLite {
		dir = "embeddedmigrations/sqlite"
		gooseDialect = "sqlite3"
	}

	if err := goose.SetDialect(gooseDialect); err != nil {
		return fmt.Errorf("database: set goose dialect: %w", err)
	}
	if err := goose.Up(db, dir); err != nil {
		return fmt.Errorf("database: run goose up: %w", err)
	}
	return nil
}
