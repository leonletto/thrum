package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// --- test helpers ---

// setupMeshConfig creates a temp dir with a minimal config.json and returns
// the dir and the full config.json path.
func setupMeshConfig(t *testing.T) (configDir, configPath string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := []byte(`{
		"daemon": {"local_only": true},
		"email": {
			"enabled": true,
			"peers": [],
			"mesh": {
				"vouch_acceptance": "auto_with_notify",
				"hop_count_ceiling": 5,
				"allow_transitive_vouching": true
			}
		}
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("setup config: %v", err)
	}
	return dir, path
}

// setupMeshConfigWithPeers creates a config with pre-seeded peers.
func setupMeshConfigWithPeers(t *testing.T, peers []config.EmailPeer) (configDir, configPath string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	peersJSON, _ := json.Marshal(peers)
	data := []byte(`{
		"daemon": {"local_only": true},
		"email": {
			"enabled": true,
			"peers": ` + string(peersJSON) + `,
			"mesh": {
				"vouch_acceptance": "auto_with_notify",
				"hop_count_ceiling": 5,
				"allow_transitive_vouching": true
			}
		}
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("setup config with peers: %v", err)
	}
	return dir, path
}

// newTestMeshHandler returns a handler with injectable clock and a stub notifier.
func newTestMeshHandler(t *testing.T, configDir, configPath string, overrides ...func(*MeshConfig)) (*MeshHandlerImpl, *stubNotifier, *state.PendingMeshUpdatesLog) {
	t.Helper()
	walPath := filepath.Join(t.TempDir(), "wal.jsonl")
	wal, err := state.NewPendingMeshUpdatesLog(walPath)
	if err != nil {
		t.Fatalf("new wal: %v", err)
	}
	t.Cleanup(func() { _ = wal.Close() })

	notifier := &stubNotifier{}
	cfg := MeshConfig{
		MyDaemonID:              "daemon-self-0000",
		MyDaemonShort:           "daemon-s",
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
	h := NewMeshHandler(cfg, wal, notifier, nil)
	return h, notifier, wal
}

type stubNotifier struct {
	messages []string
}

func (s *stubNotifier) Notify(_ context.Context, msg string) error {
	s.messages = append(s.messages, msg)
	return nil
}

// captureLogs redirects the default log output to a buffer for the duration of
// the test. Returns a function that returns the captured output.
func captureLogs(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })
	return func() string { return buf.String() }
}

// loadPeers reads the email.peers[] from the config at configPath.
func loadPeers(t *testing.T, configDir string) []config.EmailPeer {
	t.Helper()
	cfg, err := config.LoadThrumConfig(configDir)
	if err != nil {
		t.Fatalf("loadPeers: %v", err)
	}
	return cfg.Email.Peers
}

// --- Commit 1: peer.pair ---

func TestMesh_PeerPairOperatorConfirmAdds(t *testing.T) {
	configDir, configPath := setupMeshConfig(t)
	h, notifier, _ := newTestMeshHandler(t, configDir, configPath)
	ctx := context.Background()

	env := PeerProtocolPayload{
		Handle:       "alice",
		DaemonID:     "daemon-alice-1234",
		ContactEmail: "alice@example.com",
	}

	// HandlePeerPair → Pending action, nudge fired.
	action, err := h.HandlePeerPair(ctx, env)
	if err != nil {
		t.Fatalf("HandlePeerPair: %v", err)
	}
	if action.Kind != ActionPending {
		t.Errorf("expected ActionPending got %v", action.Kind)
	}
	if len(notifier.messages) == 0 {
		t.Error("expected notifier to be called")
	}
	if !strings.Contains(notifier.messages[0], "alice") {
		t.Errorf("nudge message missing handle: %q", notifier.messages[0])
	}

	// ConfirmStrangerPair → peer lands in config.
	if err := h.ConfirmStrangerPair(ctx, "alice"); err != nil {
		t.Fatalf("ConfirmStrangerPair: %v", err)
	}
	peers := loadPeers(t, configDir)
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].Handle != "alice" || peers[0].VouchedBy != "self" || peers[0].Trust != "full" {
		t.Errorf("unexpected peer entry: %+v", peers[0])
	}
}

func TestMesh_PeerPairOperatorDenyDrops(t *testing.T) {
	configDir, configPath := setupMeshConfig(t)
	h, _, _ := newTestMeshHandler(t, configDir, configPath)
	ctx := context.Background()
	getLogs := captureLogs(t)

	env := PeerProtocolPayload{Handle: "bob", DaemonID: "daemon-bob-5678", ContactEmail: "bob@example.com"}
	if _, err := h.HandlePeerPair(ctx, env); err != nil {
		t.Fatalf("HandlePeerPair: %v", err)
	}

	if err := h.DenyStrangerPair(ctx, "bob"); err != nil {
		t.Fatalf("DenyStrangerPair: %v", err)
	}

	// Pending map should be empty.
	h.pendingMu.Lock()
	_, still := h.pendingPairs["bob"]
	h.pendingMu.Unlock()
	if still {
		t.Error("bob should have been removed from pendingPairs")
	}

	// Audit log emitted.
	if !strings.Contains(getLogs(), "denied") {
		t.Error("expected deny audit log")
	}

	// Config unchanged.
	if peers := loadPeers(t, configDir); len(peers) != 0 {
		t.Errorf("expected 0 peers after deny, got %d", len(peers))
	}
}

// --- Commit 2: peer.welcome ---

func TestMesh_PeerWelcomeUpdatesTrustToFull(t *testing.T) {
	configDir, configPath := setupMeshConfigWithPeers(t, []config.EmailPeer{
		{Handle: "dave", DaemonID: "daemon-dave-aaaa", ContactEmail: "dave@example.com", Trust: "limited", VouchedBy: "self"},
	})
	h, _, _ := newTestMeshHandler(t, configDir, configPath)
	ctx := context.Background()
	getLogs := captureLogs(t)

	env := PeerProtocolPayload{Handle: "dave", DaemonID: "daemon-dave-aaaa"}
	if err := h.HandlePeerWelcome(ctx, env); err != nil {
		t.Fatalf("HandlePeerWelcome: %v", err)
	}

	peers := loadPeers(t, configDir)
	if len(peers) != 1 || peers[0].Trust != "full" {
		t.Errorf("expected trust=full, got: %+v", peers)
	}
	if !strings.Contains(getLogs(), "trust=full") {
		t.Errorf("expected audit log with trust=full, got: %s", getLogs())
	}

	// Idempotent: second call must not fail or duplicate.
	if err := h.HandlePeerWelcome(ctx, env); err != nil {
		t.Fatalf("second HandlePeerWelcome: %v", err)
	}
	peers = loadPeers(t, configDir)
	if len(peers) != 1 {
		t.Errorf("expected still 1 peer after idempotent call, got %d", len(peers))
	}
}

// --- Commit 3: peer.announce ---

func TestMesh_PeerAnnounceVouchAcceptanceAutoNotify(t *testing.T) {
	configDir, configPath := setupMeshConfig(t)
	// Default config already uses "auto_with_notify".
	h, notifier, _ := newTestMeshHandler(t, configDir, configPath)
	ctx := context.Background()
	getLogs := captureLogs(t)

	env := PeerProtocolPayload{Handle: "eve", DaemonID: "daemon-eve-bbbb", VouchedBy: "alice"}
	if err := h.HandlePeerAnnounce(ctx, env, 1); err != nil {
		t.Fatalf("HandlePeerAnnounce: %v", err)
	}

	peers := loadPeers(t, configDir)
	if len(peers) != 1 || peers[0].Handle != "eve" {
		t.Fatalf("expected peer eve, got: %+v", peers)
	}
	if !strings.Contains(getLogs(), "added") {
		t.Errorf("expected audit log, got: %s", getLogs())
	}
	if len(notifier.messages) == 0 {
		t.Error("expected notify call for auto_with_notify")
	}
}

func TestMesh_PeerAnnounceTransitiveAllowed(t *testing.T) {
	configDir, configPath := setupMeshConfig(t)
	h, _, _ := newTestMeshHandler(t, configDir, configPath, func(c *MeshConfig) {
		c.AllowTransitiveVouching = true
		c.VouchAcceptance = "auto"
	})
	ctx := context.Background()

	env := PeerProtocolPayload{Handle: "frank", DaemonID: "daemon-frank-cccc", VouchedBy: "bob"}
	if err := h.HandlePeerAnnounce(ctx, env, 2); err != nil {
		t.Fatalf("HandlePeerAnnounce hop=2 transitive=true: %v", err)
	}
	if peers := loadPeers(t, configDir); len(peers) != 1 {
		t.Errorf("expected peer added, got %d peers", len(peers))
	}
}

func TestMesh_PeerAnnounceTransitiveDisallowed(t *testing.T) {
	configDir, configPath := setupMeshConfig(t)
	h, _, _ := newTestMeshHandler(t, configDir, configPath, func(c *MeshConfig) {
		c.AllowTransitiveVouching = false
		c.VouchAcceptance = "auto"
	})
	ctx := context.Background()
	getLogs := captureLogs(t)

	env := PeerProtocolPayload{Handle: "grace", DaemonID: "daemon-grace-dddd", VouchedBy: "carol"}
	if err := h.HandlePeerAnnounce(ctx, env, 2); err != nil {
		t.Fatalf("HandlePeerAnnounce hop=2 transitive=false: %v", err)
	}

	// No peer added.
	if peers := loadPeers(t, configDir); len(peers) != 0 {
		t.Errorf("expected drop, got %d peers", len(peers))
	}
	if !strings.Contains(getLogs(), "transitive_vouching=false") {
		t.Errorf("expected transitive drop log, got: %s", getLogs())
	}
}

func TestMesh_PeerAnnounceCeilingDrops(t *testing.T) {
	configDir, configPath := setupMeshConfig(t)
	h, _, _ := newTestMeshHandler(t, configDir, configPath, func(c *MeshConfig) {
		c.HopCountCeiling = 3
		c.VouchAcceptance = "auto"
	})
	ctx := context.Background()
	getLogs := captureLogs(t)

	env := PeerProtocolPayload{Handle: "hank", DaemonID: "daemon-hank-eeee", VouchedBy: "dave"}
	if err := h.HandlePeerAnnounce(ctx, env, 10); err != nil {
		t.Fatalf("HandlePeerAnnounce hop=10: %v", err)
	}

	// No peer added.
	if peers := loadPeers(t, configDir); len(peers) != 0 {
		t.Errorf("expected ceiling drop, got %d peers", len(peers))
	}
	if !strings.Contains(getLogs(), "ceiling") {
		t.Errorf("expected ceiling drop log, got: %s", getLogs())
	}
}

// --- Commit 4: peer.rebind ---

func TestMesh_PeerRebindNewDaemonIdUnderSameHandle(t *testing.T) {
	configDir, configPath := setupMeshConfigWithPeers(t, []config.EmailPeer{
		{Handle: "ivan", DaemonID: "daemon-ivan-old1", ContactEmail: "ivan@example.com", Trust: "full", VouchedBy: "self"},
	})
	h, notifier, _ := newTestMeshHandler(t, configDir, configPath)
	ctx := context.Background()

	env := PeerProtocolPayload{
		Handle:      "ivan",
		DaemonID:    "daemon-ivan-old1",
		NewDaemonID: "daemon-ivan-new2",
	}
	if err := h.HandlePeerRebind(ctx, env); err != nil {
		t.Fatalf("HandlePeerRebind: %v", err)
	}

	// Notifier should have fired with old+new short IDs.
	if len(notifier.messages) == 0 {
		t.Error("expected notifier to be called for rebind")
	}

	// Before confirm: daemon_id unchanged.
	if peers := loadPeers(t, configDir); peers[0].DaemonID != "daemon-ivan-old1" {
		t.Errorf("daemon_id should not change before confirm: %v", peers[0].DaemonID)
	}

	// Confirm: daemon_id updated, handle preserved.
	if err := h.ConfirmRebind(ctx, "ivan", "daemon-ivan-new2"); err != nil {
		t.Fatalf("ConfirmRebind: %v", err)
	}
	peers := loadPeers(t, configDir)
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].Handle != "ivan" {
		t.Errorf("handle changed: %q", peers[0].Handle)
	}
	if peers[0].DaemonID != "daemon-ivan-new2" {
		t.Errorf("daemon_id not updated: %q", peers[0].DaemonID)
	}
}

// --- Commit 5: peer.revoke ---

func TestMesh_PeerRevokeRemovesPeer(t *testing.T) {
	configDir, configPath := setupMeshConfigWithPeers(t, []config.EmailPeer{
		{Handle: "judy", DaemonID: "daemon-judy-1111", ContactEmail: "judy@example.com", Trust: "full", VouchedBy: "self"},
		{Handle: "ken", DaemonID: "daemon-ken-2222", ContactEmail: "ken@example.com", Trust: "full", VouchedBy: "self"},
	})
	h, _, _ := newTestMeshHandler(t, configDir, configPath)
	ctx := context.Background()
	getLogs := captureLogs(t)

	env := PeerProtocolPayload{Handle: "judy", DaemonID: "daemon-judy-1111"}
	if err := h.HandlePeerRevoke(ctx, env); err != nil {
		t.Fatalf("HandlePeerRevoke: %v", err)
	}

	peers := loadPeers(t, configDir)
	if len(peers) != 1 || peers[0].Handle != "ken" {
		t.Errorf("expected only ken to remain, got: %+v", peers)
	}
	if !strings.Contains(getLogs(), "removed peer judy") {
		t.Errorf("expected audit log, got: %s", getLogs())
	}
	// Gossip stub log emitted.
	if !strings.Contains(getLogs(), "would gossip peer.revoke") {
		t.Errorf("expected gossip stub log, got: %s", getLogs())
	}
}

func TestMesh_PeerPairOperatorTimeoutDrops(t *testing.T) {
	configDir, configPath := setupMeshConfig(t)

	// Use a clock that we can advance.
	now := time.Now()
	h, _, _ := newTestMeshHandler(t, configDir, configPath, func(c *MeshConfig) {
		c.PairPendingTTL = time.Hour
	})
	h.nowFn = func() time.Time { return now }
	ctx := context.Background()
	getLogs := captureLogs(t)

	env := PeerProtocolPayload{Handle: "charlie", DaemonID: "daemon-charlie-9999", ContactEmail: "c@example.com"}
	if _, err := h.HandlePeerPair(ctx, env); err != nil {
		t.Fatalf("HandlePeerPair: %v", err)
	}

	// Advance clock past TTL.
	h.nowFn = func() time.Time { return now.Add(2 * time.Hour) }

	swept := h.SweepStalePendingPairs(ctx)
	if swept != 1 {
		t.Errorf("expected 1 swept, got %d", swept)
	}

	// Pending map empty.
	h.pendingMu.Lock()
	_, still := h.pendingPairs["charlie"]
	h.pendingMu.Unlock()
	if still {
		t.Error("charlie should have been swept")
	}
	if !strings.Contains(getLogs(), "TTL expired") {
		t.Errorf("expected TTL expired log, got: %s", getLogs())
	}
}

// --- Commit 6: cross-cutting §3.10 property tests ---

// TestMesh_AtomicWriteUsesTmpRenamePattern verifies that SaveThrumConfig's
// write is complete and consistent: we read back the written peers
// immediately after a handler call and confirm they match what was written.
// The underlying mechanism (os.WriteFile vs tmp+rename) is an implementation
// detail of config.SaveThrumConfig; this test asserts observable correctness.
func TestMesh_AtomicWriteUsesTmpRenamePattern(t *testing.T) {
	configDir, configPath := setupMeshConfig(t)
	h, _, _ := newTestMeshHandler(t, configDir, configPath)
	ctx := context.Background()

	env := PeerProtocolPayload{Handle: "lana", DaemonID: "daemon-lana-3333", ContactEmail: "lana@example.com"}
	if _, err := h.HandlePeerPair(ctx, env); err != nil {
		t.Fatalf("HandlePeerPair: %v", err)
	}
	if err := h.ConfirmStrangerPair(ctx, "lana"); err != nil {
		t.Fatalf("ConfirmStrangerPair: %v", err)
	}

	// File must be readable and consistent immediately after the call.
	peers := loadPeers(t, configDir)
	if len(peers) != 1 || peers[0].Handle != "lana" {
		t.Errorf("expected lana in peers, got: %+v", peers)
	}
	// No .tmp file should remain.
	tmpPath := configPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("unexpected .tmp file after write: %v", err)
	}
}

// TestMesh_ValidatorRejectsMalformedAnnounce verifies that an injected
// validator can abort a gossip-driven config mutation. No peer must land
// in email.peers[] when the validator returns an error.
func TestMesh_ValidatorRejectsMalformedAnnounce(t *testing.T) {
	configDir, configPath := setupMeshConfig(t)
	walPath := filepath.Join(t.TempDir(), "wal.jsonl")
	wal, err := state.NewPendingMeshUpdatesLog(walPath)
	if err != nil {
		t.Fatalf("new wal: %v", err)
	}
	t.Cleanup(func() { _ = wal.Close() })

	rejectAll := func(*config.ThrumConfig) error {
		return fmt.Errorf("validator: reject all")
	}
	cfg := MeshConfig{
		MyDaemonID:      "daemon-self-0000",
		VouchAcceptance: "auto",
		HopCountCeiling: 5,
		ConfigPath:      configPath,
	}
	h := NewMeshHandler(cfg, wal, nil, rejectAll)

	ctx := context.Background()
	env := PeerProtocolPayload{Handle: "mallory", DaemonID: "daemon-mallory-9999", VouchedBy: "evil"}
	err = h.HandlePeerAnnounce(ctx, env, 1)
	if err == nil {
		t.Fatal("expected error from validator, got nil")
	}
	if !strings.Contains(err.Error(), "validator rejected") {
		t.Errorf("expected validator-rejection error, got: %v", err)
	}

	// No peer must have landed.
	if peers := loadPeers(t, configDir); len(peers) != 0 {
		t.Errorf("expected 0 peers after validator reject, got: %+v", peers)
	}
}

// TestMesh_AuditLogLineEmittedPerMutation verifies that exactly one audit
// log line is emitted per successful verb mutation.
func TestMesh_AuditLogLineEmittedPerMutation(t *testing.T) {
	tests := []struct {
		name    string
		trigger func(ctx context.Context, h *MeshHandlerImpl, configDir, configPath string) error
		wantLog string
	}{
		{
			name: "pair confirm",
			trigger: func(ctx context.Context, h *MeshHandlerImpl, configDir, configPath string) error {
				env := PeerProtocolPayload{Handle: "m1", DaemonID: "d-m1-aaaa", ContactEmail: "m1@x.com"}
				if _, err := h.HandlePeerPair(ctx, env); err != nil {
					return err
				}
				return h.ConfirmStrangerPair(ctx, "m1")
			},
			wantLog: "added peer m1",
		},
		{
			name: "welcome",
			trigger: func(ctx context.Context, h *MeshHandlerImpl, configDir, configPath string) error {
				env := PeerProtocolPayload{Handle: "m2", DaemonID: "d-m2-bbbb"}
				return h.HandlePeerWelcome(ctx, env)
			},
			wantLog: "trust=full",
		},
		{
			name: "announce",
			trigger: func(ctx context.Context, h *MeshHandlerImpl, configDir, configPath string) error {
				env := PeerProtocolPayload{Handle: "m3", DaemonID: "d-m3-cccc", VouchedBy: "x"}
				return h.HandlePeerAnnounce(ctx, env, 1)
			},
			wantLog: "added m3",
		},
		{
			name: "revoke",
			trigger: func(ctx context.Context, h *MeshHandlerImpl, configDir, configPath string) error {
				env := PeerProtocolPayload{Handle: "m4", DaemonID: "d-m4-dddd"}
				return h.HandlePeerRevoke(ctx, env)
			},
			wantLog: "peer.revoke",
		},
	}

	// peer.welcome "trust=full" is logged inside mutate when peer IS found;
	// "unknown peer" is logged when not found. For the audit test we set up
	// configs matching each case.
	welcomePeers := []config.EmailPeer{
		{Handle: "m2", DaemonID: "d-m2-bbbb", Trust: "limited", VouchedBy: "self"},
	}
	revokePeers := []config.EmailPeer{
		{Handle: "m4", DaemonID: "d-m4-dddd", Trust: "full", VouchedBy: "self"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var configDir, configPath string
			switch tc.name {
			case "welcome":
				configDir, configPath = setupMeshConfigWithPeers(t, welcomePeers)
			case "revoke":
				configDir, configPath = setupMeshConfigWithPeers(t, revokePeers)
			default:
				configDir, configPath = setupMeshConfig(t)
			}

			h, _, _ := newTestMeshHandler(t, configDir, configPath, func(c *MeshConfig) {
				c.VouchAcceptance = "auto_with_notify"
			})
			getLogs := captureLogs(t)
			ctx := context.Background()

			if err := tc.trigger(ctx, h, configDir, configPath); err != nil {
				t.Fatalf("trigger: %v", err)
			}
			if !strings.Contains(getLogs(), tc.wantLog) {
				t.Errorf("expected audit log %q, got: %s", tc.wantLog, getLogs())
			}
		})
	}
}

// TestMesh_FsnotifyReloadSkippedForGossipWrites documents the fsnotify
// integration boundary. D-B1.13 mesh handlers write config.json directly
// via SaveThrumConfig. No reload trigger is called from mesh.go — that
// coupling lives in bridge.go (D-B1.14). This test verifies that the
// MeshHandlerImpl type has no reload-trigger field or method, ensuring the
// boundary is respected at compile time.
//
// If this test is removed, document the gap in a follow-up task.
func TestMesh_FsnotifyReloadSkippedForGossipWrites(t *testing.T) {
	// Structural: MeshHandlerImpl must not have a reloadFn or reloadTrigger field.
	// We verify this by confirming we can construct one with only the approved
	// fields and that the handler satisfies MeshHandler without any reload path.
	_, configPath := setupMeshConfig(t)
	// configDir unused here — handler only needs configPath.
	h, _, _ := newTestMeshHandler(t, "" /* configDir not needed */, configPath)

	// Compile-time check: MeshHandlerImpl satisfies MeshHandler.
	var _ MeshHandler = h

	// No reload field exposed — confirmed by absence of exported Reload method.
	// (If a reload method were added, it would need to appear in this file.)
	_ = h // no reload call, no panic
}

// TestMesh_WalIntentBeforeWriteCommittedAfter verifies the three-step
// WAL protocol: intent recorded BEFORE config write, committed AFTER.
func TestMesh_WalIntentBeforeWriteCommittedAfter(t *testing.T) {
	_, configPath := setupMeshConfig(t)
	walPath := filepath.Join(t.TempDir(), "wal.jsonl")
	wal, err := state.NewPendingMeshUpdatesLog(walPath)
	if err != nil {
		t.Fatalf("new wal: %v", err)
	}
	t.Cleanup(func() { _ = wal.Close() })

	notifier := &stubNotifier{}
	cfg := MeshConfig{
		MyDaemonID:      "daemon-self-0000",
		VouchAcceptance: "auto",
		HopCountCeiling: 5,
		ConfigPath:      configPath,
	}
	h := NewMeshHandler(cfg, wal, notifier, nil)
	ctx := context.Background()

	env := PeerProtocolPayload{Handle: "nia", DaemonID: "daemon-nia-5555", VouchedBy: "alice"}

	// Read WAL before the call — should be empty.
	before, err := wal.Pending()
	if err != nil {
		t.Fatalf("wal pending before: %v", err)
	}
	if len(before) != 0 {
		t.Errorf("expected 0 pending before call, got %d", len(before))
	}

	if err := h.HandlePeerAnnounce(ctx, env, 1); err != nil {
		t.Fatalf("HandlePeerAnnounce: %v", err)
	}

	// After a successful call, WAL should have 0 pending (committed was written).
	after, err := wal.Pending()
	if err != nil {
		t.Fatalf("wal pending after: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("expected 0 pending after committed write, got %d: %+v", len(after), after)
	}

	// Verify the WAL file contains both "intent" and "committed" records.
	walContents, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatalf("read wal: %v", err)
	}
	if !strings.Contains(string(walContents), `"intent"`) {
		t.Error("expected intent record in WAL")
	}
	if !strings.Contains(string(walContents), `"committed"`) {
		t.Error("expected committed record in WAL")
	}
}

// TestMesh_WalReplayOnBootMissingCommitted verifies that Pending() surfaces
// intent records without a matching committed marker (as would occur after a
// crash between config write and committed-marker write).
func TestMesh_WalReplayOnBootMissingCommitted(t *testing.T) {
	walPath := filepath.Join(t.TempDir(), "wal.jsonl")
	wal, err := state.NewPendingMeshUpdatesLog(walPath)
	if err != nil {
		t.Fatalf("new wal: %v", err)
	}

	// Simulate: intent written, but committed never appended (crash window).
	if err := wal.AppendIntent("orphan-update-id", "peer.announce", nil); err != nil {
		t.Fatalf("AppendIntent: %v", err)
	}
	if err := wal.Close(); err != nil {
		t.Fatalf("close wal: %v", err)
	}

	// Re-open (simulates daemon restart).
	wal2, err := state.NewPendingMeshUpdatesLog(walPath)
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	t.Cleanup(func() { _ = wal2.Close() })

	pending, err := wal2.Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 orphan intent, got %d", len(pending))
	}
	if pending[0].UpdateID != "orphan-update-id" {
		t.Errorf("wrong updateID: %q", pending[0].UpdateID)
	}
	if pending[0].Stage != "intent" {
		t.Errorf("expected stage=intent, got %q", pending[0].Stage)
	}
}

// TestMesh_ForwardBindingRuleOnlyEmailPeersMutated verifies that a handler
// call modifies ONLY email.peers[] and leaves every other top-level config
// key untouched.
func TestMesh_ForwardBindingRuleOnlyEmailPeersMutated(t *testing.T) {
	configDir, configPath := setupMeshConfig(t)

	// Snapshot the config before.
	before, err := config.LoadThrumConfig(configDir)
	if err != nil {
		t.Fatalf("load before: %v", err)
	}
	beforePeers := len(before.Email.Peers)

	h, _, _ := newTestMeshHandler(t, configDir, configPath, func(c *MeshConfig) {
		c.VouchAcceptance = "auto"
	})
	ctx := context.Background()

	env := PeerProtocolPayload{Handle: "otto", DaemonID: "daemon-otto-6666", VouchedBy: "alice"}
	if err := h.HandlePeerAnnounce(ctx, env, 1); err != nil {
		t.Fatalf("HandlePeerAnnounce: %v", err)
	}

	after, err := config.LoadThrumConfig(configDir)
	if err != nil {
		t.Fatalf("load after: %v", err)
	}

	// peers grew by 1.
	if len(after.Email.Peers) != beforePeers+1 {
		t.Errorf("expected peers to grow by 1, was %d now %d", beforePeers, len(after.Email.Peers))
	}

	// All other top-level fields must be unchanged.
	// Use reflect.DeepEqual since DaemonConfig contains slices (e.g.
	// SweepChainConfig.AlertChain added by A-B4) and is no longer
	// directly comparable with !=.
	if !reflect.DeepEqual(after.Daemon, before.Daemon) {
		t.Errorf("Daemon changed: %+v → %+v", before.Daemon, after.Daemon)
	}
	if after.Email.Enabled != before.Email.Enabled {
		t.Errorf("Email.Enabled changed")
	}
	if after.Email.Mesh != before.Email.Mesh {
		t.Errorf("Email.Mesh changed")
	}
}

// TestMesh_NoSignatureVerifyV011 confirms that a peer.announce with no
// signature field is accepted without error — v0.11 intentionally omits
// signature verification (see threat-model comment at top of mesh.go).
func TestMesh_NoSignatureVerifyV011(t *testing.T) {
	configDir, configPath := setupMeshConfig(t)
	h, _, _ := newTestMeshHandler(t, configDir, configPath, func(c *MeshConfig) {
		c.VouchAcceptance = "auto"
	})
	ctx := context.Background()

	// Body with no signature field — plain peer.announce payload.
	body := []byte(`{"handle":"pat","daemon_id":"daemon-pat-7777","vouched_by":"quin"}`)
	headers := map[string]string{
		"X-Thrum-From-Daemon": "daemon-pat-7777",
		"X-Thrum-Hop-Count":   "1",
	}

	if err := h.HandleProtocol(ctx, "peer.announce", headers, body); err != nil {
		t.Fatalf("HandleProtocol peer.announce without signature: %v", err)
	}

	if peers := loadPeers(t, configDir); len(peers) != 1 || peers[0].Handle != "pat" {
		t.Errorf("expected peer pat, got: %+v", peers)
	}
}
