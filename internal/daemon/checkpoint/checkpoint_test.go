package checkpoint_test

import (
	"testing"

	"github.com/leonletto/thrum/internal/daemon/checkpoint"
	"github.com/leonletto/thrum/internal/schema"
)

func TestGetCheckpoint_NonExistent(t *testing.T) {
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cp, err := checkpoint.GetCheckpoint(db, "d_nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cp != nil {
		t.Error("expected nil checkpoint for non-existent peer")
	}
}

func TestCheckpoint_CreateReadUpdate(t *testing.T) {
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	peerID := "d_peer1"

	// Create checkpoint
	if err := checkpoint.UpdateCheckpoint(db, peerID, 42, 1700000000); err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}

	// Read it back
	cp, err := checkpoint.GetCheckpoint(db, peerID)
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if cp == nil {
		t.Fatal("expected non-nil checkpoint")
	}
	if cp.PeerDaemonID != peerID {
		t.Errorf("expected peer %s, got %s", peerID, cp.PeerDaemonID)
	}
	if cp.LastSyncedSeq != 42 {
		t.Errorf("expected seq 42, got %d", cp.LastSyncedSeq)
	}
	if cp.LastSyncTimestamp != 1700000000 {
		t.Errorf("expected timestamp 1700000000, got %d", cp.LastSyncTimestamp)
	}
	if cp.SyncStatus != "idle" {
		t.Errorf("expected status 'idle', got %s", cp.SyncStatus)
	}

	// Update checkpoint (idempotent)
	if err := checkpoint.UpdateCheckpoint(db, peerID, 100, 1700001000); err != nil {
		t.Fatalf("update checkpoint: %v", err)
	}

	cp, err = checkpoint.GetCheckpoint(db, peerID)
	if err != nil {
		t.Fatalf("get updated checkpoint: %v", err)
	}
	if cp.LastSyncedSeq != 100 {
		t.Errorf("expected seq 100 after update, got %d", cp.LastSyncedSeq)
	}
	if cp.LastSyncTimestamp != 1700001000 {
		t.Errorf("expected timestamp 1700001000, got %d", cp.LastSyncTimestamp)
	}
}

func TestCheckpoint_UpdateSameMultipleTimes(t *testing.T) {
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	peerID := "d_peer_multi"

	// Update same checkpoint multiple times
	for i := int64(1); i <= 10; i++ {
		if err := checkpoint.UpdateCheckpoint(db, peerID, i*10, 1700000000+i); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}

	cp, err := checkpoint.GetCheckpoint(db, peerID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cp.LastSyncedSeq != 100 {
		t.Errorf("expected final seq 100, got %d", cp.LastSyncedSeq)
	}
}

func TestCheckpoint_ListCheckpoints(t *testing.T) {
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Empty list
	cps, err := checkpoint.ListCheckpoints(db)
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(cps) != 0 {
		t.Errorf("expected 0 checkpoints, got %d", len(cps))
	}

	// Add 3 peers
	for _, peerID := range []string{"d_alpha", "d_beta", "d_gamma"} {
		if err := checkpoint.UpdateCheckpoint(db, peerID, 10, 1700000000); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	cps, err = checkpoint.ListCheckpoints(db)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(cps) != 3 {
		t.Errorf("expected 3 checkpoints, got %d", len(cps))
	}

	// Verify ordering (by peer_daemon_id)
	if cps[0].PeerDaemonID != "d_alpha" {
		t.Errorf("expected first peer d_alpha, got %s", cps[0].PeerDaemonID)
	}
}

func TestCheckpoint_ErrorPaths(t *testing.T) {
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Close DB to trigger errors
	db.Close()

	_, err = checkpoint.GetCheckpoint(db, "d_test")
	if err == nil {
		t.Error("expected error from GetCheckpoint on closed db")
	}

	err = checkpoint.UpdateCheckpoint(db, "d_test", 1, 100)
	if err == nil {
		t.Error("expected error from UpdateCheckpoint on closed db")
	}

	err = checkpoint.UpdateSyncStatus(db, "d_test", "syncing")
	if err == nil {
		t.Error("expected error from UpdateSyncStatus on closed db")
	}

	_, err = checkpoint.ListCheckpoints(db)
	if err == nil {
		t.Error("expected error from ListCheckpoints on closed db")
	}
}

func TestCheckpoint_UpdateSyncStatus(t *testing.T) {
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	peerID := "d_statustest"
	if err := checkpoint.UpdateCheckpoint(db, peerID, 1, 1700000000); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Update status to syncing
	if err := checkpoint.UpdateSyncStatus(db, peerID, "syncing"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	cp, err := checkpoint.GetCheckpoint(db, peerID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cp.SyncStatus != "syncing" {
		t.Errorf("expected status 'syncing', got %s", cp.SyncStatus)
	}

	// Update to error
	if err := checkpoint.UpdateSyncStatus(db, peerID, "error"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	cp, err = checkpoint.GetCheckpoint(db, peerID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cp.SyncStatus != "error" {
		t.Errorf("expected status 'error', got %s", cp.SyncStatus)
	}
}
