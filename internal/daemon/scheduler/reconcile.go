package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
)

// ReconcileBoot walks all non-terminal state rows and calls
// Handler.Reconcile for each whose handler is currently registered.
//
// Per spec §8.4.3: at daemon start, every scheduler_job_state row in
// scheduled / dispatched / running is potentially stale — the daemon
// died mid-run. ReconcileBoot asks each row's handler whether the run
// recovered (returns a definitive state) or was lost (ErrLostTrack →
// StateFailed with the canonical reason string).
//
// Rows whose handler is NOT yet registered (typical for bridge-owned
// internal jobs that RegisterInternal AFTER Scheduler.Start) are SKIPPED
// here and picked up by per-handler-registration reconciliation in
// RegisterInternal / RegisterTypeHandler (Task 21).
func (s *Scheduler) ReconcileBoot(ctx context.Context) error {
	rows, err := s.state.NonTerminalAtBoot(ctx)
	if err != nil {
		return fmt.Errorf("reconcile: load non-terminal: %w", err)
	}
	for _, row := range rows {
		s.reconcileOne(ctx, row)
	}
	return nil
}

// reconcileOne runs the reconcile contract for a single state row.
// Errors are logged but not surfaced — boot reconciliation is
// best-effort; a single misbehaving handler should not block the daemon's
// startup path.
func (s *Scheduler) reconcileOne(ctx context.Context, row *StateRow) {
	spec, ok := s.JobSpec(row.JobID)
	if !ok {
		// No registered job for this row. Could be a removed job whose
		// state row is stale, OR a bridge-owned job that hasn't called
		// RegisterInternal yet. Cleanup of orphaned rows for removed
		// jobs is a v0.11.x feature; for now we leave the row alone.
		return
	}
	s.mu.RLock()
	handler := s.resolveHandler(spec)
	s.mu.RUnlock()
	if handler == nil {
		// Type handler not registered yet (e.g. scheduled_agent before
		// B-B1's RegisterTypeHandler). Treat as not-yet-reconcilable.
		return
	}

	newState, err := handler.Reconcile(ctx, spec, row.LastRunID, row.CurrentState)
	reporter := &stateReporter{store: s.state, jobID: row.JobID, runID: row.LastRunID}
	switch {
	case errors.Is(err, ErrLostTrack):
		// Canonical §8.4.3 reason string.
		if tErr := reporter.Transition(StateFailed, "lost across daemon restart", nil); tErr != nil {
			log.Printf("scheduler: reconcile transition-failed %s: %v", row.JobID, tErr)
		}
	case err != nil:
		log.Printf("scheduler: reconcile %s: %v", row.JobID, err)
	case newState != row.CurrentState:
		if tErr := reporter.Transition(newState, "reconciled at boot", nil); tErr != nil {
			log.Printf("scheduler: reconcile transition %s: %v", row.JobID, tErr)
		}
	}
}
