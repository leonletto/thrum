package daemon

import (
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
