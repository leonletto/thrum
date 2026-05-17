package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// Compile-time assertion: ThrumCommandHandler satisfies scheduler.Handler.
// Plan Task 19 Step 1 + spec §8.4.1.
var _ scheduler.Handler = (*ThrumCommandHandler)(nil)

// newTestThrumCommandHandler constructs a ThrumCommandHandler with a
// hardwired bin path so tests don't rely on os.Executable (which in `go
// test` returns the test binary, not the thrum binary).
func newTestThrumCommandHandler(bin string) *ThrumCommandHandler {
	return &ThrumCommandHandler{binResolver: func() (string, error) { return bin, nil }}
}

// TestThrumCommandHandler_InvokesResolvedBinaryWithArgs verifies the
// composed CommandSpec is built with the resolved bin path + spec.Args and
// delegated to CommandHandler. Uses /bin/echo to stand in for the real
// thrum binary so the test doesn't need a built thrum + works in any CI
// image with a POSIX shell.
func TestThrumCommandHandler_InvokesResolvedBinaryWithArgs(t *testing.T) {
	skipIfNoSh(t)
	h := newTestThrumCommandHandler("/bin/echo")
	spec := scheduler.JobSpec{
		ID:           "test-tc",
		Type:         "thrum_command",
		ThrumCommand: &scheduler.ThrumCommandSpec{Args: []string{"version", "info"}},
	}
	reporter := &captureReporter{}
	if err := h.Dispatch(context.Background(), spec, "run-1", reporter, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if reporter.finalState != scheduler.StateCompleted {
		t.Errorf("state = %q; want completed", reporter.finalState)
	}
	stdoutTail, _ := reporter.detailsAtFinal["stdout_tail"].(string)
	if !strings.Contains(stdoutTail, "version info") {
		t.Errorf("stdout_tail %q doesn't contain the expected args", stdoutTail)
	}
}

// TestThrumCommandHandler_MissingSpec guards the nil-ThrumCommand path so
// the handler reports failed instead of panicking.
func TestThrumCommandHandler_MissingSpec(t *testing.T) {
	h := NewThrumCommandHandler()
	spec := scheduler.JobSpec{ID: "t", Type: "thrum_command", ThrumCommand: nil}
	reporter := &captureReporter{}
	if err := h.Dispatch(context.Background(), spec, "run-1", reporter, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if reporter.finalState != scheduler.StateFailed {
		t.Errorf("state = %q; want failed", reporter.finalState)
	}
}

// TestThrumCommandHandler_BinResolverError verifies a binResolver failure
// surfaces as StateFailed (not a panic).
func TestThrumCommandHandler_BinResolverError(t *testing.T) {
	h := &ThrumCommandHandler{
		binResolver: func() (string, error) { return "", errors.New("synthetic resolver failure") },
	}
	spec := scheduler.JobSpec{
		ID:           "t",
		Type:         "thrum_command",
		ThrumCommand: &scheduler.ThrumCommandSpec{Args: []string{"version"}},
	}
	reporter := &captureReporter{}
	if err := h.Dispatch(context.Background(), spec, "run-1", reporter, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if reporter.finalState != scheduler.StateFailed {
		t.Errorf("state = %q; want failed", reporter.finalState)
	}
}

// TestThrumCommandHandler_RealOsExecutable smoke-tests the production
// binResolver path: NewThrumCommandHandler() sets binResolver=os.Executable.
// We just verify the resolver returns a non-empty path; actually executing
// the test binary with `version` args would fail (test binaries don't
// understand thrum subcommands), so we don't run Dispatch.
func TestThrumCommandHandler_RealOsExecutable(t *testing.T) {
	h := NewThrumCommandHandler()
	path, err := h.binResolver()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if path == "" {
		t.Error("os.Executable returned empty path")
	}
}
