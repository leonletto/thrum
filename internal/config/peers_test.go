package config_test

import (
	"encoding/json"
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
