package daemon

import (
	"fmt"
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
	_ = checkpoint.UpdateSyncStatus(m.state.DB(), peerDaemonID, "syncing")
	defer func() {
		status := "idle"
		if err != nil {
			status = "error"
		}
		_ = checkpoint.UpdateSyncStatus(m.state.DB(), peerDaemonID, status)
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

// PeerRegistry returns the peer registry.
func (m *DaemonSyncManager) PeerRegistry() *PeerRegistry {
	return m.peers
}
