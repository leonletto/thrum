package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExportConfigFiles(t *testing.T) {
	// Create mock thrum dir
	thrumDir := filepath.Join(t.TempDir(), ".thrum")
	for _, dir := range []string{"identities", "context", "var"} {
		if err := os.MkdirAll(filepath.Join(thrumDir, dir), 0750); err != nil {
			t.Fatal(err)
		}
	}

	// Write files
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{"daemon":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "identities", "coord_main.json"), []byte(`{"name":"coord"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "identities", "impl_auth.json"), []byte(`{"name":"impl"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "context", "coord_main.md"), []byte("# Context"), 0600); err != nil {
		t.Fatal(err)
	}

	// Write runtime files that should NOT be copied
	if err := os.WriteFile(filepath.Join(thrumDir, "var", "messages.db"), []byte("sqlite"), 0600); err != nil {
		t.Fatal(err)
	}

	backupDir := t.TempDir()
	if err := ExportConfigFiles(thrumDir, backupDir); err != nil {
		t.Fatalf("ExportConfigFiles() error: %v", err)
	}

	// Verify config.json
	if _, err := os.Stat(filepath.Join(backupDir, "config", "config.json")); err != nil {
		t.Error("config.json not found in backup")
	}

	// Verify identities
	for _, name := range []string{"coord_main.json", "impl_auth.json"} {
		if _, err := os.Stat(filepath.Join(backupDir, "config", "identities", name)); err != nil {
			t.Errorf("identity %s not found in backup", name)
		}
	}

	// Verify context
	if _, err := os.Stat(filepath.Join(backupDir, "config", "context", "coord_main.md")); err != nil {
		t.Error("context file not found in backup")
	}

	// Verify var/ was NOT copied
	if _, err := os.Stat(filepath.Join(backupDir, "config", "var")); err == nil {
		t.Error("var/ directory should not be in backup")
	}
}

func TestExportConfigFiles_MissingOptionalDirs(t *testing.T) {
	thrumDir := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatal(err)
	}
	// Only config.json, no identities/ or context/
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	backupDir := t.TempDir()
	if err := ExportConfigFiles(thrumDir, backupDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(backupDir, "config", "config.json")); err != nil {
		t.Error("config.json not found")
	}
}

func TestExportConfigFiles_NoConfigFile(t *testing.T) {
	thrumDir := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatal(err)
	}

	backupDir := t.TempDir()
	if err := ExportConfigFiles(thrumDir, backupDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No config.json is fine — dir should still be created
	if _, err := os.Stat(filepath.Join(backupDir, "config")); err != nil {
		t.Error("config backup dir not created")
	}
}
