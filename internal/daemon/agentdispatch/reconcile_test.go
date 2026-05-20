package agentdispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/worktree"
)

// reconJournalStub is a JournalReader fake scoped to reconcile tests.
// Tests stuff `events` with the per-stage transitions that drive the
// row-class branching; the NonTerminalWorktrees side is exercised
// in B2's sweep tests and stays empty here.
type reconJournalStub struct {
	events    []scheduler.Event
	eventsErr error

	// referenced + referencedErr drive NonTerminalWorktrees for
	// B2's SweepOrphans tests. B1's row-class tests leave both
	// zero so NonTerminalWorktrees yields (nil, nil) — semantically
	// equivalent to "no journal references".
	referenced    map[string]bool
	referencedErr error
}

func (j *reconJournalStub) EventsForRun(_ context.Context, _ string) ([]scheduler.Event, error) {
	return j.events, j.eventsErr
}

func (j *reconJournalStub) NonTerminalWorktrees(_ context.Context) (map[string]bool, error) {
	// Return nil rather than an empty map per B1 review MINOR M2;
	// callers indexing into a nil map still get the zero-value
	// (false) so the sweep treats "no refs" identically.
	return j.referenced, j.referencedErr
}

// reconTmuxStub answers CheckPane only — all other TmuxRPC methods
// panic so a test that accidentally exercises them surfaces loudly.
// The Reconciler only ever calls CheckPane.
type reconTmuxStub struct {
	alive     bool
	checkErr  error
	checkArg  string
	checkSeen bool
}

func (s *reconTmuxStub) CheckPane(_ context.Context, target string) (bool, error) {
	s.checkArg = target
	s.checkSeen = true
	return s.alive, s.checkErr
}

func (s *reconTmuxStub) TmuxCreate(_ context.Context, _ string, _ TmuxCreateOpts) error {
	panic("reconTmuxStub: TmuxCreate not used in reconcile tests")
}

func (s *reconTmuxStub) TmuxLaunch(_ context.Context, _ string) error {
	panic("reconTmuxStub: TmuxLaunch not used in reconcile tests")
}

func (s *reconTmuxStub) WaitForPaneReady(_ context.Context, _ string) error {
	panic("reconTmuxStub: WaitForPaneReady not used in reconcile tests")
}

func (s *reconTmuxStub) TmuxKillSession(_ context.Context, _ string) error {
	panic("reconTmuxStub: TmuxKillSession not used in reconcile tests")
}

func (s *reconTmuxStub) PaneSendCtrlCExit(_ context.Context, _ string) error {
	panic("reconTmuxStub: PaneSendCtrlCExit not used in reconcile tests")
}

func (s *reconTmuxStub) PaneInjectPrompt(_ context.Context, _, _ string) error {
	panic("reconTmuxStub: PaneInjectPrompt not used in reconcile tests")
}

// reconWorktreeStub answers Destroy; Create panics. Per
// thrum-6qmf.4.92: this stub is intentionally NOT consolidated with
// stubWorktreeMgr (scheduled_agent_test.go) because the two test
// files live in different packages — reconcile_test.go is `package
// agentdispatch` (needs internal access to BootReconciler.pathExists
// + nowFn fields), while scheduled_agent_test.go is `package
// agentdispatch_test` (external/black-box). A shared stub would
// require either (a) capitalizing identifiers + moving to a non-
// test file, or (b) restructuring one test file's package
// declaration — both heavier than the duplication being eliminated.
// The Create-panic safety net pins "Reconciler never creates
// worktrees"; an accidental Create() invocation during refactor
// surfaces immediately rather than silently passing a happy-path
// Create through.
type reconWorktreeStub struct {
	destroyCalls []worktree.DestroyOpts
	destroyErr   error
}

func (s *reconWorktreeStub) Create(_ context.Context, _ worktree.CreateOpts) (*worktree.CreateResult, error) {
	panic("reconWorktreeStub: Create not used in reconcile tests — see type docstring for the consolidation rationale (thrum-6qmf.4.92)")
}

func (s *reconWorktreeStub) Destroy(_ context.Context, opts worktree.DestroyOpts) (*worktree.DestroyResult, error) {
	s.destroyCalls = append(s.destroyCalls, opts)
	return &worktree.DestroyResult{}, s.destroyErr
}

// reconcileFixture wires a BootReconciler against fresh stubs +
// returns the stubs so tests can stuff inputs and assert outputs.
// reposPath, agentName, pathExistsAnswer + clock are operator knobs;
// later tests will rerun this with different overrides.
type reconcileFixture struct {
	journal       *reconJournalStub
	tmux          *reconTmuxStub
	wt            *reconWorktreeStub
	lifecycle     *stubLifecycleStore
	reconciler    *BootReconciler
	pathAnswer    map[string]bool
	pathQueriedAt []string
	now           time.Time
}

func newReconcileFixture(t *testing.T) *reconcileFixture {
	t.Helper()
	f := &reconcileFixture{
		journal:    &reconJournalStub{},
		tmux:       &reconTmuxStub{},
		wt:         &reconWorktreeStub{},
		lifecycle:  &stubLifecycleStore{},
		pathAnswer: map[string]bool{},
		now:        time.Unix(1747353600, 0).UTC(),
	}
	f.reconciler = NewBootReconciler(
		"/repo",
		f.tmux,
		f.wt,
		f.journal,
		f.lifecycle,
	)
	// Replace os.Stat with a map lookup so tests don't touch the
	// filesystem. Default-zero (false) means "path missing".
	f.reconciler.pathExists = func(p string) bool {
		f.pathQueriedAt = append(f.pathQueriedAt, p)
		return f.pathAnswer[p]
	}
	f.reconciler.nowFn = func() time.Time { return f.now }
	return f
}

// stage3CompleteEvent journals "stage 3 complete" with worktree_path
// and branch_name — the canonical post-Create record set the
// scheduled-agent dispatch protocol writes after worktree.Create
// + EnsureMirrored both succeed.
func stage3CompleteEvent(t0 time.Time, runID, worktreePath, branchName string) scheduler.Event {
	return scheduler.Event{
		JobID: "docs_bot_job", RunID: runID,
		EventTime: t0.Add(time.Second),
		FromState: scheduler.StateRunning,
		ToState:   scheduler.StateRunning,
		Reason:    "stage 3 complete",
		Details: map[string]any{
			"worktree_path": worktreePath,
			"branch_name":   branchName,
		},
	}
}

// stage4CompleteEvent journals "stage 4 complete" with tmux session.
func stage4CompleteEvent(t0 time.Time, runID, tmuxSession string) scheduler.Event {
	return scheduler.Event{
		JobID: "docs_bot_job", RunID: runID,
		EventTime: t0.Add(2 * time.Second),
		FromState: scheduler.StateRunning,
		ToState:   scheduler.StateRunning,
		Reason:    "stage 4 complete",
		Details: map[string]any{
			"tmux_session_name": tmuxSession,
		},
	}
}

// scheduledAgentJobSpec returns a minimal JobSpec with ScheduledAgent
// populated so the lifecycle-event path (spec §7.7 row 5) has an
// AgentName key to record under.
func scheduledAgentJobSpec(target string) scheduler.JobSpec {
	return scheduler.JobSpec{
		ID:   "docs_bot_job",
		Type: "scheduled_agent",
		ScheduledAgent: &scheduler.ScheduledAgentSpec{
			Target: target,
		},
	}
}

// TestReconcileRun_Row1_NoWorktreeJournaled_RollsBackToScheduled
// covers spec §7.7 row 1: no `worktree_path` recorded yet. Daemon
// died between stage 1 (dispatch) and stage 3 (worktree create)
// before the worktree directory existed. Result: roll back to
// scheduled with nothing on disk to clean.
func TestReconcileRun_Row1_NoWorktreeJournaled_RollsBackToScheduled(t *testing.T) {
	f := newReconcileFixture(t)
	// Only a stage-1-ish dispatch event with no worktree_path.
	f.journal.events = []scheduler.Event{
		{
			JobID: "docs_bot_job", RunID: "r1",
			EventTime: f.now,
			ToState:   scheduler.StateDispatched,
			Reason:    "tick fired",
		},
	}

	newState, err := f.reconciler.ReconcileRun(context.Background(),
		scheduledAgentJobSpec("docs_bot"), "r1", scheduler.StateDispatched)
	if err != nil {
		t.Fatalf("ReconcileRun: %v", err)
	}
	if newState != scheduler.StateScheduled {
		t.Errorf("newState = %q, want %q", newState, scheduler.StateScheduled)
	}
	if len(f.wt.destroyCalls) != 0 {
		t.Errorf("Destroy called %d times, want 0 (no worktree journaled)", len(f.wt.destroyCalls))
	}
	if len(f.lifecycle.appended) != 0 {
		t.Errorf("lifecycle events appended %d, want 0 (row 1 is silent)", len(f.lifecycle.appended))
	}
	if f.tmux.checkSeen {
		t.Errorf("CheckPane called for row 1 (no tmux session journaled)")
	}
}

// TestReconcileRun_Row2_WorktreeGoneFromDisk_RollsBackAndJournalsDiscrepancy
// covers spec §7.7 row 2: `worktree_path` journaled but the directory
// is gone from disk (operator hand-cleanup, partition unmount,
// pre-existing daemon crash already cleaned). Result: roll back
// without attempting Destroy + append ONE reconcile_worktree_discrepancy
// lifecycle event for operator visibility. Pins thrum-6qmf.4.91: the
// distinct event kind synthesizes B1's option-B interpretation
// (operator-cleanup is not a crash) with the spec's
// 'journal the discrepancy' clause — operators see the row in
// `thrum team --journal` without it misclassifying as crash_detected.
func TestReconcileRun_Row2_WorktreeGoneFromDisk_RollsBackAndJournalsDiscrepancy(t *testing.T) {
	f := newReconcileFixture(t)
	f.journal.events = []scheduler.Event{
		stage3CompleteEvent(f.now, "r1", "/gone/wt1", "agent/x/job-r1"),
	}
	// pathAnswer["/gone/wt1"] defaults to false → "missing".

	newState, err := f.reconciler.ReconcileRun(context.Background(),
		scheduledAgentJobSpec("docs_bot"), "r1", scheduler.StateRunning)
	if err != nil {
		t.Fatalf("ReconcileRun: %v", err)
	}
	if newState != scheduler.StateScheduled {
		t.Errorf("newState = %q, want %q", newState, scheduler.StateScheduled)
	}
	if len(f.wt.destroyCalls) != 0 {
		t.Errorf("Destroy called %d times, want 0 (worktree already gone)", len(f.wt.destroyCalls))
	}
	if !slices.Contains(f.pathQueriedAt, "/gone/wt1") {
		t.Errorf("pathExists not queried for /gone/wt1; queries: %v", f.pathQueriedAt)
	}

	// Exactly one reconcile_worktree_discrepancy event, NOT crash_detected.
	// Crash-misclassification is the failure mode the distinct event kind
	// exists to prevent (operator hand-cleanup is intentional, not a crash).
	if len(f.lifecycle.appended) != 1 {
		t.Fatalf("lifecycle events appended %d, want 1 (row 2 discrepancy)", len(f.lifecycle.appended))
	}
	ev := f.lifecycle.appended[0]
	if ev.EventKind != state.EventReconcileWorktreeDiscrepancy {
		t.Errorf("EventKind = %q, want %q",
			ev.EventKind, state.EventReconcileWorktreeDiscrepancy)
	}
	if ev.EventKind == state.EventCrashDetected {
		t.Error("Row 2 emitted crash_detected; must use distinct kind to avoid misclassifying operator cleanup")
	}
	if ev.AgentName != "docs_bot" {
		t.Errorf("AgentName = %q, want %q", ev.AgentName, "docs_bot")
	}
	if ev.DetectionMethod != "" {
		t.Errorf("DetectionMethod = %q, want empty (Row 2 is a reconciliation observation, not a detection)", ev.DetectionMethod)
	}

	// Details JSON shape per thrum-6qmf.4.91: reconciliation_row,
	// worktree_path, journal_state_before, detected_state, resolution.
	var details map[string]any
	if err := json.Unmarshal(ev.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v (raw: %s)", err, string(ev.Details))
	}
	wantFields := map[string]any{
		"reconciliation_row":   "Row 2",
		"worktree_path":        "/gone/wt1",
		"journal_state_before": "running",
		"detected_state":       "path-missing",
		"resolution":           "rolled-back-to-scheduled",
	}
	for k, want := range wantFields {
		if got, ok := details[k]; !ok {
			t.Errorf("details missing field %q (got: %+v)", k, details)
		} else if got != want {
			t.Errorf("details[%q] = %v, want %v", k, got, want)
		}
	}
}

// TestReconcileRun_Row3_WorktreeExistsNoTmux_DestroysAndRollsBack
// covers spec §7.7 row 3: stage 3 wrote worktree_path but daemon
// died before stage 4 wrote tmux_session_name. Worktree directory
// is intact (Create succeeded) but no tmux session exists.
// Result: destroy orphan + roll back.
func TestReconcileRun_Row3_WorktreeExistsNoTmux_DestroysAndRollsBack(t *testing.T) {
	f := newReconcileFixture(t)
	f.pathAnswer["/wt1"] = true
	f.journal.events = []scheduler.Event{
		stage3CompleteEvent(f.now, "r1", "/wt1", "agent/x/job-r1"),
	}

	newState, err := f.reconciler.ReconcileRun(context.Background(),
		scheduledAgentJobSpec("docs_bot"), "r1", scheduler.StateRunning)
	if err != nil {
		t.Fatalf("ReconcileRun: %v", err)
	}
	if newState != scheduler.StateScheduled {
		t.Errorf("newState = %q, want %q", newState, scheduler.StateScheduled)
	}
	if len(f.wt.destroyCalls) != 1 {
		t.Fatalf("Destroy called %d times, want 1", len(f.wt.destroyCalls))
	}
	got := f.wt.destroyCalls[0]
	if got.RepoPath != "/repo" {
		t.Errorf("Destroy RepoPath = %q, want /repo", got.RepoPath)
	}
	if got.WorktreePath != "/wt1" {
		t.Errorf("Destroy WorktreePath = %q, want /wt1", got.WorktreePath)
	}
	if got.Branch != "agent/x/job-r1" {
		t.Errorf("Destroy Branch = %q, want agent/x/job-r1", got.Branch)
	}
	if !got.Force {
		t.Errorf("Destroy Force = false, want true (reconcile may race operator cleanup)")
	}
	if len(f.lifecycle.appended) != 0 {
		t.Errorf("lifecycle events appended %d, want 0 (row 3 is silent)", len(f.lifecycle.appended))
	}
	if f.tmux.checkSeen {
		t.Errorf("CheckPane called for row 3 (no tmux session journaled)")
	}
}

// TestReconcileRun_Row4_PaneAlive_ResumesRunning covers spec §7.7 row 4:
// both worktree + tmux journaled, pane survived the restart. Result:
// keep state at running; stage-7 re-entry handles the idle-nudge loop.
func TestReconcileRun_Row4_PaneAlive_ResumesRunning(t *testing.T) {
	f := newReconcileFixture(t)
	f.pathAnswer["/wt1"] = true
	f.tmux.alive = true
	f.journal.events = []scheduler.Event{
		stage3CompleteEvent(f.now, "r1", "/wt1", "agent/x/job-r1"),
		stage4CompleteEvent(f.now, "r1", "docs_bot"),
	}

	newState, err := f.reconciler.ReconcileRun(context.Background(),
		scheduledAgentJobSpec("docs_bot"), "r1", scheduler.StateRunning)
	if err != nil {
		t.Fatalf("ReconcileRun: %v", err)
	}
	if newState != scheduler.StateRunning {
		t.Errorf("newState = %q, want %q", newState, scheduler.StateRunning)
	}
	if len(f.wt.destroyCalls) != 0 {
		t.Errorf("Destroy called %d times, want 0 (pane alive — do not clean)", len(f.wt.destroyCalls))
	}
	if len(f.lifecycle.appended) != 0 {
		t.Errorf("lifecycle events appended %d, want 0 (row 4 is silent)", len(f.lifecycle.appended))
	}
	if !f.tmux.checkSeen {
		t.Errorf("CheckPane was not called; pane-alive determination requires it")
	}
	if f.tmux.checkArg != "docs_bot" {
		t.Errorf("CheckPane target = %q, want docs_bot", f.tmux.checkArg)
	}
}

// TestReconcileRun_Row5_PaneGone_DestroysAndMarksFailed covers spec
// §7.7 row 5: worktree + tmux journaled, pane is gone after restart.
// Result: destroy worktree + branch, append crash_detected
// (detection=restart_reconciliation), return StateFailed so the
// scheduler increments consecutive_failures.
func TestReconcileRun_Row5_PaneGone_DestroysAndMarksFailed(t *testing.T) {
	f := newReconcileFixture(t)
	f.pathAnswer["/wt1"] = true
	f.tmux.alive = false
	f.journal.events = []scheduler.Event{
		stage3CompleteEvent(f.now, "r1", "/wt1", "agent/x/job-r1"),
		stage4CompleteEvent(f.now, "r1", "docs_bot"),
	}

	newState, err := f.reconciler.ReconcileRun(context.Background(),
		scheduledAgentJobSpec("docs_bot"), "r1", scheduler.StateRunning)
	if err != nil {
		t.Fatalf("ReconcileRun: %v", err)
	}
	if newState != scheduler.StateFailed {
		t.Errorf("newState = %q, want %q", newState, scheduler.StateFailed)
	}
	if len(f.wt.destroyCalls) != 1 {
		t.Fatalf("Destroy called %d times, want 1", len(f.wt.destroyCalls))
	}
	if got := f.wt.destroyCalls[0]; got.WorktreePath != "/wt1" || got.Branch != "agent/x/job-r1" || !got.Force {
		t.Errorf("Destroy opts = %+v, want WorktreePath=/wt1 Branch=agent/x/job-r1 Force=true", got)
	}
	if len(f.lifecycle.appended) != 1 {
		t.Fatalf("lifecycle events appended %d, want 1 (crash_detected)", len(f.lifecycle.appended))
	}
	ev := f.lifecycle.appended[0]
	if ev.AgentName != "docs_bot" {
		t.Errorf("AgentName = %q, want docs_bot", ev.AgentName)
	}
	if ev.EventKind != state.EventCrashDetected {
		t.Errorf("EventKind = %q, want %q", ev.EventKind, state.EventCrashDetected)
	}
	if ev.DetectionMethod != state.DetectionRestartReconciliation {
		t.Errorf("DetectionMethod = %q, want %q", ev.DetectionMethod, state.DetectionRestartReconciliation)
	}
	if !ev.EventTime.Equal(f.now) {
		t.Errorf("EventTime = %v, want injected now %v", ev.EventTime, f.now)
	}
	if ev.Reason == "" {
		t.Errorf("Reason is empty; want operator-facing pane-terminated string")
	}
}

// TestReconcileRun_CheckPaneError_PreservesLastState_NoDestroy pins
// the critical safety property called out by code-reviewer IMPORTANT
// #1: a transient tmux-RPC error (daemon not yet up, mux socket flaky)
// must NOT be misread as "pane gone" and trigger row-5's destroy
// path. The reconciler returns (lastState, wrapped err) so the
// scheduler logs the failure and re-attempts on a later pass; no
// worktree gets irreversibly destroyed and no crash_detected row
// gets falsely appended.
func TestReconcileRun_CheckPaneError_PreservesLastState_NoDestroy(t *testing.T) {
	f := newReconcileFixture(t)
	f.pathAnswer["/wt1"] = true
	f.tmux.checkErr = errors.New("tmux rpc timeout")
	f.journal.events = []scheduler.Event{
		stage3CompleteEvent(f.now, "r1", "/wt1", "agent/x/job-r1"),
		stage4CompleteEvent(f.now, "r1", "docs_bot"),
	}

	newState, err := f.reconciler.ReconcileRun(context.Background(),
		scheduledAgentJobSpec("docs_bot"), "r1", scheduler.StateRunning)
	if err == nil {
		t.Fatalf("expected wrapped CheckPane error, got nil")
	}
	if newState != scheduler.StateRunning {
		t.Errorf("newState = %q, want unchanged StateRunning (preserve lastState on RPC error)", newState)
	}
	if len(f.wt.destroyCalls) != 0 {
		t.Errorf("Destroy called %d times, want 0 (must not destroy live worktree on transient RPC error)", len(f.wt.destroyCalls))
	}
	if len(f.lifecycle.appended) != 0 {
		t.Errorf("crash_detected appended %d times, want 0 (no crash determination possible)", len(f.lifecycle.appended))
	}
}

// TestReconcileRun_Row5_LifecycleAppendError_StillTransitions verifies
// row 5 still returns StateFailed even if the lifecycle Append fails.
// The Destroy + state-transition halves of row 5 are independently
// observable and must not be coupled to a flaky lifecycle write.
// Tests the slog.Warn path; non-state-affecting failure surfaces in
// the daemon log rather than blocking the transition.
func TestReconcileRun_Row5_LifecycleAppendError_StillTransitions(t *testing.T) {
	f := newReconcileFixture(t)
	f.pathAnswer["/wt1"] = true
	f.tmux.alive = false
	f.lifecycle.appendErr = errors.New("db write failed")
	f.journal.events = []scheduler.Event{
		stage3CompleteEvent(f.now, "r1", "/wt1", "agent/x/job-r1"),
		stage4CompleteEvent(f.now, "r1", "docs_bot"),
	}

	newState, err := f.reconciler.ReconcileRun(context.Background(),
		scheduledAgentJobSpec("docs_bot"), "r1", scheduler.StateRunning)
	if err != nil {
		t.Fatalf("ReconcileRun: %v (lifecycle Append failure must not propagate)", err)
	}
	if newState != scheduler.StateFailed {
		t.Errorf("newState = %q, want StateFailed (transition is independent of lifecycle write)", newState)
	}
	if len(f.wt.destroyCalls) != 1 {
		t.Errorf("Destroy called %d times, want 1 (still cleans up)", len(f.wt.destroyCalls))
	}
	if len(f.lifecycle.appended) != 0 {
		t.Errorf("appended stored %d events, want 0 (appendErr should short-circuit recording)", len(f.lifecycle.appended))
	}
}

// TestReconcileRun_JournalError_PassesThrough verifies that a journal
// read failure returns the original lastState (so the scheduler's
// reconcileOne sees no transition) and wraps the err. Without this
// pin, a flaky journal would silently roll runs back to scheduled.
func TestReconcileRun_JournalError_PassesThrough(t *testing.T) {
	f := newReconcileFixture(t)
	f.journal.eventsErr = errors.New("db gone")

	newState, err := f.reconciler.ReconcileRun(context.Background(),
		scheduledAgentJobSpec("docs_bot"), "r1", scheduler.StateRunning)
	if err == nil {
		t.Fatalf("expected wrapped journal error, got nil")
	}
	if newState != scheduler.StateRunning {
		t.Errorf("newState = %q, want unchanged (StateRunning passed in)", newState)
	}
}

// TestReconcileRun_Row5_NilScheduledAgent_SkipsLifecycleAppend ensures
// the lifecycle write only fires when ScheduledAgent is populated.
// Defensive: A-B1's per-row walker passes the JobSpec untouched so a
// malformed spec at boot doesn't crash the reconciler.
func TestReconcileRun_Row5_NilScheduledAgent_SkipsLifecycleAppend(t *testing.T) {
	f := newReconcileFixture(t)
	f.pathAnswer["/wt1"] = true
	f.tmux.alive = false
	f.journal.events = []scheduler.Event{
		stage3CompleteEvent(f.now, "r1", "/wt1", "agent/x/job-r1"),
		stage4CompleteEvent(f.now, "r1", "docs_bot"),
	}
	spec := scheduler.JobSpec{ID: "docs_bot_job", Type: "scheduled_agent"} // ScheduledAgent unset

	newState, err := f.reconciler.ReconcileRun(context.Background(),
		spec, "r1", scheduler.StateRunning)
	if err != nil {
		t.Fatalf("ReconcileRun: %v", err)
	}
	if newState != scheduler.StateFailed {
		t.Errorf("newState = %q, want StateFailed (defensive: still destroys + fails)", newState)
	}
	if len(f.wt.destroyCalls) != 1 {
		t.Errorf("Destroy called %d times, want 1 (orphan cleanup still fires)", len(f.wt.destroyCalls))
	}
	if len(f.lifecycle.appended) != 0 {
		t.Errorf("lifecycle events appended %d, want 0 (nil ScheduledAgent skips append)", len(f.lifecycle.appended))
	}
}

// TestExtractJournalState_LaterEventsOverwrite verifies the ASC-order
// extraction picks up the latest non-empty value for each field. This
// guards against an event-replay refactor that picks `events[0]`
// instead of walking forward.
func TestExtractJournalState_LaterEventsOverwrite(t *testing.T) {
	t0 := time.Unix(1747353600, 0)
	events := []scheduler.Event{
		{Details: map[string]any{"worktree_path": "/old", "branch_name": "old-branch"}, EventTime: t0},
		{Details: map[string]any{"worktree_path": "/new", "branch_name": "new-branch"}, EventTime: t0.Add(time.Second)},
		{Details: map[string]any{"tmux_session_name": "sess1"}, EventTime: t0.Add(2 * time.Second)},
	}
	got := extractJournalState(events)
	if got.WorktreePath != "/new" {
		t.Errorf("WorktreePath = %q, want /new (later events should overwrite)", got.WorktreePath)
	}
	if got.BranchName != "new-branch" {
		t.Errorf("BranchName = %q, want new-branch", got.BranchName)
	}
	if got.TmuxSessionName != "sess1" {
		t.Errorf("TmuxSessionName = %q, want sess1", got.TmuxSessionName)
	}
}

// --- B2: SweepOrphans + cancel-residue (Task 69) ---

// reconDirEntry is a minimal os.DirEntry implementation for the
// readDir injection point. Only Name() is read by SweepOrphans;
// IsDir/Type/Info are required by the interface but never consulted
// (the sweep accepts both files and dirs whose names match the
// pattern — the underlying worktree.Destroy decides what's
// actually destroyable).
type reconDirEntry struct{ name string }

func (e reconDirEntry) Name() string      { return e.name }
func (e reconDirEntry) IsDir() bool       { return true }
func (e reconDirEntry) Type() os.FileMode { return os.ModeDir }
func (e reconDirEntry) Info() (os.FileInfo, error) {
	panic("reconDirEntry: Info not used in sweep tests")
}

// pruneRecorder captures gitWorktreePrune invocations + canned
// errors so tests can assert the trailing prune fires (or doesn't)
// and that prune failures don't blow up the sweep.
type pruneRecorder struct {
	calls     []string
	returnErr error
}

func (p *pruneRecorder) prune(_ context.Context, repoPath string) error {
	p.calls = append(p.calls, repoPath)
	return p.returnErr
}

// newSweepFixture wires a BootReconciler whose readDir returns a
// canned entry list (over an injected basePath) + gitWorktreePrune
// records invocations. The fixture stores the basepath since
// SweepOrphans derives it from worktree.InferBasePath(repoPath);
// tests set repoPath such that InferBasePath returns the expected
// directory, and the readDir hook returns the prepared entries
// regardless of the path argument.
type sweepFixture struct {
	*reconcileFixture
	prune       *pruneRecorder
	entries     []os.DirEntry
	readDirErr  error
	dirRequests []string
}

func newSweepFixture(t *testing.T) *sweepFixture {
	t.Helper()
	f := newReconcileFixture(t)
	prune := &pruneRecorder{}
	sf := &sweepFixture{
		reconcileFixture: f,
		prune:            prune,
	}
	f.reconciler.readDir = func(dir string) ([]os.DirEntry, error) {
		sf.dirRequests = append(sf.dirRequests, dir)
		return sf.entries, sf.readDirErr
	}
	f.reconciler.gitWorktreePrune = prune.prune
	return sf
}

// TestSweepOrphans_DestroysUnreferencedAged verifies the happy path:
// a directory matching thrum-non7 §3.4 (agent-job-ts), older than
// the grace period, NOT in the non-terminal-worktrees set, gets
// destroyed with the canonical branch-name reconstruction. Prune
// fires after the destroy.
func TestSweepOrphans_DestroysUnreferencedAged(t *testing.T) {
	sf := newSweepFixture(t)
	basePath := worktree.InferBasePath(sf.reconciler.repoPath)
	if basePath == "" {
		t.Skip("HOME unresolved; can't derive sweep basepath")
	}
	// Orphan timestamp = 2 minutes ago (well past grace period).
	orphanTs := sf.now.Add(-2 * time.Minute).Unix()
	sf.entries = []os.DirEntry{
		reconDirEntry{name: fmt.Sprintf("docs_bot-jobX-%d", orphanTs)},
	}

	if err := sf.reconciler.SweepOrphans(context.Background()); err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}

	if len(sf.wt.destroyCalls) != 1 {
		t.Fatalf("Destroy called %d times, want 1", len(sf.wt.destroyCalls))
	}
	got := sf.wt.destroyCalls[0]
	wantPath := filepath.Join(basePath, fmt.Sprintf("docs_bot-jobX-%d", orphanTs))
	if got.WorktreePath != wantPath {
		t.Errorf("Destroy WorktreePath = %q, want %q", got.WorktreePath, wantPath)
	}
	wantBranch := fmt.Sprintf("agent/docs_bot/job-jobX-%d", orphanTs)
	if got.Branch != wantBranch {
		t.Errorf("Destroy Branch = %q, want %q", got.Branch, wantBranch)
	}
	if !got.Force {
		t.Errorf("Destroy Force = false, want true (sweep must clean orphan branches even if dirty)")
	}
	if got.RepoPath != "/repo" {
		t.Errorf("Destroy RepoPath = %q, want /repo", got.RepoPath)
	}
	if len(sf.prune.calls) != 1 {
		t.Errorf("prune called %d times, want 1 (trailing prune always fires)", len(sf.prune.calls))
	}
}

// TestSweepOrphans_SkipsReferenced verifies that directories whose
// path appears in NonTerminalWorktrees stay untouched even when
// they're older than the grace period.
func TestSweepOrphans_SkipsReferenced(t *testing.T) {
	sf := newSweepFixture(t)
	basePath := worktree.InferBasePath(sf.reconciler.repoPath)
	if basePath == "" {
		t.Skip("HOME unresolved; can't derive sweep basepath")
	}
	orphanTs := sf.now.Add(-2 * time.Minute).Unix()
	name := fmt.Sprintf("docs_bot-jobX-%d", orphanTs)
	sf.entries = []os.DirEntry{reconDirEntry{name: name}}
	sf.journal.referenced = map[string]bool{
		filepath.Join(basePath, name): true,
	}

	if err := sf.reconciler.SweepOrphans(context.Background()); err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if len(sf.wt.destroyCalls) != 0 {
		t.Errorf("Destroy called %d times, want 0 (referenced path must be preserved)", len(sf.wt.destroyCalls))
	}
	if len(sf.prune.calls) != 1 {
		t.Errorf("prune called %d times, want 1", len(sf.prune.calls))
	}
}

// TestSweepOrphans_SkipsWithinGracePeriod pins the 60s grace
// branch: directories younger than orphanSweepGracePeriod stay
// untouched even when they're not in the referenced set. Without
// this, a dispatch whose stage-3 journal-write hasn't landed yet
// would race the sweep and get its worktree destroyed.
func TestSweepOrphans_SkipsWithinGracePeriod(t *testing.T) {
	sf := newSweepFixture(t)
	// Directory created 30 seconds ago (inside the 60s grace).
	youngTs := sf.now.Add(-30 * time.Second).Unix()
	name := fmt.Sprintf("docs_bot-jobY-%d", youngTs)
	sf.entries = []os.DirEntry{reconDirEntry{name: name}}

	if err := sf.reconciler.SweepOrphans(context.Background()); err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if len(sf.wt.destroyCalls) != 0 {
		t.Errorf("Destroy called %d times, want 0 (under grace period)", len(sf.wt.destroyCalls))
	}
	if len(sf.prune.calls) != 1 {
		t.Errorf("prune called %d times, want 1", len(sf.prune.calls))
	}
}

// TestSweepOrphans_AtGraceBoundary_AgedExactly60sIsSwept pins the
// boundary condition: exactly 60 seconds old is treated as "old"
// (>= grace). spec §9.10.6 calls this out — without the boundary
// pin, a future refactor could flip "<" into "<=" and silently
// extend the grace by one tick.
func TestSweepOrphans_AtGraceBoundary_AgedExactly60sIsSwept(t *testing.T) {
	sf := newSweepFixture(t)
	exactlyAtBoundary := sf.now.Add(-orphanSweepGracePeriod).Unix()
	name := fmt.Sprintf("docs_bot-jobB-%d", exactlyAtBoundary)
	sf.entries = []os.DirEntry{reconDirEntry{name: name}}

	if err := sf.reconciler.SweepOrphans(context.Background()); err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if len(sf.wt.destroyCalls) != 1 {
		t.Errorf("Destroy called %d times, want 1 (boundary treated as expired)", len(sf.wt.destroyCalls))
	}
}

// TestSweepOrphans_IgnoresNonMatchingNames verifies that files /
// directories not matching the thrum-non7 §3.4 pattern are left
// alone. Common operator-state directories (config, var, tmp)
// shouldn't trigger Destroy.
func TestSweepOrphans_IgnoresNonMatchingNames(t *testing.T) {
	sf := newSweepFixture(t)
	sf.entries = []os.DirEntry{
		reconDirEntry{name: "config"},
		reconDirEntry{name: "var"},
		reconDirEntry{name: "no-trailing-digits"},
		reconDirEntry{name: "missing_dashes_123"}, // zero dashes; pattern needs ≥2 dash separators
	}

	if err := sf.reconciler.SweepOrphans(context.Background()); err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if len(sf.wt.destroyCalls) != 0 {
		t.Errorf("Destroy called %d times, want 0 (no matching names)", len(sf.wt.destroyCalls))
	}
}

// TestSweepOrphans_BasepathMissing_PruneStillRuns pins the
// pre-condition handling: a fresh daemon with no worktree
// directories yet shouldn't fail the sweep — but it should still
// run prune so dangling metadata from a wiped-but-not-pruned past
// life gets cleaned up.
func TestSweepOrphans_BasepathMissing_PruneStillRuns(t *testing.T) {
	sf := newSweepFixture(t)
	sf.readDirErr = &os.PathError{Op: "readdir", Path: "/nonexistent", Err: os.ErrNotExist}

	if err := sf.reconciler.SweepOrphans(context.Background()); err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if len(sf.prune.calls) != 1 {
		t.Errorf("prune called %d times, want 1 (basepath absent must still prune)", len(sf.prune.calls))
	}
}

// TestSweepOrphans_ReadDirError_OtherThanNotExist_Surfaces verifies
// non-ENOENT readdir failures propagate as errors. ENOENT is
// expected on fresh daemons; permission errors etc. need to surface
// so operators can investigate.
func TestSweepOrphans_ReadDirError_OtherThanNotExist_Surfaces(t *testing.T) {
	sf := newSweepFixture(t)
	sf.readDirErr = errors.New("readdir EACCES")

	err := sf.reconciler.SweepOrphans(context.Background())
	if err == nil {
		t.Fatalf("expected wrapped readdir error, got nil")
	}
	if len(sf.prune.calls) != 0 {
		t.Errorf("prune called %d times, want 0 (sweep must abort on hard readdir error)", len(sf.prune.calls))
	}
}

// TestSweepOrphans_JournalReadError_Surfaces verifies that a
// NonTerminalWorktrees lookup failure aborts the sweep. Otherwise
// the sweep would proceed without the "skip referenced" guard and
// could destroy live worktrees.
func TestSweepOrphans_JournalReadError_Surfaces(t *testing.T) {
	sf := newSweepFixture(t)
	sf.journal.referencedErr = errors.New("db gone")
	// Provide an aged orphan so the sweep would otherwise try to
	// destroy it — failure must come from the journal read, not
	// from a no-op walk.
	sf.entries = []os.DirEntry{
		reconDirEntry{name: fmt.Sprintf("docs_bot-jobX-%d", sf.now.Add(-2*time.Minute).Unix())},
	}

	err := sf.reconciler.SweepOrphans(context.Background())
	if err == nil {
		t.Fatalf("expected wrapped journal error, got nil")
	}
	if len(sf.wt.destroyCalls) != 0 {
		t.Errorf("Destroy called %d times, want 0 (must not proceed without referenced set)", len(sf.wt.destroyCalls))
	}
	if len(sf.prune.calls) != 0 {
		t.Errorf("prune called %d times, want 0 (aborts before prune)", len(sf.prune.calls))
	}
}

// TestSweepOrphans_PruneFailure_NonFatal verifies BLOCKING #5 fix:
// `git worktree prune` failures log via slog.Warn but DON'T cause
// SweepOrphans to return an error. The per-orphan destroys already
// cleaned the visible filesystem; prune is best-effort metadata
// cleanup that operators can re-attempt.
func TestSweepOrphans_PruneFailure_NonFatal(t *testing.T) {
	sf := newSweepFixture(t)
	sf.prune.returnErr = errors.New("git prune EBUSY")

	if err := sf.reconciler.SweepOrphans(context.Background()); err != nil {
		t.Fatalf("SweepOrphans: %v (prune failure must not propagate)", err)
	}
	if len(sf.prune.calls) != 1 {
		t.Errorf("prune call count = %d, want 1", len(sf.prune.calls))
	}
}

// TestSweepOrphans_Task69_CancelPostAddPreRedirectResidue covers
// thrum-non7 §3.5 residue class #4 + B-B1 plan Task 69. Scenario:
// worktree.Create succeeded mid-EnsureMirrored, then context-cancel
// fired before the journal-write recorded worktree_path. The
// directory exists on disk but the journal never named it as
// referenced. SweepOrphans must find it via the basepath scan and
// destroy it.
//
// The "integration" framing in plan §3443-3448 specifies setting
// up a real worktree mid-cancel; here the cancel-residue STATE
// (filesystem entry present + journal lacking the path) is
// reproduced via the same fixtures the other sweep tests use,
// which is sufficient to exercise the sweep's branching for
// this residue class. A wider end-to-end exercise lives in B3's
// AC §9.10.5 integration coverage.
func TestSweepOrphans_Task69_CancelPostAddPreRedirectResidue(t *testing.T) {
	sf := newSweepFixture(t)
	basePath := worktree.InferBasePath(sf.reconciler.repoPath)
	if basePath == "" {
		t.Skip("HOME unresolved; can't derive sweep basepath")
	}
	// Cancel-residue: worktree.Create wrote the directory under
	// the canonical name 3 minutes ago, but EnsureMirrored hit a
	// context-cancel before the stage-3-complete journal entry
	// wrote `worktree_path`. The journal's NonTerminalWorktrees
	// view therefore lacks this path entirely.
	residueTs := sf.now.Add(-3 * time.Minute).Unix()
	residueName := fmt.Sprintf("docs_bot-cancelled_job-%d", residueTs)
	sf.entries = []os.DirEntry{reconDirEntry{name: residueName}}
	// referenced map intentionally nil — no journal entry exists.

	if err := sf.reconciler.SweepOrphans(context.Background()); err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}

	if len(sf.wt.destroyCalls) != 1 {
		t.Fatalf("Destroy called %d times, want 1 (cancel-residue must be cleaned)", len(sf.wt.destroyCalls))
	}
	got := sf.wt.destroyCalls[0]
	wantPath := filepath.Join(basePath, residueName)
	if got.WorktreePath != wantPath {
		t.Errorf("Destroy WorktreePath = %q, want %q", got.WorktreePath, wantPath)
	}
	wantBranch := fmt.Sprintf("agent/docs_bot/job-cancelled_job-%d", residueTs)
	if got.Branch != wantBranch {
		t.Errorf("Destroy Branch = %q, want %q (branch reconstruction must match worktree.Create's scheme)", got.Branch, wantBranch)
	}
	if !got.Force {
		t.Errorf("Destroy Force = false, want true")
	}
	if len(sf.prune.calls) != 1 {
		t.Errorf("prune calls = %d, want 1 (cleans dangling .git/worktrees/ metadata)", len(sf.prune.calls))
	}
}

// TestSweepOrphans_DestroyFailure_ContinuesScan verifies one stuck
// orphan (e.g. file permissions) doesn't prevent subsequent orphans
// from being swept. Each destroy is independent.
func TestSweepOrphans_DestroyFailure_ContinuesScan(t *testing.T) {
	sf := newSweepFixture(t)
	sf.wt.destroyErr = errors.New("git worktree remove EACCES")
	tsA := sf.now.Add(-2 * time.Minute).Unix()
	tsB := sf.now.Add(-3 * time.Minute).Unix()
	sf.entries = []os.DirEntry{
		reconDirEntry{name: fmt.Sprintf("alpha-jobA-%d", tsA)},
		reconDirEntry{name: fmt.Sprintf("beta-jobB-%d", tsB)},
	}

	if err := sf.reconciler.SweepOrphans(context.Background()); err != nil {
		t.Fatalf("SweepOrphans: %v (one stuck orphan must not abort the sweep)", err)
	}
	if len(sf.wt.destroyCalls) != 2 {
		t.Errorf("Destroy called %d times, want 2 (continue past first failure)", len(sf.wt.destroyCalls))
	}
}

// TestExtractJournalState_RollbackKeysIgnored verifies that the
// rollback-row keys (worktree_path_destroyed, tmux_session_killed)
// are NOT mistaken for live state. Without this guard, a rollback
// event would clobber the live worktree_path back to "" and we'd
// wrongly classify into row 1.
func TestExtractJournalState_RollbackKeysIgnored(t *testing.T) {
	t0 := time.Unix(1747353600, 0)
	events := []scheduler.Event{
		{Details: map[string]any{"worktree_path": "/wt1", "branch_name": "agent/x"}, EventTime: t0},
		{Details: map[string]any{"tmux_session_name": "sess1"}, EventTime: t0.Add(time.Second)},
		{Details: map[string]any{"worktree_path_destroyed": "/wt1", "tmux_session_killed": "sess1"}, EventTime: t0.Add(2 * time.Second)},
	}
	got := extractJournalState(events)
	if got.WorktreePath != "/wt1" {
		t.Errorf("WorktreePath = %q, want /wt1 (rollback key must not clobber)", got.WorktreePath)
	}
	if got.TmuxSessionName != "sess1" {
		t.Errorf("TmuxSessionName = %q, want sess1 (rollback key must not clobber)", got.TmuxSessionName)
	}
}
