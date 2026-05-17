package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"
)

// CleanupHandler implements Handler for the substrate-owned
// `internal.scheduler_event_cleanup` job. Once a day it runs a DELETE
// pass against scheduler_job_events, dropping rows older than
// retentionDays. Default retention is 7 days; daemon config exposes
// `daemon.scheduler.event_retention_days` to override.
//
// This is the first concrete RegisterInternal consumer. A-B2 will follow
// with BackupScheduler + PeriodicSyncScheduler; D-B1 with email-poll +
// email-dedup-cleanup; A-B4 with stalled-sweep + reminder-dispatch.
type CleanupHandler struct {
	store         *StateStore
	retentionDays int
}

// NewCleanupHandler builds a cleanup handler with `retentionDays` of
// event retention. Non-positive values are clamped to the canonical
// 7-day default.
func NewCleanupHandler(store *StateStore, retentionDays int) *CleanupHandler {
	if retentionDays <= 0 {
		retentionDays = 7
	}
	return &CleanupHandler{store: store, retentionDays: retentionDays}
}

func (c *CleanupHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"pruning": 5 * time.Minute}
}

// Reconcile reports completed: cleanup is idempotent, a partially-run
// prune just leaves more old events behind, and the next tick prunes
// them. There's nothing to recover.
func (c *CleanupHandler) Reconcile(_ context.Context, _ JobSpec, _ string, _ State) (State, error) {
	return StateCompleted, nil
}

// Dispatch transitions running → completed (or failed on DB error) with
// the DELETE pass in between.
func (c *CleanupHandler) Dispatch(ctx context.Context, _ JobSpec, _ string, reporter StateReporter, _ <-chan *Completion) error {
	if err := reporter.Transition(StateRunning, "pruning old events", nil); err != nil {
		return err
	}
	if err := reporter.Stage("pruning"); err != nil {
		return err
	}
	if err := c.runOnce(ctx); err != nil {
		return reporter.Transition(StateFailed, "prune error: "+err.Error(), nil)
	}
	return reporter.Transition(StateCompleted, "events pruned", nil)
}

// runOnce executes one DELETE pass and is the package-internal test
// entry point. Returns the SQL error verbatim; the caller wraps for
// state-machine reporting.
func (c *CleanupHandler) runOnce(ctx context.Context) error {
	cutoff := time.Now().Add(-time.Duration(c.retentionDays) * 24 * time.Hour).Unix()
	res, err := c.store.DB().ExecContext(ctx,
		`DELETE FROM scheduler_job_events WHERE event_time < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("delete old events: %w", err)
	}
	rows, _ := res.RowsAffected()
	log.Printf("scheduler.cleanup: pruned %d events older than %d days", rows, c.retentionDays)
	return nil
}
