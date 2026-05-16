package scheduler

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
)

// JobSpec is the in-memory job specification carried by Scheduler. Field
// shape matches the nested-under-type schema from canonical-ref §4.1.
// JobSpec is a value type — the JobSpec(id) accessor returns a snapshot
// copy, so callers cannot mutate the live spec.
//
// Cross-epic stability commitment: this struct is consumed by B-B1, B-B2,
// A-B4. Adding fields is backwards-compatible; renaming or removing fields
// requires coordinator coordination.
type JobSpec struct {
	ID          string
	Type        string // "command" | "thrum_command" | "scheduled_agent" | "nudge" | "internal"
	Schedule    string // raw schedule string per canonical §4.1.1
	Enabled     bool
	Description string
	RunAtStart  bool
	Jitter      time.Duration // 0 = use scheduler default
	ScheduleTZ  string        // empty = use daemon.schedule_tz
	CatchUp     string        // "skip" | "run_most_recent"
	// Per-type sub-trees — exactly one is populated based on Type.
	Command        *CommandSpec
	ThrumCommand   *ThrumCommandSpec
	ScheduledAgent *ScheduledAgentSpec
	Nudge          *NudgeSpec
	StageTimeouts  map[string]time.Duration // per-stage override; spec §6.4
}

// CommandSpec is the type:command sub-tree (canonical §4.1).
type CommandSpec struct {
	Exec                 string
	WorkingDir           string
	Env                  map[string]string
	TimeoutSeconds       int
	FailureEscalateAfter int // default 3 per Q7.2

	// Args is INTERNAL-ONLY. Not part of the user-facing JSON schema
	// (canonical §4.1.1 type:command row). The thrum_command handler
	// populates this when composing a synthetic command spec (E1.1 Task 15)
	// so we get argv-slice invocation that bypasses shell parsing. The
	// user-facing parse path for type:command leaves this empty — operator
	// `exec` strings are shell-parsed by the underlying invocation. Do NOT
	// surface this field in job.show output.
	Args []string `json:"-"`
}

// ThrumCommandSpec is the type:thrum_command sub-tree.
type ThrumCommandSpec struct {
	Args                 []string
	FailureEscalateAfter int // default 3
}

// ScheduledAgentSpec is the type:scheduled_agent sub-tree (B-B1 consumes).
type ScheduledAgentSpec struct {
	Target               string
	Primer               string
	BaseBranch           string // default "main"
	WorktreePersistent   bool
	IdleNudgeSeconds     int    // default 90
	MaxIdleNudges        int    // default 5
	TeardownGraceSeconds int    // default 10
	FailureEscalateAfter int    // default 3
	BudgetMode           string // "notify" | "block"; default = daemon.billing_mode
	DailyTokenBudget     int
	MonthlyTokenBudget   int
}

// NudgeSpec is the type:nudge sub-tree (B-B1 consumes).
type NudgeSpec struct {
	Target               string
	Message              string
	FailureEscalateAfter int // default 3
}

// InternalOpts carries per-internal-job options that aren't part of the
// schedule string. RegisterInternal callers pass this; user-job parse paths
// derive the equivalent fields from the spec sub-tree.
type InternalOpts struct {
	RunAtStart bool          // brainstorm Q2.2
	Jitter     time.Duration // optional override; 0 = use scheduler default
	CatchUp    string        // "skip" | "run_most_recent"; default "skip"
}

// Config carries Scheduler dependencies. *safedb.DB per project rule
// feedback_safecmd_safedb (philosophy doc Anti-Pattern #1).
type Config struct {
	DB       *safedb.DB
	DaemonID string         // for jitter computation
	Location *time.Location // default location; per-job ScheduleTZ overrides
}

// Scheduler is the unified scheduling substrate.
type Scheduler struct {
	cfg   Config
	state *StateStore

	mu           sync.RWMutex       // guards specs + handlers + typeHandlers
	specs        map[string]JobSpec // job_id → spec
	handlers     map[string]Handler // by job_id (internal jobs register theirs here too)
	typeHandlers map[string]Handler // by type-name: scheduled_agent / nudge
	reactorWake  chan struct{}      // closed/sent-to on registration to wake the reactor
	runReg       *runRegistry       // per-run cancel-func + signal-channel registries

	stopCh   chan struct{}
	stopOnce sync.Once
}

// idRE pins the kebab-case ID shape from canonical §4.1: lowercase
// alphanumeric + hyphen, must begin with a letter, max 64 chars.
var idRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// InternalPrefix reserves the `internal.*` namespace for daemon-essential
// jobs. The user-job validator (E1.5 Task 30) rejects any user job with
// this prefix.
const InternalPrefix = "internal."

// New constructs a Scheduler. Caller invokes Start() to begin dispatching.
func New(cfg Config) *Scheduler {
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	return &Scheduler{
		cfg:          cfg,
		state:        NewStateStore(cfg.DB),
		specs:        map[string]JobSpec{},
		handlers:     map[string]Handler{},
		typeHandlers: map[string]Handler{},
		reactorWake:  make(chan struct{}, 1),
		runReg:       newRunRegistry(),
		stopCh:       make(chan struct{}),
	}
}

// RegisterInternal registers a daemon-essential periodic job in the
// reserved `internal.*` namespace.
//
// PANICS on (a) missing `internal.` prefix, (b) bad ID shape (kebab-case
// post-prefix), or (c) duplicate ID. Per spec §5.3 + brainstorm Q1
// invariant: internal registration happens once at daemon startup;
// failures are programmer errors that should crash the daemon early, not
// propagate as runtime errors.
//
// Cross-epic stability commitment: signature is
// (id string, schedule string, opts InternalOpts, h Handler) returning
// nothing — matches spec §5.3 exactly. Consumed by A-B4, A-B2, A-B3, C-B1,
// D-B1, MB-1.S6. Do not break.
func (s *Scheduler) RegisterInternal(id, schedule string, opts InternalOpts, h Handler) {
	if !strings.HasPrefix(id, InternalPrefix) {
		panic(fmt.Sprintf("scheduler: RegisterInternal id %q must have %q prefix", id, InternalPrefix))
	}
	suffix := strings.TrimPrefix(id, InternalPrefix)
	if !idRE.MatchString(suffix) {
		panic(fmt.Sprintf("scheduler: RegisterInternal id suffix must match %s; got %q", idRE.String(), id))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.specs[id]; exists {
		panic(fmt.Sprintf("scheduler: duplicate RegisterInternal for %q", id))
	}
	catchUp := opts.CatchUp
	if catchUp == "" {
		catchUp = "skip"
	}
	s.specs[id] = JobSpec{
		ID:         id,
		Type:       "internal",
		Schedule:   schedule,
		Enabled:    true,
		RunAtStart: opts.RunAtStart,
		Jitter:     opts.Jitter,
		CatchUp:    catchUp,
	}
	s.handlers[id] = h
	s.wakeReactor()
}

// RegisterTypeHandler registers a handler for a user job type
// (`scheduled_agent`, `nudge`). Substrate-owned handlers (`command`,
// `thrum_command`) are registered by the substrate itself during New() —
// callers don't.
//
// Cross-epic stability commitment: B-B1 E6.1 and E6.3 consume this.
func (s *Scheduler) RegisterTypeHandler(jobType string, h Handler) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.typeHandlers[jobType]; exists {
		return fmt.Errorf("scheduler: type handler for %q already registered", jobType)
	}
	s.typeHandlers[jobType] = h
	return nil
}

// JobSpec returns the in-memory job spec for the given id; (zero, false)
// if absent. Returned JobSpec is a value-copy snapshot — safe to read
// after returning; does NOT reflect later config reloads.
//
// Cross-epic accessor for B-B1 wake-time, B-B2 `thrum cron show`, A-B4
// stalled-sweep (in-Go join against the SQL fetch from
// scheduler_job_state).
func (s *Scheduler) JobSpec(id string) (JobSpec, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	spec, ok := s.specs[id]
	return spec, ok
}

// wakeReactor signals the reactor that a registration or config change
// landed; reactor re-scans the heap on the next iteration. Non-blocking
// (buffered chan size 1) — one pending wake is enough.
func (s *Scheduler) wakeReactor() {
	select {
	case s.reactorWake <- struct{}{}:
	default:
	}
}

// Start launches the reactor goroutine. Returns after the reactor is
// running; reactor runs until Stop() is called or `ctx` is cancelled.
//
// Reactor body lives in reactor.go (Task 11). For E1.1 Task 10 the body
// is a minimal stub that simply blocks on stopCh / ctx.
func (s *Scheduler) Start(ctx context.Context) error {
	go s.runReactor(ctx)
	return nil
}

// Stop signals the reactor to exit. Safe to call multiple times; the
// stopOnce guard prevents double-close panics. Currently does not block
// on reactor exit — Task 11 may add a wait-group for orderly shutdown.
func (s *Scheduler) Stop(_ context.Context) error {
	s.stopOnce.Do(func() { close(s.stopCh) })
	return nil
}

// runReactor body lives in reactor.go (Task 11). It's the single goroutine
// that owns the heap and dispatches.

// Handler / StateReporter / Completion / sentinel errors live in handler.go
// (canonical home). E1.1 Task 10 originally put them here for self-contained
// compilation; E1.3 Task 19 moved them to handler.go for downstream consumers.

// runRegistry + newRunRegistry live in registry.go (Task 12). The struct
// tracks per-run cancel-func + signal-channel maps for in-flight dispatches;
// see registry.go for the canonical definition + methods.
