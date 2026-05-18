package agentdispatch

import (
	"context"
	"errors"
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
}

func (j *reconJournalStub) EventsForRun(_ context.Context, _ string) ([]scheduler.Event, error) {
	return j.events, j.eventsErr
}

func (j *reconJournalStub) NonTerminalWorktrees(_ context.Context) (map[string]bool, error) {
	return map[string]bool{}, nil
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

// reconWorktreeStub answers Destroy; Create panics. The Reconciler
// never creates worktrees.
type reconWorktreeStub struct {
	destroyCalls []worktree.DestroyOpts
	destroyErr   error
}

func (s *reconWorktreeStub) Create(_ context.Context, _ worktree.CreateOpts) (*worktree.CreateResult, error) {
	panic("reconWorktreeStub: Create not used in reconcile tests")
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

// TestReconcileRun_Row2_WorktreeGoneFromDisk_RollsBackSilently covers
// spec §7.7 row 2: `worktree_path` journaled but the directory is
// gone from disk (operator hand-cleanup, partition unmount,
// pre-existing daemon crash already cleaned). Result: roll back
// without attempting Destroy.
func TestReconcileRun_Row2_WorktreeGoneFromDisk_RollsBackSilently(t *testing.T) {
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
	if len(f.lifecycle.appended) != 0 {
		t.Errorf("lifecycle events appended %d, want 0 (row 2 is silent)", len(f.lifecycle.appended))
	}
	if !slices.Contains(f.pathQueriedAt, "/gone/wt1") {
		t.Errorf("pathExists not queried for /gone/wt1; queries: %v", f.pathQueriedAt)
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
