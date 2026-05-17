// Package agentdispatch holds the B-B1 agent-lifecycle dispatch and
// housekeeping handlers — the scheduler-Handler implementations that
// the daemon registers against A-B1's RegisterInternal at boot.
//
// The package is intentionally lean: it pulls in `internal/daemon/state`
// for the agent_lifecycle_events store and `internal/daemon/scheduler`
// for the Handler interface, but stays independent of cmd/thrum so the
// wiring point is testable without booting the full daemon.
package agentdispatch

import (
	"context"
	"log/slog"
	"time"

	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// CleanupHandler is the scheduler.Handler implementation for the
// canonical `internal.agent_lifecycle_cleanup` daily job. Per
// substrate-canonical-reference.md §6.3 it prunes
// agent_lifecycle_events rows older than retentionDays once per day.
//
// Pattern mirrors A-B1's scheduler.CleanupHandler: idempotent prune;
// reports Running → Completed (or Failed on store error) via the
// scheduler StateReporter so scheduler_job_state + scheduler_job_events
// stay in sync.
type CleanupHandler struct {
	store         state.AgentLifecycleStore
	retentionDays int
}

// NewCleanupHandler returns a handler that prunes events older than
// retentionDays. Non-positive values clamp to the canonical 7-day
// default per canonical §6.3 + Q-Spec-3 (no operator override below 1).
func NewCleanupHandler(s state.AgentLifecycleStore, retentionDays int) *CleanupHandler {
	if retentionDays <= 0 {
		retentionDays = 7
	}
	return &CleanupHandler{store: s, retentionDays: retentionDays}
}

// Stages declares the single "pruning" stage. The 5-minute budget is
// generous — a real prune is sub-second; the budget covers a future
// world where the table has accumulated millions of rows or sits behind
// network-attached storage.
func (h *CleanupHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"pruning": 5 * time.Minute}
}

// Reconcile reports completed: cleanup is idempotent, a partially-run
// prune just leaves more old events behind, and the next tick prunes
// them. There is nothing to recover at boot.
func (h *CleanupHandler) Reconcile(_ context.Context, _ scheduler.JobSpec, _ string, _ scheduler.State) (scheduler.State, error) {
	return scheduler.StateCompleted, nil
}

// Dispatch transitions Running → Completed (or Failed on store error)
// with one DELETE pass in between. The reporter writes both
// scheduler_job_state and scheduler_job_events atomically per A-B1
// spec §8.4.2.
func (h *CleanupHandler) Dispatch(ctx context.Context, _ scheduler.JobSpec, _ string, reporter scheduler.StateReporter, _ <-chan *scheduler.Completion) error {
	if err := reporter.Transition(scheduler.StateRunning, "pruning old agent lifecycle events", nil); err != nil {
		return err
	}
	if err := reporter.Stage("pruning"); err != nil {
		return err
	}
	rows, err := h.runOnce(ctx)
	if err != nil {
		return reporter.Transition(scheduler.StateFailed, "prune error: "+err.Error(), nil)
	}
	return reporter.Transition(scheduler.StateCompleted, "events pruned", map[string]any{
		"rows_deleted":   rows,
		"retention_days": h.retentionDays,
	})
}

// runOnce executes one DELETE pass and is the package-internal test
// entry point. Returns the row count + the raw store error so callers
// can attribute failures back to the SQL layer.
func (h *CleanupHandler) runOnce(ctx context.Context) (int64, error) {
	cutoff := time.Now().Add(-time.Duration(h.retentionDays) * 24 * time.Hour)
	rows, err := h.store.PruneOlderThan(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	slog.Info("agent_lifecycle_cleanup: pruned",
		"rows", rows, "retention_days", h.retentionDays)
	return rows, nil
}
