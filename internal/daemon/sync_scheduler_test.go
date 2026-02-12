package daemon

import (
	"context"
	"testing"
	"time"
)

func TestPeriodicSyncScheduler_Start_StopsOnCancel(t *testing.T) {
	st := createTestStateForSync(t)

	syncManager, err := NewDaemonSyncManager(st, t.TempDir())
	if err != nil {
		t.Fatalf("create sync manager: %v", err)
	}

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

	syncManager, err := NewDaemonSyncManager(st, t.TempDir())
	if err != nil {
		t.Fatalf("create sync manager: %v", err)
	}

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
	_, err = st.DB().Exec(`INSERT OR REPLACE INTO sync_checkpoints (peer_daemon_id, last_synced_sequence, last_sync_timestamp, sync_status) VALUES (?, ?, ?, ?)`,
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

func TestPeriodicSyncScheduler_SyncFromPeersNoPeers(t *testing.T) {
	st := createTestStateForSync(t)

	syncManager, err := NewDaemonSyncManager(st, t.TempDir())
	if err != nil {
		t.Fatalf("create sync manager: %v", err)
	}

	scheduler := NewPeriodicSyncScheduler(syncManager, st)

	// Should not panic with no peers
	scheduler.syncFromPeers()
}
