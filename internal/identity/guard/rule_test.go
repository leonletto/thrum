package guard

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// testRuleCtx is a fluent-builder wrapper around CheckContext that
// exposes test-only observability hooks (reclaim counter, warn-log
// tripwire) so assertions read as a single statement per scenario.
type testRuleCtx struct {
	cc         *CheckContext
	reclaimed  bool
	warnLogged bool
	warnBuf    *bytes.Buffer
}

type ruleOpt func(*testRuleCtx)

func withChain(c []int) ruleOpt               { return func(r *testRuleCtx) { r.cc.Chain = c } }
func withClosestRuntime(pid int, _ string) ruleOpt {
	return func(r *testRuleCtx) { r.cc.ClosestRtPID = pid }
}
func withIdentityAgentPID(pid int) ruleOpt {
	return func(r *testRuleCtx) { r.cc.IdentityAgentPID = pid }
}
func withCWDMatch(b bool) ruleOpt   { return func(r *testRuleCtx) { r.cc.CWDMatches = b } }
func withTmuxMatch(b bool) ruleOpt  { return func(r *testRuleCtx) { r.cc.TmuxMatches = b } }
func withMode(m Mode) ruleOpt       { return func(r *testRuleCtx) { r.cc.Mode = m } }
func withDeadReclaimMode(m Mode) ruleOpt {
	return func(r *testRuleCtx) { r.cc.DeadReclaimMode = m }
}
func withReclaimFails(err error) ruleOpt {
	return func(r *testRuleCtx) {
		r.cc.reclaim = func() error { return err }
	}
}
func withPIDAlive(pid int, alive bool) ruleOpt {
	return func(r *testRuleCtx) {
		prior := r.cc.IsPIDAlive
		r.cc.IsPIDAlive = func(p int) bool {
			if p == pid {
				return alive
			}
			if prior != nil {
				return prior(p)
			}
			return true
		}
	}
}

func newTestCtx(t *testing.T, opts ...ruleOpt) *testRuleCtx {
	t.Helper()
	buf := &bytes.Buffer{}
	r := &testRuleCtx{
		warnBuf: buf,
		cc: &CheckContext{
			Ctx:         context.Background(),
			Mode:        ModeStrict,
			TmuxMatches: true, // neutral default: absent-on-both counts as match
		},
	}
	r.cc.warnLogger = slog.New(slog.NewJSONHandler(buf, nil))
	// The reclaim hook flips the observable flag; production wires
	// this to guard.WritePID via Epic 4 callers.
	r.cc.reclaim = func() error {
		r.reclaimed = true
		return nil
	}
	for _, o := range opts {
		o(r)
	}
	r.cc.warnLoggedPtr = &r.warnLogged
	r.cc.reclaimedPtr = &r.reclaimed
	return r
}

// runRule invokes Rule with the built CheckContext.
func (r *testRuleCtx) runRule() error { return Rule(r.cc) }

func TestRule_NoRuntimeAncestor_Proceeds(t *testing.T) {
	ctx := newTestCtx(t, withClosestRuntime(0, ""), withIdentityAgentPID(999))
	if err := ctx.runRule(); err != nil {
		t.Errorf("want nil (no-runtime passthrough), got %v", err)
	}
}

func TestRule_RuntimeAncestor_PIDInChain_Proceeds(t *testing.T) {
	ctx := newTestCtx(t,
		withClosestRuntime(100, "claude"),
		withChain([]int{100, 200, 300}),
		withIdentityAgentPID(200),
	)
	if err := ctx.runRule(); err != nil {
		t.Errorf("want nil (pid match in chain), got %v", err)
	}
}

func TestRule_RuntimeAncestor_PIDZero_FallbackCWDTmuxMatch_Proceeds(t *testing.T) {
	ctx := newTestCtx(t,
		withClosestRuntime(100, "claude"),
		withChain([]int{100}),
		withIdentityAgentPID(0),
		withCWDMatch(true),
		withTmuxMatch(true),
	)
	if err := ctx.runRule(); err != nil {
		t.Errorf("want nil (PID==0 CWD+TMUX match), got %v", err)
	}
}

func TestRule_RuntimeAncestor_DeadPID_AutoReclaim(t *testing.T) {
	ctx := newTestCtx(t,
		withClosestRuntime(100, "claude"),
		withChain([]int{100}),
		withIdentityAgentPID(999),
		withPIDAlive(999, false),
		withCWDMatch(true),
		withTmuxMatch(true),
	)
	if err := ctx.runRule(); err != nil {
		t.Errorf("want nil (reclaim), got %v", err)
	}
	if !ctx.reclaimed {
		t.Error("expected reclaim side-effect")
	}
}

func TestRule_LivePIDMismatch_HardError(t *testing.T) {
	ctx := newTestCtx(t,
		withClosestRuntime(100, "claude"),
		withChain([]int{100}),
		withIdentityAgentPID(999),
		withPIDAlive(999, true),
	)
	err := ctx.runRule()
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var gErr *Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *Error, got %T", err)
	}
	if gErr.Guard != "cross_worktree" {
		t.Errorf("guard=%q", gErr.Guard)
	}
	if gErr.Reason != "pid_mismatch" {
		t.Errorf("reason=%q", gErr.Reason)
	}
}

func TestRule_LivePIDMismatch_PopulatesDetectedAgent(t *testing.T) {
	// Seed an identities dir with a file whose agent_pid is in the
	// caller's chain — that's the caller's "detected" agent. Rule
	// fires because the identity file being checked (999) is not.
	dir := t.TempDir()
	writeIdentityFile(t, dir, "impl_caller", 100, "claude")
	ctx := newTestCtx(t,
		withClosestRuntime(100, "claude"),
		withChain([]int{100}),
		withIdentityAgentPID(999),
		withPIDAlive(999, true),
	)
	ctx.cc.IdentitiesDir = dir
	ctx.cc.ExpectedAgent = "impl_target"

	err := ctx.runRule()
	var gErr *Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *Error, got %T", err)
	}
	if gErr.ExpectedAgent != "impl_target" {
		t.Errorf("ExpectedAgent=%q want impl_target", gErr.ExpectedAgent)
	}
	if gErr.DetectedAgent != "impl_caller" {
		t.Errorf("DetectedAgent=%q want impl_caller (resolved via findOwnedIdentity)", gErr.DetectedAgent)
	}
}

func TestRule_WarnMode_LogsButProceeds(t *testing.T) {
	ctx := newTestCtx(t,
		withClosestRuntime(100, "claude"),
		withChain([]int{100}),
		withIdentityAgentPID(999),
		withPIDAlive(999, true),
		withMode(ModeWarn),
	)
	if err := ctx.runRule(); err != nil {
		t.Errorf("warn mode should proceed, got %v", err)
	}
	if !ctx.warnLogged {
		t.Error("expected warnLogged tripwire to fire")
	}
	if !strings.Contains(ctx.warnBuf.String(), "cross_worktree") {
		t.Errorf("warn mode must emit structured slog with guard name; got %q", ctx.warnBuf.String())
	}
}

func TestRule_DeadPIDAutoReclaimOff_FallsThroughToStrict(t *testing.T) {
	// When dead_pid_auto_reclaim is disabled, a dead owner with
	// CWD+TMUX match must NOT be auto-reclaimed — it falls through
	// to step 3.4's strict hard error.
	ctx := newTestCtx(t,
		withClosestRuntime(100, "claude"),
		withChain([]int{100}),
		withIdentityAgentPID(999),
		withPIDAlive(999, false),
		withCWDMatch(true),
		withTmuxMatch(true),
		withDeadReclaimMode(ModeOff),
	)
	err := ctx.runRule()
	if err == nil {
		t.Fatal("want error (reclaim disabled → 3.4 strict)")
	}
	if ctx.reclaimed {
		t.Error("reclaim must not fire when DeadReclaimMode=off")
	}
}

func TestRule_DeadPIDAutoReclaimWarn_LogsAndReclaims(t *testing.T) {
	ctx := newTestCtx(t,
		withClosestRuntime(100, "claude"),
		withChain([]int{100}),
		withIdentityAgentPID(999),
		withPIDAlive(999, false),
		withCWDMatch(true),
		withTmuxMatch(true),
		withDeadReclaimMode(ModeWarn),
	)
	if err := ctx.runRule(); err != nil {
		t.Fatalf("warn reclaim should proceed, got %v", err)
	}
	if !ctx.reclaimed {
		t.Error("reclaim should still fire in warn mode")
	}
	if !strings.Contains(ctx.warnBuf.String(), "dead_pid_auto_reclaim") {
		t.Errorf("warn mode should emit slog event; got %q", ctx.warnBuf.String())
	}
}

func TestRule_ReclaimError_PropagatesUnderStrict(t *testing.T) {
	// A failed reclaim (disk full, lock contention, perms) must
	// not silently fall through to "allow." Propagate the error
	// so the caller surfaces the failure rather than the agent
	// running with a stale identity file.
	ctx := newTestCtx(t,
		withClosestRuntime(100, "claude"),
		withChain([]int{100}),
		withIdentityAgentPID(999),
		withPIDAlive(999, false),
		withCWDMatch(true),
		withTmuxMatch(true),
		withReclaimFails(errors.New("disk full")),
	)
	err := ctx.runRule()
	if err == nil {
		t.Fatal("want error when reclaim fails")
	}
}

func TestRule_OffMode_NoOp(t *testing.T) {
	ctx := newTestCtx(t,
		withClosestRuntime(100, "claude"),
		withChain([]int{100}),
		withIdentityAgentPID(999),
		withPIDAlive(999, true),
		withMode(ModeOff),
	)
	if err := ctx.runRule(); err != nil {
		t.Errorf("off mode should proceed, got %v", err)
	}
	if ctx.warnLogged {
		t.Error("off mode should not trip warnLogged")
	}
	if ctx.warnBuf.Len() != 0 {
		t.Errorf("off mode must not emit any slog events; got %q", ctx.warnBuf.String())
	}
}
