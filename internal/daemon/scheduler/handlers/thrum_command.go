package handlers

import (
	"context"
	"os"
	"time"

	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// ThrumCommandHandler implements scheduler.Handler for type=thrum_command
// jobs. Per spec §8.3.9 it invokes the daemon's own binary (resolved at
// dispatch time via os.Executable) with the operator-supplied Args slice,
// bypassing shell parsing for safer argv-slice invocation.
//
// Internally it composes a synthetic CommandSpec and delegates to
// CommandHandler so env isolation, signal handling, and stdout/stderr
// capture are shared between the two handlers.
type ThrumCommandHandler struct {
	// binResolver returns the absolute path to the thrum binary. Production
	// uses os.Executable; tests inject a known-good binary (e.g. /bin/echo)
	// because os.Executable in `go test` returns the test binary, not thrum.
	binResolver func() (string, error)
}

func NewThrumCommandHandler() *ThrumCommandHandler {
	return &ThrumCommandHandler{binResolver: os.Executable}
}

func (t *ThrumCommandHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"executing": defaultCommandTimeout}
}

func (t *ThrumCommandHandler) Reconcile(_ context.Context, _ scheduler.JobSpec, _ string, _ scheduler.State) (scheduler.State, error) {
	return scheduler.StateFailed, scheduler.ErrLostTrack
}

func (t *ThrumCommandHandler) Dispatch(ctx context.Context, job scheduler.JobSpec, runID string, reporter scheduler.StateReporter, signals <-chan *scheduler.Completion) error {
	if job.ThrumCommand == nil {
		return reporter.Transition(scheduler.StateFailed, "thrum_command spec missing", nil)
	}
	binPath, err := t.binResolver()
	if err != nil {
		return reporter.Transition(scheduler.StateFailed, "binResolver: "+err.Error(), nil)
	}

	// Compose a synthetic command spec and delegate so the exec + capture
	// + signal path stays single-sourced. Args populates CommandSpec.Args
	// (internal-only field — see CommandSpec docs).
	fakeJob := job
	fakeJob.Type = "command"
	fakeJob.Command = &scheduler.CommandSpec{
		Exec:           binPath,
		Args:           job.ThrumCommand.Args,
		TimeoutSeconds: 300,
	}
	return NewCommandHandler().Dispatch(ctx, fakeJob, runID, reporter, signals)
}
