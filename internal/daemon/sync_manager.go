package daemon

import (
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
func (m *DaemonSyncManager) SyncFromPeer(peerAddr string, peerDaemonID string) (applied, skipped int, err error) {
	// Get checkpoint for this peer
	afterSeq, err := m.applier.GetCheckpoint(peerDaemonID)
	if err != nil {
		return 0, 0, fmt.Errorf("get checkpoint: %w", err)
	}

	// Set sync status
	if statusErr := checkpoint.UpdateSyncStatus(m.state.DB(), peerDaemonID, "syncing"); statusErr != nil {
		log.Printf("sync: failed to update status to 'syncing' for %s: %v", peerDaemonID, statusErr)
	}
	defer func() {
		status := "idle"
		if err != nil {
			status = "error"
		}
		if statusErr := checkpoint.UpdateSyncStatus(m.state.DB(), peerDaemonID, status); statusErr != nil {
			log.Printf("sync: failed to update status to '%s' for %s: %v", status, peerDaemonID, statusErr)
		}
	}()

	totalApplied := 0
	totalSkipped := 0

	err = m.client.PullAllEvents(peerAddr, afterSeq, func(events []eventlog.Event, nextSeq int64) error {
		a, s, applyErr := m.applier.ApplyAndCheckpoint(peerDaemonID, events, nextSeq)
		totalApplied += a
		totalSkipped += s
		return applyErr
	})

	if err != nil {
		return totalApplied, totalSkipped, err
	}

	// Update peer last seen
	_ = m.peers.UpdatePeerLastSeen(peerDaemonID)

	return totalApplied, totalSkipped, nil
}

// PeerStatusInfo is the peer status returned to callers.
type PeerStatusInfo struct {
	DaemonID string
	Hostname string
	Port     int
	LastSeen string
	Status   string
	LastSeq  int64
}

// ListPeers returns the status of all known peers.
func (m *DaemonSyncManager) ListPeers() []PeerStatusInfo {
	peerList := m.peers.ListPeers()
	var statuses []PeerStatusInfo

	for _, p := range peerList {
		ago := time.Since(p.LastSeen).Truncate(time.Second)
		lastSeen := ago.String() + " ago"
		if ago > 24*time.Hour {
			lastSeen = p.LastSeen.Format("2006-01-02 15:04")
		}

		var lastSeq int64
		cp, err := checkpoint.GetCheckpoint(m.state.DB(), p.DaemonID)
		if err == nil && cp != nil {
			lastSeq = cp.LastSyncedSeq
		}

		statuses = append(statuses, PeerStatusInfo{
			DaemonID: p.DaemonID,
			Hostname: p.Hostname,
			Port:     p.Port,
			LastSeen: lastSeen,
			Status:   p.Status,
			LastSeq:  lastSeq,
		})
	}

	return statuses
}

// AddPeer manually adds a peer by hostname and port.
func (m *DaemonSyncManager) AddPeer(hostname string, port int) error {
	addr := fmt.Sprintf("%s:%d", hostname, port)

	// Try to query peer info
	info, err := m.client.QueryPeerInfo(addr)
	if err != nil {
		// Add with hostname-derived ID if we can't reach the peer
		return m.peers.AddPeer(&PeerInfo{
			DaemonID: "d_" + hostname,
			Hostname: hostname,
			Port:     port,
			Status:   "offline",
		})
	}

	return m.peers.AddPeer(&PeerInfo{
		DaemonID:  info.DaemonID,
		Hostname:  hostname,
		FQDN:      info.Hostname,
		Port:      port,
		PublicKey: info.PublicKey,
		Status:    "active",
	})
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
	applied, skipped, err := m.SyncFromPeer(addr, daemonID)
	if err != nil {
		log.Printf("sync.notify: sync from %s failed: %v", daemonID, err)
		return
	}

	log.Printf("sync.notify: synced from %s — applied=%d skipped=%d", daemonID, applied, skipped)
}

// BroadcastNotify sends sync.notify to all known peers.
// This is fire-and-forget — failures are logged but don't block.
func (m *DaemonSyncManager) BroadcastNotify(daemonID string, latestSeq int64, eventCount int) {
	peers := m.peers.ListPeers()
	for _, peer := range peers {
		go func(p *PeerInfo) {
			addr := p.Addr()
			if err := m.client.SendNotify(addr, daemonID, latestSeq, eventCount); err != nil {
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
		ago := time.Since(p.LastSeen).Truncate(time.Second)
		lastSeen := ago.String() + " ago"
		if ago > 24*time.Hour {
			lastSeen = p.LastSeen.Format("2006-01-02 15:04")
		}

		var lastSeq int64
		cp, err := checkpoint.GetCheckpoint(m.state.DB(), p.DaemonID)
		if err == nil && cp != nil {
			lastSeq = cp.LastSyncedSeq
		}

		statuses = append(statuses, PeerStatusInfo{
			DaemonID: p.DaemonID,
			Hostname: p.Hostname,
			Port:     p.Port,
			LastSeen: lastSeen,
			Status:   p.Status,
			LastSeq:  lastSeq,
		})
	}

	return len(peerList), statuses
}

// Client returns the sync client.
func (m *DaemonSyncManager) Client() *SyncClient {
	return m.client
}

// PeerRegistry returns the peer registry.
func (m *DaemonSyncManager) PeerRegistry() *PeerRegistry {
	return m.peers
}
