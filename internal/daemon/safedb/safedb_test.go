package safedb_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *safedb.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	_, err = raw.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, name TEXT)")
	if err != nil {
		t.Fatal(err)
	}
	return safedb.New(raw)
}

func TestQueryContext(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, "INSERT INTO test (name) VALUES (?)", "alice")
	if err != nil {
		t.Fatal(err)
	}

	rows, err := db.QueryContext(ctx, "SELECT name FROM test WHERE id = ?", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("expected a row")
	}
	var name string
	if err := rows.Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "alice" {
		t.Fatalf("got %q, want %q", name, "alice")
	}
}

func TestQueryRowContext(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, "INSERT INTO test (name) VALUES (?)", "bob")
	if err != nil {
		t.Fatal(err)
	}

	var name string
	err = db.QueryRowContext(ctx, "SELECT name FROM test WHERE id = ?", 1).Scan(&name)
	if err != nil {
		t.Fatal(err)
	}
	if name != "bob" {
		t.Fatalf("got %q, want %q", name, "bob")
	}
}

func TestCancelledContext(t *testing.T) {
	db := openTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := db.ExecContext(ctx, "INSERT INTO test (name) VALUES (?)", "carol")
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
}

func TestBeginTx(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO test (name) VALUES (?)", "dave")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestRawEscape(t *testing.T) {
	db := openTestDB(t)
	raw := db.Raw()
	if raw == nil {
		t.Fatal("Raw() returned nil")
	}
}

func TestClose(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db := safedb.New(raw)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
