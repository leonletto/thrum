package config_test

import (
	"encoding/json"
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
	// Defaults should be applied
	if cfg.Daemon.SyncInterval != config.DefaultSyncInterval {
		t.Errorf("expected SyncInterval=%d, got %d", config.DefaultSyncInterval, cfg.Daemon.SyncInterval)
	}
	if cfg.Daemon.WSPort != config.DefaultWSPort {
		t.Errorf("expected WSPort=%q, got %q", config.DefaultWSPort, cfg.Daemon.WSPort)
	}
	if cfg.Runtime.Primary != "" {
		t.Errorf("expected empty Runtime.Primary, got %q", cfg.Runtime.Primary)
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
	// Defaults should be applied
	if cfg.Daemon.SyncInterval != config.DefaultSyncInterval {
		t.Errorf("expected SyncInterval=%d, got %d", config.DefaultSyncInterval, cfg.Daemon.SyncInterval)
	}
	if cfg.Daemon.WSPort != config.DefaultWSPort {
		t.Errorf("expected WSPort=%q, got %q", config.DefaultWSPort, cfg.Daemon.WSPort)
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

func TestLoadThrumConfig_FullSchema(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	data := `{
		"runtime": {"primary": "claude"},
		"daemon": {
			"local_only": true,
			"sync_interval": 30,
			"ws_port": "9999"
		}
	}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Runtime.Primary != "claude" {
		t.Errorf("expected Runtime.Primary=claude, got %q", cfg.Runtime.Primary)
	}
	if !cfg.Daemon.LocalOnly {
		t.Error("expected LocalOnly=true")
	}
	if cfg.Daemon.SyncInterval != 30 {
		t.Errorf("expected SyncInterval=30, got %d", cfg.Daemon.SyncInterval)
	}
	if cfg.Daemon.WSPort != "9999" {
		t.Errorf("expected WSPort=9999, got %q", cfg.Daemon.WSPort)
	}
}

func TestLoadThrumConfig_BackwardsCompat(t *testing.T) {
	// Old config with only daemon.local_only should still work
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
	// New fields should get defaults
	if cfg.Daemon.SyncInterval != config.DefaultSyncInterval {
		t.Errorf("expected default SyncInterval, got %d", cfg.Daemon.SyncInterval)
	}
	if cfg.Daemon.WSPort != config.DefaultWSPort {
		t.Errorf("expected default WSPort, got %q", cfg.Daemon.WSPort)
	}
	if cfg.Runtime.Primary != "" {
		t.Errorf("expected empty Runtime.Primary, got %q", cfg.Runtime.Primary)
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

func TestSaveThrumConfig_WithRuntime(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{
		Runtime: config.RuntimeConfig{Primary: "claude"},
		Daemon:  config.DaemonConfig{LocalOnly: true, SyncInterval: 30, WSPort: "9999"},
	}

	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("failed to load saved config: %v", err)
	}
	if loaded.Runtime.Primary != "claude" {
		t.Errorf("expected Runtime.Primary=claude, got %q", loaded.Runtime.Primary)
	}
	if loaded.Daemon.SyncInterval != 30 {
		t.Errorf("expected SyncInterval=30, got %d", loaded.Daemon.SyncInterval)
	}
	if loaded.Daemon.WSPort != "9999" {
		t.Errorf("expected WSPort=9999, got %q", loaded.Daemon.WSPort)
	}
}

func TestSaveThrumConfig_OmitsEmptyRuntime(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{Daemon: config.DaemonConfig{LocalOnly: true}}

	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read raw file — should not have a "runtime" key
	data, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["runtime"]; exists {
		t.Error("expected runtime key to be omitted when Primary is empty")
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
