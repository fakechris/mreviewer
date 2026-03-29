package database

import (
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/db/sqlitedb"
)

// NewStore creates a db.Store backed by the appropriate implementation for the
// given dialect. For MySQL it returns *db.Queries (sqlc-generated); for SQLite
// it returns *sqlitedb.Queries (hand-written).
func NewStore(conn db.DBTX, dialect Dialect) db.Store {
	switch dialect {
	case DialectSQLite:
		return sqlitedb.New(conn)
	default:
		return db.New(conn)
	}
}

// StoreFactory returns a constructor function that creates db.Store instances
// for the given dialect. This allows services to create stores from arbitrary
// db.DBTX values (e.g. transactions) without knowing which dialect is in use.
func StoreFactory(dialect Dialect) func(db.DBTX) db.Store {
	switch dialect {
	case DialectSQLite:
		return func(conn db.DBTX) db.Store { return sqlitedb.New(conn) }
	default:
		return func(conn db.DBTX) db.Store { return db.New(conn) }
	}
}
