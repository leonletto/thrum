package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// fakeFireSink records every Fire call and optionally returns an error.
type fakeFireSink struct {
	calls []*Reminder
	err   error
}

func (f *fakeFireSink) Fire(_ context.Context, r *Reminder) error {
	f.calls = append(f.calls, r)
	return f.err
}

// constInterval is a deterministic ReArmPolicy for tests.
type constInterval struct{ d time.Duration }

func (c constInterval) NextAfter(_ *Reminder, fired time.Time) time.Time {
	return fired.Add(c.d)
}

func TestDispatcher_TickFiresDueOpenReminders(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	past := now.Add(-5 * time.Minute)
	r := &Reminder{
		Source: SourceAgent, TriggerKind: TriggerTime, SourceAgent: "a",
		TriggerAt: &past, TargetAgent: "a", Body: "past",
	}
	if err := s.Mint(ctx, r); err != nil {
		t.Fatalf("mint: %v", err)
	}
	sink := &fakeFireSink{}
	d := NewDispatcher(s, sink, constInterval{d: time.Hour})
	if err := d.Tick(ctx, now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(sink.calls) != 1 || sink.calls[0].ID != r.ID {
		t.Fatalf("sink calls = %+v, want one with id %s", sink.calls, r.ID)
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateFired {
		t.Errorf("State = %q (want fired)", got.State)
	}
	if got.LastFiredAt == nil {
		t.Error("LastFiredAt should be populated post-Tick")
	}
}

func TestDispatcher_ConditionTriggeredRearmsOpen(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	past := now.Add(-1 * time.Minute)
	r := &Reminder{
		Source:         SourceDaemon,
		TriggerKind:    TriggerConditionPaneQuiet,
		TriggerMeta:    json.RawMessage(`{"agent":"docs_bot"}`),
		TargetChain:    []string{"@coord"},
		PaneSnapshot:   "snap",
		TargetAgent:    "docs_bot",
		NextReminderAt: &past,
	}
	if err := s.Mint(ctx, r); err != nil {
		t.Fatalf("mint: %v", err)
	}
	sink := &fakeFireSink{}
	d := NewDispatcher(s, sink, constInterval{d: 15 * time.Minute})
	if err := d.Tick(ctx, now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(sink.calls) != 1 {
		t.Fatalf("sink calls = %d, want 1", len(sink.calls))
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateOpen {
		t.Errorf("State = %q (want open per Q3.4)", got.State)
	}
	if got.LastFiredAt == nil || !got.LastFiredAt.Equal(now.Truncate(time.Second)) {
		t.Errorf("LastFiredAt = %v, want %v", got.LastFiredAt, now)
	}
	wantNext := now.Add(15 * time.Minute).Truncate(time.Second)
	if got.NextReminderAt == nil || !got.NextReminderAt.Equal(wantNext) {
		t.Errorf("NextReminderAt = %v, want %v", got.NextReminderAt, wantNext)
	}
}

func TestDispatcher_SkipsNotDueYet(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	future := now.Add(2 * time.Hour)
	r := &Reminder{
		Source: SourceAgent, TriggerKind: TriggerTime, SourceAgent: "a",
		TriggerAt: &future, TargetAgent: "a", Body: "future",
	}
	if err := s.Mint(ctx, r); err != nil {
		t.Fatalf("mint: %v", err)
	}
	sink := &fakeFireSink{}
	d := NewDispatcher(s, sink, constInterval{d: time.Hour})
	if err := d.Tick(ctx, now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(sink.calls) != 0 {
		t.Errorf("sink calls = %d, want 0 (row not due)", len(sink.calls))
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateOpen {
		t.Errorf("State = %q (want still open)", got.State)
	}
}

func TestDispatcher_SkipsTerminalStates(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	past := now.Add(-1 * time.Hour)
	r := &Reminder{
		Source: SourceAgent, TriggerKind: TriggerTime, SourceAgent: "a",
		TriggerAt: &past, TargetAgent: "a", Body: "past",
	}
	if err := s.Mint(ctx, r); err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := s.Clear(ctx, r.ID, "leon"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	sink := &fakeFireSink{}
	d := NewDispatcher(s, sink, constInterval{d: time.Hour})
	if err := d.Tick(ctx, now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(sink.calls) != 0 {
		t.Errorf("sink calls = %d, want 0 (row cleared)", len(sink.calls))
	}
}

// Sink errors must not poison the batch — remaining rows still fire.
// Row whose sink errored stays open so the next tick retries (at-least-
// once delivery semantics).
func TestDispatcher_SinkErrorContinuesBatch(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	past := now.Add(-1 * time.Hour)

	for _, body := range []string{"first", "second", "third"} {
		r := &Reminder{
			Source: SourceAgent, TriggerKind: TriggerTime, SourceAgent: "a",
			TriggerAt: &past, TargetAgent: "a", Body: body,
		}
		if err := s.Mint(ctx, r); err != nil {
			t.Fatalf("mint %s: %v", body, err)
		}
	}
	sink := &fakeFireSink{err: errors.New("sink down")}
	d := NewDispatcher(s, sink, constInterval{d: time.Hour})
	if err := d.Tick(ctx, now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(sink.calls) != 3 {
		t.Errorf("sink calls = %d, want all 3", len(sink.calls))
	}
	// All three rows must still be open — sink errors aborted the
	// state transition so they re-fire next tick.
	srcAgent := SourceAgent
	open, err := s.List(ctx, ListFilter{Source: &srcAgent, State: stateRef(StateOpen)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(open) != 3 {
		t.Errorf("open rows post-Tick = %d, want 3 (none fired due to sink error)", len(open))
	}
}

func TestDispatcher_EmptyDueOpen(t *testing.T) {
	s := newTestStore(t)
	sink := &fakeFireSink{}
	d := NewDispatcher(s, sink, constInterval{d: time.Hour})
	if err := d.Tick(ctx, time.Now()); err != nil {
		t.Errorf("Tick on empty store: %v", err)
	}
	if len(sink.calls) != 0 {
		t.Errorf("sink calls = %d, want 0", len(sink.calls))
	}
}

// NoopFireSink is the production placeholder used until E4.5 Task 25
// swaps in DeliverySink. Asserting it returns nil keeps that contract
// explicit so a future "useful default" change doesn't silently break
// the dispatcher's expected sink shape.
func TestNoopFireSink_ReturnsNil(t *testing.T) {
	err := NoopFireSink{}.Fire(ctx, &Reminder{ID: "test"})
	if err != nil {
		t.Errorf("NoopFireSink.Fire returned %v, want nil", err)
	}
}

// SweepInterval is the production ReArmPolicy. Test asserts the arithmetic
// matches the documented "fired + Interval" formula.
func TestSweepInterval_NextAfter(t *testing.T) {
	now := time.Now().UTC()
	p := SweepInterval{Interval: 15 * time.Minute}
	got := p.NextAfter(&Reminder{}, now)
	want := now.Add(15 * time.Minute)
	if !got.Equal(want) {
		t.Errorf("NextAfter = %v, want %v", got, want)
	}
}

func stateRef(s State) *State { return &s }
