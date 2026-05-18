package agentdispatch

import (
	"context"
	"testing"

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
	killCalls []string
	killErr   error
}

func (r *recordingTmux) CheckPane(_ context.Context, _ string) (bool, error)         { return false, nil }
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
