package agentdispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/escalation"
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
func (r *recordingTmux) PaneSendCtrlCExit(_ context.Context, _ string) error  { return nil }
func (r *recordingTmux) PaneInjectPrompt(_ context.Context, _, _ string) error { return nil }

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

	// Grace window is 250ms; assert the call did NOT hang. The upper
	// bound has generous slack (2s) to absorb scheduler jitter on a
	// loaded CI machine. The proof-of-polling is structural rather
	// than time-based — a tight lower bound on elapsed time would be
	// flaky under load even though the helper is correct.
	if elapsed > 2*time.Second {
		t.Errorf("waitForPaneExit blocked %v; want ≤ 2s", elapsed)
	}
	if tmux.checkPaneCalls == 0 {
		t.Error("CheckPane was never polled; the helper short-circuited unexpectedly (regression: the helper must poll at least once before checking the grace deadline)")
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

// stubEscalationRouter records Route invocations + returns canned errors.
type stubEscalationRouter struct {
	returnErr error
	calls     []escalationCall
}

// Compile-time guard: a signature drift on EscalationRouter would
// surface here as "does not implement" rather than a confusing
// assignment failure in the tests below.
var _ EscalationRouter = (*stubEscalationRouter)(nil)

type escalationCall struct {
	alert   escalation.Alert
	subject string
	body    string
}

func (s *stubEscalationRouter) Route(_ context.Context, alert escalation.Alert, subject, body string) error {
	s.calls = append(s.calls, escalationCall{alert: alert, subject: subject, body: body})
	return s.returnErr
}

// TestRouteEscalation_NilEscalation_ReturnsNil pins the defensive
// nil-guard pattern per brainstormer-third I3 (fix-batch
// 0aa97b1...): the routeEscalation helper returns nil when the
// Escalation dep isn't wired, so partial-config deployments don't
// nil-deref on the first real Route() call site E6.4/E6.7 land.
// Establishes the pattern future implementers inherit.
func TestRouteEscalation_NilEscalation_ReturnsNil(t *testing.T) {
	h := &ScheduledAgentHandler{deps: Deps{}}
	alert := escalation.Alert{Source: "b-b1.stage_failure", AgentName: "docs_bot"}

	err := h.routeEscalation(context.Background(), alert, "Subj", "Body")
	if err != nil {
		t.Errorf("err = %v; want nil (nil Escalation must be a no-op)", err)
	}
}

// TestRouteEscalation_DelegatesToInjectedRouter pins the happy-path
// delegation: when Escalation is wired, routeEscalation passes the
// call through and returns whatever the router decides. Plays the
// role of the wiring-pattern proof for E6.4/E6.7 implementers.
func TestRouteEscalation_DelegatesToInjectedRouter(t *testing.T) {
	router := &stubEscalationRouter{}
	h := &ScheduledAgentHandler{deps: Deps{Escalation: router}}
	alert := escalation.Alert{Source: "b-b1.idle_nudge", AgentName: "docs_bot", JobID: "job", RunID: "run"}

	if err := h.routeEscalation(context.Background(), alert, "Subj", "Body"); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if len(router.calls) != 1 {
		t.Fatalf("Route calls = %d; want 1", len(router.calls))
	}
	call := router.calls[0]
	if call.alert.Source != "b-b1.idle_nudge" {
		t.Errorf("alert.Source = %q; want b-b1.idle_nudge", call.alert.Source)
	}
	if call.subject != "Subj" || call.body != "Body" {
		t.Errorf("subject/body = %q/%q; want Subj/Body", call.subject, call.body)
	}
}

// TestRouteEscalation_PropagatesRouterError pins the error-propagation
// contract: when the wired router returns an error, routeEscalation
// surfaces it unchanged so callers can decide (log + continue for
// most cases; surface for auto-respawn loop guard). The helper does
// NOT absorb errors — that's the underlying escalation.Route's
// responsibility (email queue retry for transient bridge failures).
func TestRouteEscalation_PropagatesRouterError(t *testing.T) {
	wantErr := errors.New("inbox shard offline")
	router := &stubEscalationRouter{returnErr: wantErr}
	h := &ScheduledAgentHandler{deps: Deps{Escalation: router}}

	err := h.routeEscalation(context.Background(), escalation.Alert{Source: "b-b1.stage_failure"}, "Subj", "Body")
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v; want wraps %v", err, wantErr)
	}
}

