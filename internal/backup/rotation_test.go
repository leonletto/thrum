package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

func TestCompressCurrentToArchive(t *testing.T) {
	base := t.TempDir()
	currentDir := filepath.Join(base, "current")
	archivesDir := filepath.Join(base, "archives")

	if err := os.MkdirAll(currentDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(currentDir, "events.jsonl"), []byte(`{"e":1}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(currentDir, "messages"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(currentDir, "messages", "agent1.jsonl"), []byte(`{"m":1}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	zipPath, err := CompressCurrentToArchive(currentDir, archivesDir)
	if err != nil {
		t.Fatalf("CompressCurrentToArchive() error: %v", err)
	}

	if zipPath == "" {
		t.Fatal("expected non-empty zip path")
	}

	// Verify zip exists
	info, err := os.Stat(zipPath)
	if err != nil {
		t.Fatalf("zip not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("expected non-empty zip file")
	}

	// Verify current dir was cleared
	if _, err := os.Stat(currentDir); !os.IsNotExist(err) {
		t.Error("expected current dir to be removed after archive")
	}
}

func TestCompressCurrentToArchive_EmptyDir(t *testing.T) {
	base := t.TempDir()
	currentDir := filepath.Join(base, "current")
	archivesDir := filepath.Join(base, "archives")
	if err := os.MkdirAll(currentDir, 0750); err != nil {
		t.Fatal(err)
	}

	zipPath, err := CompressCurrentToArchive(currentDir, archivesDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if zipPath != "" {
		t.Errorf("expected empty zip path for empty dir, got %q", zipPath)
	}
}

func TestCompressCurrentToArchive_NoDir(t *testing.T) {
	base := t.TempDir()
	zipPath, err := CompressCurrentToArchive(filepath.Join(base, "nonexistent"), filepath.Join(base, "archives"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if zipPath != "" {
		t.Errorf("expected empty zip path, got %q", zipPath)
	}
}

func createFakeArchive(t *testing.T, archivesDir string, ts time.Time) string {
	t.Helper()
	name := ts.UTC().Format(archiveTimeFormat) + ".zip"
	path := filepath.Join(archivesDir, name)
	if err := os.WriteFile(path, []byte("fake zip"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApplyRetention_Daily(t *testing.T) {
	archivesDir := filepath.Join(t.TempDir(), "archives")
	if err := os.MkdirAll(archivesDir, 0750); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		createFakeArchive(t, archivesDir, base.AddDate(0, 0, -i))
	}

	retention := config.RetentionConfig{Daily: 5, Weekly: 0, Monthly: 0}
	if err := ApplyRetention(archivesDir, retention); err != nil {
		t.Fatalf("ApplyRetention() error: %v", err)
	}

	entries, _ := os.ReadDir(archivesDir)
	if len(entries) != 5 {
		t.Errorf("expected 5 archives after retention, got %d", len(entries))
	}
}

func TestApplyRetention_Weekly(t *testing.T) {
	archivesDir := filepath.Join(t.TempDir(), "archives")
	if err := os.MkdirAll(archivesDir, 0750); err != nil {
		t.Fatal(err)
	}

	// Create archives spanning 8 weeks
	base := time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		createFakeArchive(t, archivesDir, base.AddDate(0, 0, -i*7))
	}

	retention := config.RetentionConfig{Daily: 0, Weekly: 4, Monthly: 0}
	if err := ApplyRetention(archivesDir, retention); err != nil {
		t.Fatalf("ApplyRetention() error: %v", err)
	}

	entries, _ := os.ReadDir(archivesDir)
	if len(entries) != 4 {
		t.Errorf("expected 4 archives after weekly retention, got %d", len(entries))
	}
}

func TestApplyRetention_MonthlyForever(t *testing.T) {
	archivesDir := filepath.Join(t.TempDir(), "archives")
	if err := os.MkdirAll(archivesDir, 0750); err != nil {
		t.Fatal(err)
	}

	// Create one archive per month for 12 months
	base := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		createFakeArchive(t, archivesDir, base.AddDate(0, -i, 0))
	}

	retention := config.RetentionConfig{Daily: 0, Weekly: 0, Monthly: -1} // keep forever
	if err := ApplyRetention(archivesDir, retention); err != nil {
		t.Fatalf("ApplyRetention() error: %v", err)
	}

	entries, _ := os.ReadDir(archivesDir)
	if len(entries) != 12 {
		t.Errorf("expected all 12 monthly archives kept, got %d", len(entries))
	}
}

func TestApplyRetention_PreRestoreExempt(t *testing.T) {
	archivesDir := filepath.Join(t.TempDir(), "archives")
	if err := os.MkdirAll(archivesDir, 0750); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		createFakeArchive(t, archivesDir, base.AddDate(0, 0, -i))
	}

	// Add pre-restore safety backup
	preRestorePath := filepath.Join(archivesDir, "pre-restore-2026-02-28T091500.zip")
	if err := os.WriteFile(preRestorePath, []byte("safety"), 0600); err != nil {
		t.Fatal(err)
	}

	retention := config.RetentionConfig{Daily: 1, Weekly: 0, Monthly: 0}
	if err := ApplyRetention(archivesDir, retention); err != nil {
		t.Fatalf("ApplyRetention() error: %v", err)
	}

	// Pre-restore should survive even though daily=1
	if _, err := os.Stat(preRestorePath); err != nil {
		t.Error("pre-restore backup should not be deleted by rotation")
	}

	entries, _ := os.ReadDir(archivesDir)
	// 1 daily + 1 pre-restore = 2
	if len(entries) != 2 {
		t.Errorf("expected 2 files (1 daily + 1 pre-restore), got %d", len(entries))
	}
}

func TestApplyRetention_EmptyDir(t *testing.T) {
	archivesDir := filepath.Join(t.TempDir(), "archives")
	if err := os.MkdirAll(archivesDir, 0750); err != nil {
		t.Fatal(err)
	}

	err := ApplyRetention(archivesDir, config.RetentionConfig{Daily: 5})
	if err != nil {
		t.Fatalf("unexpected error on empty dir: %v", err)
	}
}

func TestApplyRetention_NonexistentDir(t *testing.T) {
	err := ApplyRetention(filepath.Join(t.TempDir(), "nope"), config.RetentionConfig{Daily: 5})
	if err != nil {
		t.Fatalf("unexpected error on nonexistent dir: %v", err)
	}
}
