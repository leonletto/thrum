package sync

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// stubWalker is a test double for WalkerInvoker.
type stubWalker struct {
	called atomic.Int32
	err    error
}

func (s *stubWalker) WalkAndWrite(ctx context.Context) error {
	s.called.Add(1)
	return s.err
}

func TestTriggers_SyncOnWrite_WalkerSuccess(t *testing.T) {
	tmpDir := setupMergeTestRepo(t)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	syncer := NewSyncer(tmpDir, syncDir, false)
	projector := setupTestProjector(t, tmpDir)
	loop := NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), false)

	ctx := context.Background()
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Failed to start loop: %v", err)
	}
	defer func() { _ = loop.Stop() }()

	walker := &stubWalker{}
	triggers := NewTriggers(loop)
	triggers.SetWalker(walker)

	// Wait for initial sync to complete
	deadline := time.After(2 * time.Second)
	for {
		status := loop.GetStatus()
		if !status.LastSyncAt.IsZero() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Timeout waiting for initial sync")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	beforeTrigger := loop.GetStatus().LastSyncAt

	// SyncOnWrite: walker succeeds → TriggerSync is called
	triggers.SyncOnWrite(ctx)

	deadline = time.After(2 * time.Second)
	for {
		status := loop.GetStatus()
		if status.LastSyncAt.After(beforeTrigger) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Timeout waiting for LastSyncAt to advance after SyncOnWrite")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	if walker.called.Load() < 1 {
		t.Errorf("expected walker to be called at least once, got %d", walker.called.Load())
	}
}

func TestTriggers_SyncOnWrite_WalkerFailure(t *testing.T) {
	tmpDir := setupMergeTestRepo(t)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	syncer := NewSyncer(tmpDir, syncDir, false)
	projector := setupTestProjector(t, tmpDir)
	loop := NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), false)

	ctx := context.Background()
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Failed to start loop: %v", err)
	}
	defer func() { _ = loop.Stop() }()

	walker := &stubWalker{err: errors.New("walker boom")}
	triggers := NewTriggers(loop)
	triggers.SetWalker(walker)

	// Wait for initial sync to complete
	deadline := time.After(2 * time.Second)
	for {
		status := loop.GetStatus()
		if !status.LastSyncAt.IsZero() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Timeout waiting for initial sync")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	beforeTrigger := loop.GetStatus().LastSyncAt

	// SyncOnWrite: walker fails → TriggerSync must NOT be called
	triggers.SyncOnWrite(ctx)

	// Give it time to confirm sync does NOT run
	time.Sleep(100 * time.Millisecond)

	if loop.GetStatus().LastSyncAt.After(beforeTrigger) {
		t.Error("sync ran after a walker failure — expected it to be suppressed")
	}
	if walker.called.Load() < 1 {
		t.Errorf("expected walker to be called, got %d", walker.called.Load())
	}
}

func TestTriggers_SyncOnWrite_NoWalker(t *testing.T) {
	tmpDir := setupMergeTestRepo(t)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	syncer := NewSyncer(tmpDir, syncDir, false)
	projector := setupTestProjector(t, tmpDir)
	loop := NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), false)

	ctx := context.Background()
	if err := loop.Start(ctx); err != nil {
		t.Fatalf("Failed to start loop: %v", err)
	}
	defer func() { _ = loop.Stop() }()

	// No walker set — SyncOnWrite should still call TriggerSync
	triggers := NewTriggers(loop)

	// Wait for initial sync
	deadline := time.After(2 * time.Second)
	for {
		status := loop.GetStatus()
		if !status.LastSyncAt.IsZero() {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Timeout waiting for initial sync")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	beforeTrigger := loop.GetStatus().LastSyncAt
	triggers.SyncOnWrite(ctx)

	deadline = time.After(2 * time.Second)
	for {
		status := loop.GetStatus()
		if status.LastSyncAt.After(beforeTrigger) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Timeout waiting for LastSyncAt to advance after SyncOnWrite (no walker)")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestTriggers_SyncManual(t *testing.T) {
	tmpDir := setupMergeTestRepo(t)
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	syncer := NewSyncer(tmpDir, syncDir, false)
	projector := setupTestProjector(t, tmpDir)
	loop := NewSyncLoop(syncer, projector, tmpDir, syncDir, filepath.Join(tmpDir, ".thrum"), false)

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
	ctx := context.Background()

	// Should not panic when loop is nil
	triggers.SyncOnWrite(ctx)
	triggers.SyncManual()
}
