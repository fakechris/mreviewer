package database

import "strings"

// Dialect represents the SQL database engine in use.
type Dialect int

const (
	DialectMySQL  Dialect = iota
	DialectSQLite
)

func (d Dialect) String() string {
	switch d {
	case DialectSQLite:
		return "sqlite"
	default:
		return "mysql"
	}
}

// DetectDialect determines the database dialect from a DSN string.
// SQLite DSNs start with "sqlite://", "file:", or end with ".db"/".sqlite".
// Everything else is treated as MySQL.
func DetectDialect(dsn string) Dialect {
	lower := strings.ToLower(dsn)
	if strings.HasPrefix(lower, "sqlite://") ||
		strings.HasPrefix(lower, "file:") ||
		strings.HasSuffix(lower, ".db") ||
		strings.HasSuffix(lower, ".sqlite") ||
		strings.HasSuffix(lower, ".sqlite3") ||
		lower == ":memory:" {
		return DialectSQLite
	}
	return DialectMySQL
}
