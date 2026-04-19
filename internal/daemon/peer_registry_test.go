package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestPeerInfo_NewFields_Persist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	reg, err := NewPeerRegistry(path)
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	info := &PeerInfo{
		DaemonID:     "d_newfields",
		Name:         "newfields-peer",
		Address:      "peer.example.com:9100",
		Transport:    "tailscale",
		RepoPath:     "/home/user/project",
		// thrum-b6yv: pass an unclean prefix to assert the AddPeer
		// boundary sanitizes it. "remote." → "remote-".
		ProxyPrefix:  "remote.",
		RemoteAgents: []string{"agent-a", "agent-b"},
		Role:         "listener",
	}
	if err := reg.AddPeer(info); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	// Reload from disk
	reg2, err := NewPeerRegistry(path)
	if err != nil {
		t.Fatalf("NewPeerRegistry reload: %v", err)
	}

	got := reg2.GetPeer("d_newfields")
	if got == nil {
		t.Fatal("peer not found after reload")
	}
	if got.Transport != "tailscale" {
		t.Errorf("Transport = %q, want %q", got.Transport, "tailscale")
	}
	if got.RepoPath != "/home/user/project" {
		t.Errorf("RepoPath = %q, want %q", got.RepoPath, "/home/user/project")
	}
	if got.ProxyPrefix != "remote-" {
		t.Errorf("ProxyPrefix = %q, want %q (auto-sanitized at AddPeer boundary)", got.ProxyPrefix, "remote-")
	}
	if len(got.RemoteAgents) != 2 || got.RemoteAgents[0] != "agent-a" || got.RemoteAgents[1] != "agent-b" {
		t.Errorf("RemoteAgents = %v, want [agent-a agent-b]", got.RemoteAgents)
	}
	if got.Role != "listener" {
		t.Errorf("Role = %q, want %q", got.Role, "listener")
	}
}

func TestAddRemoteAgent(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	if err := reg.AddPeer(&PeerInfo{DaemonID: "d_ra", Name: "ra-peer", Address: "ra:9100"}); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	// Add first agent
	if err := reg.AddRemoteAgent("ra-peer", "alpha"); err != nil {
		t.Fatalf("AddRemoteAgent alpha: %v", err)
	}
	got := reg.FindPeerByName("ra-peer")
	if len(got.RemoteAgents) != 1 || got.RemoteAgents[0] != "alpha" {
		t.Errorf("RemoteAgents = %v, want [alpha]", got.RemoteAgents)
	}

	// Add second agent
	if err := reg.AddRemoteAgent("ra-peer", "beta"); err != nil {
		t.Fatalf("AddRemoteAgent beta: %v", err)
	}
	got = reg.FindPeerByName("ra-peer")
	if len(got.RemoteAgents) != 2 {
		t.Errorf("RemoteAgents len = %d, want 2", len(got.RemoteAgents))
	}

	// Idempotent: add alpha again — should still be 2 agents
	if err := reg.AddRemoteAgent("ra-peer", "alpha"); err != nil {
		t.Fatalf("AddRemoteAgent alpha (dup): %v", err)
	}
	got = reg.FindPeerByName("ra-peer")
	if len(got.RemoteAgents) != 2 {
		t.Errorf("after dup add, RemoteAgents len = %d, want 2", len(got.RemoteAgents))
	}

	// Error on unknown peer
	if err := reg.AddRemoteAgent("no-such-peer", "gamma"); err == nil {
		t.Error("expected error for unknown peer")
	}
}

func TestRemoveRemoteAgent(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	if err := reg.AddPeer(&PeerInfo{
		DaemonID:     "d_rr",
		Name:         "rr-peer",
		Address:      "rr:9100",
		RemoteAgents: []string{"alpha", "beta", "gamma"},
	}); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	// Remove middle agent
	if err := reg.RemoveRemoteAgent("rr-peer", "beta"); err != nil {
		t.Fatalf("RemoveRemoteAgent beta: %v", err)
	}
	got := reg.FindPeerByName("rr-peer")
	if len(got.RemoteAgents) != 2 {
		t.Errorf("RemoteAgents len = %d, want 2 after remove", len(got.RemoteAgents))
	}
	for _, a := range got.RemoteAgents {
		if a == "beta" {
			t.Error("beta should have been removed")
		}
	}

	// Remove non-existent agent — should be a no-op
	if err := reg.RemoveRemoteAgent("rr-peer", "delta"); err != nil {
		t.Fatalf("RemoveRemoteAgent delta (no-op): %v", err)
	}
	got = reg.FindPeerByName("rr-peer")
	if len(got.RemoteAgents) != 2 {
		t.Errorf("RemoteAgents len = %d, want 2 after no-op remove", len(got.RemoteAgents))
	}

	// Error on unknown peer
	if err := reg.RemoveRemoteAgent("no-such-peer", "alpha"); err == nil {
		t.Error("expected error for unknown peer")
	}
}

func TestNewPeerRegistry_BackupOnReconciliation(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	varDir := filepath.Join(thrumDir, "var")
	_ = os.MkdirAll(varDir, 0o750)

	// config.json has the authoritative (new) daemon_id.
	const authoritativeID = "d_01HYTEST_BACKUP_AUTH_ID00"
	cfgJSON := `{"identity":{"daemon_id":"` + authoritativeID + `","init_at":"2026-01-01T00:00:00Z"}}`
	_ = os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(cfgJSON), 0o600)

	// peers.json has a stale id — this triggers reconciliation + backup.
	const staleID = "d_stale_backup_test_id"
	stalePeers := `{"local":{"daemon_id":"` + staleID + `","port":0},"peers":[]}`
	peersPath := filepath.Join(varDir, "peers.json")
	_ = os.WriteFile(peersPath, []byte(stalePeers), 0o600)

	// First call: should create backup.
	_, err := NewPeerRegistry(peersPath)
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	bakPath := peersPath + ".pre-rotation-bak"
	bakData, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("backup file not created: %v", err)
	}
	// Backup must contain the pre-rotation stale id.
	if !strings.Contains(string(bakData), staleID) {
		t.Fatalf("backup does not contain stale daemon_id %q: %s", staleID, bakData)
	}

	// Second call: backup must NOT be overwritten.
	_, err = NewPeerRegistry(peersPath)
	if err != nil {
		t.Fatalf("NewPeerRegistry second call: %v", err)
	}
	bakData2, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("backup file disappeared: %v", err)
	}
	if string(bakData2) != string(bakData) {
		t.Fatalf("backup overwritten on second call; want pre-rotation bytes unchanged")
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

func TestNewPeerRegistry_UsesConfigJSONDaemonID(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	varDir := filepath.Join(thrumDir, "var")
	if err := os.MkdirAll(varDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Pre-seed config.json with a specific daemon_id.
	const wantID = "d_01HYTESTCONFIGJSONSEED01"
	cfgJSON := `{"identity":{"daemon_id":"` + wantID + `","init_at":"2026-01-01T00:00:00Z"}}`
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create peer registry; it should pick up the config.json id.
	reg, err := NewPeerRegistry(filepath.Join(varDir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	if got := reg.LocalDaemonID(); got != wantID {
		t.Fatalf("LocalDaemonID = %q, want %q", got, wantID)
	}
}

func TestNewPeerRegistry_ReconcilesStalePeersJSONWithConfig(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	varDir := filepath.Join(thrumDir, "var")
	_ = os.MkdirAll(varDir, 0o750)

	// config.json has the authoritative id.
	const authoritativeID = "d_01HYTESTAUTHORITATIVE00"
	cfgJSON := `{"identity":{"daemon_id":"` + authoritativeID + `","init_at":"2026-01-01T00:00:00Z"}}`
	_ = os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(cfgJSON), 0o600)

	// peers.json has a stale id (simulating a pre-upgrade file).
	stalePeers := `{"local":{"daemon_id":"d_stale_id_in_peers_json","port":0},"peers":[]}`
	peersPath := filepath.Join(varDir, "peers.json")
	_ = os.WriteFile(peersPath, []byte(stalePeers), 0o600)

	reg, err := NewPeerRegistry(peersPath)
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	if got := reg.LocalDaemonID(); got != authoritativeID {
		t.Fatalf("LocalDaemonID = %q, want %q (config.json wins over stale peers.json)", got, authoritativeID)
	}

	// Verify peers.json was rewritten with the reconciled id.
	data, err := os.ReadFile(peersPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), authoritativeID) {
		t.Fatalf("peers.json not updated with authoritative id: %s", data)
	}
	if strings.Contains(string(data), "d_stale_id_in_peers_json") {
		t.Fatalf("peers.json still contains stale id: %s", data)
	}
}

// xir.29: ReconcileStatus persists across save/load and survives registry
// reopen. The field is written in lowercase snake_case per the JSON
// convention used throughout PeerInfo.
func TestPeerInfo_ReconcileStatusPersistence(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "peers.json")
	r, err := NewPeerRegistry(tmp)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	p := &PeerInfo{
		Name:            "alpha",
		DaemonID:        "01TESTDAEMON",
		Address:         "192.168.1.5:7731",
		Token:           "tok-123",
		Transport:       "network",
		ReconcileStatus: "drift_reconcile_failed",
	}
	if err := r.AddPeer(p); err != nil {
		t.Fatalf("add: %v", err)
	}
	r2, err := NewPeerRegistry(tmp)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got := r2.FindPeerByToken("tok-123")
	if got == nil {
		t.Fatalf("peer not found after reload")
	}
	if got.ReconcileStatus != "drift_reconcile_failed" {
		t.Errorf("ReconcileStatus = %q, want drift_reconcile_failed", got.ReconcileStatus)
	}
}

// xir.29: SetReconcileStatus updates the reconcile-status marker on a
// peer by daemon_id, persists atomically, and handles both set-and-clear
// plus unknown-peer rejection.
func TestPeerRegistry_SetReconcileStatus(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "peers.json")
	r, err := NewPeerRegistry(tmp)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := r.AddPeer(&PeerInfo{Name: "a", DaemonID: "01DID", Token: "t"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := r.SetReconcileStatus("01DID", "drift_reconcile_failed"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := r.FindPeerByToken("t"); got.ReconcileStatus != "drift_reconcile_failed" {
		t.Errorf("status = %q", got.ReconcileStatus)
	}

	if err := r.SetReconcileStatus("01DID", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := r.FindPeerByToken("t"); got.ReconcileStatus != "" {
		t.Errorf("cleared status = %q", got.ReconcileStatus)
	}

	if err := r.SetReconcileStatus("01UNKNOWN", "drift_reconcile_failed"); err == nil {
		t.Errorf("expected error for unknown daemon_id")
	}

	// Reopen registry and verify the cleared status survives.
	r2, err := NewPeerRegistry(tmp)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := r2.FindPeerByToken("t"); got.ReconcileStatus != "" {
		t.Errorf("cleared status not persisted: %q", got.ReconcileStatus)
	}
}

// TestSanitizeProxyPrefix covers thrum-b6yv character-class rules.
// TestSanitizeProxyPrefix covers thrum-b6yv character-class rules,
// including the documented silent-drop behavior for non-ASCII runes.
func TestSanitizeProxyPrefix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"mock-salesforce", "mock-salesforce"},
		{"my.repo", "my-repo"},
		{"path/to/repo", "path-to-repo"},
		{"foo\\bar", "foo-bar"},
		{"name with spaces", "name-with-spaces"},
		{"plain_snake", "plain_snake"},
		{"UPPER123", "UPPER123"},
		{"mix.of/weird chars!", "mix-of-weird-chars"}, // '!' is dropped
		{"", ""},
		// Documented silent-drop of non-ASCII runes. Intentional for
		// now; see SanitizeProxyPrefix's doc comment for the rationale
		// and deferred follow-up (transliteration).
		{"léon-mac", "lon-mac"},
		{"café", "caf"},
		{"北京", ""},
	}
	for _, c := range cases {
		if got := SanitizeProxyPrefix(c.in); got != c.want {
			t.Errorf("SanitizeProxyPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDeriveProxyPrefix covers the fallback order (remote_repo_name →
// peer name → empty) plus nil guard.
func TestDeriveProxyPrefix(t *testing.T) {
	if got := DeriveProxyPrefix(nil); got != "" {
		t.Errorf("DeriveProxyPrefix(nil) = %q, want \"\"", got)
	}
	if got := DeriveProxyPrefix(&PeerInfo{RemoteRepoName: "mock-salesforce", Name: "host"}); got != "mock-salesforce" {
		t.Errorf("RemoteRepoName preferred: got %q", got)
	}
	if got := DeriveProxyPrefix(&PeerInfo{Name: "leonsmacm1pro.local"}); got != "leonsmacm1pro-local" {
		t.Errorf("peer name fallback sanitized: got %q", got)
	}
	if got := DeriveProxyPrefix(&PeerInfo{}); got != "" {
		t.Errorf("empty peer → empty prefix, got %q", got)
	}
}

// TestPeerRegistry_FreshJoin_StampsFromRemoteRepoName exercises the
// peer.join code path at the registry/derive level: construct a peer
// the way the RPC handler does (JoinPeer returns PeerInfo with
// RemoteRepoName populated), derive + stamp ProxyPrefix, persist, and
// verify the stored entry has the expected prefix.
func TestPeerRegistry_FreshJoin_StampsFromRemoteRepoName(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	// Shape mirrors peer.join --type local after syncManager.JoinPeer:
	// Role=dialer, Transport=local, RemoteRepoName exchanged via pair.
	p := &PeerInfo{
		Name:           "leonsmacm1pro.local",
		DaemonID:       "d_sf_01",
		Token:          "tok-sf",
		Role:           "dialer",
		Transport:      "local",
		RepoPath:       "/tmp/sibling",
		RemoteRepoName: "mock-salesforce",
	}
	p.ProxyPrefix = DeriveProxyPrefix(p)
	if err := reg.AddPeer(p); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	got := reg.GetPeer("d_sf_01")
	if got == nil {
		t.Fatalf("GetPeer(d_sf_01) = nil")
	}
	if got.ProxyPrefix != "mock-salesforce" {
		t.Errorf("ProxyPrefix = %q, want %q", got.ProxyPrefix, "mock-salesforce")
	}
}

// TestPeerRegistry_FreshJoin_FallsBackToPeerName covers the case where
// RemoteRepoName is empty at peer.join time (older listener not emitting
// repo metadata, or a legacy path). DeriveProxyPrefix must fall back to
// the peer name and the stamped value must persist through AddPeer.
func TestPeerRegistry_FreshJoin_FallsBackToPeerName(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	p := &PeerInfo{
		Name:      "leonsmacm1pro.local",
		DaemonID:  "d_sf_02",
		Token:     "tok-sf2",
		Role:      "dialer",
		Transport: "local",
		// RemoteRepoName deliberately empty
	}
	p.ProxyPrefix = DeriveProxyPrefix(p)
	if err := reg.AddPeer(p); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	got := reg.GetPeer("d_sf_02")
	if got == nil {
		t.Fatalf("GetPeer(d_sf_02) = nil")
	}
	if got.ProxyPrefix != "leonsmacm1pro-local" {
		t.Errorf("ProxyPrefix = %q, want %q (peer-name fallback, sanitized)", got.ProxyPrefix, "leonsmacm1pro-local")
	}
}

// TestPeerRegistry_TwoPairsSameRepoName documents the actual uniqueness
// boundary: AddPeer keys on DaemonID, NOT peer name. Two pairs with the
// same remote_repo_name but distinct daemon_ids therefore both persist
// and produce identical ProxyPrefix values. Proxy-prefix collision is
// ultimately bounded by the `agents` table's agent_id uniqueness at
// Relay-start time, not at peer-registry time. This test captures the
// status-quo so future refactors do not silently change it.
func TestPeerRegistry_TwoPairsSameRepoName(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	first := &PeerInfo{
		Name:           "host-a",
		DaemonID:       "d_a",
		Token:          "tok-a",
		Role:           "dialer",
		RemoteRepoName: "mock-salesforce",
	}
	first.ProxyPrefix = DeriveProxyPrefix(first)
	if err := reg.AddPeer(first); err != nil {
		t.Fatalf("AddPeer(first): %v", err)
	}

	second := &PeerInfo{
		Name:           "host-a", // same name, different daemon_id
		DaemonID:       "d_b",
		Token:          "tok-b",
		Role:           "dialer",
		RemoteRepoName: "mock-salesforce",
	}
	second.ProxyPrefix = DeriveProxyPrefix(second)
	if err := reg.AddPeer(second); err != nil {
		t.Fatalf("AddPeer(second): %v (expected success — name is not the uniqueness key)", err)
	}

	if len(reg.ListPeers()) != 2 {
		t.Fatalf("ListPeers() = %d, want 2 (both stored — keyed by DaemonID)", len(reg.ListPeers()))
	}
	if reg.GetPeer("d_a").ProxyPrefix != "mock-salesforce" ||
		reg.GetPeer("d_b").ProxyPrefix != "mock-salesforce" {
		t.Error("both peers must keep their derived prefix; collision resolves later at agent.register")
	}
}

// TestPeerRegistry_ProxyPrefixMigration covers the load-time retroactive
// stamp: a legacy peers.json with proxy_prefix="" on pre-existing entries
// is migrated to a derived prefix (and persisted back to disk).
func TestPeerRegistry_ProxyPrefixMigration(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "var"), 0o750); err != nil {
		t.Fatal(err)
	}
	peersPath := filepath.Join(thrumDir, "var", "peers.json")

	// Write a legacy peers.json: one entry with RemoteRepoName, one without,
	// and one that already has proxy_prefix (must be left alone).
	body := `{
  "local": {"daemon_id": "d_local", "port": 9100},
  "peers": [
    {"name": "host-a", "daemon_id": "d_a", "remote_repo_name": "mock-salesforce"},
    {"name": "leonsmacm1pro.local", "daemon_id": "d_b"},
    {"name": "kept", "daemon_id": "d_c", "proxy_prefix": "already-set"}
  ]
}`
	if err := os.WriteFile(peersPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	reg, err := NewPeerRegistry(peersPath)
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}

	peers := reg.ListPeers()
	byID := make(map[string]*PeerInfo, len(peers))
	for _, p := range peers {
		byID[p.DaemonID] = p
	}
	if got := byID["d_a"].ProxyPrefix; got != "mock-salesforce" {
		t.Errorf("d_a ProxyPrefix = %q, want %q", got, "mock-salesforce")
	}
	if got := byID["d_b"].ProxyPrefix; got != "leonsmacm1pro-local" {
		t.Errorf("d_b ProxyPrefix = %q, want %q", got, "leonsmacm1pro-local")
	}
	if got := byID["d_c"].ProxyPrefix; got != "already-set" {
		t.Errorf("d_c ProxyPrefix = %q, want %q (pre-set must be preserved)", got, "already-set")
	}

	// Verify the migration was persisted to disk.
	data, err := os.ReadFile(peersPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"proxy_prefix": "mock-salesforce"`) {
		t.Errorf("peers.json missing migrated mock-salesforce prefix; got: %s", data)
	}
	if !strings.Contains(string(data), `"proxy_prefix": "leonsmacm1pro-local"`) {
		t.Errorf("peers.json missing migrated fallback prefix; got: %s", data)
	}
}
