package sync

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"time"
)

// syncWalkerTimeout caps WalkAndWrite duration as defense-in-depth
// against a pathological SQLite hang or unbounded walker work — see
// thrum-s7is.7. 30s is generous headroom: post-s7is.6 cold-start fix,
// steady-state incremental walks complete in milliseconds. The bound
// exists so a misbehaving walker can't lock the daemon indefinitely
// when triggered from state.WriteEvent (which already detaches from
// the caller's RPC ctx via context.Background — see SyncOnWrite).
//
// Package-level var (not const) so tests can override to a small value
// without sleeping for the production ceiling.
var syncWalkerTimeout = 30 * time.Second

// syncCompactorTimeout caps the compactor closure registered via
// SetCompactor when invoked from SyncOnWrite — see thrum-roz1.
// 60s (not 30s like the walker) because the events-table DELETE is
// the dominant compactor cost AND the events table currently has no
// timestamp index (only sequence/type/origin per
// internal/schema/schema.go:603-605), so
// `DELETE FROM events WHERE timestamp < ?` requires a full table scan
// that runs O(N) in row count. The follow-up thrum-7ojv adds a
// timestamp index which would let this drop to 30s symmetric with the
// walker; until that ships the 60s ceiling absorbs the unindexed-scan
// runtime on large events tables without re-introducing the cascade
// to agent.register session-resurrect that 30s was too tight to avoid.
//
// Package-level var (not const) for the same reason as syncWalkerTimeout.
var syncCompactorTimeout = 60 * time.Second

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
		// timeouts.
		//
		// Cap with syncWalkerTimeout (s7is.7): an unbounded walker
		// could lock the daemon forever on a pathological SQLite hang.
		// The bound is defense-in-depth; steady-state incremental
		// walks finish in milliseconds.
		walkerCtx, cancel := context.WithTimeout(context.Background(), syncWalkerTimeout)
		err := t.walker.WalkAndWrite(walkerCtx)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				slog.Warn("sync.walker_timeout",
					"duration_ceiling_s", int(syncWalkerTimeout/time.Second),
					"guidance", "investigate_lastwalkat_drift_or_sqlite_hang",
				)
			}
			log.Printf("sync: snapshot walker failed: %v", err)
			slog.Error("sync.walker_failed", "err", err)
			return
		}
	}
	if t.compact != nil {
		// Detach from caller ctx (thrum-roz1, same cancel shape as
		// walker s7is.7; slog fields intentionally extended for
		// daemon-log greppability per coord review):
		// SyncOnWrite is invoked from inside state.WriteEvent while
		// state.Lock() is held. If the compactor used the caller's ctx
		// (typically a ~10s RPC deadline) and the events-table DELETE
		// took longer than that, the compactor would error with
		// "context deadline exceeded" AND burn the deadline for any
		// concurrent / subsequent op in that RPC — notably the next
		// agent.register's ensureActiveSession SELECT, which then
		// fails with "check active session: context deadline exceeded"
		// and surfaces to the user as "daemon may be unresponsive —
		// try thrum daemon restart". The cascade had 99 occurrences
		// across 8+ agents pre-fix. Using Background + a generous
		// ceiling breaks the coupling at the root.
		compactStart := time.Now()
		compactCtx, cancel := context.WithTimeout(context.Background(), syncCompactorTimeout)
		err := t.compact(compactCtx)
		elapsed := time.Since(compactStart)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				slog.Warn("sync.compactor_timeout",
					"phase", "[sync/compactor]",
					"elapsed_s", elapsed.Seconds(),
					"duration_ceiling_s", int(syncCompactorTimeout/time.Second),
					"guidance", "investigate_events_table_size_or_add_timestamp_index",
				)
			}
			// Non-fatal: compaction is maintenance, not a sync gate.
			log.Printf("[sync/compactor] failed (elapsed=%s): %v", elapsed, err)
			slog.Warn("sync.compactor_failed", "err", err, "elapsed_s", elapsed.Seconds())
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
