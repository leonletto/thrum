package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/schema"
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

// --- scheduler.RegisterInternal binding (thrum-6qmf.3.27) ---

// newSchedulerForTest sets up a real A-B1 scheduler.Scheduler backed by an
// on-disk SQLite DB (in-memory doesn't survive multi-connection access).
// Returns the scheduler + a cleanup that Stops it.
func newSchedulerForTest(t *testing.T) *scheduler.Scheduler {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "scheduler.db")
	raw, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(raw); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	s := scheduler.New(scheduler.Config{
		DB:       safedb.New(raw),
		DaemonID: "test-daemon",
	})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	return s
}

func TestDispatcher_RegistersInternalJob(t *testing.T) {
	s := newSchedulerForTest(t)
	store := newTestStore(t)
	d := NewDispatcher(store, NoopFireSink{}, SweepInterval{Interval: 15 * time.Minute})
	d.Register(s, 30)

	spec, ok := s.JobSpec(internalReminderJobID)
	if !ok {
		t.Fatalf("JobSpec(%q) not found after Register", internalReminderJobID)
	}
	if spec.ID != internalReminderJobID {
		t.Errorf("spec.ID = %q, want %q", spec.ID, internalReminderJobID)
	}
	if spec.Type != "internal" {
		t.Errorf("spec.Type = %q, want internal", spec.Type)
	}
	if spec.Schedule != "@every 30s" {
		t.Errorf("spec.Schedule = %q, want '@every 30s'", spec.Schedule)
	}
	if !spec.Enabled {
		t.Error("internal job should be enabled by default")
	}
	if spec.CatchUp != "skip" {
		t.Errorf("spec.CatchUp = %q, want 'skip' (missed-tick storm prevention)", spec.CatchUp)
	}
	if spec.RunAtStart {
		t.Error("RunAtStart should be false (fresh boot waits for first tick rather than firing all reminders at once)")
	}
}

func TestDispatcher_Register_RejectsNonPositiveInterval(t *testing.T) {
	s := newSchedulerForTest(t)
	store := newTestStore(t)
	d := NewDispatcher(store, NoopFireSink{}, SweepInterval{Interval: 15 * time.Minute})

	for _, badInterval := range []int{0, -1, -30} {
		t.Run("interval", func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("expected panic for intervalSeconds=%d", badInterval)
				}
			}()
			d.Register(s, badInterval)
		})
	}
}

func TestDispatcher_Register_DuplicatePanics(t *testing.T) {
	s := newSchedulerForTest(t)
	store := newTestStore(t)
	d := NewDispatcher(store, NoopFireSink{}, SweepInterval{Interval: 15 * time.Minute})
	d.Register(s, 30)
	defer func() {
		if r := recover(); r == nil {
			t.Error("second Register should panic (scheduler.RegisterInternal panics on duplicate)")
		}
	}()
	d.Register(s, 30)
}

// dispatcherHandler interface implementation — direct unit tests on the
// adapter rather than going through the scheduler's run lifecycle.
// recordingReporter captures Transition calls.
type recordingReporter struct {
	transitions []scheduler.State
	reasons     []string
}

func (r *recordingReporter) Transition(to scheduler.State, reason string, _ map[string]any) error {
	r.transitions = append(r.transitions, to)
	r.reasons = append(r.reasons, reason)
	return nil
}
func (r *recordingReporter) Stage(_ string) error { return nil }

func TestDispatcherHandler_Dispatch_HappyPath(t *testing.T) {
	store := newTestStore(t)
	d := NewDispatcher(store, NoopFireSink{}, SweepInterval{Interval: 15 * time.Minute})
	h := &dispatcherHandler{d: d}
	reporter := &recordingReporter{}

	err := h.Dispatch(ctx, scheduler.JobSpec{ID: internalReminderJobID}, "run-1", reporter, nil)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(reporter.transitions) != 2 {
		t.Fatalf("transitions = %v, want [Running, Completed]", reporter.transitions)
	}
	if reporter.transitions[0] != scheduler.StateRunning {
		t.Errorf("first transition = %q, want Running", reporter.transitions[0])
	}
	if reporter.transitions[1] != scheduler.StateCompleted {
		t.Errorf("second transition = %q, want Completed", reporter.transitions[1])
	}
}

func TestDispatcherHandler_Stages_NonEmpty(t *testing.T) {
	h := &dispatcherHandler{}
	stages := h.Stages()
	if len(stages) == 0 {
		t.Error("Stages() must be non-empty (scheduler API contract)")
	}
}

func TestDispatcherHandler_Reconcile_ReportsCompleted(t *testing.T) {
	h := &dispatcherHandler{}
	got, err := h.Reconcile(ctx, scheduler.JobSpec{}, "run-1", scheduler.StateRunning)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got != scheduler.StateCompleted {
		t.Errorf("Reconcile state = %q, want Completed (idempotent — next tick picks up any rows that didn't transition)", got)
	}
}
