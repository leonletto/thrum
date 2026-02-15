package safedb

import (
	"context"
	"database/sql"
)

// DB wraps *sql.DB and ONLY exposes context-aware methods.
// This enforces at compile time that all database queries pass a context,
// ensuring the server's per-request timeout propagates to SQLite.
//
// Raw Query/Exec/QueryRow are deliberately hidden — any code that
// tries to use them gets a compile error, forcing migration to
// the context-aware variants.
type DB struct {
	db *sql.DB
}

// New wraps a *sql.DB in the safe wrapper.
func New(db *sql.DB) *DB {
	return &DB{db: db}
}

// QueryContext executes a query that returns rows.
func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.db.QueryContext(ctx, query, args...)
}

// QueryRowContext executes a query that returns at most one row.
func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.db.QueryRowContext(ctx, query, args...)
}

// ExecContext executes a query that doesn't return rows.
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.db.ExecContext(ctx, query, args...)
}

// BeginTx starts a transaction with context.
func (d *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return d.db.BeginTx(ctx, opts)
}

// Raw returns the underlying *sql.DB for schema setup and migrations ONLY.
// Using this in handler code is a code review red flag.
func (d *DB) Raw() *sql.DB {
	return d.db
}

// Close closes the underlying database connection.
func (d *DB) Close() error {
	return d.db.Close()
}
