package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

func TestLoadThrumConfig_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Daemon.LocalOnly {
		t.Error("expected LocalOnly=false when no config file exists")
	}
}

func TestLoadThrumConfig_LocalOnlyTrue(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"daemon":{"local_only":true}}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Daemon.LocalOnly {
		t.Error("expected LocalOnly=true")
	}
}

func TestLoadThrumConfig_LocalOnlyFalse(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"daemon":{"local_only":false}}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Daemon.LocalOnly {
		t.Error("expected LocalOnly=false")
	}
}

func TestLoadThrumConfig_EmptyJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Daemon.LocalOnly {
		t.Error("expected LocalOnly=false for empty config")
	}
}

func TestLoadThrumConfig_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{invalid`), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadThrumConfig(tmpDir)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSaveThrumConfig_CreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{Daemon: config.DaemonConfig{LocalOnly: true}}

	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read back and verify
	loaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("failed to load saved config: %v", err)
	}
	if !loaded.Daemon.LocalOnly {
		t.Error("expected LocalOnly=true after save")
	}
}

func TestSaveThrumConfig_PreservesUnknownKeys(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Write a config with an extra top-level key
	if err := os.WriteFile(configPath, []byte(`{"custom":"keep_me","daemon":{"local_only":false}}`), 0600); err != nil {
		t.Fatal(err)
	}

	// Save with local_only=true
	cfg := &config.ThrumConfig{Daemon: config.DaemonConfig{LocalOnly: true}}
	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read raw file and check "custom" key survived
	data, err := os.ReadFile(configPath) //nolint:gosec // test file, path from t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "keep_me") {
		t.Errorf("unknown key was lost after save, got:\n%s", content)
	}

	// Verify daemon section was updated
	loaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Daemon.LocalOnly {
		t.Error("expected LocalOnly=true after save")
	}
}
