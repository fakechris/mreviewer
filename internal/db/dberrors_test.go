package db

import (
	"errors"
	"fmt"
	"testing"

	mysql "github.com/go-sql-driver/mysql"
)

func TestIsDuplicateKeyErrorDetectsMySQLError1062(t *testing.T) {
	err := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry"}
	if !IsDuplicateKeyError(err) {
		t.Fatal("expected duplicate-key error to be detected")
	}
}

func TestIsDuplicateKeyErrorDetectsWrappedMySQLError1062(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &mysql.MySQLError{Number: 1062, Message: "Duplicate entry"})
	if !IsDuplicateKeyError(err) {
		t.Fatal("expected wrapped duplicate-key error to be detected")
	}
}

func TestIsDuplicateKeyErrorRejectsOtherErrors(t *testing.T) {
	if IsDuplicateKeyError(errors.New("plain error")) {
		t.Fatal("expected non-duplicate error to be rejected")
	}
}

func TestIsDuplicateKeyErrorDetectsSQLiteUniqueConstraint(t *testing.T) {
	err := errors.New("UNIQUE constraint failed: gitlab_instances.url")
	if !IsDuplicateKeyError(err) {
		t.Fatal("expected SQLite UNIQUE constraint error to be detected")
	}
}

func TestIsDuplicateKeyErrorDetectsWrappedSQLiteUniqueConstraint(t *testing.T) {
	err := fmt.Errorf("insert failed: %w", errors.New("UNIQUE constraint failed: projects.gitlab_instance_id, projects.gitlab_project_id"))
	if !IsDuplicateKeyError(err) {
		t.Fatal("expected wrapped SQLite UNIQUE constraint error to be detected")
	}
}
