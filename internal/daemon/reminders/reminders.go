// Package reminders owns the polymorphic reminders substrate (schema v25,
// canonical-ref §3.5). One table carries time-triggered reminders
// (agent/user/daemon-set) and condition-triggered stalled-agent sweep
// entries; rows discriminate via (Source, TriggerKind).
//
// The Store interface is the only persistence surface. Sweep's idempotent
// mint and the dispatcher's fire-and-rearm are first-class methods rather
// than secondary interfaces so consumers don't need runtime type
// assertions to access them (dual-review IMPORTANT #11).
package reminders

import (
	"context"
	"encoding/json"
	"time"
)

// Source identifies who minted the reminder row.
type Source string

const (
	SourceDaemon Source = "daemon"
	SourceAgent  Source = "agent"
	SourceUser   Source = "user"
)

// TriggerKind identifies the firing rule. Extensible: future condition
// kinds (e.g. condition_token_budget_exceeded) plug in alongside
// condition_pane_quiet without schema change.
type TriggerKind string

const (
	TriggerTime               TriggerKind = "time"
	TriggerConditionPaneQuiet TriggerKind = "condition_pane_quiet"
)

// State is the reminder lifecycle position. See canonical-ref §3.5 state
// machine: open → fired (terminal for one-shot) | cleared | cancelled.
// Condition-triggered rows recycle fired → open via FireAndRearm.
type State string

const (
	StateOpen      State = "open"
	StateFired     State = "fired"
	StateCleared   State = "cleared"
	StateCancelled State = "cancelled"
)

// DeferEntry is one row in defer_history (JSON-encoded column).
type DeferEntry struct {
	DeferredBy string    `json:"deferred_by"`
	DeferTo    time.Time `json:"defer_to"`
	When       time.Time `json:"when"`
}

// Reminder is one row in the reminders table with JSON columns parsed.
// Pointer fields are NULLable in the DDL; non-pointer fields are NOT NULL.
type Reminder struct {
	ID             string
	Source         Source
	SourceAgent    string // empty when Source != SourceAgent
	TriggerKind    TriggerKind
	TriggerAt      *time.Time      // nil for condition triggers
	TriggerMeta    json.RawMessage // populated for condition triggers
	TargetAgent    string
	TargetChain    []string // parsed from JSON column
	Body           string
	RaisedAt       time.Time
	NextReminderAt *time.Time // nil in terminal states
	LastFiredAt    *time.Time
	State          State
	PaneSnapshot   string // truncated to 16KB at insert (canonical §3.11 Guard 5)
	DeferHistory   []DeferEntry
	ClearedAt      *time.Time
	CancelledAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ListFilter scopes a List query. Pointer fields are tri-state: nil means
// "don't filter on this column"; a set value filters on equality.
type ListFilter struct {
	Source      *Source
	TriggerKind *TriggerKind
	State       *State
	TargetAgent string
	SourceAgent string
	Limit       int // 0 = unlimited
}

// Store is the persistence interface for the reminders table.
//
// All methods take a context and surface errors directly. Sweep's
// idempotent-mint (MintConditionForAgent) and the dispatcher's
// re-arm-after-fire (FireAndRearm) are first-class members so consumers
// don't need to assert against a secondary interface.
type Store interface {
	// Mint inserts a new reminder row. The validator (validator.go) must
	// have approved the row before calling. ID is expected to be
	// pre-populated by MintID; ID collisions surface as a unique-constraint
	// error.
	Mint(ctx context.Context, r *Reminder) error

	// Get returns the row with the given id, or sql.ErrNoRows.
	Get(ctx context.Context, id string) (*Reminder, error)

	// List returns rows matching the filter. State==nil returns all states.
	List(ctx context.Context, filter ListFilter) ([]*Reminder, error)

	// OpenForAgent returns the agent's open rows (target_agent matches,
	// state='open'). Used by `thrum team` integration.
	OpenForAgent(ctx context.Context, agent string) ([]*Reminder, error)

	// Defer appends to defer_history and updates next_reminder_at.
	Defer(ctx context.Context, id string, until time.Time, by string) error

	// Clear transitions an open or fired row to state='cleared' with
	// cleared_at=now. Caller authorization enforced at RPC layer.
	Clear(ctx context.Context, id string, by string) error

	// Cancel transitions an open row to state='cancelled' with
	// cancelled_at=now. Distinct from Clear so observability can
	// distinguish operator dismissal from "no longer relevant".
	Cancel(ctx context.Context, id string, by string) error

	// Fire is the terminal transition for one-shot reminders: state=open →
	// state=fired, last_fired_at=fired, next_reminder_at=NULL. Use only
	// for TriggerTime rows.
	Fire(ctx context.Context, id string, fired time.Time) error

	// FireAndRearm is the recurring transition for condition-triggered
	// reminders: state stays 'open', last_fired_at=fired,
	// next_reminder_at=next. Use only for condition_* trigger kinds.
	FireAndRearm(ctx context.Context, id string, fired, next time.Time) error

	// DueOpen returns rows where state='open' AND next_reminder_at <= now.
	// Backed by idx_reminders_next (partial index on state='open').
	DueOpen(ctx context.Context, now time.Time) ([]*Reminder, error)

	// MintConditionForAgent enforces the sweep idempotency match-key:
	// (target_agent, trigger_kind='condition_pane_quiet', state='open').
	// Returns (row, minted, err) where minted=false means an open row
	// already existed and `row` is the existing one — caller should treat
	// that as a no-op rather than retry. Used by E4.3 stalled-sweep.
	MintConditionForAgent(
		ctx context.Context,
		agent string,
		meta json.RawMessage,
		chain []string,
		snapshot string,
		nextReminderAt time.Time,
	) (*Reminder, bool, error)
}
