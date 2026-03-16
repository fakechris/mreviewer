// Package db contains sqlc-generated query code and transaction helpers for
// MySQL-backed persistence. The helpers ensure that multi-row lifecycle writes
// (webhook intake, review runs, comment actions) are atomic: either the full
// set of rows is committed or nothing is persisted.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// TxFunc is the callback executed inside a transaction. The *Queries argument
// is scoped to the transaction and must be used for all DB work.
type TxFunc func(ctx context.Context, q *Queries) error

// RunTx executes fn inside a single database transaction. If fn returns a
// non-nil error or panics the transaction is rolled back; otherwise it is
// committed. This prevents partial lifecycle writes such as orphaned
// hook_events, review_runs, or comment_actions records.
func RunTx(ctx context.Context, db *sql.DB, fn TxFunc) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}

	q := New(tx)

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p) // re-raise after rollback
		}
	}()

	if err := fn(ctx, q); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			// Preserve both the original function error and the rollback
			// error so that errors.Is works against either one.
			return errors.Join(err, fmt.Errorf("db: rollback: %w", rbErr))
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

// RunTxWith is like RunTx but accepts a pre-configured *sql.TxOptions for
// setting isolation level or read-only mode.
func RunTxWith(ctx context.Context, db *sql.DB, opts *sql.TxOptions, fn TxFunc) error {
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}

	q := New(tx)

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(ctx, q); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			// Preserve both the original function error and the rollback
			// error so that errors.Is works against either one.
			return errors.Join(err, fmt.Errorf("db: rollback: %w", rbErr))
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}
