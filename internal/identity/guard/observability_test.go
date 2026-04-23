package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// TestEmitGuardFire_UnifiedShape exercises the central observability
// helper: every guard outcome — strict rejection, warn fall-through,
// Rule auto-reclaim — must produce a single slog event with the same
// baseline fields so operators can alert on guard= + outcome=.
func TestEmitGuardFire_UnifiedShape(t *testing.T) {
	cases := []struct {
		name    string
		mode    Mode
		outcome string
		e       *Error
	}{
		{
			name:    "strict denies",
			mode:    ModeStrict,
			outcome: "denied",
			e: &Error{
				Guard:         "cross_worktree",
				Reason:        "pid_mismatch",
				CallerPID:     1234,
				CallerCWD:     "/tmp/x",
				ExpectedAgent: "impl_a",
				ExpectedPID:   4321,
			},
		},
		{
			name:    "warn allows",
			mode:    ModeWarn,
			outcome: "allowed",
			e:       &Error{Guard: "unauthenticated_rpc", Reason: "no_caller_agent_id"},
		},
		{
			name:    "dead-pid reclaim",
			mode:    ModeStrict,
			outcome: "auto_reclaimed",
			e:       &Error{Guard: "dead_pid_auto_reclaim", Reason: "reclaim", ExpectedPID: 999999},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			logger := slog.New(slog.NewJSONHandler(buf, nil))
			emitGuardFire(logger, tc.mode, tc.outcome, tc.e)

			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("parse slog record: %v (raw: %q)", err, buf.String())
			}
			if rec["msg"] != "identity_guard_fire" {
				t.Errorf("msg = %v, want identity_guard_fire", rec["msg"])
			}
			if rec["guard"] != tc.e.Guard {
				t.Errorf("guard = %v, want %v", rec["guard"], tc.e.Guard)
			}
			if rec["mode"] != string(tc.mode) {
				t.Errorf("mode = %v, want %v", rec["mode"], tc.mode)
			}
			if rec["outcome"] != tc.outcome {
				t.Errorf("outcome = %v, want %v", rec["outcome"], tc.outcome)
			}
			if rec["reason"] != tc.e.Reason {
				t.Errorf("reason = %v, want %v", rec["reason"], tc.e.Reason)
			}
		})
	}
}

// TestEmitGuardFire_NilLoggerNoOp confirms passing a nil logger is a
// safe no-op — every guard call site may pass nil without a panic.
func TestEmitGuardFire_NilLoggerNoOp(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("nil logger should not panic, got %v", r)
		}
	}()
	emitGuardFire(nil, ModeStrict, "denied", &Error{Guard: "x", Reason: "y"})
}

// TestGuardStrictMode_EmitsEvent proves strict-mode rejection emits
// the unified structured event (closing the pre-5.3 gap where only
// warn mode was observable).
func TestGuardStrictMode_EmitsEvent(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))

	// G2 strict-mode rejection on a non-git tempdir.
	err := G2(ModeStrict, t.TempDir(), false, logger)
	if err == nil {
		t.Fatal("want G2 error on non-git dir in strict mode")
	}
	var rec map[string]any
	if jErr := json.Unmarshal(buf.Bytes(), &rec); jErr != nil {
		t.Fatalf("strict mode should emit event; got %q (parse err: %v)", buf.String(), jErr)
	}
	if rec["guard"] != "non_git_bootstrap" {
		t.Errorf("guard = %v, want non_git_bootstrap", rec["guard"])
	}
	if rec["outcome"] != "denied" {
		t.Errorf("outcome = %v, want denied", rec["outcome"])
	}
	if rec["mode"] != "strict" {
		t.Errorf("mode = %v, want strict", rec["mode"])
	}
}

// TestG3_StrictMode_EmitsEvent confirms G3 also emits on strict-mode
// reject — the event shape is the same regardless of which guard
// fired. G3 is the pickiest because it fires on bootstrap paths.
func TestG3_StrictMode_EmitsEvent(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	if err := G3(ModeStrict, "", logger); err == nil {
		t.Fatal("want G3 error on empty CallerAgentID in strict mode")
	}
	if !strings.Contains(buf.String(), "unauthenticated_rpc") {
		t.Errorf("G3 strict-mode event missing; got %q", buf.String())
	}
	if !strings.Contains(buf.String(), `"outcome":"denied"`) {
		t.Errorf("G3 strict-mode event missing outcome=denied; got %q", buf.String())
	}
}

// TestRule_DeadPIDReclaim_EmitsEvent proves Rule's auto-reclaim
// clause emits a distinct outcome=auto_reclaimed event so operators
// can monitor the reclaim frequency.
func TestRule_DeadPIDReclaim_EmitsEvent(t *testing.T) {
	// Use a minimal CheckContext that exercises the dead-PID reclaim
	// branch: the chain contains IdentityAgentPID so Rule matches,
	// but IsPIDAlive returns false → step 3.3 auto-reclaim path.
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	cc := &CheckContext{
		Ctx:              context.Background(),
		Mode:             ModeStrict,
		DeadReclaimMode:  ModeStrict,
		Chain:            []int{1000, 1500}, // does NOT contain IdentityAgentPID
		ClosestRtPID:     1000,
		IdentityAgentPID: 2000, // dead, not in chain → step 3.3
		IsPIDAlive:       func(pid int) bool { return false },
		CWDMatches:       true,
		TmuxMatches:      true,
		ExpectedAgent:    "impl_self",
		IdentitiesDir:    t.TempDir(),
		warnLogger:       logger,
	}
	_ = Rule(cc)
	// Reclaim doesn't write an identity file in this synthetic
	// harness (IdentitiesDir is empty of matching files) but it
	// should still emit the reclaim event.
	if !strings.Contains(buf.String(), "auto_reclaimed") && !strings.Contains(buf.String(), "dead_pid_auto_reclaim") {
		t.Errorf("Rule dead-PID reclaim should emit event; got %q", buf.String())
	}
}
