package daemon

import (
	"context"
	"log"
	"time"

	"github.com/leonletto/thrum/internal/daemon/checkpoint"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// DefaultSyncInterval is the default interval for periodic sync fallback.
const DefaultSyncInterval = 5 * time.Minute

// DefaultRecentSyncThreshold is how recently a peer must have been synced to skip it.
const DefaultRecentSyncThreshold = 2 * time.Minute

// PeriodicSyncScheduler runs background sync from all known peers as a safety net
// for missed push notifications.
type PeriodicSyncScheduler struct {
	syncManager     *DaemonSyncManager
	state           *state.State
	interval        time.Duration
	recentThreshold time.Duration
}

// NewPeriodicSyncScheduler creates a new periodic sync scheduler.
func NewPeriodicSyncScheduler(syncManager *DaemonSyncManager, st *state.State) *PeriodicSyncScheduler {
	return &PeriodicSyncScheduler{
		syncManager:     syncManager,
		state:           st,
		interval:        DefaultSyncInterval,
		recentThreshold: DefaultRecentSyncThreshold,
	}
}

// SetInterval configures the sync interval.
func (s *PeriodicSyncScheduler) SetInterval(d time.Duration) {
	s.interval = d
}

// SetRecentThreshold configures how recently a peer must have been synced to skip it.
func (s *PeriodicSyncScheduler) SetRecentThreshold(d time.Duration) {
	s.recentThreshold = d
}

// Start begins periodic sync. It blocks until the context is canceled.
func (s *PeriodicSyncScheduler) Start(ctx context.Context) {
	log.Printf("periodic_sync: starting with interval=%s, recent_threshold=%s", s.interval, s.recentThreshold)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("periodic_sync: stopping")
			return
		case <-ticker.C:
			s.syncFromPeers()
		}
	}
}

// syncFromPeers attempts to sync from all known peers, skipping recently-synced ones.
func (s *PeriodicSyncScheduler) syncFromPeers() {
	peers := s.syncManager.PeerRegistry().ListPeers()
	if len(peers) == 0 {
		return
	}

	synced := 0
	skipped := 0

	for _, peer := range peers {
		// Skip peers that were recently synced
		if s.wasRecentlySynced(peer.DaemonID) {
			skipped++
			continue
		}

		addr := peer.Addr()
		applied, _, err := s.syncManager.SyncFromPeer(context.Background(), addr, peer.DaemonID)
		if err != nil {
			log.Printf("periodic_sync: sync from %s failed: %v", peer.DaemonID, err)
			continue
		}

		if applied > 0 {
			log.Printf("periodic_sync: synced %d events from %s", applied, peer.DaemonID)
		}
		synced++
	}

	if synced > 0 || skipped > 0 {
		log.Printf("periodic_sync: completed — synced=%d skipped=%d", synced, skipped)
	}
}

// wasRecentlySynced checks if a peer was synced within the recent threshold.
func (s *PeriodicSyncScheduler) wasRecentlySynced(peerDaemonID string) bool {
	cp, err := checkpoint.GetCheckpoint(context.Background(), s.state.DB(), peerDaemonID)
	if err != nil || cp == nil {
		return false
	}

	lastSync := time.Unix(cp.LastSyncTimestamp, 0)
	return time.Since(lastSync) < s.recentThreshold
}
