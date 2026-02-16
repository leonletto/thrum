package daemon

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
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
}

// NewDaemonSyncManager creates a new sync manager.
func NewDaemonSyncManager(st *state.State, varDir string) (*DaemonSyncManager, error) {
	peersFile := filepath.Join(varDir, "peers.json")
	peers, err := NewPeerRegistry(peersFile)
	if err != nil {
		return nil, fmt.Errorf("create peer registry: %w", err)
	}

	return &DaemonSyncManager{
		state:   st,
		peers:   peers,
		client:  NewSyncClient(),
		applier: NewSyncApplier(st),
	}, nil
}

// SyncFromPeer pulls events from a specific peer and applies them.
func (m *DaemonSyncManager) SyncFromPeer(ctx context.Context, peerAddr string, peerDaemonID string) (applied, skipped int, err error) {
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

	err = m.client.PullAllEvents(peerAddr, afterSeq, token, func(events []eventlog.Event, nextSeq int64) error {
		a, s, applyErr := m.applier.ApplyAndCheckpoint(ctx, peerDaemonID, events, nextSeq)
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
			DaemonID: p.DaemonID,
			Name:     p.Name,
			Address:  p.Address,
			LastSync: lastSync,
			LastSeq:  lastSeq,
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
// This is used by the CLI "peer join" command.
func (m *DaemonSyncManager) JoinPeer(peerAddr, code, localDaemonID, localName, localAddress string) (*PeerInfo, error) {
	result, err := m.client.RequestPairing(peerAddr, code, localDaemonID, localName, localAddress)
	if err != nil {
		return nil, err
	}

	if result.Status != "paired" {
		return nil, fmt.Errorf("pairing failed: status=%s", result.Status)
	}

	peer := &PeerInfo{
		DaemonID: result.DaemonID,
		Name:     result.Name,
		Address:  peerAddr,
		Token:    result.Token,
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
	peers := m.peers.ListPeers()
	for _, peer := range peers {
		go func(p *PeerInfo) {
			addr := p.Addr()
			if err := m.client.SendNotify(addr, daemonID, latestSeq, eventCount, p.Token); err != nil {
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
			DaemonID: p.DaemonID,
			Name:     p.Name,
			Address:  p.Address,
			LastSync: lastSync,
			LastSeq:  lastSeq,
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
