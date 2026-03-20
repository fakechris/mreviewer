package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/go-sql-driver/mysql"
	"github.com/pressly/goose/v3"
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

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("open mysql: %w", err)
	}
	defer db.Close()

	if err := goose.SetDialect("mysql"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.Up(db, migrationsDir); err != nil {
		return fmt.Errorf("run goose up: %w", err)
	}

	return nil
}
