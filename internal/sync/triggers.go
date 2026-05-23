package sync

import (
	"context"
	"log"
	"log/slog"
)

// WalkerInvoker is the minimal interface Triggers uses to call the
// snapshot walker before firing sync. Decouples this package from
// internal/sync/snapshot to avoid an import cycle (snapshot depends on
// internal/sync/state; if triggers imported snapshot directly the
// daemon bootstrap's wiring order would be constrained needlessly).
// The production type is *snapshot.Walker; tests pass a stub.
type WalkerInvoker interface {
	WalkAndWrite(ctx context.Context) error
}

// Triggers provides methods to trigger sync operations on specific events.
type Triggers struct {
	loop    *SyncLoop
	walker  WalkerInvoker               // optional; nil during early bootstrap or in tests that don't wire it
	compact func(context.Context) error // optional; runs after walker, before TriggerSync. See SetCompactor.
}

// NewTriggers creates a new Triggers instance wired to the given loop.
// The walker is registered separately via SetWalker so the daemon
// bootstrap can construct Triggers before the Walker (which depends on
// the state package's Writer + the per-author writers).
func NewTriggers(loop *SyncLoop) *Triggers {
	return &Triggers{loop: loop}
}

// SetWalker registers the snapshot walker invoked by SyncOnWrite
// before TriggerSync. Production daemon bootstrap calls this once at
// startup after the Walker is constructed. Idempotent — last writer
// wins; intended to be set exactly once in production.
func (t *Triggers) SetWalker(w WalkerInvoker) {
	t.walker = w
}

// SetCompactor registers a closure invoked by SyncOnWrite after the
// walker writes succeed and before TriggerSync. Per spec §5.2 + §5.3,
// CompactAll runs at sync-trigger time in addition to daemon startup;
// running here folds any dedup rewrite of messages-v2/<id>.jsonl /
// receipts/<id>.jsonl into the same commit as the walker's appends,
// keeping the a-sync history tidy.
//
// Compaction is a maintenance task, not a sync-correctness gate —
// failures are logged + slog'd but DO NOT suppress TriggerSync.
// Sub-threshold files skip dedup entirely so most invocations are
// no-ops at zero cost.
//
// The closure wraps compact.Compactor.CompactAll(ctx, db); bootstrap
// constructs it via `func(ctx) error { return compactor.CompactAll(ctx, st.DB()) }`.
// Nil-safe: tests + early-bootstrap states that don't wire it skip
// the compaction step entirely.
func (t *Triggers) SetCompactor(fn func(context.Context) error) {
	t.compact = fn
}

// SyncOnWrite is the hook fired by state.WriteEvent on a structural
// event (spec §3.2 whitelist). It runs the snapshot walker first to
// materialize state/, messages-v2/, receipts/ from the local journal
// + projection, opportunistically runs compaction (so any rewrite
// folds into the same commit), THEN triggers the sync loop's
// commit-and-push. The walker → compact → sync ordering is
// load-bearing: walker writes files, compaction tidies them, sync
// commits them.
//
// If the walker fails, sync is NOT triggered — the failed write would
// otherwise be committed as an inconsistent state. The error is
// logged + emitted via slog (event "sync.walker_failed") so operators
// can detect it.
//
// If compaction fails, the sync trigger STILL fires — compaction is
// a maintenance task that runs again at next sync-trigger AND at
// daemon startup, so transient failures are self-healing. Suppressing
// sync on a compaction failure would conflate maintenance with
// correctness.
func (t *Triggers) SyncOnWrite(ctx context.Context) {
	if t.loop == nil {
		return
	}
	if t.walker != nil {
		// Detach from caller ctx (s7is.6): SyncOnWrite is invoked from
		// inside state.WriteEvent while state.Lock() is held. The caller
		// ctx is typically an RPC context with a ~10s deadline. If the
		// walker is canceled mid-fire by that deadline expiring, the
		// next walker call still has work to do AND the failure
		// surfaces as sync.walker_failed (cosmetic-but-noisy). Use
		// Background so the walker completes regardless of caller
		// timeouts — work bounded by walker's own internal logic.
		if err := t.walker.WalkAndWrite(context.Background()); err != nil {
			log.Printf("sync: snapshot walker failed: %v", err)
			slog.Error("sync.walker_failed", "err", err)
			return
		}
	}
	if t.compact != nil {
		if err := t.compact(ctx); err != nil {
			// Non-fatal: compaction is maintenance, not a sync gate.
			log.Printf("sync: compactor failed: %v", err)
			slog.Warn("sync.compactor_failed", "err", err)
		}
	}
	t.loop.TriggerSync()
}

// SyncManual is the user-initiated trigger (RPC or CLI). It does NOT
// run the walker — manual sync just fetches + commits whatever's on
// disk. Walker runs only on structural-event writes.
func (t *Triggers) SyncManual() {
	if t.loop != nil {
		t.loop.TriggerSync()
	}
}
