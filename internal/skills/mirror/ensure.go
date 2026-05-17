package mirror

import (
	"context"
	"fmt"
)

// EnsureMirrored is the synchronous mirror primitive that B-B1's
// stage-3 wake handler (thrum-6qmf.4.51) calls to gate worktree
// readiness on a fully-populated skill mirror. Unlike the async
// channel path, EnsureMirrored returns ONLY after every canonical
// skill has landed on disk for every populated-adapter destination
// of worktreePath — and after any drift-side files (skills present
// in the worktree but absent from canonical) have been removed.
//
// Sharing: acquires the same per-destination mutex the async worker
// uses, so a B-B1 wake handler can fire concurrently with watcher-
// driven enqueues without tearing writes.
//
// Return contract (canonical spec §12.3.1):
//   - nil: every populated destination converged on disk. Caller
//     proceeds with stage-4 (binding the worktree to the wake event).
//   - ErrUnknownWorktree: worktreePath was never registered. Caller
//     should roll back the worktree (this is a wake-handler bug or
//     a config drift — fail loud).
//   - nil (null-adapter case): the worktree's runtime resolves to a
//     null adapter entry (codex/opencode/kiro/cursor in v0.11). The
//     wake handler treats this as success — there is nothing to
//     mirror for these runtimes, but the worktree itself is fine.
//   - ctx.Err(): context cancelled. Caller decides retry vs abort.
//   - wrapped ErrMirrorWrite: filesystem write failed. errors.Is
//     against ErrMirrorWrite lets B-B1's rollback path distinguish
//     mirror failure from other staging errors.
//
// Idempotent: a second EnsureMirrored call against an already-
// converged worktree returns nil without rewriting unchanged files
// (copyFile content-equality short-circuit; same code path the
// reconcile pass uses).
func (w *Worker) EnsureMirrored(ctx context.Context, worktreePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Snapshot the registration + destinations under the read lock
	// so a concurrent Stop (which closes channels and reassigns the
	// maps) cannot race the destination iteration below. Release the
	// lock before slow filesystem work — reconcileDestination takes
	// the per-destination mutex internally, which is independent of
	// stateMu.
	w.stateMu.RLock()
	if !w.started.Load() {
		w.stateMu.RUnlock()
		return ErrWorkerNotStarted
	}
	_, registered := w.registered[worktreePath]
	if !registered {
		w.stateMu.RUnlock()
		return fmt.Errorf("%w: %s", ErrUnknownWorktree, worktreePath)
	}
	rawDests := w.worktree[worktreePath]
	dests := make([]Destination, len(rawDests))
	copy(dests, rawDests)
	w.stateMu.RUnlock()

	if len(dests) == 0 {
		// Registered but every destination resolved to a null
		// adapter — treat as success-skip per spec §12.3.1.
		return nil
	}

	for _, dest := range dests {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry, err := Lookup(dest.Runtime)
		if err != nil {
			// Shouldn't happen — Worker.Start already validated
			// every destination's runtime. Surface defensively.
			return fmt.Errorf("ensure mirrored %s: %w", worktreePath, err)
		}
		if entry == nil {
			// Null adapter on this destination — success-skip per
			// spec §11. Other destinations may still need work.
			continue
		}
		// reconcileDestination acquires the destination mutex and
		// performs the canonical-vs-destination diff + apply (same
		// code path as Worker.Reconcile). Surfaces the first
		// filesystem error so B-B1's wake handler can roll back
		// (errors.Is(err, ErrMirrorWrite) is the discriminator).
		if err := w.reconcileDestination(dest, entry, nil); err != nil {
			return fmt.Errorf("ensure mirrored %s (runtime=%s): %w", worktreePath, dest.Runtime, err)
		}
	}
	return nil
}
