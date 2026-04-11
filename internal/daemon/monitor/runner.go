package monitor

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"syscall"
	"time"
)

const (
	maxLineBytes        = 2048
	lastStdoutBytes     = 500
	defaultShutdownGrace = 5 * time.Second
)

// RunnerJob is the minimal interface the Runner needs from a monitor job
// specification. Using an interface decouples runner.go from the concrete
// MonitorJob struct, which is defined in job.go by a parallel task.
type RunnerJob interface {
	GetID() string
	GetName() string
	GetArgv() []string
	GetCwd() string
	GetEnv() map[string]string
	GetDebounceSeconds() int
}

// DeliverFn is the callback used by the runner to deliver a matched line
// (or trailing summary) through the debouncer to the message pipeline.
type DeliverFn func(jobName, content string)

// ExitNoticeFn is the callback invoked when the child process exits.
// The supervisor uses it to send an exit-notice message and mark the
// monitor dead in the store. pid is the OS process id captured after
// cmd.Start() (may be 0 if the child never started).
type ExitNoticeFn func(jobName string, exitCode, pid int, duration time.Duration, lastStdoutTail string)

// Runner manages a single child process for a monitor job.
type Runner struct {
	job           RunnerJob
	re            *regexp.Regexp
	deliver       DeliverFn
	exitNotice    ExitNoticeFn
	debouncer     *Debouncer
	shutdownGrace time.Duration
}

// NewRunner constructs a Runner for the given job spec and compiled regex.
// exitNotice may be nil in unit tests that do not exercise exit handling.
// deliver must not be nil.
func NewRunner(job RunnerJob, re *regexp.Regexp, exitNotice ExitNoticeFn, deliver DeliverFn) (*Runner, error) {
	if len(job.GetArgv()) == 0 {
		return nil, fmt.Errorf("runner: empty argv")
	}
	if re == nil {
		return nil, fmt.Errorf("runner: nil regex")
	}
	if deliver == nil {
		return nil, fmt.Errorf("runner: nil deliver callback")
	}

	window := time.Duration(job.GetDebounceSeconds()) * time.Second
	if window <= 0 {
		window = 60 * time.Second
	}

	r := &Runner{
		job:           job,
		re:            re,
		deliver:       deliver,
		exitNotice:    exitNotice,
		shutdownGrace: defaultShutdownGrace,
	}
	r.debouncer = NewDebouncer(
		window,
		time.Now,
		func(content string) { deliver(job.GetName(), content) },
	)
	return r, nil
}

// Run spawns the child process, reads its merged stdout+stderr line-by-line,
// regex-filters each line, and returns when the child exits or ctx is
// canceled. Always calls exitNotice (if non-nil) before returning.
//
// Shutdown protocol on ctx cancellation: SIGTERM is sent first; if the
// child has not exited within 5 seconds, SIGKILL is sent.
func (r *Runner) Run(ctx context.Context) error {
	start := time.Now()

	// #nosec G204 -- argv is structured input from an already-trusted local
	// caller (unix-socket RPC); reaching this function already implies local
	// shell access as the daemon user. Never pass shell strings here.
	cmd := exec.CommandContext(ctx, r.job.GetArgv()[0], r.job.GetArgv()[1:]...)
	cmd.Dir = r.job.GetCwd()
	cmd.Env = buildEnv(r.job)

	// Merge stdout and stderr into a single pipe so the LineReader sees
	// both streams interleaved as the child emits them. We use os.Pipe()
	// explicitly rather than cmd.StdoutPipe() to avoid races with
	// cmd.Wait() closing the pipe too early.
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("runner: create pipe: %w", err)
	}
	cmd.Stdout = pipeW
	cmd.Stderr = pipeW

	if err := cmd.Start(); err != nil {
		_ = pipeW.Close()
		_ = pipeR.Close()
		return fmt.Errorf("runner: start %q: %w", r.job.GetArgv()[0], err)
	}
	// Capture the child PID for the exit notice (design spec §Child exit
	// requires "(pid N)" in the formatted notice).
	childPID := 0
	if cmd.Process != nil {
		childPID = cmd.Process.Pid
	}
	// Close the daemon's copy of the write end. The child still holds its
	// copy (inherited via fork/exec), so pipeR will receive EOF exactly
	// when the child exits and closes its side.
	_ = pipeW.Close()

	// Capture the last lastStdoutBytes bytes for the exit-notice tail.
	tail := newRingBuffer(lastStdoutBytes)
	tee := io.TeeReader(pipeR, tail)
	lr := NewLineReader(tee, maxLineBytes)

	// processDone is closed by Run() after cmd.Wait() returns, signalling
	// the shutdown watcher goroutine that the process has fully exited so
	// it can stop waiting on the SIGTERM grace window.
	processDone := make(chan struct{})

	// Shutdown watcher: on ctx cancellation, SIGTERM → 5s → SIGKILL.
	go func() {
		select {
		case <-ctx.Done():
			if cmd.Process != nil {
				_ = cmd.Process.Signal(syscall.SIGTERM)
				select {
				case <-processDone:
					// Child exited cleanly after SIGTERM — nothing more to do.
				case <-time.After(r.shutdownGrace):
					// Grace period elapsed; force-kill.
					_ = cmd.Process.Kill()
				}
			}
		case <-processDone:
			// Process exited naturally before ctx was canceled.
		}
	}()

	// flushTimer fires r.debouncer.FlushExpired() after the debounce window
	// elapses, so any matches suppressed since the leading-edge emit are
	// delivered as a trailing summary. The timer is reset on every match so
	// a fresh wave of activity extends the window; an inactive stream lets
	// it fire once at (lastEmitAt + window). time.AfterFunc is used so
	// Reset is safe to call from the read-loop goroutine without drain
	// gymnastics (Go 1.23+ timer semantics).
	flushTimer := time.AfterFunc(24*time.Hour, func() {
		r.debouncer.FlushExpired()
	})
	flushTimer.Stop()
	defer flushTimer.Stop()

	// Read, filter, and emit matches through the debouncer.
	for {
		line, err := lr.ReadLine()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("monitor runner %s: read error: %v", r.job.GetName(), err)
			break
		}
		if r.re.MatchString(line.Content) {
			// Per design spec §Line-length cap, a truncated line that matches
			// is delivered as the first 2KB followed by an explicit marker so
			// the receiver can distinguish truncation from natural EOL.
			content := line.Content
			if line.Truncated {
				content += "\n[line truncated, original length ≥ 2048 bytes]"
			}
			delay := r.debouncer.OnMatch(content)
			if delay > 0 {
				flushTimer.Reset(delay)
			}
		}
	}

	// Wait for the process to fully exit and collect its exit code.
	waitErr := cmd.Wait()
	close(processDone)

	_ = pipeR.Close()

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	if r.exitNotice != nil {
		r.exitNotice(r.job.GetName(), exitCode, childPID, time.Since(start), tail.String())
	}

	return nil
}

// buildEnv constructs the child process environment from a minimal base
// (pulling HOME, USER, LANG, TZ from the daemon's own env via os.Getenv)
// plus any job-specific overrides in job.Env.
//
// The daemon's full environment is NOT inherited — only the five base keys
// are pulled individually via os.Getenv, then job.Env is layered on top.
func buildEnv(job RunnerJob) []string {
	base := []string{
		"HOME=" + osGetenv("HOME"),
		"USER=" + osGetenv("USER"),
		"LANG=" + osGetenv("LANG"),
		"TZ=" + osGetenv("TZ"),
		"PATH=/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin",
	}
	for k, v := range job.GetEnv() {
		base = append(base, k+"="+v)
	}
	return base
}

// osGetenv is a package-level variable so tests can override it to inject a
// predictable environment without relying on the test process's real env.
var osGetenv = os.Getenv

// ringBuffer keeps the last N bytes written to it, discarding older bytes.
type ringBuffer struct {
	buf []byte
	max int
}

func newRingBuffer(max int) *ringBuffer { return &ringBuffer{max: max} }

func (b *ringBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
	return len(p), nil
}

func (b *ringBuffer) String() string { return string(b.buf) }
