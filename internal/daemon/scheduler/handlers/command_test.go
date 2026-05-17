package handlers

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// Compile-time assertion: CommandHandler satisfies scheduler.Handler.
// Drift in either side (plan Task 19 Step 1 + spec §8.4.1 interface
// signatures) fails the build at this line. Mirrors the in-package
// `_ StateReporter = (*stateReporter)(nil)` pin in handler_test.go.
var _ scheduler.Handler = (*CommandHandler)(nil)

// captureReporter is a test StateReporter recording the transition history
// + the details payload for the final transition. Mirrors the substrate's
// real stateReporter API just enough for the handler-side test surface.
type captureReporter struct {
	finalState     scheduler.State
	detailsAtFinal map[string]any
	transitions    []scheduler.State
	stages         []string
}

func (c *captureReporter) Transition(to scheduler.State, _ string, details map[string]any) error {
	c.transitions = append(c.transitions, to)
	c.finalState = to
	c.detailsAtFinal = details
	return nil
}

func (c *captureReporter) Stage(name string) error {
	c.stages = append(c.stages, name)
	return nil
}

// skipIfNoSh skips tests that need /bin/sh — keeps the package go-getable
// on weird CI images. macOS + Linux always have /bin/sh.
func skipIfNoSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("command handler tests require POSIX shell")
	}
}

func TestCommandHandler_ExitZeroIsCompleted(t *testing.T) {
	skipIfNoSh(t)
	h := NewCommandHandler()
	spec := scheduler.JobSpec{
		ID:      "test-job",
		Type:    "command",
		Command: &scheduler.CommandSpec{Exec: "/usr/bin/true"},
	}
	reporter := &captureReporter{}
	if err := h.Dispatch(context.Background(), spec, "run-1", reporter, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if reporter.finalState != scheduler.StateCompleted {
		t.Errorf("state = %q; want completed (transitions=%v)", reporter.finalState, reporter.transitions)
	}
	if got, _ := reporter.detailsAtFinal["exit_code"].(int); got != 0 {
		t.Errorf("exit_code = %v; want 0", reporter.detailsAtFinal["exit_code"])
	}
}

func TestCommandHandler_ExitNonZeroIsFailed(t *testing.T) {
	skipIfNoSh(t)
	h := NewCommandHandler()
	spec := scheduler.JobSpec{
		ID:      "test-job",
		Type:    "command",
		Command: &scheduler.CommandSpec{Exec: "/usr/bin/false"},
	}
	reporter := &captureReporter{}
	if err := h.Dispatch(context.Background(), spec, "run-1", reporter, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if reporter.finalState != scheduler.StateFailed {
		t.Errorf("state = %q; want failed", reporter.finalState)
	}
	if got, _ := reporter.detailsAtFinal["exit_code"].(int); got == 0 {
		t.Errorf("exit_code = %v; want non-zero", reporter.detailsAtFinal["exit_code"])
	}
}

// TestCommandHandler_WorkingDir verifies cmd.Dir is honored by running
// /bin/pwd in a tempdir and checking the stdout tail. macOS may report a
// /private/-prefixed canonical path, so we resolve both before comparing.
func TestCommandHandler_WorkingDir(t *testing.T) {
	skipIfNoSh(t)
	h := NewCommandHandler()
	tmpDir := t.TempDir()
	realTmpDir, err := filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	spec := scheduler.JobSpec{
		ID:   "test-wd",
		Type: "command",
		Command: &scheduler.CommandSpec{
			Exec:           "/bin/pwd",
			WorkingDir:     tmpDir,
			TimeoutSeconds: 5,
		},
	}
	reporter := &captureReporter{}
	if err := h.Dispatch(context.Background(), spec, "run-1", reporter, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if reporter.finalState != scheduler.StateCompleted {
		t.Errorf("state = %q", reporter.finalState)
	}
	stdoutTail, _ := reporter.detailsAtFinal["stdout_tail"].(string)
	if !strings.Contains(stdoutTail, tmpDir) && !strings.Contains(stdoutTail, realTmpDir) {
		t.Errorf("stdout_tail %q doesn't contain working_dir %q (or %q)", stdoutTail, tmpDir, realTmpDir)
	}
}

// TestCommandHandler_EnvIsolation: a daemon-env var set via t.Setenv must
// NOT be visible to the child. Only spec.Command.Env is passed.
func TestCommandHandler_EnvIsolation(t *testing.T) {
	skipIfNoSh(t)
	t.Setenv("SECRET_DAEMON_VAR", "should-not-leak")

	h := NewCommandHandler()
	spec := scheduler.JobSpec{
		ID:   "test-env",
		Type: "command",
		Command: &scheduler.CommandSpec{
			Exec:           "/bin/sh",
			Args:           []string{"-c", `echo "GREETING=$GREETING|SECRET=$SECRET_DAEMON_VAR"`},
			Env:            map[string]string{"GREETING": "hello"},
			TimeoutSeconds: 5,
		},
	}
	reporter := &captureReporter{}
	if err := h.Dispatch(context.Background(), spec, "run-1", reporter, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if reporter.finalState != scheduler.StateCompleted {
		t.Fatalf("state = %q; want completed", reporter.finalState)
	}
	stdoutTail, _ := reporter.detailsAtFinal["stdout_tail"].(string)
	if !strings.Contains(stdoutTail, "GREETING=hello") {
		t.Errorf("child did not see GREETING=hello: %q", stdoutTail)
	}
	if strings.Contains(stdoutTail, "should-not-leak") {
		t.Errorf("daemon env var leaked to child: %q", stdoutTail)
	}
	// Trim trailing whitespace so we can assert SECRET= ends with empty value.
	if trimmed := strings.TrimRight(stdoutTail, "\n\r "); !strings.HasSuffix(trimmed, "SECRET=") {
		t.Errorf("SECRET_DAEMON_VAR should be empty in child env (expected stdout to end with `SECRET=`): %q", stdoutTail)
	}
}

// TestCommandHandler_CancelSendsSIGTERM: parent ctx cancel propagates to
// the child as SIGTERM. A long-running sleep responds to SIGTERM
// immediately so the test doesn't have to exercise the SIGKILL escalation
// path (full-grace test would need a SIGTERM-trapping script and is
// flaky in CI).
func TestCommandHandler_CancelSendsSIGTERM(t *testing.T) {
	skipIfNoSh(t)
	h := NewCommandHandler()
	spec := scheduler.JobSpec{
		ID:   "test-cancel",
		Type: "command",
		Command: &scheduler.CommandSpec{
			Exec:           "/bin/sh",
			Args:           []string{"-c", "sleep 30"},
			TimeoutSeconds: 60,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	reporter := &captureReporter{}
	done := make(chan struct{})
	go func() {
		_ = h.Dispatch(ctx, spec, "run-1", reporter, nil)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
		if reporter.finalState != scheduler.StateCancelled {
			t.Errorf("state = %q; want cancelled (transitions=%v)", reporter.finalState, reporter.transitions)
		}
	case <-time.After(10 * time.Second):
		t.Error("Dispatch did not return within 10s of cancel")
	}
}

// TestCommandHandler_MissingSpec covers the defensive guard against a
// type=command job with a nil Command sub-tree (validator should reject
// this earlier, but the handler must not panic).
func TestCommandHandler_MissingSpec(t *testing.T) {
	h := NewCommandHandler()
	spec := scheduler.JobSpec{ID: "test", Type: "command", Command: nil}
	reporter := &captureReporter{}
	if err := h.Dispatch(context.Background(), spec, "run-1", reporter, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if reporter.finalState != scheduler.StateFailed {
		t.Errorf("state = %q; want failed", reporter.finalState)
	}
}
