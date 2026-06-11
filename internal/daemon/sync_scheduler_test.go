package daemon

import (
	"context"
	"testing"
	"time"
)

func TestPeriodicSyncScheduler_Start_StopsOnCancel(t *testing.T) {
	st := createTestStateForSync(t)

	syncManager := NewDaemonSyncManager(st, createTestPeerRegistry(t))

	scheduler := NewPeriodicSyncScheduler(syncManager, st)
	scheduler.SetInterval(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		scheduler.Start(ctx)
		close(done)
	}()

	// Let it run for a couple of ticks
	time.Sleep(150 * time.Millisecond)

	cancel()
	select {
	case <-done:
		// Clean shutdown
	case <-time.After(time.Second):
		t.Fatal("scheduler didn't stop after context cancel")
	}
}

func TestPeriodicSyncScheduler_SkipsRecentlySynced(t *testing.T) {
	st := createTestStateForSync(t)

	syncManager := NewDaemonSyncManager(st, createTestPeerRegistry(t))

	scheduler := NewPeriodicSyncScheduler(syncManager, st)
	scheduler.SetRecentThreshold(1 * time.Hour) // Long threshold

	// Add a peer
	_ = syncManager.PeerRegistry().AddPeer(&PeerInfo{
		DaemonID: "test-peer",
		Name:     "test-host",
		Address:  "test-host:9100",
	})

	// No checkpoint exists, so wasRecentlySynced should return false
	if scheduler.wasRecentlySynced("test-peer") {
		t.Error("expected wasRecentlySynced=false when no checkpoint exists")
	}

	// Set a recent checkpoint
	_, err := st.RawDB().Exec(`INSERT OR REPLACE INTO sync_checkpoints (peer_daemon_id, last_synced_sequence, last_sync_timestamp, sync_status) VALUES (?, ?, ?, ?)`,
		"test-peer", 100, time.Now().Unix(), "idle")
	if err != nil {
		t.Fatalf("insert checkpoint: %v", err)
	}

	// Now it should be considered recently synced
	if !scheduler.wasRecentlySynced("test-peer") {
		t.Error("expected wasRecentlySynced=true when checkpoint is recent")
	}

	// With a very short threshold, it should NOT be considered recent
	scheduler.SetRecentThreshold(0)
	if scheduler.wasRecentlySynced("test-peer") {
		t.Error("expected wasRecentlySynced=false with zero threshold")
	}
}

func TestTailscaleSyncIntervals(t *testing.T) {
	if TailscaleSyncInterval >= DefaultSyncInterval {
		t.Errorf("TailscaleSyncInterval (%s) should be shorter than DefaultSyncInterval (%s)",
			TailscaleSyncInterval, DefaultSyncInterval)
	}
	if TailscaleRecentSyncThreshold >= DefaultRecentSyncThreshold {
		t.Errorf("TailscaleRecentSyncThreshold (%s) should be shorter than DefaultRecentSyncThreshold (%s)",
			TailscaleRecentSyncThreshold, DefaultRecentSyncThreshold)
	}
	// thrum-823x: the recent-sync threshold MUST be >= the sync interval. With
	// threshold < interval (the original 10s < 15s), a peer synced one tick ago
	// (~15s) failed the <10s "recent" check and was redundantly re-pulled on
	// EVERY tick — pointless empty pulls. threshold >= interval makes a peer
	// synced within the last tick skip the next one. Keep this invariant pinned.
	if TailscaleRecentSyncThreshold < TailscaleSyncInterval {
		t.Errorf("TailscaleRecentSyncThreshold (%s) must be >= TailscaleSyncInterval (%s) so a peer synced within the last tick skips the next one (thrum-823x)",
			TailscaleRecentSyncThreshold, TailscaleSyncInterval)
	}
}

// TestPeriodicSyncScheduler_FreshPeerSkippedAcrossTick is the thrum-823x
// behavioral guard: with the Tailscale threshold/interval pair, a peer synced
// one full tick-interval ago must still count as "recently synced" (skipped),
// and a peer synced beyond the threshold must be pulled. Pre-fix (threshold 10s
// < interval 15s) the one-interval-ago peer was NOT skipped — the redundant
// per-tick empty pull this bead is about.
func TestPeriodicSyncScheduler_FreshPeerSkippedAcrossTick(t *testing.T) {
	st := createTestStateForSync(t)
	syncManager := NewDaemonSyncManager(st, createTestPeerRegistry(t))

	scheduler := NewPeriodicSyncScheduler(syncManager, st)
	scheduler.SetInterval(TailscaleSyncInterval)
	scheduler.SetRecentThreshold(TailscaleRecentSyncThreshold)

	_ = syncManager.PeerRegistry().AddPeer(&PeerInfo{
		DaemonID: "ts-peer",
		Name:     "ts-host",
		Address:  "ts-host:9100",
	})

	// Synced exactly one tick-interval ago: must be SKIPPED (threshold covers
	// the interval). This is the assertion that fails pre-fix.
	oneTickAgo := time.Now().Add(-TailscaleSyncInterval).Unix()
	if _, err := st.RawDB().Exec(
		`INSERT OR REPLACE INTO sync_checkpoints (peer_daemon_id, last_synced_sequence, last_sync_timestamp, sync_status) VALUES (?, ?, ?, ?)`,
		"ts-peer", 100, oneTickAgo, "idle"); err != nil {
		t.Fatalf("insert checkpoint: %v", err)
	}
	if !scheduler.wasRecentlySynced("ts-peer") {
		t.Errorf("peer synced one interval (%s) ago must be skipped, but wasRecentlySynced=false (thrum-823x: threshold %s must cover interval %s)",
			TailscaleSyncInterval, TailscaleRecentSyncThreshold, TailscaleSyncInterval)
	}

	// Synced well beyond the threshold: must be PULLED (not recent).
	beyondThreshold := time.Now().Add(-2 * TailscaleRecentSyncThreshold).Unix()
	if _, err := st.RawDB().Exec(
		`INSERT OR REPLACE INTO sync_checkpoints (peer_daemon_id, last_synced_sequence, last_sync_timestamp, sync_status) VALUES (?, ?, ?, ?)`,
		"ts-peer", 100, beyondThreshold, "idle"); err != nil {
		t.Fatalf("update checkpoint: %v", err)
	}
	if scheduler.wasRecentlySynced("ts-peer") {
		t.Errorf("peer synced %s ago (beyond threshold %s) must be pulled, but wasRecentlySynced=true",
			2*TailscaleRecentSyncThreshold, TailscaleRecentSyncThreshold)
	}
}

func TestPeriodicSyncScheduler_SyncFromPeersNoPeers(t *testing.T) {
	st := createTestStateForSync(t)

	syncManager := NewDaemonSyncManager(st, createTestPeerRegistry(t))

	scheduler := NewPeriodicSyncScheduler(syncManager, st)

	// Should not panic with no peers
	scheduler.syncFromPeers()
}
