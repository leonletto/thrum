package email

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
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
