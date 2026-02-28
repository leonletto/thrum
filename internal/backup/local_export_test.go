package backup

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	// Create local-only tables matching the real schema
	tables := []string{
		`CREATE TABLE message_reads (
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			agent_id   TEXT NOT NULL,
			read_at    TEXT NOT NULL,
			PRIMARY KEY (message_id, session_id)
		)`,
		`CREATE TABLE subscriptions (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id   TEXT NOT NULL,
			scope_type   TEXT,
			scope_value  TEXT,
			mention_role TEXT,
			created_at   TEXT NOT NULL
		)`,
		`CREATE TABLE sync_checkpoints (
			peer_daemon_id TEXT PRIMARY KEY,
			last_synced_sequence INTEGER NOT NULL DEFAULT 0,
			last_sync_timestamp INTEGER NOT NULL,
			sync_status TEXT NOT NULL DEFAULT 'idle'
		)`,
	}

	for _, ddl := range tables {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	return db
}

func TestExportLocalTables(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Insert test data
	_, err := db.Exec(`INSERT INTO message_reads (message_id, session_id, agent_id, read_at) VALUES
		('msg_1', 'ses_1', 'agent_a', '2026-01-01T00:00:00Z'),
		('msg_2', 'ses_1', 'agent_a', '2026-01-01T00:01:00Z')`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`INSERT INTO subscriptions (session_id, scope_type, scope_value, mention_role, created_at) VALUES
		('ses_1', 'agent', 'agent_a', '', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`INSERT INTO sync_checkpoints (peer_daemon_id, last_synced_sequence, last_sync_timestamp, sync_status) VALUES
		('d_abc123', 42, 1706000000, 'idle')`)
	if err != nil {
		t.Fatal(err)
	}

	backupDir := t.TempDir()
	result, err := ExportLocalTables(db, backupDir)
	if err != nil {
		t.Fatalf("ExportLocalTables() error: %v", err)
	}

	if result.Tables["message_reads"] != 2 {
		t.Errorf("expected 2 message_reads rows, got %d", result.Tables["message_reads"])
	}
	if result.Tables["subscriptions"] != 1 {
		t.Errorf("expected 1 subscription row, got %d", result.Tables["subscriptions"])
	}
	if result.Tables["sync_checkpoints"] != 1 {
		t.Errorf("expected 1 sync_checkpoint row, got %d", result.Tables["sync_checkpoints"])
	}

	// Verify JSONL is valid
	for _, table := range localOnlyTables {
		path := filepath.Join(backupDir, "local", table+".jsonl")
		f, err := os.Open(path) //nolint:gosec // test file
		if err != nil {
			t.Errorf("open %s: %v", table, err)
			continue
		}
		scanner := bufio.NewScanner(f)
		lineCount := 0
		for scanner.Scan() {
			var row map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
				t.Errorf("%s line %d: invalid JSON: %v", table, lineCount+1, err)
			}
			lineCount++
		}
		_ = f.Close()
		if lineCount != result.Tables[table] {
			t.Errorf("%s: expected %d lines, got %d", table, result.Tables[table], lineCount)
		}
	}
}

func TestExportLocalTables_EmptyTables(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	backupDir := t.TempDir()
	result, err := ExportLocalTables(db, backupDir)
	if err != nil {
		t.Fatalf("ExportLocalTables() error: %v", err)
	}

	for _, table := range localOnlyTables {
		if result.Tables[table] != 0 {
			t.Errorf("expected 0 rows for %s, got %d", table, result.Tables[table])
		}
		// File should still exist (empty)
		path := filepath.Join(backupDir, "local", table+".jsonl")
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s file to exist: %v", table, err)
		} else if info.Size() != 0 {
			t.Errorf("expected empty file for %s, got size %d", table, info.Size())
		}
	}
}

func TestExportLocalTables_RoundTrip(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	_, err := db.Exec(`INSERT INTO message_reads (message_id, session_id, agent_id, read_at) VALUES
		('msg_1', 'ses_1', 'agent_a', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}

	backupDir := t.TempDir()
	_, err = ExportLocalTables(db, backupDir)
	if err != nil {
		t.Fatalf("ExportLocalTables() error: %v", err)
	}

	// Parse back and verify field values
	path := filepath.Join(backupDir, "local", "message_reads.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var row map[string]any
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatal(err)
	}

	if row["message_id"] != "msg_1" {
		t.Errorf("expected message_id=msg_1, got %v", row["message_id"])
	}
	if row["agent_id"] != "agent_a" {
		t.Errorf("expected agent_id=agent_a, got %v", row["agent_id"])
	}
}
