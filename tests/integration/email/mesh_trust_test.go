//go:build integration

// D-B1.19 integration tests — mesh trust propagation (§17 AC #2, #6, #7).
//
// AC #2: Three-daemon gossip propagation — A pairs with B; B announces A to C
//
//	via peer.announce; C's config gains A as a peer.
//
// AC #6: Revoke propagation — A revokes B; a gossip peer.revoke is applied to
//
//	C's config, removing B from C's peer list.
//
// AC #7: Dead-laptop rebind — a fresh daemon-A' sends peer.rebind; B confirms
//
//	via ConfirmRebind; B's config carries the new daemon_id under A's handle.
//
// Pragmatic shortcuts:
//   - No full Bridge spin-up: MeshHandlerImpl is constructed directly.
//   - "Gossip propagation" is exercised by calling HandleProtocol("peer.announce")
//     on C's mesh handler — the real Bridge would send SMTP+fetch;
//     the integration value here is verifying the full config-mutation path
//     (WAL intent → config.json write → WAL committed marker).
//   - Atomic-write and audit-log properties are cross-cutting and covered in
//     peer_config_test.go.

package email_integration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// --- helpers -----------------------------------------------------------------

// setupConfigDir creates a temp dir with a minimal config.json containing zero
// or more pre-seeded peers. Returns the config dir path and the full config.json path.
func setupConfigDir(t *testing.T, peers []config.EmailPeer) (configDir, configPath string) {
	t.Helper()
	dir := t.TempDir()
	configPath = filepath.Join(dir, "config.json")

	var peersJSON []byte
	if len(peers) == 0 {
		peersJSON = []byte("[]")
	} else {
		var err error
		peersJSON, err = json.Marshal(peers)
		if err != nil {
			t.Fatalf("marshal peers: %v", err)
		}
	}

	data := []byte(`{
		"daemon": {"local_only": true},
		"email": {
			"enabled": true,
			"peers": ` + string(peersJSON) + `,
			"mesh": {
				"vouch_acceptance": "auto_with_notify",
				"hop_count_ceiling": 5,
				"allow_transitive_vouching": true,
				"revocation_propagation": "gossip"
			}
		}
	}`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return dir, configPath
}

// newMeshHandler constructs a MeshHandlerImpl for a daemon with the given
// config dir.
func newMeshHandler(t *testing.T, daemonID, configPath string, overrides ...func(*email.MeshConfig)) *email.MeshHandlerImpl {
	t.Helper()
	walPath := filepath.Join(t.TempDir(), daemonID+"-wal.jsonl")
	wal, err := state.NewPendingMeshUpdatesLog(walPath)
	if err != nil {
		t.Fatalf("new wal for %s: %v", daemonID, err)
	}
	t.Cleanup(func() { _ = wal.Close() })

	cfg := email.MeshConfig{
		MyDaemonID:              daemonID,
		MyDaemonShort:           daemonID[:8],
		VouchAcceptance:         "auto_with_notify",
		AllowTransitiveVouching: true,
		HopCountCeiling:         5,
		RevocationPropagation:   "gossip",
		PairPendingTTL:          24 * time.Hour,
		ConfigPath:              configPath,
	}
	for _, fn := range overrides {
		fn(&cfg)
	}
	return email.NewMeshHandler(cfg, wal, nil, nil)
}

// loadPeers reads config.json and returns the email.peers array.
func loadPeers(t *testing.T, configPath string) []config.EmailPeer {
	t.Helper()
	cfg, err := config.LoadThrumConfig(filepath.Dir(configPath))
	if err != nil {
		t.Fatalf("LoadThrumConfig: %v", err)
	}
	return cfg.Email.Peers
}

// findPeerByHandle returns the first peer with the given handle, or zero value.
func findPeerByHandle(peers []config.EmailPeer, handle string) (config.EmailPeer, bool) {
	for _, p := range peers {
		if p.Handle == handle {
			return p, true
		}
	}
	return config.EmailPeer{}, false
}

// makePeerPayload marshals a PeerProtocolPayload to JSON.
func makePeerPayload(t *testing.T, handle, daemonID, contactEmail, vouchedBy string) []byte {
	t.Helper()
	payload := email.PeerProtocolPayload{
		Handle:       handle,
		DaemonID:     daemonID,
		ContactEmail: contactEmail,
		VouchedBy:    vouchedBy,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// --- tests -------------------------------------------------------------------

// TestEmail_AC2_ThreeDaemonGossipPropagation verifies AC #2:
// When B announces A to C via peer.announce (hop=1, transitive_vouching=true),
// C's config.json gains A as a peer entry.
func TestEmail_AC2_ThreeDaemonGossipPropagation(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)

	const (
		daemonA = "daemon-AC2-AAAA"
		daemonB = "daemon-AC2-BBBB"
		daemonC = "daemon-AC2-CCCC"
		handleA = "alpha"
	)

	// C starts with no peers.
	_, configPathC := setupConfigDir(t, nil)
	meshC := newMeshHandler(t, daemonC, configPathC, func(c *email.MeshConfig) {
		c.AllowTransitiveVouching = true
		c.VouchAcceptance = "auto"
	})

	// B gossips A's info to C via peer.announce (hop=1).
	payload := makePeerPayload(t, handleA, daemonA, "alpha@example.com", daemonB)
	headers := map[string]string{
		"X-Thrum-From-Daemon": daemonB,
		"X-Thrum-Hop-Count":   "1",
	}

	ctx := context.Background()
	if err := meshC.HandleProtocol(ctx, "peer.announce", headers, payload); err != nil {
		t.Fatalf("HandleProtocol peer.announce: %v", err)
	}

	// C's config should now contain A.
	peers := loadPeers(t, configPathC)
	p, found := findPeerByHandle(peers, handleA)
	if !found {
		t.Fatalf("peer %q not found in C's config after gossip; peers: %+v", handleA, peers)
	}
	if p.DaemonID != daemonA {
		t.Errorf("peer DaemonID = %q, want %q", p.DaemonID, daemonA)
	}
}

// TestEmail_AC6_RevokePropagation verifies AC #6:
// When a peer.revoke arrives (gossip=true), the target peer is removed from
// the local config.
func TestEmail_AC6_RevokePropagation(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)

	const (
		daemonA = "daemon-AC6-AAAA"
		daemonB = "daemon-AC6-BBBB"
		handleB = "beta"
	)

	// C starts with both A and B as peers.
	initPeers := []config.EmailPeer{
		{Handle: "alpha", DaemonID: daemonA, Trust: "full", VouchedBy: "self"},
		{Handle: handleB, DaemonID: daemonB, Trust: "full", VouchedBy: "self"},
	}
	_, configPathC := setupConfigDir(t, initPeers)
	meshC := newMeshHandler(t, "daemon-AC6-CCCC", configPathC, func(c *email.MeshConfig) {
		c.RevocationPropagation = "gossip"
	})

	// A sends peer.revoke for B.
	payload := makePeerPayload(t, handleB, daemonB, "", "")
	headers := map[string]string{
		"X-Thrum-From-Daemon": daemonA,
	}

	ctx := context.Background()
	if err := meshC.HandleProtocol(ctx, "peer.revoke", headers, payload); err != nil {
		t.Fatalf("HandleProtocol peer.revoke: %v", err)
	}

	// C's config must no longer contain B.
	peers := loadPeers(t, configPathC)
	if _, found := findPeerByHandle(peers, handleB); found {
		t.Errorf("peer %q still present in config after revoke; peers: %+v", handleB, peers)
	}
	// A must still be present.
	if _, found := findPeerByHandle(peers, "alpha"); !found {
		t.Errorf("peer %q was unexpectedly removed by revoke", "alpha")
	}
}

// TestEmail_AC7_DeadLaptopRebind verifies AC #7:
// A fresh daemon-A' sends peer.rebind; B confirms via ConfirmRebind;
// B's config carries the new daemon_id under A's handle.
func TestEmail_AC7_DeadLaptopRebind(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)

	const (
		daemonB    = "daemon-AC7-BBBB"
		handleA    = "alpha-ac7"
		oldDaemonA = "daemon-AC7-AAAA-old"
		newDaemonA = "daemon-AC7-AAAA-new"
	)

	// B starts with old A as a peer.
	initPeers := []config.EmailPeer{
		{Handle: handleA, DaemonID: oldDaemonA, Trust: "full", VouchedBy: "self"},
	}
	_, configPathB := setupConfigDir(t, initPeers)
	meshB := newMeshHandler(t, daemonB, configPathB)

	// New daemon-A' sends peer.rebind.
	rebindPayload := struct {
		Handle      string `json:"handle"`
		DaemonID    string `json:"daemon_id"`
		NewDaemonID string `json:"new_daemon_id"`
	}{
		Handle:      handleA,
		DaemonID:    oldDaemonA,
		NewDaemonID: newDaemonA,
	}
	payloadBytes, _ := json.Marshal(rebindPayload)

	ctx := context.Background()
	if err := meshB.HandleProtocol(ctx, "peer.rebind", map[string]string{
		"X-Thrum-From-Daemon": newDaemonA,
	}, payloadBytes); err != nil {
		t.Fatalf("HandleProtocol peer.rebind: %v", err)
	}

	// Operator confirms the rebind.
	if err := meshB.ConfirmRebind(ctx, handleA, newDaemonA); err != nil {
		t.Fatalf("ConfirmRebind: %v", err)
	}

	// B's config must carry the new daemon_id under A's handle.
	peers := loadPeers(t, configPathB)
	p, found := findPeerByHandle(peers, handleA)
	if !found {
		t.Fatalf("peer %q not found after rebind; peers: %+v", handleA, peers)
	}
	if p.DaemonID != newDaemonA {
		t.Errorf("after rebind: DaemonID = %q, want %q", p.DaemonID, newDaemonA)
	}
}
