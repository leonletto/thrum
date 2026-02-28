package backup

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

func TestRunBackup(t *testing.T) {
	// Setup sync dir
	syncDir := filepath.Join(t.TempDir(), "a-sync")
	if err := os.MkdirAll(filepath.Join(syncDir, "messages"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(syncDir, "events.jsonl"), []byte("{\"e\":1}\n{\"e\":2}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(syncDir, "messages", "agent1.jsonl"), []byte("{\"m\":1}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Setup thrum dir
	thrumDir := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "identities", "test.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	// Setup SQLite
	dbPath := filepath.Join(t.TempDir(), "messages.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE message_reads (message_id TEXT, session_id TEXT, agent_id TEXT, read_at TEXT, PRIMARY KEY(message_id, session_id))`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE subscriptions (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT, scope_type TEXT, scope_value TEXT, mention_role TEXT, created_at TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE sync_checkpoints (peer_daemon_id TEXT PRIMARY KEY, last_synced_sequence INTEGER, last_sync_timestamp INTEGER, sync_status TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO message_reads VALUES ('msg_1', 'ses_1', 'agent_a', '2026-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	backupDir := t.TempDir()
	result, err := RunBackup(BackupOptions{
		BackupDir:    backupDir,
		RepoName:     "test-repo",
		SyncDir:      syncDir,
		ThrumDir:     thrumDir,
		DBPath:       dbPath,
		ThrumVersion: "0.5.0",
	})
	if err != nil {
		t.Fatalf("RunBackup() error: %v", err)
	}

	// Verify result
	if result.SyncResult.EventLines != 2 {
		t.Errorf("expected 2 event lines, got %d", result.SyncResult.EventLines)
	}
	if result.SyncResult.MessageFiles != 1 {
		t.Errorf("expected 1 message file, got %d", result.SyncResult.MessageFiles)
	}
	if result.LocalResult.Tables["message_reads"] != 1 {
		t.Errorf("expected 1 message_reads row, got %d", result.LocalResult.Tables["message_reads"])
	}

	// Verify manifest
	m := result.Manifest
	if m.Version != 1 {
		t.Errorf("expected manifest version 1, got %d", m.Version)
	}
	if m.RepoName != "test-repo" {
		t.Errorf("expected repo name test-repo, got %q", m.RepoName)
	}
	if m.ThrumVersion != "0.5.0" {
		t.Errorf("expected thrum version 0.5.0, got %q", m.ThrumVersion)
	}
	if m.Counts.Events != 2 {
		t.Errorf("expected 2 events in manifest, got %d", m.Counts.Events)
	}

	// Verify manifest file on disk
	loaded, err := ReadManifest(result.CurrentDir)
	if err != nil {
		t.Fatalf("ReadManifest() error: %v", err)
	}
	if loaded.RepoName != "test-repo" {
		t.Errorf("loaded manifest repo name: %q", loaded.RepoName)
	}

	// Verify directory structure
	currentDir := filepath.Join(backupDir, "test-repo", "current")
	for _, path := range []string{
		"events.jsonl",
		"messages/agent1.jsonl",
		"local/message_reads.jsonl",
		"config/config.json",
		"config/identities/test.json",
		"manifest.json",
	} {
		if _, err := os.Stat(filepath.Join(currentDir, path)); err != nil {
			t.Errorf("expected %s in backup: %v", path, err)
		}
	}
}

func TestRunBackup_MinimalOptions(t *testing.T) {
	backupDir := t.TempDir()
	result, err := RunBackup(BackupOptions{
		BackupDir:    backupDir,
		RepoName:     "minimal",
		ThrumVersion: "0.5.0",
	})
	if err != nil {
		t.Fatalf("RunBackup() error: %v", err)
	}

	if result.Manifest.RepoName != "minimal" {
		t.Errorf("expected repo name minimal, got %q", result.Manifest.RepoName)
	}
}

func TestRunBackup_MissingBackupDir(t *testing.T) {
	_, err := RunBackup(BackupOptions{RepoName: "test"})
	if err == nil {
		t.Error("expected error for missing backup dir")
	}
}

func TestRunBackup_MissingRepoName(t *testing.T) {
	_, err := RunBackup(BackupOptions{BackupDir: t.TempDir()})
	if err == nil {
		t.Error("expected error for missing repo name")
	}
}
