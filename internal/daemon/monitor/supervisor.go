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

// Typed errors returned by Add (translated to user-friendly messages by the
// RPC handler layer in Epic B).
var (
	ErrCapExceeded      = errors.New("maximum concurrent monitors reached")
	ErrNameTaken        = errors.New("monitor name already in use")
	ErrDebounceTooShort = errors.New("debounce below 30s minimum")
	ErrInvalidRegex     = errors.New("invalid match pattern")
)

// SubmitSpec is the value-object passed from an RPC handler to Add. It holds
// the user-supplied monitor configuration before it is validated and persisted.
type SubmitSpec struct {
	Name            string
	Argv            []string
	MatchPattern    string
	Target          string
	Cwd             string
	Env             map[string]string
	DebounceSeconds int
}

// runnerHandle groups the per-runner context cancel function and a done channel
// that is closed when the runner goroutine returns.
type runnerHandle struct {
	job    *MonitorJob
	cancel context.CancelFunc
	done   chan struct{}
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
}

// NewMonitorSupervisor constructs a supervisor backed by store and delivery.
// The supervisor's runner map is empty until Start is called.
func NewMonitorSupervisor(store *MonitorStore, delivery *Delivery) *MonitorSupervisor {
	return &MonitorSupervisor{
		store:    store,
		delivery: delivery,
		runners:  make(map[string]*runnerHandle),
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

	persisted, err := s.store.ListByStatus(ctx, StatusRunning)
	if err != nil {
		log.Printf("monitor_supervisor: reload failed: %v", err)
	} else {
		for _, job := range persisted {
			if launchErr := s.launch(ctx, job); launchErr != nil {
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
// ErrDebounceTooShort, ErrInvalidRegex) that the RPC handler can translate to
// user-friendly messages.
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
	if err := s.launch(ctx, job); err != nil {
		// Roll back the DB insert so the caller sees no partial state.
		_ = s.store.Delete(context.Background(), job.ID)
		releasePending()
		return "", err
	}
	releasePending()
	return job.ID, nil
}

// Stop cancels the runner for the given ID, waits for it to exit, and then
// deletes the row from the DB.  Returns ErrNotFound if no running monitor has
// that ID.
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

	h.cancel()
	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
		return errors.New("runner did not exit in time")
	}

	return s.store.Delete(ctx, id)
}

// List returns all monitor jobs from the store regardless of status.
func (s *MonitorSupervisor) List(ctx context.Context) ([]*MonitorJob, error) {
	return s.store.ListAll(ctx)
}

// GetByID returns the persisted MonitorJob for the given ID.
func (s *MonitorSupervisor) GetByID(ctx context.Context, id string) (*MonitorJob, error) {
	return s.store.GetByID(ctx, id)
}

// launch creates a Runner for job and starts its goroutine.  The runner's
// context is derived from the provided parent so that supervisor shutdown
// (ctx.Done) propagates into every child.  The done channel is closed when the
// goroutine returns.
//
// The caller is responsible for holding s.mu when appropriate; during the
// startup reload phase no concurrent mutation is possible, and during Add the
// mu is released before launch is called.
func (s *MonitorSupervisor) launch(ctx context.Context, job *MonitorJob) error {
	re, err := regexp.Compile(job.MatchPattern)
	if err != nil {
		return fmt.Errorf("compile regex for %s: %w", job.Name, err)
	}

	runnerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	// Capture a stable copy of the ID for the closures below.
	jobID := job.ID
	jobTarget := job.Target

	exitNotice := func(jobName string, exitCode, pid int, duration time.Duration, tail string) {
		// Per design spec §Child exit, exit notices include the child PID so
		// the operator can correlate with ps / system logs.
		content := fmt.Sprintf(
			"[monitor:%s] exited with code %d after %s (pid %d)\nrestart: thrum monitor restart %s\nstdout (last 500 bytes): %s",
			jobName, exitCode, duration.Round(time.Second), pid, jobID, tail,
		)
		_ = s.delivery.Deliver(context.Background(), jobName, jobTarget, content)
		_ = s.store.MarkDead(context.Background(), jobID, exitCode, time.Now())
		s.mu.Lock()
		delete(s.runners, jobID)
		s.mu.Unlock()
	}

	deliver := func(jobName, content string) {
		_ = s.delivery.Deliver(context.Background(), jobName, jobTarget, content)
	}

	// monitorJobAdapter implements RunnerJob so *MonitorJob can be passed to
	// NewRunner without adding accessor methods to the job.go struct.
	adapter := &monitorJobAdapter{job: job}

	r, err := NewRunner(adapter, re, exitNotice, deliver)
	if err != nil {
		cancel()
		return err
	}

	s.mu.Lock()
	s.runners[jobID] = &runnerHandle{
		job:    job,
		cancel: cancel,
		done:   done,
	}
	s.mu.Unlock()

	go func() {
		defer close(done)
		if runErr := r.Run(runnerCtx); runErr != nil {
			log.Printf("monitor_supervisor: runner %s exited: %v", job.Name, runErr)
		}
	}()

	return nil
}

// monitorJobAdapter wraps *MonitorJob and implements the RunnerJob interface
// defined in runner.go.  This avoids adding accessor methods to job.go and
// keeps the two files decoupled.
type monitorJobAdapter struct {
	job *MonitorJob
}

func (a *monitorJobAdapter) GetID() string              { return a.job.ID }
func (a *monitorJobAdapter) GetName() string            { return a.job.Name }
func (a *monitorJobAdapter) GetArgv() []string          { return a.job.Argv }
func (a *monitorJobAdapter) GetCwd() string             { return a.job.Cwd }
func (a *monitorJobAdapter) GetEnv() map[string]string  { return a.job.Env }
func (a *monitorJobAdapter) GetDebounceSeconds() int    { return a.job.DebounceSeconds }
