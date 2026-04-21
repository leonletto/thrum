package permission

import (
	"context"
	"database/sql"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
)

// supervisorSessionID is the sentinel session_id assigned to every
// message authored by the @supervisor_<project> pseudo-agent. The
// supervisor has no real session, but the messages table requires a
// non-empty value — a well-known literal keeps "find all
// supervisor-authored messages" queryable by either agent_id or
// session_id. Defined as a constant so the future reply-parser (Epic
// C) and inbox filters can reference it without string-literal drift.
const supervisorSessionID = "supervisor"

// Permission is the top-level struct wiring the feature together.
// Constructed once at daemon boot and passed into the message-create
// hook and the tmux.check-pane handler.
//
// Fields are unexported because no external package should mutate them
// after New — all public entry points live on *Permission.
type Permission struct {
	state        *state.State
	store        *Store
	supervisorID string // e.g. "supervisor_thrum" — used as sender for nudges
	projectName  string // e.g. "thrum" — rendered into the nudge body
	thrumDir     string // .thrum/ path — for reading supervisor config

	// nowFunc is injected by tests that need a fake clock (scheduler
	// cadence tests). Production code leaves it nil; (*Permission).now
	// falls back to time.Now().UTC() in that case.
	nowFunc func() time.Time

	// keystrokeSender is injected by reply_test to capture approve/deny
	// dispatches without touching real tmux. Production leaves it nil;
	// sendKeystroke falls back to defaultKeystroke (tmux.SendKeys /
	// tmux.SendSpecialKey) in that case.
	keystrokeSender func(target, key string) error

	// paneCapture is injected by reply_test to simulate pane state at
	// dispatch time. Production leaves it nil; the pre-send pane
	// recheck falls back to tmux.CapturePane in that case. See
	// TryResolve's pre-send check for why this seam exists
	// (thrum-rfy3: race-safe defense-in-depth beyond the atomic claim).
	paneCapture func(target string, lines int) (string, error)
}

// New builds a Permission. The state argument may be nil for unit tests
// that do not exercise event-write paths (e.g. reply parser or
// scheduler time-logic tests); the store is always initialized from db.
func New(st *state.State, db *sql.DB, supervisorID, projectName, thrumDir string) *Permission {
	return &Permission{
		state:        st,
		store:        NewStore(db),
		supervisorID: supervisorID,
		projectName:  projectName,
		thrumDir:     thrumDir,
	}
}

// SetClock installs a test-only clock. PRODUCTION CODE MUST NOT CALL
// THIS. Daemon boot leaves nowFunc nil so the scheduler uses the
// wall clock. Exported (rather than via an export_test.go pattern)
// only because cross-package tests in internal/daemon/rpc need to
// reach it — there is no compile-time barrier to misuse, so this
// contract is enforced by naming + documentation alone.
//
// NOT SAFE for concurrent use. Not safe to call after goroutines
// have started reading p.nowFunc. Tests must install the clock
// before exercising any scheduler entry point.
func (p *Permission) SetClock(now func() time.Time) {
	p.nowFunc = now
}

// SetKeystrokeSenderForTest installs a test-only keystroke dispatcher
// so cross-package integration tests (e.g. internal/daemon/rpc) can
// exercise the full reply-dispatch chain without touching real tmux.
//
// PRODUCTION CODE MUST NOT CALL THIS. Production always uses the
// default (defaultKeystroke → tmux.SendSpecialKey / tmux.SendKeys
// -l). Exported only because cross-package tests cannot reach an
// export_test.go helper; the test-only intent is enforced by naming
// and documentation, not by compile-time guards.
//
// NOT SAFE for concurrent use. NOT SAFE to call after goroutines
// have started reading p.keystrokeSender — install the fake before
// the hook is wired, not after. A future concurrent-mutation race
// would NOT be caught by the current tests because the scheduler
// reads keystrokeSender under no lock.
func (p *Permission) SetKeystrokeSenderForTest(fn func(target, key string) error) {
	p.keystrokeSender = fn
}

// SetPaneCaptureForTest installs a test-only pane capture function so
// reply-path tests can simulate pane state at dispatch time without
// touching real tmux. PRODUCTION CODE MUST NOT CALL THIS. Production
// uses tmux.CapturePane via the lazy fallback in captureForRecheck.
//
// NOT SAFE for concurrent use. Install before the hook is wired.
func (p *Permission) SetPaneCaptureForTest(fn func(target string, lines int) (string, error)) {
	p.paneCapture = fn
}

// Store returns the backing *Store so tests and cross-package
// integration fixtures can seed rows directly. Not intended for
// production code — production accessors (OnDetection, OnRecovery,
// AfterMessageCreate, ReloadOnBoot) handle store interactions
// internally.
func (p *Permission) Store() *Store {
	return p.store
}

// ReloadOnBoot sweeps the permission_nudges table once at daemon
// boot, returning the non-expired rows so runDaemon can log the
// pending count for operator visibility. Called once before
// HandleCheckPane starts accepting traffic; safe on an empty store.
//
// No in-memory rehydration is needed — OnDetection re-reads the
// store on every check-pane fire, so reminders resume at the
// correct cadence automatically once the daemon is back up. This
// helper exists so runDaemon can stay off the test-only Store()
// accessor: production code paths should never touch *Store
// directly.
func (p *Permission) ReloadOnBoot(ctx context.Context) ([]*NudgeRow, error) {
	return p.store.ReloadOnBoot(ctx)
}
