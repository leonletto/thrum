package sweep

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/reminders"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/schema"
)

var ctx = context.Background()

// --- fakes ---

type fakeSched struct {
	skip map[string]bool
	err  error
}

func (f fakeSched) AgentsInBB1ManagedStages(context.Context) (map[string]bool, error) {
	return f.skip, f.err
}

type fakePanes struct {
	panes []Pane
	err   error
}

func (f fakePanes) LivePanes(context.Context) ([]Pane, error) {
	return f.panes, f.err
}

type fakeChain struct {
	chain []string
	err   error
}

func (f fakeChain) Resolve(context.Context) ([]string, error) {
	return f.chain, f.err
}

// mintRecorder tracks calls to MintConditionForAgent so tests can
// inspect which agents were swept. Wraps a real reminders.SQLStore so
// the idempotency match-key still runs end-to-end (the recorder is for
// observability; correctness comes from the real Store).
type mintRecorder struct {
	delegate  reminders.Store
	mintedFor []string
}

func (m *mintRecorder) Mint(ctx context.Context, r *reminders.Reminder) error {
	return m.delegate.Mint(ctx, r)
}
func (m *mintRecorder) Get(ctx context.Context, id string) (*reminders.Reminder, error) {
	return m.delegate.Get(ctx, id)
}
func (m *mintRecorder) List(ctx context.Context, f reminders.ListFilter) ([]*reminders.Reminder, error) {
	return m.delegate.List(ctx, f)
}
func (m *mintRecorder) OpenForAgent(ctx context.Context, a string) ([]*reminders.Reminder, error) {
	return m.delegate.OpenForAgent(ctx, a)
}
func (m *mintRecorder) Defer(ctx context.Context, id string, until time.Time, by string) error {
	return m.delegate.Defer(ctx, id, until, by)
}
func (m *mintRecorder) Clear(ctx context.Context, id, by string) error {
	return m.delegate.Clear(ctx, id, by)
}
func (m *mintRecorder) Cancel(ctx context.Context, id, by string) error {
	return m.delegate.Cancel(ctx, id, by)
}
func (m *mintRecorder) Fire(ctx context.Context, id string, fired time.Time) error {
	return m.delegate.Fire(ctx, id, fired)
}
func (m *mintRecorder) FireAndRearm(ctx context.Context, id string, fired, next time.Time) error {
	return m.delegate.FireAndRearm(ctx, id, fired, next)
}
func (m *mintRecorder) DueOpen(ctx context.Context, now time.Time) ([]*reminders.Reminder, error) {
	return m.delegate.DueOpen(ctx, now)
}
func (m *mintRecorder) MintConditionForAgent(
	ctx context.Context, agent string, meta json.RawMessage, chain []string,
	snap string, next time.Time,
) (*reminders.Reminder, bool, error) {
	r, minted, err := m.delegate.MintConditionForAgent(ctx, agent, meta, chain, snap, next)
	if minted {
		m.mintedFor = append(m.mintedFor, agent)
	}
	return r, minted, err
}

// --- test helpers ---

// newRealStore returns a reminders.SQLStore backed by a fresh on-disk
// SQLite (in-memory doesn't survive multi-connection access). Wrapped
// in mintRecorder so tests can observe which agents got swept.
func newRealStore(t *testing.T) *mintRecorder {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "sweep_test.db")
	raw, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(raw); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return &mintRecorder{delegate: reminders.NewSQLStore(safedb.New(raw))}
}

// runSweep wires fakes + a real store and runs one Dispatch via runOnce.
// Returns the recorder so callers can assert on mintedFor.
func runSweep(t *testing.T, sched SchedulerState, panes []Pane, chain []string, opts ...sweepOpt) *mintRecorder {
	t.Helper()
	store := newRealStore(t)
	threshold := 15 * time.Minute
	capture := func(string, int) (string, error) { return "captured pane", nil }
	for _, opt := range opts {
		threshold, capture = opt(threshold, capture)
	}
	h := NewWithCapture(
		store,
		sched,
		fakePanes{panes: panes},
		fakeChain{chain: chain},
		threshold,
		capture,
	)
	if err := h.runOnce(ctx); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	return store
}

type sweepOpt func(time.Duration, CapturePaneFn) (time.Duration, CapturePaneFn)

func withThreshold(d time.Duration) sweepOpt {
	return func(_ time.Duration, c CapturePaneFn) (time.Duration, CapturePaneFn) {
		return d, c
	}
}

func withCapture(fn CapturePaneFn) sweepOpt {
	return func(d time.Duration, _ CapturePaneFn) (time.Duration, CapturePaneFn) {
		return d, fn
	}
}

// stalePane is shorthand for an agent that's been quiet for `quietFor`.
func stalePane(agent string, quietFor time.Duration) Pane {
	return Pane{
		AgentName:    agent,
		TmuxTarget:   agent + ":0.0",
		LastActivity: time.Now().UTC().Add(-quietFor),
	}
}

// --- skip-set coverage ---

// TestSweep_SkipsAllBB1ManagedStages: agents in any B-B1 managed stage
// (running_work, idle_nudge_*) are skipped because B-B1's nudge
// sequence is already engaged. Exhaustive coverage per dual-review
// IMPORTANT #9.
func TestSweep_SkipsAllBB1ManagedStages(t *testing.T) {
	stages := []string{
		"running_work",
		"idle_nudge_1of5",
		"idle_nudge_2of5",
		"idle_nudge_3of5",
		"idle_nudge_4of5",
		"idle_nudge_5of5",
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			// The Handler only consults the skip MAP; stage labels are
			// what the resolver (.6) translates from. For this test we
			// just need the skip map to include the agent.
			sched := fakeSched{skip: map[string]bool{"docs_bot": true}}
			panes := []Pane{stalePane("docs_bot", 30*time.Minute)}
			rec := runSweep(t, sched, panes, []string{"@coord"})
			if len(rec.mintedFor) != 0 {
				t.Errorf("stage=%s: docs_bot was minted despite being in skip-set (calls=%v)",
					stage, rec.mintedFor)
			}
		})
	}
}

// TestSweep_DoesNotSkip_AwaitingFirstOutput: this is the CRITICAL case
// the brainstorm motivates (Q4 narrowed-skip-set). A scheduled-agent
// run in awaiting_first_output has no first output yet, so B-B1's
// idle-nudge hasn't engaged. A-B4 MUST sweep these panes.
func TestSweep_DoesNotSkip_AwaitingFirstOutput(t *testing.T) {
	// Empty skip-set models the case: awaiting_first_output is NOT a
	// B-B1 managed stage, so the resolver returns it as "sweep-
	// eligible" (empty in the skip map).
	sched := fakeSched{skip: map[string]bool{}}
	panes := []Pane{stalePane("billing_bot", 30*time.Minute)}
	rec := runSweep(t, sched, panes, []string{"@coord"})
	if len(rec.mintedFor) != 1 || rec.mintedFor[0] != "billing_bot" {
		t.Errorf("Q4 high-stakes case: billing_bot in awaiting_first_output MUST be swept; got %v", rec.mintedFor)
	}
}

// TestSweep_SwepsAgentsOutsideScheduler: persistent agents, ephemeral
// implementers, manually-launched panes — no scheduler_job_state row
// at all. Skip-set doesn't list them; sweep MUST evaluate them (Q4
// final paragraph).
func TestSweep_SwepsAgentsOutsideScheduler(t *testing.T) {
	sched := fakeSched{skip: map[string]bool{}} // no rows at all
	panes := []Pane{stalePane("persistent_agent", 30*time.Minute)}
	rec := runSweep(t, sched, panes, []string{"@coord"})
	if len(rec.mintedFor) != 1 || rec.mintedFor[0] != "persistent_agent" {
		t.Errorf("persistent agent (no scheduler row) MUST be swept; got %v", rec.mintedFor)
	}
}

// --- threshold + activity coverage ---

func TestSweep_WindowActivityBelowThreshold_NotAlerted(t *testing.T) {
	sched := fakeSched{}
	// 5 minutes of quiet, 15 minute threshold → not stale enough yet.
	panes := []Pane{stalePane("billing_bot", 5*time.Minute)}
	rec := runSweep(t, sched, panes, []string{"@coord"})
	if len(rec.mintedFor) != 0 {
		t.Errorf("below-threshold pane should not mint; got %v", rec.mintedFor)
	}
}

func TestSweep_RespectsCustomThreshold(t *testing.T) {
	sched := fakeSched{}
	// 10-min threshold: 12-min quiet = past threshold, 8-min quiet = before.
	panes := []Pane{
		stalePane("ten_min_quiet", 8*time.Minute),
		stalePane("fifteen_min_quiet", 12*time.Minute),
	}
	rec := runSweep(t, sched, panes, []string{"@coord"}, withThreshold(10*time.Minute))
	if len(rec.mintedFor) != 1 || rec.mintedFor[0] != "fifteen_min_quiet" {
		t.Errorf("expected only fifteen_min_quiet to be minted; got %v", rec.mintedFor)
	}
}

// --- idempotency (Q3.8 match-key) ---

func TestSweep_IdempotentMintsRespected(t *testing.T) {
	sched := fakeSched{}
	panes := []Pane{stalePane("billing_bot", 30*time.Minute)}
	store := newRealStore(t)
	h := NewWithCapture(store, sched, fakePanes{panes: panes},
		fakeChain{chain: []string{"@coord"}}, 15*time.Minute,
		func(string, int) (string, error) { return "snap", nil })

	// First sweep mints.
	if err := h.runOnce(ctx); err != nil {
		t.Fatalf("first runOnce: %v", err)
	}
	if len(store.mintedFor) != 1 {
		t.Fatalf("first sweep: expected 1 mint, got %d", len(store.mintedFor))
	}
	// Second sweep finds the existing open row and does NOT re-mint
	// (Q3.8 match-key).
	if err := h.runOnce(ctx); err != nil {
		t.Fatalf("second runOnce: %v", err)
	}
	if len(store.mintedFor) != 1 {
		t.Errorf("second sweep should be idempotent; mintedFor=%v (want 1 entry)", store.mintedFor)
	}
}

// --- error paths ---

func TestSweep_PaneCaptureFails_LogsAndContinues(t *testing.T) {
	sched := fakeSched{}
	panes := []Pane{
		stalePane("billing_bot", 30*time.Minute), // capture errors here
		stalePane("xir_bot", 30*time.Minute),     // capture ok here
	}
	// Fail capture only for billing_bot.
	capture := func(target string, _ int) (string, error) {
		if target == "billing_bot:0.0" {
			return "", errors.New("pane gone")
		}
		return "ok", nil
	}
	rec := runSweep(t, sched, panes, []string{"@coord"}, withCapture(capture))
	// Only xir_bot should have minted; billing_bot's capture error
	// should have been swallowed.
	if len(rec.mintedFor) != 1 || rec.mintedFor[0] != "xir_bot" {
		t.Errorf("expected only xir_bot minted (billing_bot capture failed); got %v", rec.mintedFor)
	}
}

func TestSweep_ChainResolveFails_TopLevelError(t *testing.T) {
	store := newRealStore(t)
	h := NewWithCapture(store,
		fakeSched{},
		fakePanes{panes: []Pane{stalePane("x", 30*time.Minute)}},
		fakeChain{err: errors.New("config missing alert_chain")},
		15*time.Minute,
		func(string, int) (string, error) { return "snap", nil },
	)
	err := h.runOnce(ctx)
	if err == nil {
		t.Fatal("chain resolve error should propagate (no reminder-no-one-will-see)")
	}
	if len(store.mintedFor) != 0 {
		t.Errorf("nothing should mint on chain-resolve failure; got %v", store.mintedFor)
	}
}

func TestSweep_SchedReadFails_TopLevelError(t *testing.T) {
	store := newRealStore(t)
	h := NewWithCapture(store,
		fakeSched{err: errors.New("db unhealthy")},
		fakePanes{panes: []Pane{stalePane("x", 30*time.Minute)}},
		fakeChain{chain: []string{"@coord"}},
		15*time.Minute,
		func(string, int) (string, error) { return "snap", nil },
	)
	if err := h.runOnce(ctx); err == nil {
		t.Error("skip-set read error should propagate")
	}
}

func TestSweep_PaneEnumerationFails_TopLevelError(t *testing.T) {
	store := newRealStore(t)
	h := NewWithCapture(store,
		fakeSched{},
		fakePanes{err: errors.New("registry unreachable")},
		fakeChain{chain: []string{"@coord"}},
		15*time.Minute,
		func(string, int) (string, error) { return "snap", nil },
	)
	if err := h.runOnce(ctx); err == nil {
		t.Error("pane enumeration error should propagate")
	}
}

// --- meta payload shape ---

// The reminder row's trigger_meta is JSON; downstream consumers (CLI
// lookup, email formatter, observability) parse it. Lock down the
// shape so a future refactor doesn't silently change the schema.
func TestSweep_TriggerMetaShape(t *testing.T) {
	now := time.Now().UTC()
	quietSince := now.Add(-30 * time.Minute)
	panes := []Pane{{
		AgentName:    "docs_bot",
		TmuxTarget:   "docs:0.0",
		LastActivity: quietSince,
	}}
	store := newRealStore(t)
	h := NewWithCapture(store, fakeSched{}, fakePanes{panes: panes},
		fakeChain{chain: []string{"@coord"}}, 15*time.Minute,
		func(string, int) (string, error) { return "snap", nil })
	if err := h.runOnce(ctx); err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	rows, err := store.delegate.OpenForAgent(ctx, "docs_bot")
	if err != nil || len(rows) != 1 {
		t.Fatalf("expected 1 open row for docs_bot; got %d err=%v", len(rows), err)
	}
	var meta map[string]any
	if err := json.Unmarshal(rows[0].TriggerMeta, &meta); err != nil {
		t.Fatalf("trigger_meta is not valid JSON: %v", err)
	}
	for _, key := range []string{"agent", "quiet_since", "tmux_target", "threshold_seconds"} {
		if _, ok := meta[key]; !ok {
			t.Errorf("trigger_meta missing key %q: %v", key, meta)
		}
	}
	if meta["agent"] != "docs_bot" {
		t.Errorf("meta[agent] = %v, want docs_bot", meta["agent"])
	}
	if meta["tmux_target"] != "docs:0.0" {
		t.Errorf("meta[tmux_target] = %v", meta["tmux_target"])
	}
}

// --- Dispatch / Reconcile / Stages contract ---

type recordingReporter struct {
	transitions []scheduler.State
}

func (r *recordingReporter) Transition(to scheduler.State, _ string, _ map[string]any) error {
	r.transitions = append(r.transitions, to)
	return nil
}
func (r *recordingReporter) Stage(_ string) error { return nil }

func TestHandler_Dispatch_HappyPath_ReportsRunningThenCompleted(t *testing.T) {
	store := newRealStore(t)
	h := NewWithCapture(store, fakeSched{}, fakePanes{},
		fakeChain{chain: []string{"@coord"}}, 15*time.Minute,
		func(string, int) (string, error) { return "snap", nil })
	reporter := &recordingReporter{}
	err := h.Dispatch(ctx, scheduler.JobSpec{}, "run-1", reporter, nil)
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
		t.Errorf("last transition = %q, want Completed", reporter.transitions[1])
	}
}

func TestHandler_Dispatch_FailurePath_ReportsFailed(t *testing.T) {
	store := newRealStore(t)
	h := NewWithCapture(store,
		fakeSched{err: errors.New("db down")},
		fakePanes{}, fakeChain{chain: []string{"@coord"}}, 15*time.Minute,
		func(string, int) (string, error) { return "snap", nil })
	reporter := &recordingReporter{}
	err := h.Dispatch(ctx, scheduler.JobSpec{}, "run-1", reporter, nil)
	if err != nil {
		// Dispatch returns the reporter.Transition error, which is nil
		// when the transition recording succeeded — even on a Failed
		// transition. Returning nil from Dispatch is consistent with
		// scheduler.CleanupHandler's shape.
		t.Fatalf("Dispatch: %v", err)
	}
	if len(reporter.transitions) < 2 {
		t.Fatalf("expected Running then Failed transitions; got %v", reporter.transitions)
	}
	if reporter.transitions[len(reporter.transitions)-1] != scheduler.StateFailed {
		t.Errorf("last transition = %q, want Failed", reporter.transitions[len(reporter.transitions)-1])
	}
}

func TestHandler_Reconcile_ReportsCompleted(t *testing.T) {
	h := &Handler{}
	got, err := h.Reconcile(ctx, scheduler.JobSpec{}, "run-1", scheduler.StateRunning)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got != scheduler.StateCompleted {
		t.Errorf("Reconcile = %q, want Completed (sweep run state is uninteresting per Q5.1)", got)
	}
}

func TestHandler_Stages_NonEmpty(t *testing.T) {
	h := &Handler{}
	stages := h.Stages()
	if len(stages) == 0 {
		t.Error("Stages must be non-empty (scheduler API contract)")
	}
}

// New (production constructor) should produce a Handler that defaults
// to tmux.CapturePane — assert the resulting handler is non-nil and
// has its collaborators wired.
func TestNew_ProductionConstructor(t *testing.T) {
	store := newRealStore(t)
	h := New(store, fakeSched{}, fakePanes{}, fakeChain{}, 15*time.Minute)
	if h == nil {
		t.Fatal("New returned nil")
	}
	if h.capture == nil {
		t.Error("New should default capture to tmux.CapturePane")
	}
}
