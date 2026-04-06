package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// newTestPeerManager creates a PeerManager backed by a temp-dir registry.
func newTestPeerManager(t *testing.T) (*PeerManager, *PeerRegistry) {
	t.Helper()
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	pm := NewPeerManager(reg, "9999", nil)
	return pm, reg
}

// TestPeerManager_BuildConfigs verifies that only dialer-role peers produce configs.
func TestPeerManager_BuildConfigs(t *testing.T) {
	pm, reg := newTestPeerManager(t)

	// Add a dialer peer.
	dialer := &PeerInfo{
		Name:     "sf-node",
		DaemonID: "daemon-sf",
		Address:  "100.64.1.2:9100",
		Token:    "tok-sf",
		Role:     "dialer",
		PairedAt: time.Now(),
		LastSync: time.Now(),
	}
	if err := reg.AddPeer(dialer); err != nil {
		t.Fatalf("AddPeer(dialer): %v", err)
	}

	// Add a listener peer — should be excluded.
	listener := &PeerInfo{
		Name:     "ny-node",
		DaemonID: "daemon-ny",
		Address:  "100.64.1.3:9100",
		Token:    "tok-ny",
		Role:     "listener",
		PairedAt: time.Now(),
		LastSync: time.Now(),
	}
	if err := reg.AddPeer(listener); err != nil {
		t.Fatalf("AddPeer(listener): %v", err)
	}

	configs := pm.BuildConfigs()

	if len(configs) != 1 {
		t.Fatalf("BuildConfigs() returned %d configs, want 1", len(configs))
	}
	cfg := configs[0]
	if cfg.PeerName != "sf-node" {
		t.Errorf("PeerName = %q, want %q", cfg.PeerName, "sf-node")
	}
	if cfg.LocalWSPort != "9999" {
		t.Errorf("LocalWSPort = %q, want %q", cfg.LocalWSPort, "9999")
	}
	if cfg.PeerAddress != "100.64.1.2:9100" {
		t.Errorf("PeerAddress = %q, want %q", cfg.PeerAddress, "100.64.1.2:9100")
	}
	if cfg.BridgeUserID != "user:peer-sf-node" {
		t.Errorf("BridgeUserID = %q, want %q", cfg.BridgeUserID, "user:peer-sf-node")
	}
}

// TestPeerManager_BuildConfigs_LocalPeer verifies local transport uses PeerRepoPath.
func TestPeerManager_BuildConfigs_LocalPeer(t *testing.T) {
	pm, reg := newTestPeerManager(t)

	local := &PeerInfo{
		Name:      "local-twin",
		DaemonID:  "daemon-twin",
		Token:     "tok-twin",
		Role:      "dialer",
		Transport: "local",
		RepoPath:  "/home/dev/other-repo",
		PairedAt:  time.Now(),
		LastSync:  time.Now(),
	}
	if err := reg.AddPeer(local); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	configs := pm.BuildConfigs()

	if len(configs) != 1 {
		t.Fatalf("BuildConfigs() returned %d configs, want 1", len(configs))
	}
	cfg := configs[0]
	if cfg.PeerRepoPath != "/home/dev/other-repo" {
		t.Errorf("PeerRepoPath = %q, want %q", cfg.PeerRepoPath, "/home/dev/other-repo")
	}
	if cfg.PeerAddress != "" {
		t.Errorf("PeerAddress should be empty for local peer, got %q", cfg.PeerAddress)
	}
}

// TestPeerManager_ActiveCount verifies the count starts at 0.
func TestPeerManager_ActiveCount(t *testing.T) {
	pm, _ := newTestPeerManager(t)
	if count := pm.ActiveCount(); count != 0 {
		t.Errorf("ActiveCount() = %d, want 0", count)
	}
}

// TestPeerManager_StopAll verifies that StopAll clears the bridge map.
func TestPeerManager_StopAll(t *testing.T) {
	pm, _ := newTestPeerManager(t)

	// Manually inject a fake bridge to test StopAll without real connections.
	ctx, cancel := context.WithCancel(context.Background())
	pm.mu.Lock()
	pm.bridges["fake-peer"] = &runningBridge{cancel: cancel}
	pm.mu.Unlock()

	if count := pm.ActiveCount(); count != 1 {
		t.Fatalf("ActiveCount() before StopAll = %d, want 1", count)
	}

	pm.StopAll()

	if count := pm.ActiveCount(); count != 0 {
		t.Errorf("ActiveCount() after StopAll = %d, want 0", count)
	}

	// Verify the context was canceled.
	select {
	case <-ctx.Done():
		// expected
	default:
		t.Error("bridge context should have been canceled by StopAll")
	}
}
