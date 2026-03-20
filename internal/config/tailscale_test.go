package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadTailscaleConfig_Defaults(t *testing.T) {
	// Clear any env vars that might interfere
	for _, k := range []string{"THRUM_TS_ENABLED", "THRUM_TS_HOSTNAME", "THRUM_TS_PORT", "THRUM_TS_AUTHKEY", "THRUM_TS_STATE_DIR", "THRUM_TAILSCALE_CONTROL_URL"} {
		t.Setenv(k, "")
	}

	cfg := LoadTailscaleConfig("/tmp/.thrum")

	if cfg.Enabled {
		t.Error("expected Enabled=false by default")
	}
	if cfg.Port != DefaultTailscalePort {
		t.Errorf("Port = %d, want %d", cfg.Port, DefaultTailscalePort)
	}
	if cfg.StateDir != "/tmp/.thrum/var/tsnet" {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, "/tmp/.thrum/var/tsnet")
	}

	// Hostname should be auto-derived
	hostname, _ := os.Hostname()
	expected := strings.ToLower(hostname) + "-thrum"
	if cfg.Hostname != expected {
		t.Errorf("Hostname = %q, want auto-derived %q", cfg.Hostname, expected)
	}
}

func TestLoadTailscaleConfig_AutoHostname(t *testing.T) {
	for _, k := range []string{"THRUM_TS_ENABLED", "THRUM_TS_HOSTNAME", "THRUM_TS_PORT", "THRUM_TS_AUTHKEY"} {
		t.Setenv(k, "")
	}

	cfg := LoadTailscaleConfig(t.TempDir())

	hostname, _ := os.Hostname()
	expected := strings.ToLower(hostname) + "-thrum"
	if cfg.Hostname != expected {
		t.Errorf("Hostname = %q, want auto-derived %q", cfg.Hostname, expected)
	}
}

func TestLoadTailscaleConfig_ExplicitHostnameOverridesAuto(t *testing.T) {
	t.Setenv("THRUM_TS_HOSTNAME", "custom-host")
	cfg := LoadTailscaleConfig(t.TempDir())
	if cfg.Hostname != "custom-host" {
		t.Errorf("Hostname = %q, want %q", cfg.Hostname, "custom-host")
	}
}

func TestLoadTailscaleConfig_FromEnv(t *testing.T) {
	t.Setenv("THRUM_TS_ENABLED", "true")
	t.Setenv("THRUM_TS_HOSTNAME", "my-daemon")
	t.Setenv("THRUM_TS_PORT", "9200")
	t.Setenv("THRUM_TS_AUTHKEY", "tskey-test-123")
	t.Setenv("THRUM_TS_STATE_DIR", "/custom/state")
	t.Setenv("THRUM_TAILSCALE_CONTROL_URL", "https://headscale.example.com")

	cfg := LoadTailscaleConfig("/tmp/.thrum")

	if !cfg.Enabled {
		t.Error("expected Enabled=true")
	}
	if cfg.Hostname != "my-daemon" {
		t.Errorf("Hostname = %q, want %q", cfg.Hostname, "my-daemon")
	}
	if cfg.Port != 9200 {
		t.Errorf("Port = %d, want 9200", cfg.Port)
	}
	if cfg.AuthKey != "tskey-test-123" {
		t.Errorf("AuthKey = %q, want %q", cfg.AuthKey, "tskey-test-123")
	}
	if cfg.StateDir != "/custom/state" {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, "/custom/state")
	}
	if cfg.ControlURL != "https://headscale.example.com" {
		t.Errorf("ControlURL = %q, want %q", cfg.ControlURL, "https://headscale.example.com")
	}
}

func TestLoadTailscaleConfig_EnabledVariants(t *testing.T) {
	for _, val := range []string{"true", "1", "yes"} {
		t.Setenv("THRUM_TS_ENABLED", val)
		cfg := LoadTailscaleConfig("/tmp/.thrum")
		if !cfg.Enabled {
			t.Errorf("expected Enabled=true for THRUM_TS_ENABLED=%q", val)
		}
	}

	for _, val := range []string{"false", "0", "no", ""} {
		t.Setenv("THRUM_TS_ENABLED", val)
		cfg := LoadTailscaleConfig("/tmp/.thrum")
		if cfg.Enabled {
			t.Errorf("expected Enabled=false for THRUM_TS_ENABLED=%q", val)
		}
	}
}

func TestSaveAuthKeyToEnvFile_NewFile(t *testing.T) {
	dir := t.TempDir()
	err := SaveAuthKeyToEnvFile(dir, "tskey-test-123")
	if err != nil {
		t.Fatalf("SaveAuthKeyToEnvFile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "THRUM_TS_AUTHKEY=tskey-test-123") {
		t.Errorf("expected auth key in .env, got: %s", data)
	}
}

func TestSaveAuthKeyToEnvFile_UpdateExisting(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("THRUM_TS_AUTHKEY=old-key\nOTHER=value\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := SaveAuthKeyToEnvFile(dir, "new-key"); err != nil {
		t.Fatalf("SaveAuthKeyToEnvFile: %v", err)
	}

	data, _ := os.ReadFile(envPath)
	content := string(data)
	if !strings.Contains(content, "THRUM_TS_AUTHKEY=new-key") {
		t.Errorf("expected updated auth key, got: %s", content)
	}
	if strings.Contains(content, "old-key") {
		t.Error("old auth key should be replaced")
	}
	if !strings.Contains(content, "OTHER=value") {
		t.Error("other env vars should be preserved")
	}
}

func TestTailscaleConfig_Validate_Disabled(t *testing.T) {
	cfg := TailscaleConfig{Enabled: false}
	if err := cfg.Validate(); err != nil {
		t.Errorf("disabled config should validate: %v", err)
	}
}

func TestTailscaleConfig_Validate_MissingHostname(t *testing.T) {
	cfg := TailscaleConfig{Enabled: true, Port: 9100, AuthKey: "tskey-test"}
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for missing hostname")
	}
}

func TestTailscaleConfig_Validate_BadPort(t *testing.T) {
	// Port 0 is valid (not yet configured)
	cfg := TailscaleConfig{Enabled: true, Hostname: "test", Port: 0, AuthKey: "tskey-test"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("port 0 should be valid (not yet configured): %v", err)
	}

	cfg.Port = 70000
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for port 70000")
	}
}

func TestTailscaleConfig_Validate_MissingAuthKey(t *testing.T) {
	cfg := TailscaleConfig{Enabled: true, Hostname: "test", Port: 9100}
	err := cfg.Validate()
	if err == nil {
		t.Error("expected error for missing auth key")
	}
}

func TestTailscaleConfig_Validate_Valid(t *testing.T) {
	cfg := TailscaleConfig{
		Enabled:  true,
		Hostname: "my-daemon",
		Port:     9100,
		AuthKey:  "tskey-test-123",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected valid config: %v", err)
	}
}
