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
	loop   *SyncLoop
	walker WalkerInvoker // optional; nil during early bootstrap or in tests that don't wire it
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

// SyncOnWrite is the hook fired by state.WriteEvent on a structural
// event (spec §3.2 whitelist). It runs the snapshot walker first to
// materialize state/, messages-v2/, receipts/ from the local journal
// + projection, THEN triggers the sync loop's commit-and-push. The
// order is load-bearing: walker writes files, sync commits them. A
// reversed order would produce empty commits.
//
// If the walker fails, sync is NOT triggered — the failed write would
// otherwise be committed as an inconsistent state. The error is
// logged + emitted via slog (event "sync.walker_failed") so operators
// can detect it.
func (t *Triggers) SyncOnWrite(ctx context.Context) {
	if t.loop == nil {
		return
	}
	if t.walker != nil {
		if err := t.walker.WalkAndWrite(ctx); err != nil {
			log.Printf("sync: snapshot walker failed: %v", err)
			slog.Error("sync.walker_failed", "err", err)
			return
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
