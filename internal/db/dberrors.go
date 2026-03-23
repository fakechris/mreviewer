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
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return true
	}
	return strings.Contains(err.Error(), "Duplicate entry") ||
		strings.Contains(err.Error(), "Error 1062")
}
