// Package handlers contains the substrate-owned scheduler handlers
// (command, thrum_command). User-facing handlers (scheduled_agent, nudge)
// live in B-B1. Internal-job handlers register via Scheduler.RegisterInternal
// at daemon startup.
package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

const (
	defaultCommandTimeout = 5 * time.Minute
	defaultTeardownGrace  = 5 * time.Second
	// stdoutTailCap is the byte ceiling for stdout/stderr tail captures
	// recorded in event details. Larger output is truncated from the front
	// — operators usually care about the last N bytes for diagnostics.
	stdoutTailCap = 4096
)

// CommandHandler implements scheduler.Handler for type=command jobs.
// Substrate-owned per spec §2.3: every Scheduler ships with this handler
// already registered; operators do not need to register it.
type CommandHandler struct{}

func NewCommandHandler() *CommandHandler { return &CommandHandler{} }

func (c *CommandHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"executing": defaultCommandTimeout}
}

// Reconcile reports lost-track. The command handler does not survive
// daemon restart — the child process was killed when the daemon stopped,
// so a previously-dispatched run is unrecoverable.
func (c *CommandHandler) Reconcile(_ context.Context, _ scheduler.JobSpec, _ string, _ scheduler.State) (scheduler.State, error) {
	return scheduler.StateFailed, scheduler.ErrLostTrack
}

// Dispatch runs the command. Behavior:
//   - Env isolation: child sees ONLY spec.Command.Env (daemon env does
//     not leak). PATH resolution for `spec.Exec` uses the daemon's PATH at
//     exec.LookPath time, but the child process itself has the spec's env.
//   - WorkingDir from spec.
//   - SIGTERM on context cancel; SIGKILL after a 5s grace if the child
//     ignores SIGTERM.
//   - stdout/stderr tail (last 4KB each) recorded in event details.
//
// The signals channel is unused by command handler — there is no
// thrum-bound completion signal for shell commands.
func (c *CommandHandler) Dispatch(ctx context.Context, job scheduler.JobSpec, _ string, reporter scheduler.StateReporter, _ <-chan *scheduler.Completion) error {
	if job.Command == nil {
		return reporter.Transition(scheduler.StateFailed, "command spec missing", nil)
	}
	spec := job.Command

	if err := reporter.Transition(scheduler.StateRunning, "command executing", nil); err != nil {
		return err
	}
	if err := reporter.Stage("executing"); err != nil {
		return err
	}

	timeout := defaultCommandTimeout
	if spec.TimeoutSeconds > 0 {
		timeout = time.Duration(spec.TimeoutSeconds) * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Internal-use Args slice (set by thrum_command handler in Task 15) takes
	// precedence over shell-parsing Exec. User-facing type:command leaves
	// Args empty and the binary is invoked via Exec only (no shell parse
	// here — the plan defers shell-parsing to a follow-up; substrate cuts a
	// clean line at "binary path + optional internal args").
	//nolint:gosec // G204: spec.Exec is operator-controlled job config by design
	cmd := exec.CommandContext(execCtx, spec.Exec, spec.Args...)
	if spec.WorkingDir != "" {
		cmd.Dir = spec.WorkingDir
	}
	// Env isolation: empty slice, then spec.Env. Daemon env does NOT leak.
	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = newTailWriter(&stdoutBuf, stdoutTailCap)
	cmd.Stderr = newTailWriter(&stderrBuf, stdoutTailCap)

	if err := cmd.Start(); err != nil {
		return reporter.Transition(scheduler.StateFailed, "start: "+err.Error(), nil)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case waitErr := <-done:
		details := map[string]any{
			"stdout_tail": stdoutBuf.String(),
			"stderr_tail": stderrBuf.String(),
			"exit_code":   exitCodeOf(waitErr),
		}
		if waitErr == nil {
			return reporter.Transition(scheduler.StateCompleted, "exit 0", details)
		}
		return reporter.Transition(scheduler.StateFailed, "exit "+strconv.Itoa(exitCodeOf(waitErr)), details)

	case <-execCtx.Done():
		// Timeout or parent cancel. SIGTERM, then SIGKILL after grace.
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-done:
			// Exited within grace.
		case <-time.After(defaultTeardownGrace):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
		reason := "cancelled"
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			reason = fmt.Sprintf("timeout after %s", timeout)
		}
		return reporter.Transition(scheduler.StateCancelled, reason, map[string]any{
			"stdout_tail": stdoutBuf.String(),
			"stderr_tail": stderrBuf.String(),
		})
	}
}

// exitCodeOf extracts the process exit code from a Wait() error. Returns 0
// on success, the exit code on *exec.ExitError, and -1 for other errors
// (e.g., signal-killed or I/O failure).
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// tailWriter retains the last `limit` bytes written. Bytes past the limit
// are dropped from the front of the buffer.
type tailWriter struct {
	buf   *bytes.Buffer
	limit int
}

func newTailWriter(buf *bytes.Buffer, limit int) io.Writer {
	return &tailWriter{buf: buf, limit: limit}
}

func (t *tailWriter) Write(p []byte) (int, error) {
	n, err := t.buf.Write(p)
	if t.buf.Len() > t.limit {
		excess := t.buf.Len() - t.limit
		t.buf.Next(excess)
	}
	return n, err
}
