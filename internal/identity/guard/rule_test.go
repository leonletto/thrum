package guard

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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
	// The reclaim hook flips the observable flag; real implementation
	// will route through guard.WritePID in task 2.8.
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
		t.Error("expected warn log")
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
		t.Error("off mode should not log")
	}
}
