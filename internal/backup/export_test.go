package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExportSyncData(t *testing.T) {
	// Create mock sync worktree
	syncDir := filepath.Join(t.TempDir(), "a-sync")
	if err := os.MkdirAll(filepath.Join(syncDir, "messages"), 0750); err != nil {
		t.Fatal(err)
	}

	// Write events.jsonl
	eventsData := `{"event_id":"evt_1","type":"agent.register"}
{"event_id":"evt_2","type":"message.create"}
{"event_id":"evt_3","type":"agent.session.start"}
`
	if err := os.WriteFile(filepath.Join(syncDir, "events.jsonl"), []byte(eventsData), 0600); err != nil {
		t.Fatal(err)
	}

	// Write message files
	for _, name := range []string{"agent1.jsonl", "agent2.jsonl"} {
		data := `{"event_id":"evt_m1","type":"message.create"}
`
		if err := os.WriteFile(filepath.Join(syncDir, "messages", name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}

	backupDir := filepath.Join(t.TempDir(), "backup")
	result, err := ExportSyncData(syncDir, backupDir)
	if err != nil {
		t.Fatalf("ExportSyncData() error: %v", err)
	}

	if result.EventLines != 3 {
		t.Errorf("expected 3 event lines, got %d", result.EventLines)
	}
	if result.MessageFiles != 2 {
		t.Errorf("expected 2 message files, got %d", result.MessageFiles)
	}

	// Verify files exist
	if _, err := os.Stat(filepath.Join(backupDir, "events.jsonl")); err != nil {
		t.Error("events.jsonl not found in backup")
	}
	for _, name := range []string{"agent1.jsonl", "agent2.jsonl"} {
		if _, err := os.Stat(filepath.Join(backupDir, "messages", name)); err != nil {
			t.Errorf("%s not found in backup", name)
		}
	}
}

func TestExportSyncData_MissingSyncDir(t *testing.T) {
	_, err := ExportSyncData("/nonexistent/dir", t.TempDir())
	if err == nil {
		t.Error("expected error for missing sync dir")
	}
}

func TestExportSyncData_EmptyMessagesDir(t *testing.T) {
	syncDir := filepath.Join(t.TempDir(), "a-sync")
	if err := os.MkdirAll(filepath.Join(syncDir, "messages"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(syncDir, "events.jsonl"), []byte(`{"e":1}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(t.TempDir(), "backup")
	result, err := ExportSyncData(syncDir, backupDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MessageFiles != 0 {
		t.Errorf("expected 0 message files, got %d", result.MessageFiles)
	}
}

func TestExportSyncData_NoMessagesDir(t *testing.T) {
	syncDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(syncDir, "events.jsonl"), []byte(`{"e":1}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(t.TempDir(), "backup")
	result, err := ExportSyncData(syncDir, backupDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.EventLines != 1 {
		t.Errorf("expected 1 event line, got %d", result.EventLines)
	}
	if result.MessageFiles != 0 {
		t.Errorf("expected 0 message files, got %d", result.MessageFiles)
	}
}

func TestExportSyncData_NoEventsFile(t *testing.T) {
	syncDir := t.TempDir()
	// Only messages dir, no events.jsonl
	if err := os.MkdirAll(filepath.Join(syncDir, "messages"), 0750); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(t.TempDir(), "backup")
	result, err := ExportSyncData(syncDir, backupDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.EventLines != 0 {
		t.Errorf("expected 0 event lines, got %d", result.EventLines)
	}
}
