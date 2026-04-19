package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

func TestPeersConfig_Defaults(t *testing.T) {
	cfg := config.DefaultPeersConfig()
	if !cfg.AutoConnect {
		t.Fatal("AutoConnect should default to true")
	}
	if cfg.PairingCodeLength != 16 {
		t.Fatalf("PairingCodeLength = %d, want 16", cfg.PairingCodeLength)
	}
}

func TestPeersConfig_FromJSON(t *testing.T) {
	data := `{"auto_connect": false, "pairing_code_length": 8}`
	var cfg config.PeersConfig
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.AutoConnect {
		t.Fatal("AutoConnect should be false")
	}
	if cfg.PairingCodeLength != 8 {
		t.Fatalf("PairingCodeLength = %d, want 8", cfg.PairingCodeLength)
	}
}

func TestLoadThrumConfig_PeersDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadThrumConfig: %v", err)
	}
	if !cfg.Peers.AutoConnect {
		t.Fatal("Peers.AutoConnect should default to true")
	}
	if cfg.Peers.PairingCodeLength != 16 {
		t.Fatalf("Peers.PairingCodeLength = %d, want 16", cfg.Peers.PairingCodeLength)
	}
	if cfg.Daemon.PeerPort != "auto" {
		t.Fatalf("Daemon.PeerPort = %q, want auto", cfg.Daemon.PeerPort)
	}
}

// TestLoadThrumConfig_ExistingFileWithoutPeersStanza covers thrum-1k00:
// configs written before the peers stanza existed leave cfg.Peers at its
// Go zero-value after json.Unmarshal. ApplyDefaults must still fill
// AutoConnect=true in that case, otherwise auto-connect silently disables
// on every pre-existing install.
func TestLoadThrumConfig_ExistingFileWithoutPeersStanza(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	// Valid config.json with no "peers" key — mirrors mock-salesforce's
	// real config that shipped before peers config existed.
	body := `{"daemon": {"local_only": true, "sync_interval": 60, "ws_port": "auto"}}`
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadThrumConfig: %v", err)
	}
	if !cfg.Peers.AutoConnect {
		t.Fatal("Peers.AutoConnect must default to true when peers stanza absent from existing config.json")
	}
	if cfg.Peers.PairingCodeLength != 16 {
		t.Fatalf("Peers.PairingCodeLength = %d, want 16", cfg.Peers.PairingCodeLength)
	}
}

// TestLoadThrumConfig_ExplicitAutoConnectFalse guards the regression path:
// a user who explicitly disables auto_connect (and provides the rest of
// the peers stanza) must keep AutoConnect=false.
func TestLoadThrumConfig_ExplicitAutoConnectFalse(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	body := `{"peers": {"auto_connect": false, "pairing_code_length": 16}}`
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadThrumConfig: %v", err)
	}
	if cfg.Peers.AutoConnect {
		t.Fatal("Peers.AutoConnect must remain false when explicitly disabled")
	}
	if cfg.Peers.PairingCodeLength != 16 {
		t.Fatalf("Peers.PairingCodeLength = %d, want 16", cfg.Peers.PairingCodeLength)
	}
}

// TestLoadThrumConfig_ExplicitAutoConnectFalseZeroLength regression-guards
// the sentinel-decoupling fix: a user who writes
// `{"peers": {"auto_connect": false, "pairing_code_length": 0}}` (or
// simply `{"peers": {"auto_connect": false}}`) must keep AutoConnect=false
// even though cfg.Peers ends up at the Go zero-value after JSON unmarshal.
// The stanza-presence check via raw JSON is what preserves the explicit
// choice; PairingCodeLength still receives its defaulted 16 separately.
func TestLoadThrumConfig_ExplicitAutoConnectFalseZeroLength(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	body := `{"peers": {"auto_connect": false, "pairing_code_length": 0}}`
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadThrumConfig: %v", err)
	}
	if cfg.Peers.AutoConnect {
		t.Fatal("Peers.AutoConnect must remain false even with zero pairing_code_length")
	}
	if cfg.Peers.PairingCodeLength != 16 {
		t.Fatalf("Peers.PairingCodeLength = %d, want 16 (field-level default)", cfg.Peers.PairingCodeLength)
	}
}
