// THREAT MODEL — D-B1.13 mesh handlers (v0.11)
//
// Trust root: mailbox-access. Per design-spec §14, the v0.11 substrate
// treats "you have IMAP credentials to the shared mailbox" as the
// authoritative sender claim. No cryptographic signature verify; no
// pubkey parse/store/compare. All peer.* verbs accept the
// X-Thrum-From-Daemon header as the routing-identity claim.
//
// Out of scope for v0.11 (reserved for v0.11.x via E18):
//   - Ed25519 keypair at thrum init
//   - daemon_pubkeys table
//   - Signed envelope canonical-form
//   - Replay-nonce population
//
// Forward-binding rule: ONLY email.peers[] is daemon-mutated. Any other
// config key under daemon-driven write is a scope violation and triggers
// code-review rejection.

package email

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// MeshConfig holds policy + paths for the mesh handler.
type MeshConfig struct {
	MyDaemonID              string
	MyDaemonShort           string
	VouchAcceptance         string // "auto_with_notify" | "manual" | "auto" — default auto_with_notify
	AllowTransitiveVouching bool
	HopCountCeiling         int // default 5
	RevocationPropagation   string
	PairPendingTTL          time.Duration // default 24h
	ConfigPath              string        // full path to .thrum/config.json
}

// ConfigValidator runs on every candidate config snapshot before
// persistence. Returns nil to accept, error to reject.
type ConfigValidator func(*config.ThrumConfig) error

// PeerProtocolPayload is the JSON-decoded body of a peer.* envelope.
// All five verbs use this shape; verb-specific fields are optional.
type PeerProtocolPayload struct {
	Handle       string `json:"handle"`
	DaemonID     string `json:"daemon_id"`
	ContactEmail string `json:"contact_email,omitempty"`
	VouchedBy    string `json:"vouched_by,omitempty"`
	NewDaemonID  string `json:"new_daemon_id,omitempty"` // for peer.rebind
}

// MeshHandlerImpl is the production MeshHandler satisfying the
// inbound.MeshHandler interface from D-B1.11.
type MeshHandlerImpl struct {
	cfg      MeshConfig
	wal      *state.PendingMeshUpdatesLog
	notifier CoordinatorNotifier
	validate ConfigValidator // nil = no validation

	// configMu gates ALL config.json reads/writes to prevent torn reads.
	configMu sync.Mutex

	pendingMu    sync.Mutex
	pendingPairs map[string]pendingPair // keyed by handle

	// pendingRebinds holds rebind confirmations awaiting operator confirm.
	pendingRebinds map[string]pendingRebind // keyed by handle

	nowFn func() time.Time // injectable; default time.Now
}

type pendingPair struct {
	handle       string
	daemonID     string
	contactEmail string
	addedAt      time.Time
}

type pendingRebind struct {
	handle      string
	oldDaemonID string
	newDaemonID string
	addedAt     time.Time
}

// NewMeshHandler returns a MeshHandlerImpl ready for use.
func NewMeshHandler(cfg MeshConfig, wal *state.PendingMeshUpdatesLog, notifier CoordinatorNotifier, validator ConfigValidator) *MeshHandlerImpl {
	if cfg.HopCountCeiling == 0 {
		cfg.HopCountCeiling = 5
	}
	if cfg.PairPendingTTL == 0 {
		cfg.PairPendingTTL = 24 * time.Hour
	}
	if cfg.VouchAcceptance == "" {
		cfg.VouchAcceptance = "auto_with_notify"
	}
	return &MeshHandlerImpl{
		cfg:            cfg,
		wal:            wal,
		notifier:       notifier,
		validate:       validator,
		pendingPairs:   make(map[string]pendingPair),
		pendingRebinds: make(map[string]pendingRebind),
		nowFn:          time.Now,
	}
}

// HandleProtocol routes a verb to the appropriate per-verb handler.
// Satisfies inbound.MeshHandler.
func (m *MeshHandlerImpl) HandleProtocol(ctx context.Context, verb string, headers map[string]string, body []byte) error {
	var env PeerProtocolPayload
	if len(body) > 0 {
		if err := json.Unmarshal(body, &env); err != nil {
			return fmt.Errorf("mesh: parse payload for %s: %w", verb, err)
		}
	}

	hopCount := 0
	if h := headers["X-Thrum-Hop-Count"]; h != "" {
		_, _ = fmt.Sscanf(h, "%d", &hopCount)
	}

	switch verb {
	case "peer.welcome":
		return m.HandlePeerWelcome(ctx, env)
	case "peer.announce":
		return m.HandlePeerAnnounce(ctx, env, hopCount)
	case "peer.rebind":
		return m.HandlePeerRebind(ctx, env)
	case "peer.revoke":
		return m.HandlePeerRevoke(ctx, env)
	default:
		return fmt.Errorf("mesh: unknown verb %q", verb)
	}
}

// HandleStrangerPair handles the case where an unknown peer sends peer.pair.
// Satisfies inbound.MeshHandler.
func (m *MeshHandlerImpl) HandleStrangerPair(ctx context.Context, headers map[string]string, body []byte) (ProcessAction, error) {
	var env PeerProtocolPayload
	if len(body) > 0 {
		if err := json.Unmarshal(body, &env); err != nil {
			return ProcessAction{}, fmt.Errorf("mesh: parse peer.pair payload: %w", err)
		}
	}
	_, err := m.HandlePeerPair(ctx, env)
	if err != nil {
		return ProcessAction{}, err
	}
	return ProcessAction{Kind: ActionPending, Reason: "peer.pair pending operator confirm"}, nil
}

// HandlePeerPair records a pending pair and nudges the operator.
// Returns Pending action.
func (m *MeshHandlerImpl) HandlePeerPair(ctx context.Context, env PeerProtocolPayload) (ProcessAction, error) {
	srcPeerID := env.DaemonID
	shortID := shortID(srcPeerID)

	m.pendingMu.Lock()
	m.pendingPairs[env.Handle] = pendingPair{
		handle:       env.Handle,
		daemonID:     env.DaemonID,
		contactEmail: env.ContactEmail,
		addedAt:      m.nowFn(),
	}
	m.pendingMu.Unlock()

	log.Printf("[email/mesh] peer.pair from %s: pending operator confirm for %s (id %s, contact %s)",
		srcPeerID, env.Handle, shortID, env.ContactEmail)

	if m.notifier != nil {
		msg := fmt.Sprintf("Pair with %s (id %s) at %s? Reply with `thrum email pair --to %s` to confirm, or ignore to decline.",
			env.Handle, shortID, env.ContactEmail, env.Handle)
		_ = m.notifier.Notify(ctx, msg)
	}

	return ProcessAction{Kind: ActionPending, Reason: "peer.pair pending operator confirm"}, nil
}

// ConfirmStrangerPair adds a pending pair to email.peers[] (vouched_by=self).
func (m *MeshHandlerImpl) ConfirmStrangerPair(ctx context.Context, handle string) error {
	m.pendingMu.Lock()
	pp, ok := m.pendingPairs[handle]
	if ok {
		delete(m.pendingPairs, handle)
	}
	m.pendingMu.Unlock()

	if !ok {
		return fmt.Errorf("mesh: no pending pair for handle %q", handle)
	}

	updateID := fmt.Sprintf("peer.pair-%d", m.nowFn().UnixNano())
	return m.mutateConfigUnderMutex(updateID, "peer.pair", func(cfg *config.ThrumConfig) error {
		cfg.Email.Peers = append(cfg.Email.Peers, config.EmailPeer{
			Handle:       pp.handle,
			DaemonID:     pp.daemonID,
			ContactEmail: pp.contactEmail,
			VouchedBy:    "self",
			AddedAt:      m.nowFn().UTC().Format(time.RFC3339),
			Trust:        "full",
		})
		log.Printf("[email/mesh] peer.pair from %s: added peer %s (id %s, contact %s) vouched_by=self",
			pp.daemonID, pp.handle, shortID(pp.daemonID), pp.contactEmail)
		// Gossip stub — bridge.go (D-B1.14) wires actual SMTP outbound.
		log.Printf("[email/mesh] would gossip peer.welcome to %s", pp.handle)
		return nil
	})
}

// DenyStrangerPair drops a pending pair and increments the deny counter
// (logged for audit; no in-memory counter exposed in this package).
func (m *MeshHandlerImpl) DenyStrangerPair(_ context.Context, handle string) error {
	m.pendingMu.Lock()
	_, ok := m.pendingPairs[handle]
	if ok {
		delete(m.pendingPairs, handle)
	}
	m.pendingMu.Unlock()

	if !ok {
		return fmt.Errorf("mesh: no pending pair for handle %q", handle)
	}
	log.Printf("[email/mesh] peer.pair denied by operator for handle %s", handle)
	return nil
}

// SweepStalePendingPairs removes pending pairs that exceeded PairPendingTTL.
// Returns the count of swept pairs. Called from bridge.go ticker (D-B1.14).
func (m *MeshHandlerImpl) SweepStalePendingPairs(_ context.Context) int {
	m.pendingMu.Lock()
	defer m.pendingMu.Unlock()

	now := m.nowFn()
	var swept int
	for handle, pp := range m.pendingPairs {
		if now.Sub(pp.addedAt) > m.cfg.PairPendingTTL {
			delete(m.pendingPairs, handle)
			log.Printf("[email/mesh] peer.pair TTL expired for handle %s (id %s)", pp.handle, shortID(pp.daemonID))
			swept++
		}
	}
	return swept
}

// HandlePeerWelcome updates a peer's trust to full. Idempotent.
func (m *MeshHandlerImpl) HandlePeerWelcome(ctx context.Context, env PeerProtocolPayload) error {
	srcPeerID := env.DaemonID

	updateID := fmt.Sprintf("peer.welcome-%d", m.nowFn().UnixNano())
	return m.mutateConfigUnderMutex(updateID, "peer.welcome", func(cfg *config.ThrumConfig) error {
		for i := range cfg.Email.Peers {
			if cfg.Email.Peers[i].Handle == env.Handle {
				if cfg.Email.Peers[i].Trust == "full" {
					// Idempotent: already full — no-op.
					return nil
				}
				cfg.Email.Peers[i].Trust = "full"
				log.Printf("[email/mesh] peer.welcome from %s: peer %s trust=full", srcPeerID, env.Handle)
				return nil
			}
		}
		// Peer not found — log and skip; no mutation.
		log.Printf("[email/mesh] peer.welcome from %s: unknown peer %s (no config entry)", srcPeerID, env.Handle)
		return nil
	})
}

// HandlePeerAnnounce gossips a new peer into the local config, subject to
// vouch_acceptance policy, hop ceiling, and transitive-vouching flag.
func (m *MeshHandlerImpl) HandlePeerAnnounce(ctx context.Context, env PeerProtocolPayload, hopCount int) error {
	srcPeerID := env.DaemonID

	// Hop ceiling guard.
	if hopCount > m.cfg.HopCountCeiling {
		log.Printf("[email/mesh] peer.announce from %s: dropped hop_count=%d ceiling=%d", srcPeerID, hopCount, m.cfg.HopCountCeiling)
		return nil
	}

	// Transitive-vouching guard: hop > 1 means a relay (not a direct intro).
	if !m.cfg.AllowTransitiveVouching && hopCount > 1 {
		log.Printf("[email/mesh] peer.announce from %s: dropped transitive_vouching=false hop=%d", srcPeerID, hopCount)
		return nil
	}

	// Resolve effective policy from config field or MeshConfig override.
	policy := m.cfg.VouchAcceptance

	switch policy {
	case "manual":
		// Reuse the pending-pair flow to require explicit operator confirmation.
		_, err := m.HandlePeerPair(ctx, env)
		return err

	case "auto":
		return m.addAnnouncedPeer(ctx, env, srcPeerID, false)

	default: // "auto_with_notify" (and unrecognized values — safe default)
		return m.addAnnouncedPeer(ctx, env, srcPeerID, true)
	}
}

// HandlePeerRebind records a pending rebind (new daemon-id under same handle)
// and nudges the operator.
func (m *MeshHandlerImpl) HandlePeerRebind(ctx context.Context, env PeerProtocolPayload) error {
	srcPeerID := env.DaemonID
	newID := env.NewDaemonID
	if newID == "" {
		newID = env.DaemonID
	}

	// Find old daemon-id from current config.
	m.configMu.Lock()
	cfg, err := config.LoadThrumConfig(filepath.Dir(m.cfg.ConfigPath))
	m.configMu.Unlock()
	if err != nil {
		return fmt.Errorf("mesh: load config for rebind: %w", err)
	}

	oldDaemonID := ""
	for _, p := range cfg.Email.Peers {
		if p.Handle == env.Handle {
			oldDaemonID = p.DaemonID
			break
		}
	}

	m.pendingMu.Lock()
	m.pendingRebinds[env.Handle] = pendingRebind{
		handle:      env.Handle,
		oldDaemonID: oldDaemonID,
		newDaemonID: newID,
		addedAt:     m.nowFn(),
	}
	m.pendingMu.Unlock()

	log.Printf("[email/mesh] peer.rebind from %s: pending operator confirm for handle %s new id %s",
		srcPeerID, env.Handle, shortID(newID))

	if m.notifier != nil {
		msg := fmt.Sprintf("Peer %s's daemon-id changed (was %s, now %s). Confirm with `thrum email rebind --to %s`.",
			env.Handle, shortID(oldDaemonID), shortID(newID), env.Handle)
		_ = m.notifier.Notify(ctx, msg)
	}
	return nil
}

// ConfirmRebind applies the new daemon-id for handle.
func (m *MeshHandlerImpl) ConfirmRebind(ctx context.Context, handle, newDaemonID string) error {
	m.pendingMu.Lock()
	_, ok := m.pendingRebinds[handle]
	if ok {
		delete(m.pendingRebinds, handle)
	}
	m.pendingMu.Unlock()

	if !ok {
		return fmt.Errorf("mesh: no pending rebind for handle %q", handle)
	}

	updateID := fmt.Sprintf("peer.rebind-%d", m.nowFn().UnixNano())
	return m.mutateConfigUnderMutex(updateID, "peer.rebind", func(cfg *config.ThrumConfig) error {
		for i := range cfg.Email.Peers {
			if cfg.Email.Peers[i].Handle == handle {
				cfg.Email.Peers[i].DaemonID = newDaemonID
				return nil
			}
		}
		return fmt.Errorf("mesh: peer %q not found for rebind", handle)
	})
}

// HandlePeerRevoke removes a peer from email.peers[].
func (m *MeshHandlerImpl) HandlePeerRevoke(ctx context.Context, env PeerProtocolPayload) error {
	srcPeerID := env.DaemonID

	updateID := fmt.Sprintf("peer.revoke-%d", m.nowFn().UnixNano())
	return m.mutateConfigUnderMutex(updateID, "peer.revoke", func(cfg *config.ThrumConfig) error {
		newPeers := cfg.Email.Peers[:0]
		found := false
		for _, p := range cfg.Email.Peers {
			if p.Handle == env.Handle {
				found = true
				continue
			}
			newPeers = append(newPeers, p)
		}
		if !found {
			log.Printf("[email/mesh] peer.revoke from %s: unknown peer %s (no-op)", srcPeerID, env.Handle)
			return nil
		}
		cfg.Email.Peers = newPeers
		log.Printf("[email/mesh] peer.revoke from %s: removed peer %s", srcPeerID, env.Handle)

		// Gossip stub: bridge.go (D-B1.14) wires actual propagation.
		if m.cfg.RevocationPropagation == "gossip" {
			log.Printf("[email/mesh] would gossip peer.revoke for %s to remaining peers", env.Handle)
		}
		return nil
	})
}

// --- private helpers ---

// mutateConfigUnderMutex is the three-step WAL + atomic-write protocol used
// by every verb that persists a config change. Callers must NOT hold configMu.
func (m *MeshHandlerImpl) mutateConfigUnderMutex(updateID, verb string, mutate func(*config.ThrumConfig) error) error {
	m.configMu.Lock()
	defer m.configMu.Unlock()

	// 1. WAL intent — must precede any config write.
	if err := m.wal.AppendIntent(updateID, verb, nil); err != nil {
		return fmt.Errorf("wal intent: %w", err)
	}

	// 2. Load current config.
	cfg, err := config.LoadThrumConfig(filepath.Dir(m.cfg.ConfigPath))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// 3. Apply in-memory mutation.
	if err := mutate(cfg); err != nil {
		return fmt.Errorf("mutate: %w", err)
	}

	// 4. Validator (optional).
	if m.validate != nil {
		if err := m.validate(cfg); err != nil {
			return fmt.Errorf("validator rejected: %w", err)
		}
	}

	// 5. Persist atomically via SaveThrumConfig (temp-file + rename internally).
	if err := config.SaveThrumConfig(filepath.Dir(m.cfg.ConfigPath), cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	// 6. WAL committed marker — config write succeeded. A failure here is safe:
	// the next boot replays Pending() and re-emits the committed marker
	// idempotently (the config mutation is already on disk).
	if err := m.wal.AppendCommitted(updateID); err != nil {
		log.Printf("[email/mesh] wal committed write failed (replay will recover): %v", err)
	}

	return nil
}

// addAnnouncedPeer adds a gossip-announced peer to config. notify=true
// emits an auto-accept notification to the operator.
func (m *MeshHandlerImpl) addAnnouncedPeer(ctx context.Context, env PeerProtocolPayload, srcPeerID string, notify bool) error {
	updateID := fmt.Sprintf("peer.announce-%d", m.nowFn().UnixNano())
	return m.mutateConfigUnderMutex(updateID, "peer.announce", func(cfg *config.ThrumConfig) error {
		cfg.Email.Peers = append(cfg.Email.Peers, config.EmailPeer{
			Handle:       env.Handle,
			DaemonID:     env.DaemonID,
			ContactEmail: env.ContactEmail,
			VouchedBy:    env.VouchedBy,
			AddedAt:      m.nowFn().UTC().Format(time.RFC3339),
			Trust:        "limited",
		})
		log.Printf("[email/mesh] peer.announce from %s: added %s (id %s, vouched_by %s)",
			srcPeerID, env.Handle, shortID(env.DaemonID), env.VouchedBy)
		if notify && m.notifier != nil {
			msg := fmt.Sprintf("Peer %s (id %s) auto-accepted via gossip from %s.",
				env.Handle, shortID(env.DaemonID), srcPeerID)
			_ = m.notifier.Notify(ctx, msg)
		}
		return nil
	})
}

// shortID returns the first 8 characters of a daemon-id for log lines.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
