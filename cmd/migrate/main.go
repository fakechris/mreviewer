package main

import (
	"fmt"
	"log"
	"os"

	_ "github.com/go-sql-driver/mysql"
	"github.com/mreviewer/mreviewer/internal/database"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dsn := os.Getenv("GOOSE_DBSTRING")
	if dsn == "" {
		return fmt.Errorf("GOOSE_DBSTRING is required")
	}

	migrationsDir := os.Getenv("GOOSE_MIGRATION_DIR")
	if migrationsDir == "" {
		migrationsDir = "/app/migrations"
	}

	db, dialect, err := database.OpenWithDialect(dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	gooseDialect := "mysql"
	if dialect == database.DialectSQLite {
		gooseDialect = "sqlite3"
		if migrationsDir == "/app/migrations" {
			migrationsDir = "/app/migrations_sqlite"
		}
	}

	if err := goose.SetDialect(gooseDialect); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.Up(db, migrationsDir); err != nil {
		return fmt.Errorf("run goose up: %w", err)
	}

	return nil
}
