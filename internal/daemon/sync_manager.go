package daemon

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/daemon/checkpoint"
	"github.com/leonletto/thrum/internal/daemon/eventlog"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// DaemonSyncManager coordinates sync operations between peers.
type DaemonSyncManager struct {
	state   *state.State
	peers   *PeerRegistry
	client  *SyncClient
	applier *SyncApplier

	// thrum-oc74: per-process notify coalescer bounding the sync.notify
	// fan-out under event bursts (see notify_coalescer.go). Lazily built on
	// the first BroadcastNotify, capturing the local daemonID (constant per
	// process — every caller passes this daemon's own id).
	notifyOnce sync.Once
	notify     *notifyCoalescer

	// thrum-w78a: per-peer single-flight gate over pulls (see pull_gate.go).
	// Covers BOTH pull entry points — the sync.notify handler pool and the
	// periodic scheduler — because both bottom out in SyncFromPeer.
	pulls *pullGate

	// thrum-aop6: per-peer dial backoff + quarantine (see dial_gate.go). Guards
	// the two storm dial paths — fanOutNotify (notify send) and SyncFromPeer
	// (pull) — so a dead/flapping peer is backed off and quarantined instead of
	// hammered into a connection-reset/EOF storm. Sits IN FRONT of pulls (claim
	// before pulls.Do).
	dials *dialGate
}

// NewDaemonSyncManager creates a new sync manager with a pre-created PeerRegistry.
func NewDaemonSyncManager(st *state.State, peers *PeerRegistry) *DaemonSyncManager {
	return &DaemonSyncManager{
		state:   st,
		peers:   peers,
		client:  NewSyncClient(),
		applier: NewSyncApplier(st),
		pulls:   newPullGate(),
		dials:   newDialGate(),
	}
}

// SyncFromPeer pulls events from a specific peer and applies them.
//
// thrum-w78a: gated per-peer single-flight. When a pull for this peer is
// already running, the call is absorbed into that flight's trailing re-pull
// and returns (0, 0, nil) immediately — N concurrent notify-triggered
// requests collapse to one in-flight pull plus exactly one trailing re-pull.
// The absorbed return is indistinguishable from "nothing new" to both
// callers (the notify handler and the scheduler just log counts).
//
// Note: an absorbed call's ctx is dropped — the trailing re-pull runs under
// the original holder's ctx, not the absorbed caller's. Intentional: every
// caller passes context.Background() today (no per-call deadline to honor),
// and the holder's flight is the one doing the real work.
func (m *DaemonSyncManager) SyncFromPeer(ctx context.Context, peerAddr string, peerDaemonID string) (applied, skipped int, err error) {
	// thrum-aop6: skip dialing a backed-off / quarantined peer. claim sits in
	// FRONT of the pull gate so an unreachable peer never even takes a flight
	// slot. The skip is indistinguishable from "nothing new" to callers.
	if !m.dials.claim(peerDaemonID) {
		return 0, 0, nil
	}
	ran := m.pulls.Do(peerDaemonID, func() {
		applied, skipped, err = m.syncFromPeerLocked(ctx, peerAddr, peerDaemonID)
	})
	if !ran {
		// Absorbed into a concurrent flight's trailing re-pull — that holder
		// records the dial outcome; we must not double-count. (Only healthy
		// peers reach here: a peer with failure state is single-admitted by
		// claim's reservation, so its pull always runs.)
		log.Printf("sync: pull for %s already in flight — absorbed into trailing re-pull", peerDaemonID)
		return 0, 0, nil
	}
	// thrum-aop6: record reachability. Only errDialFailed-tagged (connect)
	// errors count toward backoff/quarantine; a reachable peer with an apply
	// error resets to healthy.
	recordDialOutcome(m.dials, peerDaemonID, err)
	return applied, skipped, err
}

// syncFromPeerLocked is the pre-w78a SyncFromPeer body; called only under the
// pull gate's per-peer flight.
func (m *DaemonSyncManager) syncFromPeerLocked(ctx context.Context, peerAddr string, peerDaemonID string) (applied, skipped int, err error) {
	// Look up peer token for authentication
	token := m.getPeerToken(peerDaemonID)

	// Get checkpoint for this peer
	afterSeq, err := m.applier.GetCheckpoint(peerDaemonID)
	if err != nil {
		return 0, 0, fmt.Errorf("get checkpoint: %w", err)
	}

	// Set sync status
	if statusErr := checkpoint.UpdateSyncStatus(ctx, m.state.DB(), peerDaemonID, "syncing"); statusErr != nil {
		log.Printf("sync: failed to update status to 'syncing' for %s: %v", peerDaemonID, statusErr)
	}
	defer func() {
		status := "idle"
		if err != nil {
			status = "error"
		}
		if statusErr := checkpoint.UpdateSyncStatus(context.Background(), m.state.DB(), peerDaemonID, status); statusErr != nil {
			log.Printf("sync: failed to update status to '%s' for %s: %v", status, peerDaemonID, statusErr)
		}
	}()

	totalApplied := 0
	totalSkipped := 0

	err = m.client.PullAllEvents(peerAddr, afterSeq, token, func(events []eventlog.Event, nextSeq int64, filtered bool) error {
		a, s, applyErr := m.applier.ApplyAndCheckpoint(ctx, peerDaemonID, events, nextSeq, filtered)
		totalApplied += a
		totalSkipped += s
		return applyErr
	})

	if err != nil {
		return totalApplied, totalSkipped, err
	}

	// Update peer last sync time
	_ = m.peers.UpdateLastSync(peerDaemonID)

	return totalApplied, totalSkipped, nil
}

// getPeerToken returns the stored auth token for a peer, or empty string if not found.
func (m *DaemonSyncManager) getPeerToken(daemonID string) string {
	peer := m.peers.GetPeer(daemonID)
	if peer == nil {
		return ""
	}
	return peer.Token
}

// PeerStatusInfo is the peer status returned to callers.
type PeerStatusInfo struct {
	DaemonID string
	Name     string
	Address  string
	LastSync string
	LastSeq  int64
	// ReconcileStatus surfaces the xir.29 auto-reconcile marker so
	// `thrum peer list` can render a drift warning for peers where
	// auto-reconcile gave up and the user needs to run
	// `thrum peer join --type repair <name>`.
	ReconcileStatus string
}

// ListPeers returns the status of all known peers.
func (m *DaemonSyncManager) ListPeers() []PeerStatusInfo {
	peerList := m.peers.ListPeers()
	var statuses []PeerStatusInfo

	for _, p := range peerList {
		ago := time.Since(p.LastSync).Truncate(time.Second)
		lastSync := ago.String() + " ago"
		if ago > 24*time.Hour {
			lastSync = p.LastSync.Format("2006-01-02 15:04")
		}

		var lastSeq int64
		cp, err := checkpoint.GetCheckpoint(context.Background(), m.state.DB(), p.DaemonID)
		if err == nil && cp != nil {
			lastSeq = cp.LastSyncedSeq
		}

		statuses = append(statuses, PeerStatusInfo{
			DaemonID:        p.DaemonID,
			Name:            p.Name,
			Address:         p.Address,
			LastSync:        lastSync,
			LastSeq:         lastSeq,
			ReconcileStatus: p.ReconcileStatus,
		})
	}

	return statuses
}

// AddPeer manually adds a peer by hostname and port.
func (m *DaemonSyncManager) AddPeer(hostname string, port int) error {
	addr := fmt.Sprintf("%s:%d", hostname, port)

	// Try to query peer info (no token for initial discovery)
	info, err := m.client.QueryPeerInfo(addr, "")
	if err != nil {
		// Add with hostname-derived ID if we can't reach the peer
		return m.peers.AddPeer(&PeerInfo{
			DaemonID: "d_" + hostname,
			Name:     hostname,
			Address:  addr,
		})
	}

	return m.peers.AddPeer(&PeerInfo{
		DaemonID: info.DaemonID,
		Name:     info.Name,
		Address:  addr,
	})
}

// JoinPeer sends a pairing request to a remote peer and stores the result.
// Local carries the full identity metadata of this daemon sent to the remote peer.
// This is used by the CLI "peer join" command.
func (m *DaemonSyncManager) JoinPeer(peerAddr, code string, local PairMetadata) (*PeerInfo, error) {
	result, err := m.client.RequestPairing(peerAddr, code, local)
	if err != nil {
		return nil, err
	}

	if result.Status != "paired" {
		return nil, fmt.Errorf("pairing failed: status=%s", result.Status)
	}

	peer := &PeerInfo{
		DaemonID:           result.DaemonID,
		Name:               result.Name,
		Address:            peerAddr,
		Token:              result.Token,
		RemoteRepoName:     result.RepoName,
		RemoteHostname:     result.Hostname,
		RemoteRepoPath:     result.RepoPath,
		RemoteGitOriginURL: result.GitOriginURL,
	}
	if err := m.peers.AddPeer(peer); err != nil {
		return nil, fmt.Errorf("store peer: %w", err)
	}

	return peer, nil
}

// SyncFromPeerByID resolves a daemon ID to its address and triggers a pull sync.
// This is used by the sync.notify handler to trigger syncs from notifications.
func (m *DaemonSyncManager) SyncFromPeerByID(daemonID string) {
	peer := m.peers.GetPeer(daemonID)
	if peer == nil {
		log.Printf("sync.notify: unknown peer %s, ignoring", daemonID)
		return
	}

	addr := peer.Addr()
	applied, skipped, err := m.SyncFromPeer(context.Background(), addr, daemonID)
	if err != nil {
		log.Printf("sync.notify: sync from %s failed: %v", daemonID, err)
		return
	}

	log.Printf("sync.notify: synced from %s — applied=%d skipped=%d", daemonID, applied, skipped)
}

// BroadcastNotify sends sync.notify to all known peers.
// Each peer's stored token is included for authentication.
// This is fire-and-forget — failures are logged but don't block.
func (m *DaemonSyncManager) BroadcastNotify(daemonID string, latestSeq int64, eventCount int) {
	// thrum-oc74: route through the coalescer — the onEventWrite hook calls
	// this once per applied event, which under a burst fanned out
	// O(events × peers) sync.notify RPCs (the common amplifier in the
	// 2026-06-10 storms). The coalescer leading-edge-fires when idle (quiet
	// single event = zero added latency) and absorbs the burst into one
	// trailing flush per window. Safe to coalesce payloads: the receiver uses
	// latest_seq/event_count for logging only and triggers its pull keyed on
	// daemonID alone (rpc/sync_notify.go Handle).
	m.notifyOnce.Do(func() {
		m.notify = newNotifyCoalescer(notifyCoalesceWindow, func(seq int64, n int) {
			m.fanOutNotify(daemonID, seq, n)
		})
	})
	m.notify.Offer(latestSeq, eventCount)
}

// fanOutNotify is the pre-coalescer BroadcastNotify body: one fire-and-forget
// sync.notify per peer. Called only by the coalescer's flush.
func (m *DaemonSyncManager) fanOutNotify(daemonID string, latestSeq int64, eventCount int) {
	peers := m.peers.ListPeers()
	for _, peer := range peers {
		// thrum-aop6: don't dial a backed-off / quarantined peer. This is the
		// primary storm path — fire-and-forget notify fan-out with no backoff
		// was what hammered leondev:9177 into thousands of resets.
		if !m.dials.claim(peer.DaemonID) {
			continue
		}
		go func(p *PeerInfo) {
			addr := p.Addr()
			err := m.client.SendNotify(addr, daemonID, latestSeq, eventCount, p.Token)
			// Record reachability: errDialFailed (connect) -> backoff/quarantine;
			// a reached peer (even on a post-connect RPC error) resets to healthy.
			recordDialOutcome(m.dials, p.DaemonID, err)
			if err != nil {
				log.Printf("sync.notify: failed to notify %s at %s: %v", p.DaemonID, addr, err)
			}
		}(peer)
	}
}

// TailscaleSyncStatus returns current sync status info for the health endpoint.
func (m *DaemonSyncManager) TailscaleSyncStatus(hostname string) (int, []PeerStatusInfo) {
	peerList := m.peers.ListPeers()
	var statuses []PeerStatusInfo

	for _, p := range peerList {
		ago := time.Since(p.LastSync).Truncate(time.Second)
		lastSync := ago.String() + " ago"
		if ago > 24*time.Hour {
			lastSync = p.LastSync.Format("2006-01-02 15:04")
		}

		var lastSeq int64
		cp, err := checkpoint.GetCheckpoint(context.Background(), m.state.DB(), p.DaemonID)
		if err == nil && cp != nil {
			lastSeq = cp.LastSyncedSeq
		}

		statuses = append(statuses, PeerStatusInfo{
			DaemonID:        p.DaemonID,
			Name:            p.Name,
			Address:         p.Address,
			LastSync:        lastSync,
			LastSeq:         lastSeq,
			ReconcileStatus: p.ReconcileStatus,
		})
	}

	return len(peerList), statuses
}

// DetailedPeerInfo is the detailed status of a single peer.
type DetailedPeerInfo struct {
	DaemonID string
	Name     string
	Address  string
	HasToken bool
	PairedAt string
	LastSync string
	LastSeq  int64
}

// DetailedPeerStatus returns detailed status for all known peers.
func (m *DaemonSyncManager) DetailedPeerStatus() []DetailedPeerInfo {
	peerList := m.peers.ListPeers()
	var statuses []DetailedPeerInfo

	for _, p := range peerList {
		ago := time.Since(p.LastSync).Truncate(time.Second)
		lastSync := ago.String() + " ago"
		if ago > 24*time.Hour {
			lastSync = p.LastSync.Format("2006-01-02 15:04")
		}

		var lastSeq int64
		cp, err := checkpoint.GetCheckpoint(context.Background(), m.state.DB(), p.DaemonID)
		if err == nil && cp != nil {
			lastSeq = cp.LastSyncedSeq
		}

		statuses = append(statuses, DetailedPeerInfo{
			DaemonID: p.DaemonID,
			Name:     p.Name,
			Address:  p.Address,
			HasToken: p.Token != "",
			PairedAt: p.PairedAt.Format(time.RFC3339),
			LastSync: lastSync,
			LastSeq:  lastSeq,
		})
	}

	return statuses
}

// Client returns the sync client.
func (m *DaemonSyncManager) Client() *SyncClient {
	return m.client
}

// PeerRegistry returns the peer registry.
func (m *DaemonSyncManager) PeerRegistry() *PeerRegistry {
	return m.peers
}
