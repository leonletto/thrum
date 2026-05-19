package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

func TestConfigShow_NoConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatal(err)
	}

	result, err := ConfigShow(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have defaults
	if result.Daemon.WSPort.Value != "auto" {
		t.Errorf("expected WSPort 'auto', got %q", result.Daemon.WSPort.Value)
	}
	if result.Daemon.Status != "not running" {
		t.Errorf("expected daemon status 'not running', got %q", result.Daemon.Status)
	}
}

func TestConfigShow_WithConfig(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "var"), 0750); err != nil {
		t.Fatal(err)
	}

	cfg := &config.ThrumConfig{
		Runtime: config.RuntimeConfig{Primary: "claude"},
		Daemon:  config.DaemonConfig{LocalOnly: true, WSPort: "9999"},
	}
	if err := config.SaveThrumConfig(thrumDir, cfg); err != nil {
		t.Fatal(err)
	}

	result, err := ConfigShow(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Runtime.Primary != "claude" {
		t.Errorf("expected Runtime.Primary=claude, got %q", result.Runtime.Primary)
	}
	if result.Runtime.Source != "config.json" {
		t.Errorf("expected Runtime source 'config.json', got %q", result.Runtime.Source)
	}
	if result.Daemon.LocalOnly.Value != "true" {
		t.Errorf("expected LocalOnly=true, got %q", result.Daemon.LocalOnly.Value)
	}
	if result.Daemon.LocalOnly.Source != "config.json" {
		t.Errorf("expected LocalOnly source 'config.json', got %q", result.Daemon.LocalOnly.Source)
	}
	if result.Daemon.WSPort.Value != "9999" {
		t.Errorf("expected WSPort=9999, got %q", result.Daemon.WSPort.Value)
	}
}

func TestConfigShow_EnvOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatal(err)
	}

	t.Setenv("THRUM_LOCAL", "true")
	t.Setenv("THRUM_WS_PORT", "8888")

	result, err := ConfigShow(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Daemon.LocalOnly.Source != "env" {
		t.Errorf("expected LocalOnly source 'env', got %q", result.Daemon.LocalOnly.Source)
	}
	if result.Daemon.WSPort.Source != "env" {
		t.Errorf("expected WSPort source 'env', got %q", result.Daemon.WSPort.Source)
	}

	// Should list overrides
	if len(result.Overrides) < 2 {
		t.Errorf("expected at least 2 overrides, got %d", len(result.Overrides))
	}
}

func TestFormatConfigShow(t *testing.T) {
	result := &ConfigShowResult{
		ConfigFile: ".thrum/config.json",
		Runtime: ConfigRuntimeInfo{
			Primary:  "claude",
			Source:   "config.json",
			Detected: []string{"claude", "auggie"},
		},
		Daemon: ConfigDaemonInfo{
			LocalOnly: ConfigValue{Value: "true", Source: "config.json"},
			WSPort:    ConfigValue{Value: "auto", Source: "default"},
			Status:    "not running",
		},
		Identity: ConfigIdentityInfo{
			Agent:  "test_agent",
			Role:   "implementer",
			Module: "main",
		},
	}

	output := FormatConfigShow(result)

	checks := []string{
		"Thrum Configuration",
		"Runtime",
		"claude (config.json)",
		"claude, auggie",
		"Daemon",
		"Local-only:",
		"Identity",
		"test_agent",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output missing %q:\n%s", check, output)
		}
	}

	// Sync interval line must be gone
	if strings.Contains(output, "Sync interval:") {
		t.Errorf("output should not contain 'Sync interval:' after v0.10.6 removal:\n%s", output)
	}
}
