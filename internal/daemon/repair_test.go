package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/identity"
)

// newRepairTestRegistry creates an isolated PeerRegistry backed by a temp file.
// The identity stored in config.json is a fresh ULID so runs do not collide.
func newRepairTestRegistry(t *testing.T) *PeerRegistry {
	t.Helper()
	dir := t.TempDir()
	// PeerRegistry expects <thrumDir>/var/peers.json and derives thrumDir =
	// Dir(Dir(path)). Create the var/ subtree; identity.Bootstrap will
	// create config.json as needed.
	varDir := filepath.Join(dir, "var")
	if err := os.MkdirAll(varDir, 0o750); err != nil {
		t.Fatalf("mkdir var: %v", err)
	}
	reg, err := NewPeerRegistry(filepath.Join(varDir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	return reg
}

func seedPeer(t *testing.T, reg *PeerRegistry, info *PeerInfo) {
	t.Helper()
	if info.PairedAt.IsZero() {
		info.PairedAt = time.Now()
	}
	if err := reg.AddPeer(info); err != nil {
		t.Fatalf("seed peer: %v", err)
	}
}

func TestHandleRepairRequest_RefreshesRotatedDaemonID(t *testing.T) {
	reg := newRepairTestRegistry(t)
	seedPeer(t, reg, &PeerInfo{
		Name:      "alpha",
		DaemonID:  "d_old",
		Token:     "tok-alpha",
		Address:   "10.0.0.5:9100",
		Transport: "network",
	})

	local := identity.Identity{
		DaemonID:     "d_local",
		RepoName:     "home",
		Hostname:     "localhost",
		RepoPath:     "/repo",
		GitOriginURL: "git@example.com:me/home.git",
	}
	mgr := NewPeerRepairManager(reg, local, "localhost")

	dialer := PairMetadata{
		DaemonID:     "d_new",
		Name:         "alpha",
		Address:      "10.0.0.6:9100",
		RepoName:     "alpha-repo",
		Hostname:     "alpha-host",
		RepoPath:     "/alpha",
		GitOriginURL: "git@example.com:alpha/repo.git",
	}
	got, err := mgr.HandleRepairRequest("tok-alpha", dialer)
	if err != nil {
		t.Fatalf("HandleRepairRequest: %v", err)
	}
	if got.DaemonID != "d_local" {
		t.Errorf("returned DaemonID = %q, want d_local", got.DaemonID)
	}
	// Old key must be gone.
	if reg.GetPeer("d_old") != nil {
		t.Errorf("stale d_old entry still present")
	}
	// New key must be populated and carry refreshed metadata. Name is
	// preserved (repair does not rename).
	refreshed := reg.GetPeer("d_new")
	if refreshed == nil {
		t.Fatalf("no entry at d_new")
	}
	if refreshed.Name != "alpha" {
		t.Errorf("Name = %q, want alpha", refreshed.Name)
	}
	if refreshed.Address != "10.0.0.6:9100" {
		t.Errorf("Address = %q, want 10.0.0.6:9100", refreshed.Address)
	}
	if refreshed.RemoteRepoName != "alpha-repo" {
		t.Errorf("RemoteRepoName = %q", refreshed.RemoteRepoName)
	}
	if refreshed.Token != "tok-alpha" {
		t.Errorf("Token must be preserved; got %q", refreshed.Token)
	}
}

func TestHandleRepairRequest_UnknownToken(t *testing.T) {
	reg := newRepairTestRegistry(t)
	mgr := NewPeerRepairManager(reg, identity.Identity{DaemonID: "d_local"}, "localhost")

	_, err := mgr.HandleRepairRequest("bogus-token", PairMetadata{DaemonID: "d_x"})
	if err == nil {
		t.Fatalf("expected error on unknown token")
	}
	// Error message must be terse — no leak of registry state.
	if strings.Contains(err.Error(), "bogus-token") {
		t.Errorf("error leaked supplied token: %q", err.Error())
	}
}

func TestHandleRepairRequest_EmptyToken(t *testing.T) {
	reg := newRepairTestRegistry(t)
	mgr := NewPeerRepairManager(reg, identity.Identity{DaemonID: "d_local"}, "localhost")

	_, err := mgr.HandleRepairRequest("", PairMetadata{DaemonID: "d_x"})
	if err == nil {
		t.Fatalf("expected error on empty token")
	}
}

func TestHandleRepairRequest_RejectsASyncEntry(t *testing.T) {
	// a-sync peer entries have no Token in production, but we set one here
	// to exercise the Transport-guard path explicitly. The guard must
	// reject even when a token happens to match.
	reg := newRepairTestRegistry(t)
	seedPeer(t, reg, &PeerInfo{
		Name:        "async-peer",
		DaemonID:    "async:abc",
		Token:       "tok-async",
		Transport:   "a-sync",
		ASyncRemote: "git@example.com:shared/repo.git",
	})
	mgr := NewPeerRepairManager(reg, identity.Identity{DaemonID: "d_local"}, "localhost")

	_, err := mgr.HandleRepairRequest("tok-async", PairMetadata{DaemonID: "d_whatever"})
	if err == nil {
		t.Fatalf("expected rejection for a-sync entry")
	}
	if !strings.Contains(err.Error(), "a-sync") {
		t.Errorf("error should mention a-sync: %q", err.Error())
	}
}

func TestHandleRepairRequest_PreservesTokenAndName(t *testing.T) {
	reg := newRepairTestRegistry(t)
	seedPeer(t, reg, &PeerInfo{
		Name:      "beta",
		DaemonID:  "d_beta_old",
		Token:     "tok-beta",
		Address:   "127.0.0.1:1234",
		Transport: "local",
	})
	mgr := NewPeerRepairManager(reg, identity.Identity{DaemonID: "d_local"}, "localhost")

	// Dialer attempts to change Name — must be ignored.
	_, err := mgr.HandleRepairRequest("tok-beta", PairMetadata{
		DaemonID: "d_beta_new",
		Name:     "beta-renamed-attempt",
		Address:  "127.0.0.1:5678",
	})
	if err != nil {
		t.Fatalf("HandleRepairRequest: %v", err)
	}
	got := reg.GetPeer("d_beta_new")
	if got == nil {
		t.Fatalf("refreshed entry missing")
	}
	if got.Name != "beta" {
		t.Errorf("Name was changed: got %q, want beta", got.Name)
	}
	if got.Token != "tok-beta" {
		t.Errorf("Token was rotated: got %q, want tok-beta", got.Token)
	}
}

func TestHandleRepairRequest_IdempotentWhenNothingChanged(t *testing.T) {
	reg := newRepairTestRegistry(t)
	seedPeer(t, reg, &PeerInfo{
		Name:      "gamma",
		DaemonID:  "d_gamma",
		Token:     "tok-gamma",
		Address:   "192.168.1.10:9100",
		Transport: "network",
	})
	mgr := NewPeerRepairManager(reg, identity.Identity{DaemonID: "d_local"}, "localhost")

	dialer := PairMetadata{
		DaemonID: "d_gamma",
		Name:     "gamma",
		Address:  "192.168.1.10:9100",
	}
	if _, err := mgr.HandleRepairRequest("tok-gamma", dialer); err != nil {
		t.Fatalf("first repair: %v", err)
	}
	if _, err := mgr.HandleRepairRequest("tok-gamma", dialer); err != nil {
		t.Fatalf("second repair (should be idempotent): %v", err)
	}
	if reg.GetPeer("d_gamma") == nil {
		t.Errorf("entry disappeared after idempotent repair")
	}
}

func TestHandleRepairRequest_ReturnsLocalIdentity(t *testing.T) {
	reg := newRepairTestRegistry(t)
	seedPeer(t, reg, &PeerInfo{
		Name:      "delta",
		DaemonID:  "d_delta",
		Token:     "tok-delta",
		Address:   "127.0.0.1:9000",
		Transport: "local",
	})
	local := identity.Identity{
		DaemonID:     "d_me",
		RepoName:     "myrepo",
		Hostname:     "myhost",
		RepoPath:     "/my/path",
		GitOriginURL: "git@example.com:me/myrepo.git",
	}
	mgr := NewPeerRepairManager(reg, local, "myhost")

	got, err := mgr.HandleRepairRequest("tok-delta", PairMetadata{DaemonID: "d_delta"})
	if err != nil {
		t.Fatalf("HandleRepairRequest: %v", err)
	}
	if got.DaemonID != "d_me" || got.Name != "myhost" ||
		got.RepoName != "myrepo" || got.Hostname != "myhost" ||
		got.RepoPath != "/my/path" ||
		got.GitOriginURL != "git@example.com:me/myrepo.git" {
		t.Errorf("local metadata round-trip mismatch: %+v", got)
	}
}
