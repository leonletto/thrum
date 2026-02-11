package sync

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestTriggers_SyncOnWrite(t *testing.T) {
	tmpDir := setupMergeTestRepo(t)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	syncer := NewSyncer(tmpDir, syncDir, false)
	projector := setupTestProjector(t, tmpDir)
	loop := NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), 10*time.Second, false)

	ctx := context.Background()
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Failed to start loop: %v", err)
	}
	defer func() { _ = loop.Stop() }()

	triggers := NewTriggers(loop)

	// Trigger sync on write
	triggers.SyncOnWrite()

	// Poll until sync completes (with timeout)
	deadline := time.After(2 * time.Second)
	for {
		status := loop.GetStatus()
		if !status.LastSyncAt.IsZero() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Timeout waiting for LastSyncAt to be updated after SyncOnWrite")
		default:
			// Poll interval - waiting for async sync operation to complete
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestTriggers_SyncManual(t *testing.T) {
	tmpDir := setupMergeTestRepo(t)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	syncer := NewSyncer(tmpDir, syncDir, false)
	projector := setupTestProjector(t, tmpDir)
	loop := NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), 10*time.Second, false)

	ctx := context.Background()
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Failed to start loop: %v", err)
	}
	defer func() { _ = loop.Stop() }()

	triggers := NewTriggers(loop)

	// Trigger manual sync
	triggers.SyncManual()

	// Poll until sync completes (with timeout)
	deadline := time.After(2 * time.Second)
	for {
		status := loop.GetStatus()
		if !status.LastSyncAt.IsZero() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Timeout waiting for LastSyncAt to be updated after SyncManual")
		default:
			// Poll interval - waiting for async sync operation to complete
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestTriggers_NilLoop(t *testing.T) {
	triggers := NewTriggers(nil)

	// Should not panic when loop is nil
	triggers.SyncOnWrite()
	triggers.SyncManual()
}
