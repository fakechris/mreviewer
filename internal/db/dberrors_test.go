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
