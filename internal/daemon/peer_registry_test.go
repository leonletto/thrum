package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPeerRegistry_AddAndGet(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	info := &PeerInfo{
		DaemonID: "d_alice",
		Name:     "alice-laptop",
		Address:  "alice-laptop.tailnet.ts.net:9100",
	}
	if err := reg.AddPeer(info); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	got := reg.GetPeer("d_alice")
	if got == nil {
		t.Fatal("GetPeer returned nil")
	}
	if got.Name != "alice-laptop" {
		t.Errorf("Name = %q, want %q", got.Name, "alice-laptop")
	}
}

func TestPeerRegistry_AddRequiresDaemonID(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	err = reg.AddPeer(&PeerInfo{Name: "test"})
	if err == nil {
		t.Error("expected error for empty DaemonID")
	}
}

func TestPeerRegistry_ListPeers(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	_ = reg.AddPeer(&PeerInfo{DaemonID: "d_a", Name: "a", Address: "a:9100"})
	_ = reg.AddPeer(&PeerInfo{DaemonID: "d_b", Name: "b", Address: "b:9100"})

	peers := reg.ListPeers()
	if len(peers) != 2 {
		t.Errorf("ListPeers len = %d, want 2", len(peers))
	}
}

func TestPeerRegistry_RemovePeer(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	_ = reg.AddPeer(&PeerInfo{DaemonID: "d_alice", Name: "alice", Address: "alice:9100"})
	if err := reg.RemovePeer("d_alice"); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}

	if reg.GetPeer("d_alice") != nil {
		t.Error("peer should be removed")
	}
}

func TestPeerRegistry_UpdateLastSync(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	_ = reg.AddPeer(&PeerInfo{DaemonID: "d_alice", Name: "alice", Address: "alice:9100"})
	if err := reg.UpdateLastSync("d_alice"); err != nil {
		t.Fatalf("UpdateLastSync: %v", err)
	}

	got := reg.GetPeer("d_alice")
	if got.LastSync.IsZero() {
		t.Error("LastSync should be updated")
	}

	// Non-existent peer
	if err := reg.UpdateLastSync("d_unknown"); err == nil {
		t.Error("expected error for unknown peer")
	}
}

func TestPeerRegistry_RemoveStalePeers(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	// Add a peer with old LastSync
	_ = reg.AddPeer(&PeerInfo{DaemonID: "d_old", Name: "old", Address: "old:9100"})
	// Manually set old timestamp
	reg.mu.Lock()
	reg.peers["d_old"].LastSync = time.Now().Add(-2 * time.Hour)
	reg.mu.Unlock()

	_ = reg.AddPeer(&PeerInfo{DaemonID: "d_new", Name: "new", Address: "new:9100"})

	removed := reg.RemoveStalePeers(1 * time.Hour)
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	if reg.GetPeer("d_old") != nil {
		t.Error("stale peer should be removed")
	}
	if reg.GetPeer("d_new") == nil {
		t.Error("fresh peer should remain")
	}
}

func TestPeerRegistry_Persistence(t *testing.T) {
	dir := t.TempDir()
	peersFile := filepath.Join(dir, "peers.json")

	// Create and populate a registry
	reg1, err := NewPeerRegistry(peersFile)
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	_ = reg1.AddPeer(&PeerInfo{DaemonID: "d_persist", Name: "persist", Address: "persist.ts.net:9100"})

	// Create a new registry from the same file — should load persisted data
	reg2, err := NewPeerRegistry(peersFile)
	if err != nil {
		t.Fatalf("NewPeerRegistry (reload): %v", err)
	}

	got := reg2.GetPeer("d_persist")
	if got == nil {
		t.Fatal("peer should be loaded from disk")
	}
	if got.Address != "persist.ts.net:9100" {
		t.Errorf("Address = %q, want %q", got.Address, "persist.ts.net:9100")
	}
}

func TestPeerRegistry_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "d_" + string(rune('a'+i%26))
			_ = reg.AddPeer(&PeerInfo{DaemonID: id, Name: id, Address: id + ":9100"})
			_ = reg.ListPeers()
			_ = reg.GetPeer(id)
		}(i)
	}
	wg.Wait()

	// Should not panic or deadlock
	if reg.Len() == 0 {
		t.Error("expected some peers after concurrent adds")
	}
}

func TestPeerRegistry_NewSchemaFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	reg, err := NewPeerRegistry(path)
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	// Local config should be initialized with a daemon_id
	if reg.LocalDaemonID() == "" {
		t.Error("LocalDaemonID should not be empty for new registry")
	}
	if reg.LocalPort() != 0 {
		t.Error("LocalPort should be 0 for new registry (no port assigned yet)")
	}

	// Add a peer
	err = reg.AddPeer(&PeerInfo{
		Name:     "test-peer",
		Address:  "100.64.1.2:9150",
		DaemonID: "d_remote123",
		Token:    "tok_abc",
	})
	if err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	// Read raw file — should be object format, not array
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("file should be an object, not an array: %v", err)
	}
	if _, ok := raw["local"]; !ok {
		t.Error("file should contain 'local' key")
	}
	if _, ok := raw["peers"]; !ok {
		t.Error("file should contain 'peers' key")
	}
}

func TestPeerRegistry_MigrateOldFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	// Write old array format
	old := []*PeerInfo{{
		Name:     "old-peer",
		Address:  "100.64.1.2:9100",
		DaemonID: "d_old123",
		Token:    "tok_old",
		PairedAt: time.Now(),
		LastSync: time.Now(),
	}}
	data, _ := json.Marshal(old)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Load — should migrate
	reg, err := NewPeerRegistry(path)
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	if reg.LocalDaemonID() == "" {
		t.Error("LocalDaemonID should be generated during migration")
	}
	if reg.Len() != 1 {
		t.Errorf("Len = %d, want 1", reg.Len())
	}

	peer := reg.GetPeer("d_old123")
	if peer == nil {
		t.Fatal("migrated peer not found")
	}
	if peer.Name != "old-peer" {
		t.Errorf("Name = %q, want %q", peer.Name, "old-peer")
	}

	// Re-read file — should now be new format
	data2, _ := os.ReadFile(path)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data2, &raw); err != nil {
		t.Fatalf("migrated file should be an object: %v", err)
	}
	if _, ok := raw["local"]; !ok {
		t.Error("migrated file should contain 'local' key")
	}
}

func TestPeerRegistry_SetLocalPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	reg, err := NewPeerRegistry(path)
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	if err := reg.SetLocalPort(9147); err != nil {
		t.Fatalf("SetLocalPort: %v", err)
	}
	if reg.LocalPort() != 9147 {
		t.Errorf("LocalPort = %d, want 9147", reg.LocalPort())
	}

	// Reload from disk — port should persist
	reg2, err := NewPeerRegistry(path)
	if err != nil {
		t.Fatalf("NewPeerRegistry reload: %v", err)
	}
	if reg2.LocalPort() != 9147 {
		t.Errorf("persisted LocalPort = %d, want 9147", reg2.LocalPort())
	}
}

func TestPeerRegistry_DaemonIDPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	reg1, err := NewPeerRegistry(path)
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	id1 := reg1.LocalDaemonID()

	// Force a save so the file exists
	if err := reg1.SetLocalPort(9100); err != nil {
		t.Fatalf("SetLocalPort: %v", err)
	}

	// Reload — should reuse same daemon_id
	reg2, err := NewPeerRegistry(path)
	if err != nil {
		t.Fatalf("NewPeerRegistry reload: %v", err)
	}
	if reg2.LocalDaemonID() != id1 {
		t.Errorf("daemon_id changed across reload: %q -> %q", id1, reg2.LocalDaemonID())
	}
}

func TestPeerInfo_Addr(t *testing.T) {
	// Addr() now just returns the Address field
	p := &PeerInfo{Name: "alice", Address: "alice.tailnet.ts.net:9100"}
	if got := p.Addr(); got != "alice.tailnet.ts.net:9100" {
		t.Errorf("Addr = %q, want %q", got, "alice.tailnet.ts.net:9100")
	}

	// Simple hostname:port
	p2 := &PeerInfo{Name: "alice", Address: "alice:9100"}
	if got := p2.Addr(); got != "alice:9100" {
		t.Errorf("Addr = %q, want %q", got, "alice:9100")
	}
}
