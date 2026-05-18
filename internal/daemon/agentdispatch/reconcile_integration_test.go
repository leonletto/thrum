package agentdispatch_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/agentdispatch"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/internal/worktree"
)

// Integration tests for E6.9 Task 70 — AC §9.10.2 + §9.10.5 wider
// coverage than the unit pins in B1+B2. These exercise BootReconciler
// against a real *scheduler.StateStore (real SQLite + real
// scheduler_job_events writes) rather than the in-package stubs;
// scheduler.StateStore's JournalReader methods are the production
// hot-path consumed by BootReconciler.ReconcileRun + SweepOrphans.

func setupIntegrationDB(t *testing.T) (*safedb.DB, *scheduler.StateStore, state.AgentLifecycleStore) {
	t.Helper()
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	safe := safedb.New(db)
	return safe, scheduler.NewStateStore(safe), state.NewAgentLifecycleStore(safe)
}

// integrationFakeWorktree records Destroy calls without touching git.
type integrationFakeWorktree struct {
	destroyCalls []worktree.DestroyOpts
}

func (f *integrationFakeWorktree) Create(_ context.Context, _ worktree.CreateOpts) (*worktree.CreateResult, error) {
	panic("integrationFakeWorktree: Create not used in reconcile integration tests")
}

func (f *integrationFakeWorktree) Destroy(_ context.Context, opts worktree.DestroyOpts) (*worktree.DestroyResult, error) {
	f.destroyCalls = append(f.destroyCalls, opts)
	return &worktree.DestroyResult{}, nil
}

// integrationFakeTmux answers CheckPane only.
type integrationFakeTmux struct {
	aliveByTarget map[string]bool
}

func (f *integrationFakeTmux) CheckPane(_ context.Context, target string) (bool, error) {
	return f.aliveByTarget[target], nil
}

func (f *integrationFakeTmux) TmuxCreate(_ context.Context, _ string, _ agentdispatch.TmuxCreateOpts) error {
	panic("integration: TmuxCreate not used")
}

func (f *integrationFakeTmux) TmuxLaunch(_ context.Context, _ string) error {
	panic("integration: TmuxLaunch not used")
}

func (f *integrationFakeTmux) WaitForPaneReady(_ context.Context, _ string) error {
	panic("integration: WaitForPaneReady not used")
}

func (f *integrationFakeTmux) TmuxKillSession(_ context.Context, _ string) error {
	panic("integration: TmuxKillSession not used")
}

func (f *integrationFakeTmux) PaneSendCtrlCExit(_ context.Context, _ string) error {
	panic("integration: PaneSendCtrlCExit not used")
}

func (f *integrationFakeTmux) PaneInjectPrompt(_ context.Context, _, _ string) error {
	panic("integration: PaneInjectPrompt not used")
}

// TestE6_9_Integration_AC_9_10_2_PaneAlive_StateStaysRunning closes
// the integration leg of AC §9.10.2: a non-terminal run row with
// stage 3 + stage 4 events journaled in real scheduler_job_events,
// pane still alive, must reconcile to StateRunning (no transition).
//
// Verifies the full real-StateStore round-trip:
//  1. AppendEvent writes scheduler_job_events.details JSON.
//  2. EventsForRun reads the events back ASC by event_time.
//  3. BootReconciler extractJournalState walks the events.
//  4. ReconcileRun returns StateRunning (matching lastState).
//
// Without the integration cover, a bug in scheduler.StateStore's
// JSON-marshal / extract path would be invisible to B1's pure-fake
// unit tests.
func TestE6_9_Integration_AC_9_10_2_PaneAlive_StateStaysRunning(t *testing.T) {
	_, journalStore, lifecycleStore := setupIntegrationDB(t)
	ctx := context.Background()

	t0 := time.Unix(1747353600, 0)
	runID := "docs_bot_job-1747353600"
	// Stage 3 complete event: worktree journaled.
	if err := journalStore.AppendEvent(ctx, &scheduler.Event{
		JobID:     "docs_bot_job",
		RunID:     runID,
		EventTime: t0,
		FromState: scheduler.StateRunning,
		ToState:   scheduler.StateRunning,
		Reason:    "stage 3 complete",
		Details:   map[string]any{"worktree_path": "/wt1", "branch_name": "agent/docs_bot/job-r1"},
	}); err != nil {
		t.Fatalf("append stage 3: %v", err)
	}
	// Stage 4 complete event: tmux session journaled.
	if err := journalStore.AppendEvent(ctx, &scheduler.Event{
		JobID:     "docs_bot_job",
		RunID:     runID,
		EventTime: t0.Add(time.Second),
		FromState: scheduler.StateRunning,
		ToState:   scheduler.StateRunning,
		Reason:    "stage 4 complete",
		Details:   map[string]any{"tmux_session_name": "docs_bot"},
	}); err != nil {
		t.Fatalf("append stage 4: %v", err)
	}

	wt := &integrationFakeWorktree{}
	tmux := &integrationFakeTmux{aliveByTarget: map[string]bool{"docs_bot": true}}
	reconciler := agentdispatch.NewBootReconciler("/repo", tmux, wt, journalStore, lifecycleStore)
	// Pretend the worktree directory exists so Row 2 (gone from disk)
	// doesn't preempt Row 4. ReconcileRun doesn't care about real
	// filesystem state here — the worktree primitive is mocked anyway.
	agentdispatch.SetPathExistsForTest(reconciler, func(_ string) bool { return true })

	newState, err := reconciler.ReconcileRun(ctx,
		scheduler.JobSpec{
			ID:             "docs_bot_job",
			Type:           "scheduled_agent",
			ScheduledAgent: &scheduler.ScheduledAgentSpec{Target: "docs_bot"},
		},
		runID,
		scheduler.StateRunning,
	)
	if err != nil {
		t.Fatalf("ReconcileRun: %v", err)
	}
	if newState != scheduler.StateRunning {
		t.Errorf("newState = %q, want StateRunning (pane alive → resume polling, no transition)", newState)
	}
	if len(wt.destroyCalls) != 0 {
		t.Errorf("Destroy called %d times against live worktree; want 0", len(wt.destroyCalls))
	}
	// Verify NO crash_detected was appended.
	events, err := lifecycleStore.ListByAgent(ctx, "docs_bot", 10)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("lifecycle events for live agent = %d, want 0", len(events))
	}
}

// TestE6_9_Integration_AC_9_10_3_PaneGone_RealJournalRoundTrip
// extends AC §9.10.3 with a real-DB round-trip: stage 3 + stage 4
// events journaled, pane probe returns gone, verify Destroy fires
// + crash_detected lifecycle event lands in real
// agent_lifecycle_events with the correct DetectionMethod.
func TestE6_9_Integration_AC_9_10_3_PaneGone_RealJournalRoundTrip(t *testing.T) {
	_, journalStore, lifecycleStore := setupIntegrationDB(t)
	ctx := context.Background()

	t0 := time.Unix(1747353600, 0)
	runID := "crashy_bot_job-1747353600"
	for _, e := range []*scheduler.Event{
		{JobID: "crashy_bot_job", RunID: runID, EventTime: t0, ToState: scheduler.StateRunning,
			Reason: "stage 3 complete",
			Details: map[string]any{
				"worktree_path": "/wt2",
				"branch_name":   "agent/crashy_bot/job-r1",
			}},
		{JobID: "crashy_bot_job", RunID: runID, EventTime: t0.Add(time.Second), ToState: scheduler.StateRunning,
			Reason:  "stage 4 complete",
			Details: map[string]any{"tmux_session_name": "crashy_bot"}},
	} {
		if err := journalStore.AppendEvent(ctx, e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	wt := &integrationFakeWorktree{}
	tmux := &integrationFakeTmux{aliveByTarget: map[string]bool{"crashy_bot": false}}
	reconciler := agentdispatch.NewBootReconciler("/repo", tmux, wt, journalStore, lifecycleStore)
	agentdispatch.SetPathExistsForTest(reconciler, func(_ string) bool { return true })

	newState, err := reconciler.ReconcileRun(ctx,
		scheduler.JobSpec{
			ID:             "crashy_bot_job",
			Type:           "scheduled_agent",
			ScheduledAgent: &scheduler.ScheduledAgentSpec{Target: "crashy_bot"},
		},
		runID,
		scheduler.StateRunning,
	)
	if err != nil {
		t.Fatalf("ReconcileRun: %v", err)
	}
	if newState != scheduler.StateFailed {
		t.Errorf("newState = %q, want StateFailed", newState)
	}
	if len(wt.destroyCalls) != 1 {
		t.Fatalf("Destroy called %d times, want 1", len(wt.destroyCalls))
	}
	got := wt.destroyCalls[0]
	if got.WorktreePath != "/wt2" {
		t.Errorf("Destroy WorktreePath = %q, want /wt2", got.WorktreePath)
	}
	if got.Branch != "agent/crashy_bot/job-r1" {
		t.Errorf("Destroy Branch = %q, want agent/crashy_bot/job-r1", got.Branch)
	}

	// Verify lifecycle event lands in real DB.
	events, err := lifecycleStore.ListByAgent(ctx, "crashy_bot", 10)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("lifecycle events = %d, want 1 (crash_detected)", len(events))
	}
	ev := events[0]
	if ev.EventKind != state.EventCrashDetected {
		t.Errorf("EventKind = %q, want %q", ev.EventKind, state.EventCrashDetected)
	}
	if ev.DetectionMethod != state.DetectionRestartReconciliation {
		t.Errorf("DetectionMethod = %q, want %q", ev.DetectionMethod, state.DetectionRestartReconciliation)
	}
}

// TestE6_9_Integration_AC_9_10_5_CancelResidue_RealSweep covers the
// wider integration leg of AC §9.10.5 promised in B2: cancel-post-add-
// pre-redirect residue (thrum-non7 §3.5 class #4) — a worktree
// directory exists on disk under the thrum-non7 §3.4 naming, but no
// scheduler_job_state row claims it. SweepOrphans must find it via
// the basepath scan and destroy.
//
// Uses a real os.ReadDir against a tempdir populated with a residue
// directory + an empty NonTerminalWorktrees set in real SQLite. The
// prune step uses an injected fake (we don't want a real `git
// worktree prune` against an unrelated repo).
func TestE6_9_Integration_AC_9_10_5_CancelResidue_RealSweep(t *testing.T) {
	_, journalStore, lifecycleStore := setupIntegrationDB(t)
	ctx := context.Background()

	// Pin HOME so worktree.InferBasePath produces a path under the
	// per-test tempdir; populate that directory with the residue
	// entry so the real os.ReadDir resolves it.
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	basePath := worktree.InferBasePath("/repo")
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		t.Fatalf("mkdir basePath: %v", err)
	}
	residueTs := time.Now().Add(-3 * time.Minute).Unix()
	residueName := fmt.Sprintf("docs_bot-cancelled_job-%d", residueTs)
	residuePath := filepath.Join(basePath, residueName)
	if err := os.MkdirAll(residuePath, 0o755); err != nil {
		t.Fatalf("mkdir residue: %v", err)
	}

	wt := &integrationFakeWorktree{}
	tmux := &integrationFakeTmux{}
	reconciler := agentdispatch.NewBootReconciler("/repo", tmux, wt, journalStore, lifecycleStore)
	// Override prune so we don't shell out against /repo (which
	// doesn't exist in the test environment).
	pruneCalled := false
	agentdispatch.SetGitWorktreePruneForTest(reconciler, func(_ context.Context, _ string) error {
		pruneCalled = true
		return nil
	})

	if err := reconciler.SweepOrphans(ctx); err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if len(wt.destroyCalls) != 1 {
		t.Fatalf("Destroy called %d times, want 1 (cancel-residue must be cleaned)", len(wt.destroyCalls))
	}
	got := wt.destroyCalls[0]
	if got.WorktreePath != residuePath {
		t.Errorf("Destroy WorktreePath = %q, want %q", got.WorktreePath, residuePath)
	}
	wantBranch := fmt.Sprintf("agent/docs_bot/job-cancelled_job-%d", residueTs)
	if got.Branch != wantBranch {
		t.Errorf("Destroy Branch = %q, want %q (branch reconstruction must match worktree.Create scheme)", got.Branch, wantBranch)
	}
	if !pruneCalled {
		t.Errorf("prune was not invoked; expected trailing prune for SIGKILL residue cleanup")
	}
}

// TestE6_9_Integration_AC_9_10_5_LiveWorktreesNotSwept verifies the
// real-DB cross-reference path: a non-terminal scheduler_job_state
// row references a worktree via its last_run_id's stage-3 event.
// SweepOrphans must skip that path. Without this integration cover,
// a SQL bug in NonTerminalWorktrees (e.g. JOIN broken,
// json_extract path wrong) would silently destroy live worktrees.
func TestE6_9_Integration_AC_9_10_5_LiveWorktreesNotSwept(t *testing.T) {
	_, journalStore, lifecycleStore := setupIntegrationDB(t)
	ctx := context.Background()

	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	basePath := worktree.InferBasePath("/repo")
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		t.Fatalf("mkdir basePath: %v", err)
	}
	liveTs := time.Now().Add(-3 * time.Minute).Unix()
	liveName := fmt.Sprintf("docs_bot-live_job-%d", liveTs)
	livePath := filepath.Join(basePath, liveName)
	if err := os.MkdirAll(livePath, 0o755); err != nil {
		t.Fatalf("mkdir live: %v", err)
	}

	t0 := time.Unix(1747353600, 0)
	if err := journalStore.UpsertState(ctx, &scheduler.StateRow{
		JobID:        "docs_bot_job",
		Generation:   1,
		CurrentState: scheduler.StateRunning,
		LastRunID:    "live_run_1",
		CreatedAt:    t0,
		UpdatedAt:    t0,
	}); err != nil {
		t.Fatalf("upsert state: %v", err)
	}
	if err := journalStore.AppendEvent(ctx, &scheduler.Event{
		JobID:     "docs_bot_job",
		RunID:     "live_run_1",
		EventTime: t0,
		ToState:   scheduler.StateRunning,
		Reason:    "stage 3 complete",
		Details:   map[string]any{"worktree_path": livePath, "branch_name": "agent/docs_bot/job-r1"},
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	wt := &integrationFakeWorktree{}
	tmux := &integrationFakeTmux{}
	reconciler := agentdispatch.NewBootReconciler("/repo", tmux, wt, journalStore, lifecycleStore)
	agentdispatch.SetGitWorktreePruneForTest(reconciler, func(_ context.Context, _ string) error {
		return nil
	})

	if err := reconciler.SweepOrphans(ctx); err != nil {
		t.Fatalf("SweepOrphans: %v", err)
	}
	if len(wt.destroyCalls) != 0 {
		t.Errorf("Destroy called %d times against live worktree referenced by non-terminal row; want 0", len(wt.destroyCalls))
	}
}

// TestE6_9_Integration_JournalReader_StateStore_Compatibility pins
// the production-wiring assumption: *scheduler.StateStore directly
// satisfies the agentdispatch.JournalReader interface. The compile-
// time check in reconcile.go enforces this at build time, but this
// runtime test exercises both methods via the interface boundary so
// signature drift surfaces as a test failure rather than a less-
// obvious compile error in main.go.
func TestE6_9_Integration_JournalReader_StateStore_Compatibility(t *testing.T) {
	_, store, _ := setupIntegrationDB(t)
	var jr agentdispatch.JournalReader = store

	events, err := jr.EventsForRun(context.Background(), "unknown")
	if err != nil {
		t.Errorf("EventsForRun (empty DB): %v", err)
	}
	if len(events) != 0 {
		t.Errorf("EventsForRun against unknown run returned %d events; want 0", len(events))
	}
	refs, err := jr.NonTerminalWorktrees(context.Background())
	if err != nil {
		t.Errorf("NonTerminalWorktrees (empty DB): %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("NonTerminalWorktrees against empty DB returned %d entries; want 0", len(refs))
	}
}
