package agent

import (
	"context"
	"time"
)

// Mode and Identity canonical vocabularies per substrate-canonical-
// reference.md §3.3. The validator at agent.register (E6.0 Task 5,
// the only RPC that gates this interface today) enforces these
// values; storing any other string here is a code bug, not a
// user-facing surface.
const (
	ModePersistent = "persistent"
	ModeEphemeral  = "ephemeral"

	IdentityLongLived = "long_lived"
	IdentityEphemeral = "ephemeral"
)

// AutoRespawnConfig holds the loop-guard knobs for auto-respawn-enabled
// agents per canonical-ref §6.3 + spec §B-B1 Q10. Sourced from operator
// config (not the agents table) when consumed by the respawn dispatcher;
// the SQLite registry leaves these fields zero-valued so downstream
// callers can populate from their preferred source.
type AutoRespawnConfig struct {
	// EscalateAfter is the threshold of respawn_fired events within
	// WindowSeconds that trips the loop guard. Zero means "use the
	// canonical default" (3 respawns within 600s).
	EscalateAfter int

	// WindowSeconds is the rolling window (in seconds) over which the
	// loop guard counts respawn_fired events. Zero means default.
	WindowSeconds int
}

// Agent is the in-memory + DB-backed view of one row in the agents
// table (canonical-ref §3.3). Time fields use pointer-to-time.Time so
// callers can distinguish "never observed" (nil) from "observed at the
// unix epoch" (non-nil zero time).
//
// Field set tracks the post-migration-26 schema; future canonical-ref
// additions extend this struct and the SQLite Lookup query in lockstep.
type Agent struct {
	// Identity columns (set at agent.register).
	AgentID  string
	Kind     string
	Role     string
	Module   string
	Display  string
	Hostname string
	AgentPID int

	// Provenance (set at register; immutable thereafter).
	RegisteredAt string // ISO-8601; existing TEXT column, kept as-is to avoid parse cost at Lookup
	OriginDaemon string

	// Mode × identity grid (set at register; v0.11+ rows carry explicit
	// values, pre-v0.11 rows backfill to (persistent, long_lived)).
	Mode     string
	Identity string

	// Auto-respawn state. Enabled is the persistent boolean;
	// DisabledAt is the loop-guard trip marker (nil = armed, non-nil =
	// guard tripped at this time and respawns refused until cleared).
	AutoRespawnEnabled    bool
	AutoRespawnDisabledAt *time.Time

	// state.md parse-failure banner. nil = last parse OK; non-nil =
	// banner active since this time, cleared by operator ack.
	StateMdParseFailedAt *time.Time

	// Liveness signals. LastPaneAliveAt is daemon-detected pane
	// health (HandleCheckPane machinery); LastSeenAt is heartbeat-
	// driven from the agent itself. Canonical §3.11 Guard 7: do NOT
	// conflate these — pane-alive can continue updating briefly after
	// agent heartbeat stops if pane-death detection lags.
	LastPaneAliveAt *time.Time
	LastSeenAt      *time.Time

	// AutoRespawn carries operator-configured loop-guard knobs. NOT
	// stored on the agents table — the SQLite Lookup leaves these
	// zero-valued; downstream callers populate from config. Kept
	// nested here for ergonomic call-site grouping per plan §884-887.
	AutoRespawn AutoRespawnConfig
}

// AgentRegistry is the read/write API over the agents table. All
// implementations are concurrency-safe at the SQLite-connection level.
//
// The interface is intentionally narrow at E6.0 land time: it covers
// every call site the locked B-B1 plan + spec reference (NudgeHandler,
// Respawner, ack-* RPCs) and nothing speculative. Extension points
// emerge as call sites surface in later tasks.
type AgentRegistry interface {
	// Lookup returns the Agent for `name` (== agents.agent_id). When
	// no row matches, returns ErrAgentNotFound (wrapped). Callers
	// check via errors.Is.
	Lookup(ctx context.Context, name string) (Agent, error)

	// ListAutoRespawnEnabled returns all Agent rows that are eligible
	// for auto-respawn at this instant: auto_respawn_enabled = 1 AND
	// auto_respawn_disabled_at IS NULL (loop guard not tripped) AND
	// state_md_parse_failed_at IS NULL (no parse-failure banner).
	//
	// The pane-health monitor (internal/daemon/agenthealth) iterates
	// the returned set to scan for crashed agents. Filtering at the
	// list site is an optimization — per-agent OnPaneGone still
	// re-checks gate-predicate state to avoid TOCTOU races between
	// list time and probe time. Result ordering is implementation-
	// defined; callers MUST NOT depend on it.
	ListAutoRespawnEnabled(ctx context.Context) ([]Agent, error)

	// SetAutoRespawnDisabledAt arms the loop-guard trip marker.
	// Subsequent Lookups observe the timestamp; downstream respawn
	// logic refuses respawns until ClearAutoRespawnDisabledAt fires.
	SetAutoRespawnDisabledAt(ctx context.Context, name string, at time.Time) error

	// ClearAutoRespawnDisabledAt resets the marker (operator-ack flow).
	ClearAutoRespawnDisabledAt(ctx context.Context, name string) error

	// SetStateMdParseFailedAt arms the state.md parse-failure banner.
	SetStateMdParseFailedAt(ctx context.Context, name string, at time.Time) error

	// ClearStateMdParseFailedAt resets the banner (operator-ack flow).
	ClearStateMdParseFailedAt(ctx context.Context, name string) error
}
