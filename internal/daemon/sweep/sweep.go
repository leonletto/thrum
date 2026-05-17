// Package sweep implements the internal.stalled_agent_sweep handler
// (canonical-ref §6.3). At each scheduler-driven tick it:
//
//  1. Reads A-B1's skip-set — agents whose scheduler_job_state row
//     puts them in a B-B1 managed stage (idle-nudge already in flight)
//  2. Enumerates live tmux panes via PaneSource
//  3. For each pane outside the skip-set whose last activity is older
//     than the threshold:
//       a. Captures the pane snapshot via tmux.CapturePane
//       b. Mints a condition_pane_quiet reminder via
//          reminders.Store.MintConditionForAgent (idempotent — Q3.8
//          match-key collapses repeat-mints into a single open row)
//
// Stays a thin orchestrator: all collaborators are injected so the
// handler is straightforwardly unit-testable without a daemon, real
// tmux, or A-B1's scheduler running.
//
// Design pointers:
//   - Q4 (narrowed skip-set): pre-first-output stages like
//     awaiting_first_output / launching_runtime are NOT in the skip
//     set; sweep MUST catch them. B-B1's idle-nudge only engages
//     after first output.
//   - Q5.1: A-B4's own run state is uninteresting — durable state
//     lives in the reminders table. Reconcile is therefore a no-op.
//   - Q3.8: MintConditionForAgent's match-key handles idempotency at
//     the Store layer; sweep doesn't need its own dedup pass.
//   - Q3.7: threshold == sweep interval (single tunable
//     daemon.stalled_sweep.interval_minutes, default 15).
package sweep

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/leonletto/thrum/internal/daemon/reminders"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/tmux"
)

// SchedulerState is the minimal read interface A-B4 needs from A-B1.
// Returns the set of agents whose scheduler_job_state row puts them
// in a B-B1 managed stage (running_work, idle_nudge_*) — sweep skips
// those because B-B1's nudge sequence is already engaged.
//
// The concrete impl (thrum-6qmf.3.6) wraps scheduler.StateStore with
// a join against scheduler.JobSpec to filter by stage label.
type SchedulerState interface {
	AgentsInBB1ManagedStages(ctx context.Context) (map[string]bool, error)
}

// PaneSource enumerates the live panes the sweep should consider.
// Real impl (thrum-6qmf.3.9) wraps the daemon's session registry +
// tmux #{window_activity} probe; tests use a fake.
type PaneSource interface {
	LivePanes(ctx context.Context) ([]Pane, error)
}

// Pane describes a single live tmux pane the sweep evaluates.
// LastActivity is the wall-clock time of the most recent observed
// pane change (from tmux's #{window_activity} format) used to compare
// against the staleness threshold.
type Pane struct {
	AgentName    string
	TmuxTarget   string // session:window.pane
	LastActivity time.Time
}

// ChainResolver returns the delivery chain for sweep-minted reminders.
// Real impl (thrum-6qmf.3.12) reads daemon.sweep.alert_chain config
// with fallback to escalation.supervisor_agent_name; tests use a fake.
type ChainResolver interface {
	Resolve(ctx context.Context) ([]string, error)
}

// CapturePaneFn wraps the tmux capture-pane invocation. Function type
// (not interface) mirrors the SupervisorRouter pattern from
// thrum-6qmf.3.11 — production wraps tmux.CapturePane directly;
// tests substitute closures.
type CapturePaneFn func(target string, lines int) (string, error)

// snapshotLines is the per-mint pane capture depth. 200 lines is
// enough to give an operator context (the typical idle/running pane
// is 30-80 lines; 200 catches the case where a long-running task left
// scrolled output above the visible window). 200×80B/line ≈ 16KB,
// which after TruncateSnapshot hits the canonical §3.11 Guard 5 cap.
const snapshotLines = 200

// Handler implements scheduler.Handler for the
// internal.stalled_agent_sweep job. One instance per daemon; registered
// via Dispatcher-style Register hook in thrum-6qmf.3.16.
type Handler struct {
	store     reminders.Store
	sched     SchedulerState
	panes     PaneSource
	chain     ChainResolver
	threshold time.Duration // == sweep interval per Q3.7
	capture   CapturePaneFn
}

// New wires the four collaborators. The capture function defaults to
// tmux.CapturePane (production); tests pass an explicit override via
// NewWithCapture.
func New(
	store reminders.Store,
	sched SchedulerState,
	panes PaneSource,
	chain ChainResolver,
	threshold time.Duration,
) *Handler {
	return NewWithCapture(store, sched, panes, chain, threshold, tmux.CapturePane)
}

// NewWithCapture is the test-friendly constructor — same as New but
// the caller injects the CapturePane function explicitly. Production
// code prefers New; this exists so handler_test.go can drive a
// deterministic capture without going through tmux.
func NewWithCapture(
	store reminders.Store,
	sched SchedulerState,
	panes PaneSource,
	chain ChainResolver,
	threshold time.Duration,
	capture CapturePaneFn,
) *Handler {
	return &Handler{
		store:     store,
		sched:     sched,
		panes:     panes,
		chain:     chain,
		threshold: threshold,
		capture:   capture,
	}
}

// Dispatch runs one sweep pass. Reports running → completed (or
// failed) per the scheduler.Handler contract (mirrors cleanup.go +
// reminders.dispatcherHandler).
//
// Per-pane failures (capture errors, mint errors, individual chain
// resolution glitches) are logged + skipped; the batch completes
// regardless. Only top-level collaborator failures (skip-set read,
// pane enumeration, chain resolution) propagate as the Dispatch
// error — these typically signal the daemon's storage or registry
// is unhealthy enough that the whole sweep should fail loudly and
// be retried at the next cadence.
func (h *Handler) Dispatch(
	ctx context.Context,
	_ scheduler.JobSpec,
	_ string,
	reporter scheduler.StateReporter,
	_ <-chan *scheduler.Completion,
) error {
	if err := reporter.Transition(scheduler.StateRunning, "stalled-agent sweep starting", nil); err != nil {
		return err
	}

	if err := h.runOnce(ctx); err != nil {
		return reporter.Transition(scheduler.StateFailed, "sweep error: "+err.Error(), nil)
	}
	return reporter.Transition(scheduler.StateCompleted, "sweep complete", nil)
}

// runOnce is the package-internal entry point for tests + Dispatch.
// Returns the first top-level error encountered; per-pane errors are
// absorbed via logging.
func (h *Handler) runOnce(ctx context.Context) error {
	now := time.Now().UTC()

	skip, err := h.sched.AgentsInBB1ManagedStages(ctx)
	if err != nil {
		return fmt.Errorf("sweep: read skip-set: %w", err)
	}

	panes, err := h.panes.LivePanes(ctx)
	if err != nil {
		return fmt.Errorf("sweep: enumerate panes: %w", err)
	}

	chain, err := h.chain.Resolve(ctx)
	if err != nil {
		return fmt.Errorf("sweep: resolve target chain: %w", err)
	}

	for _, p := range panes {
		if skip[p.AgentName] {
			continue
		}
		if now.Sub(p.LastActivity) < h.threshold {
			continue
		}

		snap, err := h.capture(p.TmuxTarget, snapshotLines)
		if err != nil {
			// Per Q5: capture errors are logged + continued, not
			// fatal. A pane that's gone away (window closed mid-
			// sweep) shouldn't poison the rest of the batch.
			log.Printf("[sweep] capture(%s) for %s: %v (skipping)",
				p.TmuxTarget, p.AgentName, err)
			continue
		}

		meta, err := json.Marshal(map[string]any{
			"agent":             p.AgentName,
			"quiet_since":       p.LastActivity.Unix(),
			"tmux_target":       p.TmuxTarget,
			"threshold_seconds": int(h.threshold.Seconds()),
		})
		if err != nil {
			// json.Marshal of a small map[string]any with int/string
			// values can't actually fail in practice; defensive
			// continue rather than aborting the whole batch.
			log.Printf("[sweep] marshal trigger_meta for %s: %v (skipping)",
				p.AgentName, err)
			continue
		}

		// Re-arm at now+threshold so the next sweep cycle re-fires
		// the same row if the agent is still stalled. Idempotency
		// match-key ensures only one open row per target_agent.
		_, _, err = h.store.MintConditionForAgent(
			ctx, p.AgentName, meta, chain, snap, now.Add(h.threshold))
		if err != nil {
			log.Printf("[sweep] MintConditionForAgent(%s): %v (skipping)",
				p.AgentName, err)
			continue
		}
	}
	return nil
}

// Reconcile is a no-op for the sweep handler. A-B4's own run state is
// uninteresting per brainstorm Q5.1 — the durable state lives in the
// reminders table. Boot-time recovery: if the daemon died mid-sweep,
// any rows that didn't mint will be re-evaluated on the next sweep
// tick (idempotency match-key prevents duplicate firings).
func (h *Handler) Reconcile(_ context.Context, _ scheduler.JobSpec, _ string, _ scheduler.State) (scheduler.State, error) {
	return scheduler.StateCompleted, nil
}

// Stages returns a single non-empty stage. The sweep doesn't actually
// emit stage transitions; scheduler.RegisterInternal requires a
// non-empty map (the canonical contract is "max-dwell duration per
// stage" — a 5-minute dwell guards against a hung sweep eating the
// scheduler slot).
func (h *Handler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"sweeping": 5 * time.Minute}
}

// Compile-time check that Handler satisfies scheduler.Handler.
var _ scheduler.Handler = (*Handler)(nil)
