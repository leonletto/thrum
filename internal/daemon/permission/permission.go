package permission

import (
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

// SetClock installs a test-only clock. Production code must not call
// this — daemon boot leaves nowFunc nil so the scheduler uses the wall
// clock. Exported so scheduler_test.go can inject a fake clock from
// outside the package if future refactors move tests to an _test file.
func (p *Permission) SetClock(now func() time.Time) {
	p.nowFunc = now
}
