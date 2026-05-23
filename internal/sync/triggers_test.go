package sync

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
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

// slowWalker blocks on ctx.Done() and returns ctx.Err() — simulates
// a pathological hang where the walker never completes naturally and
// must be unblocked by ctx cancellation (s7is.7 defense-in-depth).
type slowWalker struct {
	called atomic.Int32
}

func (w *slowWalker) WalkAndWrite(ctx context.Context) error {
	w.called.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

// TestTriggers_SyncOnWrite_WalkerTimeout_FiresWarnAndSuppressesSync
// pins the thrum-s7is.7 contract: a walker that never returns must be
// bounded by syncWalkerTimeout, must emit a sync.walker_timeout slog
// warning with the duration_ceiling_s + guidance fields, and must
// suppress TriggerSync (the timed-out walker may have written partial
// state; committing it would be inconsistent — same gate as
// WalkerFailure).
func TestTriggers_SyncOnWrite_WalkerTimeout_FiresWarnAndSuppressesSync(t *testing.T) {
	// Shrink the ceiling to a test-friendly value; restore on cleanup.
	originalTimeout := syncWalkerTimeout
	syncWalkerTimeout = 50 * time.Millisecond
	t.Cleanup(func() { syncWalkerTimeout = originalTimeout })

	// Capture slog output to verify the sync.walker_timeout warn fires.
	var logBuf bytes.Buffer
	captureHandler := slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	originalLogger := slog.Default()
	slog.SetDefault(slog.New(captureHandler))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

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

	// Wait for initial sync so any post-trigger advance is unambiguous.
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

	walker := &slowWalker{}
	triggers := NewTriggers(loop)
	triggers.SetWalker(walker)

	// SyncOnWrite must block until the walker timeout fires (~50ms),
	// emit the warn, NOT call TriggerSync, and return.
	start := time.Now()
	triggers.SyncOnWrite(ctx)
	elapsed := time.Since(start)

	// Bound elapsed: must be at least the timeout (50ms) and not
	// excessively over (give ourselves a 2s ceiling for slow CI).
	if elapsed < 50*time.Millisecond {
		t.Errorf("SyncOnWrite returned in %v; expected ≥ syncWalkerTimeout (50ms)", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("SyncOnWrite took %v; expected ≪ 2s (timeout wrapping appears broken)", elapsed)
	}

	if walker.called.Load() != 1 {
		t.Errorf("walker should be called exactly once; got %d", walker.called.Load())
	}

	// Verify the sync.walker_timeout warn fired with the expected
	// structured fields. JSON output is order-stable enough for
	// substring checks here.
	logged := logBuf.String()
	if !strings.Contains(logged, `"msg":"sync.walker_timeout"`) {
		t.Errorf("expected sync.walker_timeout slog event; got: %s", logged)
	}
	// Presence check only on duration_ceiling_s — under this test the
	// override is 50ms which integer-divides to 0s, so the emitted value
	// is "0" (production emits "30"). Both are valid; the contract is
	// that the field is present.
	if !strings.Contains(logged, `"duration_ceiling_s":`) {
		t.Errorf("expected duration_ceiling_s slog attr; got: %s", logged)
	}
	if !strings.Contains(logged, `"guidance":"investigate_lastwalkat_drift_or_sqlite_hang"`) {
		t.Errorf("expected guidance slog attr with the s7is.7 hint string; got: %s", logged)
	}
	// The walker_failed error should ALSO fire (the timeout is a
	// failure mode, not a separate disposition).
	if !strings.Contains(logged, `"msg":"sync.walker_failed"`) {
		t.Errorf("expected sync.walker_failed slog event to also fire; got: %s", logged)
	}

	// TriggerSync must NOT have fired — same gate as the regular
	// WalkerFailure case: a timed-out walker may have left partial
	// state on disk; committing it would risk inconsistency.
	time.Sleep(100 * time.Millisecond) // grace for any racey sync
	if loop.GetStatus().LastSyncAt.After(beforeTrigger) {
		t.Error("sync ran after a walker timeout — TriggerSync must be suppressed")
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
