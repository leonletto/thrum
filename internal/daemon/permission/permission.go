package permission

import (
	"database/sql"

	"github.com/leonletto/thrum/internal/daemon/state"
)

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
