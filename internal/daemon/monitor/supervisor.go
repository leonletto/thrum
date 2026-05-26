package monitor

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oklog/ulid/v2"
)

// supervisorULIDEntropy is a package-level monotonic ULID entropy source
// backed by crypto/rand. Guarded by supervisorULIDMu because ulid.Monotonic
// is not safe for concurrent use; the identity package uses the same
// pattern (see internal/identity/identity.go:161).
var (
	supervisorULIDMu      sync.Mutex
	supervisorULIDEntropy = ulid.Monotonic(rand.Reader, 0)
)

// newMonitorID returns a new "mon_<ULID>" identifier using the
// mutex-guarded crypto/rand entropy source. Safe for concurrent callers.
func newMonitorID(t time.Time) string {
	supervisorULIDMu.Lock()
	defer supervisorULIDMu.Unlock()
	return "mon_" + ulid.MustNew(ulid.Timestamp(t), supervisorULIDEntropy).String()
}

// MaxConcurrentMonitors is the hard cap on simultaneously running monitors
// per daemon instance. Exported so tests and future config code can reference
// the same constant.
const MaxConcurrentMonitors = 100

// MinDebounceSeconds is the lowest allowed debounce value for user submissions.
const MinDebounceSeconds = 30

// DefaultDebounceSeconds is used when the caller does not specify a debounce.
const DefaultDebounceSeconds = 60

// stopSyncWait bounds how long Stop blocks the RPC critical path waiting for
// the runner goroutine to actually exit. The DB row is marked stopped
// synchronously regardless; this wait only governs whether the RPC response
// is held until the in-process runner finishes its cleanup. Override in tests
// via the package-level variable; production callers receive the const value.
//
// See thrum-puhr.9.2: previously this was 10s and gated the RPC response on
// runner exit, so a stuck reader goroutine (e.g. grandchildren holding the
// child's stdout/stderr pipe open) wedged the daemon's response writer.
var stopSyncWait = 2 * time.Second

// Typed errors returned by Add (translated to user-friendly messages by the
// RPC handler layer in Epic B).
var (
	ErrCapExceeded      = errors.New("maximum concurrent monitors reached")
	ErrNameTaken        = errors.New("monitor name already in use")
	ErrDebounceTooShort = errors.New("debounce below 30s minimum")
	ErrInvalidRegex     = errors.New("invalid match pattern")
	ErrInvalidSchedule  = errors.New("invalid schedule")
)

// SubmitSpec is the value-object passed from an RPC handler to Add. It holds
// the user-supplied monitor configuration before it is validated and persisted.
//
// Schedule is an optional 5-field cron expression. When set, the runner fires
// the child one-shot per scheduled tick (no auto-restart between ticks); when
// empty, the runner runs the child continuously with exponential-backoff
// auto-restart (capped by a per-window budget).
type SubmitSpec struct {
	Name            string
	Argv            []string
	MatchPattern    string
	Target          string
	Cwd             string
	Env             map[string]string
	DebounceSeconds int
	Schedule        string
}

// restartTunables groups the restart-budget + backoff knobs that govern
// continuous-mode auto-restart behavior. Lives on the supervisor instance
// (not as package-level vars) so multiple test supervisors can run
// concurrently with their own settings under the race detector without
// stomping on each other.
//
// Backoff schedule (clamped at MaxBackoff): InitialBackoff, doubled each
// child exit, capped at MaxBackoff. A successful run of BackoffResetAfter
// or longer resets both backoff and the restart-window history.
type restartTunables struct {
	MaxRestartsPerWindow int
	RestartBudgetWindow  time.Duration
	InitialBackoff       time.Duration
	MaxBackoff           time.Duration
	BackoffResetAfter    time.Duration
}

// defaultRestartTunables returns the production restart-budget settings:
// 10 restarts in 5 minutes, 1s→60s exponential backoff, 10s healthy-run
// resets the counter.
func defaultRestartTunables() restartTunables {
	return restartTunables{
		MaxRestartsPerWindow: 10,
		RestartBudgetWindow:  5 * time.Minute,
		InitialBackoff:       time.Second,
		MaxBackoff:           60 * time.Second,
		BackoffResetAfter:    10 * time.Second,
	}
}

// defaultScheduledTickWait is the production wait between scheduled fires:
// sleep until `until`, respecting ctx cancellation. Each supervisor holds a
// pointer to this function (or a test override) on its scheduledTickWait
// field so tests can drive ticks deterministically without burning minutes.
func defaultScheduledTickWait(ctx context.Context, until time.Time) {
	wait := time.Until(until)
	if wait < 0 {
		wait = 0
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// runnerHandle groups the per-runner context cancel function and a done channel
// that is closed when the runner goroutine returns.
//
// StoppedByUser is set by Stop() before it cancels the runner context. The
// runner's exitNotice closure reads this flag and skips store.MarkDead when
// true, because Stop is about to call store.Delete on the same row and the
// MarkDead write would just be wasted I/O overwritten by the immediate delete.
// Atomic.Bool avoids a lock here — the flag is set once and read once.
type runnerHandle struct {
	job           *MonitorJob
	cancel        context.CancelFunc
	done          chan struct{}
	stoppedByUser atomic.Bool
	// pid is the OS process id of the currently running child, set by the
	// onStart callback right after cmd.Start succeeds. 0 if not yet
	// started or already exited. Read by Supervisor.List / GetByID to
	// enrich jobToView responses (review finding R2.3).
	pid atomic.Int64
}

// MonitorSupervisor owns the set of running monitor jobs.  It loads persisted
// monitors from the DB on Start, accepts new submissions via Add, and stops
// individual runners via Stop.  Lifecycle follows the same Start(ctx) pattern
// used by BackupScheduler and PeriodicSyncScheduler.
type MonitorSupervisor struct {
	store    *MonitorStore
	delivery *Delivery

	mu      sync.Mutex
	runners map[string]*runnerHandle // id → handle
	// pending counts Add calls that have passed the cap check but not yet
	// populated runners (still doing DB insert / launch). Guarded by mu.
	// Used to prevent a TOCTOU race where two concurrent Adds see
	// len(runners) == 99 and both proceed to launch, ending with 101 runners.
	pending int

	// baseCtx is the long-lived supervisor context captured by Start(). All
	// runner contexts derive from baseCtx so their lifetime is tied to the
	// daemon's lifetime, NOT to the ctx of whatever RPC call happened to
	// invoke Add(). Without this, an RPC-submitted monitor is killed the
	// moment the RPC handler returns because its derived child context
	// dies with the request. Set exactly once by Start; read by launch().
	baseCtx context.Context

	// Restart + backoff knobs for continuous-mode auto-restart. Held per
	// instance so multiple supervisors (in tests) don't race on shared
	// package-level vars. Populated by NewMonitorSupervisor with production
	// defaults; tests instantiate with custom values for fast coverage of
	// budget-exhaustion paths.
	tunables restartTunables

	// scheduledTickWait is the function used by scheduled-mode runLoops to
	// wait until the next cron tick. Default is defaultScheduledTickWait;
	// tests override to drive ticks deterministically.
	scheduledTickWait func(ctx context.Context, until time.Time)
}

// NewMonitorSupervisor constructs a supervisor backed by store and delivery.
// The supervisor's runner map is empty until Start is called.
func NewMonitorSupervisor(store *MonitorStore, delivery *Delivery) *MonitorSupervisor {
	return &MonitorSupervisor{
		store:             store,
		delivery:          delivery,
		runners:           make(map[string]*runnerHandle),
		tunables:          defaultRestartTunables(),
		scheduledTickWait: defaultScheduledTickWait,
	}
}

// Start reloads any persisted monitors that were in StatusRunning from the DB
// and launches a Runner goroutine for each.  It then blocks until ctx is
// canceled, at which point it cancels every runner and waits for them all to
// exit (bounded by a 10-second timeout).
//
// This follows the Start(ctx context.Context) lifecycle pattern used by
// BackupScheduler and PeriodicSyncScheduler.
func (s *MonitorSupervisor) Start(ctx context.Context) {
	log.Printf("monitor_supervisor: starting, reloading persisted jobs")

	// Capture the long-lived daemon context so subsequent Add() calls via
	// RPC can derive runner contexts from it rather than from the
	// transient RPC request context. Guarded by mu because launch() reads
	// it and launch() may be called from an RPC handler goroutine.
	s.mu.Lock()
	s.baseCtx = ctx
	s.mu.Unlock()

	persisted, err := s.store.ListByStatus(ctx, StatusRunning)
	if err != nil {
		log.Printf("monitor_supervisor: reload failed: %v", err)
	} else {
		for _, job := range persisted {
			if launchErr := s.launch(job); launchErr != nil {
				log.Printf("monitor_supervisor: relaunch %s failed: %v", job.Name, launchErr)
				_ = s.store.MarkDead(ctx, job.ID, -1, time.Now())
			}
		}
	}

	<-ctx.Done()

	s.mu.Lock()
	log.Printf("monitor_supervisor: stopping, sending SIGTERM to %d runners", len(s.runners))
	handles := make([]*runnerHandle, 0, len(s.runners))
	for _, h := range s.runners {
		h.cancel()
		handles = append(handles, h)
	}
	s.mu.Unlock()

	// Wait for all runners to exit with a bounded timeout.
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for _, h := range handles {
		select {
		case <-h.done:
		case <-deadline.C:
			log.Printf("monitor_supervisor: shutdown timeout, some runners may still be running")
			return
		}
	}
	log.Printf("monitor_supervisor: stopped cleanly")
}

// Add validates spec, persists a new MonitorJob, launches its Runner goroutine,
// and returns the assigned monitor ID.  Returns typed errors (ErrCapExceeded,
// ErrDebounceTooShort, ErrInvalidRegex, ErrInvalidSchedule) that the RPC
// handler can translate to user-friendly messages.
func (s *MonitorSupervisor) Add(ctx context.Context, spec SubmitSpec) (string, error) {
	// Validation
	if spec.DebounceSeconds == 0 {
		spec.DebounceSeconds = DefaultDebounceSeconds
	}
	if spec.DebounceSeconds < MinDebounceSeconds {
		return "", ErrDebounceTooShort
	}
	if _, err := regexp.Compile(spec.MatchPattern); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidRegex, err)
	}
	if spec.Name == "" {
		return "", errors.New("name required")
	}
	if len(spec.Argv) == 0 {
		return "", errors.New("argv required")
	}
	if spec.Cwd == "" {
		return "", errors.New("cwd required")
	}
	if spec.Target == "" {
		return "", errors.New("target required")
	}
	if spec.Env == nil {
		spec.Env = make(map[string]string)
	}
	if spec.Schedule != "" {
		if _, err := ParseSchedule(spec.Schedule); err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidSchedule, err)
		}
	}

	// Cap check + slot reservation — must hold the lock across BOTH the
	// count check and the reservation to avoid a TOCTOU race: without this
	// reservation, two concurrent Add calls with 99 runners active could
	// both pass the cap check and end up launching the 100th AND 101st
	// runners. We can't put the real handle in the runners map yet (we
	// don't have a job ID until after Insert), so reserve a slot by
	// incrementing a pending counter. The counter is decremented after
	// launch succeeds or fails.
	s.mu.Lock()
	if len(s.runners)+s.pending >= MaxConcurrentMonitors {
		s.mu.Unlock()
		return "", ErrCapExceeded
	}
	s.pending++
	s.mu.Unlock()
	// Always release the reservation — success path removes it after launch
	// has populated the runners map; failure path removes it before return.
	releasePending := func() {
		s.mu.Lock()
		s.pending--
		s.mu.Unlock()
	}

	now := time.Now().UTC()
	job := &MonitorJob{
		ID:              newMonitorID(now),
		Name:            spec.Name,
		Argv:            spec.Argv,
		MatchPattern:    spec.MatchPattern,
		Target:          spec.Target,
		Cwd:             spec.Cwd,
		Env:             spec.Env,
		DebounceSeconds: spec.DebounceSeconds,
		Schedule:        spec.Schedule,
		CreatedAt:       now,
		UpdatedAt:       now,
		Status:          StatusRunning,
	}

	if err := s.store.Insert(ctx, job); err != nil {
		releasePending()
		// Translate the raw sqlite UNIQUE-constraint-on-name failure into the
		// typed ErrNameTaken sentinel so Epic B RPC handlers can return a
		// user-friendly "monitor name already in use" instead of leaking the
		// sqlite error text. modernc.org/sqlite wraps the message as:
		//   "constraint failed: UNIQUE constraint failed: monitors.name (2067)".
		if strings.Contains(err.Error(), "UNIQUE constraint failed: monitors.name") {
			return "", ErrNameTaken
		}
		return "", err
	}
	if err := s.launch(job); err != nil {
		// Roll back the DB insert so the caller sees no partial state.
		_ = s.store.Delete(context.Background(), job.ID)
		releasePending()
		return "", err
	}
	releasePending()
	return job.ID, nil
}

// Stop cancels the runner for the given ID, marks the DB row stopped, and
// best-effort waits briefly (stopSyncWait) for the runner goroutine to exit.
// The row is RETAINED (status=stopped) so subsequent Restart calls can find
// it. Returns ErrNotFound if no running monitor has that ID.
func (s *MonitorSupervisor) Stop(ctx context.Context, id string) error {
	s.mu.Lock()
	h, ok := s.runners[id]
	if ok {
		// Remove from map before releasing the lock so no other caller can race
		// on the same handle.
		delete(s.runners, id)
	}
	s.mu.Unlock()

	if !ok {
		return ErrNotFound
	}

	// Signal the runner's exitNotice closure to skip store.MarkDead — Stop
	// will write StatusStopped instead. Must be set BEFORE cancel so the
	// flag is visible when exitNotice fires.
	h.stoppedByUser.Store(true)

	h.cancel()

	// Mark the row stopped synchronously so the DB reflects user intent
	// immediately, regardless of how long OS-level cleanup of the child
	// takes. Without this, a stuck reader goroutine (e.g. grandchildren
	// holding the stdout/stderr pipe open) would leave the row in
	// running state forever after Stop's wait timed out. See
	// thrum-puhr.9.2.
	if err := s.store.MarkStopped(ctx, id); err != nil {
		return err
	}

	// Best-effort: wait briefly for the runner goroutine to actually
	// exit so callers observing the child's PID right after Stop see
	// a clean state in common cases. If the runner doesn't exit
	// within stopSyncWait, return success anyway — the runner's
	// shutdown watcher SIGKILLs the process group and the goroutine
	// finishes asynchronously. Blocking the RPC longer wedges the
	// daemon's response path.
	select {
	case <-h.done:
	case <-time.After(stopSyncWait):
	}
	return nil
}

// HasRunner reports whether a live runner with the given ID is currently
// registered. Intended for integration tests that need to verify a
// monitor's lifecycle externally; not used in production code paths.
func (s *MonitorSupervisor) HasRunner(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.runners[id]
	return ok
}

// Restart stops any live runner for the given ID and re-launches it with
// the persisted spec, PRESERVING the monitor ID. Unlike Stop+Add, the DB
// row is retained across the restart so downstream subscribers that track
// the monitor by ID continue to match. Review finding R2.1.
//
// If no monitor row exists with the given ID, returns ErrNotFound.
//
// Note: concurrent Restart calls on the SAME ID are not supported; callers
// should serialize externally. The RPC dispatcher is single-threaded per
// method-name + ID tuple in practice so this is safe for the current RPC
// caller pattern.
func (s *MonitorSupervisor) Restart(ctx context.Context, id string) error {
	// Fetch the persisted job row first. ErrNotFound if absent.
	job, err := s.store.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Cancel any live runner for this ID. stoppedByUser is set so the
	// exitNotice closure skips store.MarkDead — we do NOT want the row
	// marked dead during a restart.
	s.mu.Lock()
	h, hasRunner := s.runners[id]
	if hasRunner {
		delete(s.runners, id)
	}
	s.mu.Unlock()

	if hasRunner {
		h.stoppedByUser.Store(true)
		h.cancel()
		select {
		case <-h.done:
		case <-time.After(10 * time.Second):
			return errors.New("runner did not exit within restart timeout")
		}
	}

	// Refresh mutable fields before re-launch. Status back to running;
	// clear any prior exit record so the restarted runner starts clean.
	job.Status = StatusRunning
	job.UpdatedAt = time.Now().UTC()
	job.LastExitCode = nil
	job.LastExitAt = nil
	if err := s.store.Update(ctx, job); err != nil {
		return fmt.Errorf("monitor restart: update row: %w", err)
	}

	return s.launch(job)
}

// List returns all monitor jobs from the store regardless of status, with
// the runtime PID populated from the live runnerHandle map for any monitor
// whose child is currently running. Review finding R2.3: the RPC layer
// needs this so monitor.list can render a PID column.
func (s *MonitorSupervisor) List(ctx context.Context) ([]*MonitorJob, error) {
	jobs, err := s.store.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	s.enrichPIDs(jobs)
	return jobs, nil
}

// GetByID returns the persisted MonitorJob for the given ID, with the
// runtime PID populated from the live runnerHandle if present.
func (s *MonitorSupervisor) GetByID(ctx context.Context, id string) (*MonitorJob, error) {
	job, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	s.enrichPIDs([]*MonitorJob{job})
	return job, nil
}

// enrichPIDs overlays each job's PID field with the value published by its
// live runner (if any) so the RPC response reflects real-time state rather
// than whatever was last persisted. Safe to call on any slice; jobs with
// no live handle are left untouched. Acquires s.mu.
func (s *MonitorSupervisor) enrichPIDs(jobs []*MonitorJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range jobs {
		h, ok := s.runners[job.ID]
		if !ok {
			continue
		}
		if pid := h.pid.Load(); pid > 0 {
			p := int(pid)
			job.PID = &p
		}
	}
}

// launch creates a Runner for job and starts its goroutine.  The runner's
// context is derived from s.baseCtx (captured by Start) so the runner's
// lifetime is tied to the DAEMON, not to whatever RPC request happened to
// invoke Add. Using the caller's ctx would kill the runner the moment an
// RPC handler returns — a subtle bug that only appears under real RPC
// traffic, not in direct-call tests.
//
// If baseCtx has not been set yet (Start hasn't been called), launch falls
// back to context.Background so the reload path can still work when Start
// calls launch from its own goroutine before the baseCtx field is read.
//
// The caller is responsible for holding s.mu when appropriate; during the
// startup reload phase no concurrent mutation is possible, and during Add the
// mu is released before launch is called.
func (s *MonitorSupervisor) launch(job *MonitorJob) error {
	re, err := regexp.Compile(job.MatchPattern)
	if err != nil {
		return fmt.Errorf("compile regex for %s: %w", job.Name, err)
	}

	var schedule *Schedule
	if job.Schedule != "" {
		schedule, err = ParseSchedule(job.Schedule)
		if err != nil {
			return fmt.Errorf("parse schedule for %s: %w", job.Name, err)
		}
	}

	// Use the supervisor's long-lived base context as parent. See type doc.
	s.mu.Lock()
	baseCtx := s.baseCtx
	s.mu.Unlock()
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	runnerCtx, cancel := context.WithCancel(baseCtx)
	done := make(chan struct{})

	handle := &runnerHandle{
		job:    job,
		cancel: cancel,
		done:   done,
	}

	s.mu.Lock()
	s.runners[job.ID] = handle
	s.mu.Unlock()

	go s.runLoop(runnerCtx, handle, job, re, schedule)

	return nil
}

// runLoop owns one monitor's per-handle goroutine. It dispatches one of two
// child-lifecycle strategies based on whether the monitor has a schedule:
//
//   - scheduled (schedule != nil): wait for the next cron tick, run the
//     child one-shot, record the exit (NOT MarkDead — the monitor is
//     still healthy in scheduled mode), loop.
//
//   - continuous (schedule == nil): run the child; on exit, restart with
//     exponential backoff capped at maxBackoff. A successful run of
//     backoffResetAfter or longer resets both backoff and the
//     restart-window history. If the child exits more than
//     maxRestartsPerWindow times within restartBudgetWindow, MarkDead
//     and deliver a single "exceeded restart budget" notice to the
//     monitor's target.
//
// Cleanup on return: removes the handle from s.runners (idempotent —
// Stop/Restart may have removed it already) and closes handle.done so
// callers blocked on Stop/Restart unblock. Never calls MarkDead when
// stoppedByUser is set (Stop already wrote MarkStopped synchronously).
func (s *MonitorSupervisor) runLoop(
	ctx context.Context,
	handle *runnerHandle,
	job *MonitorJob,
	re *regexp.Regexp,
	schedule *Schedule,
) {
	jobID := job.ID
	jobName := job.Name
	jobTarget := job.Target

	defer close(handle.done)
	defer func() {
		s.mu.Lock()
		delete(s.runners, jobID)
		s.mu.Unlock()
	}()

	// Per-run exit capture filled in by exitNotice. The runner calls
	// exitNotice synchronously inside Run before returning, so the loop
	// can read this after each Run() call without channel coordination.
	var lastExit struct {
		code     int
		pid      int
		duration time.Duration
		tail     string
		fired    bool
	}
	exitNotice := func(_ string, code, pid int, d time.Duration, tail string) {
		lastExit.code = code
		lastExit.pid = pid
		lastExit.duration = d
		lastExit.tail = tail
		lastExit.fired = true
	}
	deliver := func(_, content string) {
		_ = s.delivery.Deliver(context.Background(), jobName, jobTarget, content)
	}
	onStart := func(pid int) {
		handle.pid.Store(int64(pid))
	}
	adapter := &monitorJobAdapter{job: job}

	runOnce := func() {
		lastExit.fired = false
		r, err := NewRunner(adapter, re, exitNotice, deliver, onStart)
		if err != nil {
			log.Printf("monitor_supervisor: runner %s: build failed: %v", jobName, err)
			return
		}
		if runErr := r.Run(ctx); runErr != nil {
			log.Printf("monitor_supervisor: runner %s: run error: %v", jobName, runErr)
		}
		handle.pid.Store(0)
	}

	// ── Scheduled mode ───────────────────────────────────────────────
	if schedule != nil {
		for {
			if ctx.Err() != nil || handle.stoppedByUser.Load() {
				return
			}
			next := schedule.Next(time.Now())
			// schedule.Next returns the zero time when no match exists
			// within its 5-year search window. This happens for cron
			// expressions that parse syntactically but can never fire
			// (e.g. "0 0 31 2 *" — Feb 31). Without this guard, the
			// scheduled-wait below would return immediately (negative
			// time.Until clamps to zero) and the loop would spin firing
			// the child until ctx is cancelled — pegging a goroutine
			// and spamming the child. Mark the monitor dead with a
			// dedicated notice so the operator sees what went wrong.
			if next.IsZero() {
				content := fmt.Sprintf(
					"[monitor:%s] schedule %q has no valid fire time within 5 years — marking dead. Fix the schedule and run: thrum monitor restart %s",
					jobName, job.Schedule, jobID,
				)
				_ = s.delivery.Deliver(context.Background(), jobName, jobTarget, content)
				_ = s.store.MarkDead(context.Background(), jobID, -1, time.Now())
				return
			}
			s.scheduledTickWait(ctx, next)
			if ctx.Err() != nil || handle.stoppedByUser.Load() {
				return
			}
			runOnce()
			if lastExit.fired {
				// Record the exit metadata so operators can see when the
				// monitor last fired, but keep status=running — the
				// monitor is healthy in scheduled mode; an exit is the
				// expected end of one tick.
				_ = s.store.RecordExit(context.Background(), jobID, lastExit.code, time.Now())
			}
		}
	}

	// ── Continuous mode with auto-restart + budget ───────────────────
	tun := s.tunables
	backoff := tun.InitialBackoff
	var restartTimes []time.Time
	for {
		if ctx.Err() != nil || handle.stoppedByUser.Load() {
			return
		}
		runStart := time.Now()
		runOnce()
		if ctx.Err() != nil || handle.stoppedByUser.Load() {
			return
		}

		// Refresh exit metadata so operators polling monitor.show see
		// last_exit_code / last_exit_at update between restarts — useful
		// for soak observers watching an auto-restarting monitor's
		// recent history.
		if lastExit.fired {
			_ = s.store.RecordExit(context.Background(), jobID, lastExit.code, time.Now())
		}

		// Reset backoff + restart-window on a healthy long run.
		runDuration := time.Since(runStart)
		if runDuration >= tun.BackoffResetAfter {
			backoff = tun.InitialBackoff
			restartTimes = nil
		}

		// Trim restart window history.
		now := time.Now()
		cutoff := now.Add(-tun.RestartBudgetWindow)
		trimmed := restartTimes[:0]
		for _, t := range restartTimes {
			if t.After(cutoff) {
				trimmed = append(trimmed, t)
			}
		}
		restartTimes = append(trimmed, now)

		if len(restartTimes) > tun.MaxRestartsPerWindow {
			content := fmt.Sprintf(
				"[monitor:%s] exceeded restart budget (%d exits in %s; limit %d) — marking dead. Last exit code %d (pid %d) after %s.\nstdout (last 500 bytes): %s\nrestart: thrum monitor restart %s",
				jobName, len(restartTimes), tun.RestartBudgetWindow, tun.MaxRestartsPerWindow,
				lastExit.code, lastExit.pid, lastExit.duration.Round(time.Second),
				lastExit.tail, jobID,
			)
			_ = s.delivery.Deliver(context.Background(), jobName, jobTarget, content)
			_ = s.store.MarkDead(context.Background(), jobID, lastExit.code, time.Now())
			return
		}

		// Sleep backoff before retry.
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > tun.MaxBackoff {
			backoff = tun.MaxBackoff
		}
	}
}

// monitorJobAdapter wraps *MonitorJob and implements the RunnerJob interface
// defined in runner.go.  This avoids adding accessor methods to job.go and
// keeps the two files decoupled.
type monitorJobAdapter struct {
	job *MonitorJob
}

func (a *monitorJobAdapter) GetID() string             { return a.job.ID }
func (a *monitorJobAdapter) GetName() string           { return a.job.Name }
func (a *monitorJobAdapter) GetArgv() []string         { return a.job.Argv }
func (a *monitorJobAdapter) GetCwd() string            { return a.job.Cwd }
func (a *monitorJobAdapter) GetEnv() map[string]string { return a.job.Env }
func (a *monitorJobAdapter) GetDebounceSeconds() int   { return a.job.DebounceSeconds }
