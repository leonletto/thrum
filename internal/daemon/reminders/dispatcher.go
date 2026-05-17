package reminders

import (
	"context"
	"fmt"
	"log"
	"time"
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
