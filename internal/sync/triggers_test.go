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

// TestTriggers_SyncOnWrite_CompactorRuns covers the brainstormer-third
// IMPORTANT-1 finding: spec §5.3 says CompactAll runs at sync-trigger
// time in addition to daemon startup. SyncOnWrite must invoke the
// registered compactor closure after the walker writes succeed and
// before TriggerSync.
func TestTriggers_SyncOnWrite_CompactorRuns(t *testing.T) {
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

	var compactCalls atomic.Int32
	triggers.SetCompactor(func(_ context.Context) error {
		compactCalls.Add(1)
		return nil
	})

	triggers.SyncOnWrite(ctx)

	if got := compactCalls.Load(); got != 1 {
		t.Errorf("compactor should be invoked once per SyncOnWrite; got %d calls", got)
	}
	if got := walker.called.Load(); got < 1 {
		t.Errorf("walker should be invoked at least once; got %d calls", got)
	}
}

// TestTriggers_SyncOnWrite_CompactorFailureDoesNotBlockSync pins the
// brainstormer-third intent: compaction is maintenance, not a sync-
// correctness gate. A compactor error MUST log/slog but MUST NOT
// suppress the downstream TriggerSync.
func TestTriggers_SyncOnWrite_CompactorFailureDoesNotBlockSync(t *testing.T) {
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

	// Wait for the initial sync to settle so the post-trigger
	// LastSyncAt advance is unambiguously from SyncOnWrite.
	deadline := time.After(2 * time.Second)
	for loop.GetStatus().LastSyncAt.IsZero() {
		select {
		case <-deadline:
			t.Fatal("Timeout waiting for initial sync")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	beforeTrigger := loop.GetStatus().LastSyncAt
	triggers := NewTriggers(loop)
	triggers.SetWalker(&stubWalker{})
	triggers.SetCompactor(func(_ context.Context) error {
		return errors.New("compactor boom")
	})
	triggers.SyncOnWrite(ctx)

	// Poll until LastSyncAt advances — confirms TriggerSync fired
	// despite the compactor error.
	deadline = time.After(2 * time.Second)
	for {
		status := loop.GetStatus()
		if status.LastSyncAt.After(beforeTrigger) {
			return
		}
		select {
		case <-deadline:
			t.Fatal("sync did not fire after compactor failure — compactor error must not block TriggerSync")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// TestTriggers_SyncOnWrite_WalkerFailure_SkipsCompactor confirms the
// ordering: walker failure short-circuits BEFORE compactor runs.
// Without this, a broken walker could trigger compaction on
// inconsistent state files.
func TestTriggers_SyncOnWrite_WalkerFailure_SkipsCompactor(t *testing.T) {
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
	triggers.SetWalker(&stubWalker{err: errors.New("walker boom")})

	var compactCalls atomic.Int32
	triggers.SetCompactor(func(_ context.Context) error {
		compactCalls.Add(1)
		return nil
	})

	triggers.SyncOnWrite(ctx)

	if got := compactCalls.Load(); got != 0 {
		t.Errorf("compactor must NOT run when walker fails; got %d calls", got)
	}
}
