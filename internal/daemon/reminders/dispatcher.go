package reminders

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// FireSink is what the Dispatcher calls when a reminder fires. The state
// transition (Store.Fire / Store.FireAndRearm) runs after the sink
// returns successfully — sink failures abort the transition for that
// row so the dispatcher retries on the next tick.
//
// E4.5 plugs in the real delivery wiring (supervisor-delivery for
// at-permission-prompt targets; routing-chain dispatch for daemon-source
// rows). Until then NoopFireSink lets the dispatcher land cleanly.
type FireSink interface {
	Fire(ctx context.Context, r *Reminder) error
}

// NoopFireSink is the E4.1 placeholder. E4.5 Task 25
// (thrum-6qmf.3.8) replaces references to it with the real DeliverySink.
type NoopFireSink struct{}

// Fire on the noop sink is a logged no-op. The log line is intentional
// so operators can see dispatcher activity in the early-stage build.
func (NoopFireSink) Fire(_ context.Context, r *Reminder) error {
	log.Printf("[reminders] noop fire: id=%s source=%s trigger_kind=%s target=%s",
		r.ID, r.Source, r.TriggerKind, r.TargetAgent)
	return nil
}

// ReArmPolicy decides the next_reminder_at for re-arming
// condition-triggered reminders after a fire. SweepInterval is the
// production policy; tests substitute a constant-offset stub.
type ReArmPolicy interface {
	NextAfter(r *Reminder, fired time.Time) time.Time
}

// SweepInterval re-arms condition rows by adding a fixed interval to
// the fire time. Default 15min matches
// daemon.stalled_sweep.interval_minutes per canonical §4.4 — the sweep
// cadence and the re-arm cadence are intentionally the same knob (a
// sweep that just fired and cleared the operator-visible reminder
// shouldn't re-fire before the next sweep cycle anyway).
type SweepInterval struct{ Interval time.Duration }

// NextAfter returns fired + Interval.
func (p SweepInterval) NextAfter(_ *Reminder, fired time.Time) time.Time {
	return fired.Add(p.Interval)
}

// Dispatcher scans for due reminders and fires them. Caller (A-B1's
// scheduler via Handler.Dispatch in Task 10) is responsible for cadence
// — Tick runs exactly one pass per call.
type Dispatcher struct {
	store Store
	sink  FireSink
	rearm ReArmPolicy
}

// NewDispatcher wires the dispatcher's three collaborators. Pass
// NoopFireSink{} for sink in pre-E4.5 builds and a SweepInterval{15min}
// for rearm.
func NewDispatcher(store Store, sink FireSink, rearm ReArmPolicy) *Dispatcher {
	return &Dispatcher{store: store, sink: sink, rearm: rearm}
}

// Tick runs one dispatch pass: scan DueOpen, fire each via the sink,
// transition per TriggerKind. Errors from a single row are logged but
// do not abort the batch — one bad row must not silence the rest.
//
// The `now` parameter is the dispatcher's clock seam (Implementation
// Standards #2 in the plan): tests pass synthetic `now`; production
// uses time.Now().UTC(). No separate Clock interface — the parameter
// IS the seam. C-B1 staleness tests rely on this contract.
func (d *Dispatcher) Tick(ctx context.Context, now time.Time) error {
	due, err := d.store.DueOpen(ctx, now)
	if err != nil {
		return fmt.Errorf("dispatcher: scan DueOpen: %w", err)
	}
	for _, r := range due {
		if err := d.fireOne(ctx, r, now); err != nil {
			// Log + continue — a single bad row must not poison the
			// batch. The next tick will retry rows whose state didn't
			// transition (state stayed 'open' because we never called
			// Store.Fire after the sink failure).
			log.Printf("[reminders] dispatcher: fire id=%s: %v", r.ID, err)
			continue
		}
	}
	return nil
}

// internalReminderJobID is the registered ID for the dispatcher's
// internal job. Canonical-ref §6.3 lists the exact string.
const internalReminderJobID = "internal.reminder_dispatch"

// Register binds the Dispatcher to A-B1's scheduler as the
// internal.reminder_dispatch internal job (canonical §6.3). Cadence is
// driven by daemon.reminders.dispatch_interval_seconds (canonical §4.4;
// default 30s — UX-precision knob for minute-resolution user reminders
// per Leon-brainstorm-Q3.3). Separate from the 15-min stalled-sweep
// cadence (stability knob); coupling them would defeat the Q3.3
// minute-resolution contract.
//
// PANICS on duplicate / bad ID / missing internal prefix — matches
// scheduler.RegisterInternal's "programmer error at boot, fail
// loudly" contract (spec §5.3, brainstorm Q1 invariant).
//
// intervalSeconds must be > 0. Callers should clamp to the canonical
// 30s default if their config returns 0.
func (d *Dispatcher) Register(s *scheduler.Scheduler, intervalSeconds int) {
	if intervalSeconds <= 0 {
		panic(fmt.Sprintf("reminders.Dispatcher.Register: intervalSeconds must be > 0, got %d", intervalSeconds))
	}
	s.RegisterInternal(
		internalReminderJobID,
		fmt.Sprintf("@every %ds", intervalSeconds),
		scheduler.InternalOpts{
			// RunAtStart=false: a fresh daemon boot doesn't need to
			// fire all reminders immediately; the cadence picks them
			// up within one tick (≤30s by default).
			RunAtStart: false,
			// CatchUp="skip" (default): if the daemon was down for
			// hours and accumulated missed ticks, skip past them —
			// a single Tick() call still scans DueOpen which catches
			// every overdue row in one pass. Re-firing every missed
			// tick would just churn the same rows multiple times.
			CatchUp: "skip",
		},
		&dispatcherHandler{d: d},
	)
}

// dispatcherHandler adapts Dispatcher to scheduler.Handler. The handler
// is stateless beyond its Dispatcher pointer; cadence + scheduling
// state lives in the scheduler's StateStore.
type dispatcherHandler struct{ d *Dispatcher }

// Dispatch is called once per scheduled tick. Reports running →
// completed (or failed) around a single Tick() call. The internal job
// has no observable stage progression beyond pruning vs idle, so we
// report exactly two transitions.
func (h *dispatcherHandler) Dispatch(
	ctx context.Context,
	_ scheduler.JobSpec,
	_ string,
	reporter scheduler.StateReporter,
	_ <-chan *scheduler.Completion,
) error {
	if err := reporter.Transition(scheduler.StateRunning, "scanning due reminders", nil); err != nil {
		return err
	}
	if err := h.d.Tick(ctx, time.Now().UTC()); err != nil {
		return reporter.Transition(scheduler.StateFailed, "dispatcher tick error: "+err.Error(), nil)
	}
	return reporter.Transition(scheduler.StateCompleted, "tick complete", nil)
}

// Reconcile reports completed for any non-terminal run found at boot.
// The dispatcher is idempotent — a tick that died mid-fire just leaves
// rows with state=open still due; the next tick picks them back up.
// Same shape as scheduler.CleanupHandler.
func (h *dispatcherHandler) Reconcile(_ context.Context, _ scheduler.JobSpec, _ string, _ scheduler.State) (scheduler.State, error) {
	return scheduler.StateCompleted, nil
}

// Stages declares the dispatcher's stage vocabulary. The dispatcher
// itself runs a single pass (no internal stage progression), but the
// scheduler API requires a non-empty map. A-B4 stalled-sweep keys off
// this vocabulary when deciding whether to nudge.
func (h *dispatcherHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"scanning": 5 * time.Minute}
}

// fireOne fires a single reminder and applies the appropriate state
// transition. Sink failure aborts the transition so the row re-fires on
// the next tick (at-least-once delivery semantics).
func (d *Dispatcher) fireOne(ctx context.Context, r *Reminder, now time.Time) error {
	if err := d.sink.Fire(ctx, r); err != nil {
		return fmt.Errorf("sink fire: %w", err)
	}
	switch r.TriggerKind {
	case TriggerTime:
		// One-shot terminal: state → fired, next_reminder_at NULL.
		return d.store.Fire(ctx, r.ID, now)
	case TriggerConditionPaneQuiet:
		// Recurring: state stays 'open' (Q3.4); next_reminder_at
		// advances per the rearm policy.
		next := d.rearm.NextAfter(r, now)
		return d.store.FireAndRearm(ctx, r.ID, now, next)
	default:
		return fmt.Errorf("unknown trigger_kind %q", r.TriggerKind)
	}
}
