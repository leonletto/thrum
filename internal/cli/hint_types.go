package cli

// Severity classifies a hint. "warn" can block pre-action execution (subject
// to per-hint AllowForce policy); "info" never blocks.
type Severity string

const (
	SeverityWarn Severity = "warn"
	SeverityInfo Severity = "info"
)

// Option is one suggested next-step command attached to a Hint.
type Option struct {
	Label string `json:"label"`          // "attach", "replace", "rename", "fix", etc.
	Cmd   string `json:"cmd"`            // exact command string
	Note  string `json:"note,omitempty"` // optional parenthetical caveat
}

// Hint is one suggestion emitted by a HintSource.
//
// AllowForce controls whether --force overrides a warn hint at pre-action:
//   - Zero value (false) = hard refusal; --force CANNOT override. Used for
//     conditions where proceeding would orphan live state or violate an
//     invariant (e.g. overwriting a live agent's identity).
//   - true = recoverable; --force allows the command to proceed. Used for
//     stale-state conditions the operator can knowingly overwrite.
//
// info hints never block regardless of AllowForce (the field is ignored).
type Hint struct {
	Code       string   `json:"code"`
	Severity   Severity `json:"severity"`
	Message    string   `json:"message"`
	Options    []Option `json:"options"`
	AllowForce bool     `json:"-"`
}

// HintCtx is passed to every HintSource. Post=false for pre-action collection,
// Post=true for post-success collection. Result is the command's return value
// (post-only); hint sources use it to detect things like stale-identity
// replacement that require pre-state knowledge.
//
// State is the only route a hint source reads external state — kept narrow so
// sources are unit-testable via a mock StateAccessor.
type HintCtx struct {
	Command string
	Args    []string
	Flags   map[string]any
	Post    bool
	Result  any
	State   StateAccessor
}

// HintSource is a per-command detection function. Pure on HintCtx; no side
// effects. Returns zero or more Hints in registration order.
type HintSource func(HintCtx) []Hint

// IdentityStatus describes a worktree's agent-identity state for the
// tmux.create identity-exists hint family.
type IdentityStatus int

const (
	IdentityNone  IdentityStatus = iota // no identity file in worktree
	IdentityStale                       // identity file exists, no live session
	IdentityLive                        // identity file exists, session healthy
)
