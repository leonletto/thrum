package agentdispatch

import (
	"context"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/worktree"
)

// rollback.go is exercised end-to-end by scheduled_agent_test.go (the
// per-stage failure tests in Tasks 15 + 16 already cover the cleanup
// chains). These isolation tests pin the helpers' direct contract so
// a future-refactor (e.g. swapping context plumbing, changing
// DestroyOpts shape, or reordering kill+destroy) shows up as a unit
// failure rather than a downstream stage test surprise. Package-internal
// so they can construct ScheduledAgentHandler with stub deps without
// re-declaring the test plumbing.

type recordingTmux struct {
	killCalls       []string
	killErr         error
	checkPaneAlive  bool // controls waitForPaneExit polling
	checkPaneCalls  int
}

func (r *recordingTmux) CheckPane(_ context.Context, _ string) (bool, error) {
	r.checkPaneCalls++
	return r.checkPaneAlive, nil
}
func (r *recordingTmux) TmuxCreate(_ context.Context, _ string, _ TmuxCreateOpts) error {
	return nil
}
func (r *recordingTmux) TmuxLaunch(_ context.Context, _ string) error       { return nil }
func (r *recordingTmux) WaitForPaneReady(_ context.Context, _ string) error { return nil }
func (r *recordingTmux) TmuxKillSession(_ context.Context, target string) error {
	r.killCalls = append(r.killCalls, target)
	return r.killErr
}
func (r *recordingTmux) PaneSendCtrlCExit(_ context.Context, _ string) error { return nil }

type recordingWorktree struct {
	destroyCalls []worktree.DestroyOpts
	destroyErr   error
}

func (r *recordingWorktree) Create(_ context.Context, _ worktree.CreateOpts) (*worktree.CreateResult, error) {
	return nil, nil
}
func (r *recordingWorktree) Destroy(_ context.Context, opts worktree.DestroyOpts) (*worktree.DestroyResult, error) {
	r.destroyCalls = append(r.destroyCalls, opts)
	return nil, r.destroyErr
}

// TestRollbackStage4Failure_CallsDestroyWithForceTrue pins the
// canonical stage-4 rollback contract: a single Destroy call with
// RepoPath/WorktreePath/Branch from the CreateResult and Force=true
// (ephemeral teardown requires --force; persistent rollback never
// gets here because the stage-4 path returns earlier for persistent
// runs in future tasks).
func TestRollbackStage4Failure_CallsDestroyWithForceTrue(t *testing.T) {
	wt := &recordingWorktree{}
	h := &ScheduledAgentHandler{deps: Deps{RepoPath: "/repo", Worktree: wt}}
	result := &worktree.CreateResult{
		Path:   "/tmp/wt/docs_bot-j1-1",
		Branch: "agent/docs_bot/job-j1-1",
	}

	h.rollbackStage4Failure(result)

	if len(wt.destroyCalls) != 1 {
		t.Fatalf("Destroy calls = %d; want 1", len(wt.destroyCalls))
	}
	d := wt.destroyCalls[0]
	if d.RepoPath != "/repo" {
		t.Errorf("RepoPath = %q; want /repo", d.RepoPath)
	}
	if d.WorktreePath != result.Path {
		t.Errorf("WorktreePath = %q; want %q", d.WorktreePath, result.Path)
	}
	if d.Branch != result.Branch {
		t.Errorf("Branch = %q; want %q", d.Branch, result.Branch)
	}
	if !d.Force {
		t.Error("Force = false; want true (ephemeral teardown)")
	}
}

// TestRollbackStage5Failure_KillsBeforeDestroy pins the canonical
// kill-then-destroy order per spec §7.1. Reversing this order would
// surface "file in use" errors on the destroy path because the live
// runtime would still be writing into the doomed worktree.
func TestRollbackStage5Failure_KillsBeforeDestroy(t *testing.T) {
	tmux := &recordingTmux{}
	wt := &recordingWorktree{}
	h := &ScheduledAgentHandler{deps: Deps{RepoPath: "/repo", Tmux: tmux, Worktree: wt}}
	result := &worktree.CreateResult{
		Path:   "/tmp/wt/docs_bot-j1-1",
		Branch: "agent/docs_bot/job-j1-1",
	}

	h.rollbackStage5Failure("docs_bot", result)

	if len(tmux.killCalls) != 1 || tmux.killCalls[0] != "docs_bot" {
		t.Errorf("TmuxKillSession calls = %v; want [docs_bot]", tmux.killCalls)
	}
	if len(wt.destroyCalls) != 1 {
		t.Fatalf("Destroy calls = %d; want 1", len(wt.destroyCalls))
	}
	if !wt.destroyCalls[0].Force {
		t.Error("Destroy.Force = false; want true")
	}
}

// TestRollbackStage5Failure_DestroysEvenIfKillFails pins the
// best-effort cleanup contract: a tmux kill-session error must NOT
// short-circuit the worktree.Destroy that follows. Otherwise a
// transient tmux error during cleanup would leave the worktree
// orphan-stranded.
func TestRollbackStage5Failure_DestroysEvenIfKillFails(t *testing.T) {
	tmux := &recordingTmux{killErr: context.DeadlineExceeded}
	wt := &recordingWorktree{}
	h := &ScheduledAgentHandler{deps: Deps{RepoPath: "/repo", Tmux: tmux, Worktree: wt}}
	result := &worktree.CreateResult{Path: "/p", Branch: "b"}

	h.rollbackStage5Failure("docs_bot", result)

	if len(wt.destroyCalls) != 1 {
		t.Errorf("Destroy calls = %d; want 1 (kill error must not short-circuit destroy)", len(wt.destroyCalls))
	}
}

// TestWaitForPaneExit_HonorsGraceWindow pins the canonical stage-8
// grace-window timeout: if CheckPane keeps reporting alive (e.g. a
// wedged runtime that ignored Ctrl-C), waitForPaneExit must return
// after the grace window expires rather than block indefinitely.
// Without this, a single stuck runtime would freeze the entire
// scheduler dispatcher (AC 9.2.10 race-clean depends on it returning).
func TestWaitForPaneExit_HonorsGraceWindow(t *testing.T) {
	tmux := &recordingTmux{checkPaneAlive: true} // never reports exit
	h := &ScheduledAgentHandler{deps: Deps{Tmux: tmux}}

	start := time.Now()
	h.waitForPaneExit("docs_bot", 250*time.Millisecond)
	elapsed := time.Since(start)

	// Grace window is 250ms; with the 100ms polling cadence and
	// scheduler jitter, allow a generous upper bound but assert
	// the call did NOT hang.
	if elapsed > 2*time.Second {
		t.Errorf("waitForPaneExit blocked %v; want ≤ 2s", elapsed)
	}
	if elapsed < 200*time.Millisecond {
		t.Errorf("waitForPaneExit returned in %v; expected at least grace window (250ms)", elapsed)
	}
	if tmux.checkPaneCalls == 0 {
		t.Error("CheckPane was never polled; the helper short-circuited unexpectedly")
	}
}

// TestWaitForPaneExit_ReturnsImmediatelyOnNotAlive pins the fast-path
// case: when CheckPane reports the pane is not alive on the first
// poll, waitForPaneExit returns promptly (well under the grace
// window). A regression that always burned the full grace would
// add up to 10s per teardown — visible operator-facing slowness
// across many wakes.
func TestWaitForPaneExit_ReturnsImmediatelyOnNotAlive(t *testing.T) {
	tmux := &recordingTmux{checkPaneAlive: false}
	h := &ScheduledAgentHandler{deps: Deps{Tmux: tmux}}

	start := time.Now()
	h.waitForPaneExit("docs_bot", 5*time.Second)
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("waitForPaneExit took %v with not-alive pane; want < 500ms", elapsed)
	}
}
