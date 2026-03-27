package db

import (
	"errors"
	"strings"

	mysql "github.com/go-sql-driver/mysql"
)

func IsDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	// MySQL: error 1062 "Duplicate entry".
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return true
	}
	msg := err.Error()
	if strings.Contains(msg, "Duplicate entry") || strings.Contains(msg, "Error 1062") {
		return true
	}
	// SQLite: UNIQUE constraint failed.
	if strings.Contains(msg, "UNIQUE constraint failed") {
		return true
	}
	return false
}
