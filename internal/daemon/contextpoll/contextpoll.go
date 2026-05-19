// Package contextpoll implements per-runtime context-usage polling for the
// thrum daemon. It reads each runtime's session transcript on a configurable
// interval and compares cumulative token usage against configured thresholds.
//
// The package exposes three interfaces that decouple the polling loop from the
// daemon's RPC layer (cycle prevention) and from concrete transcript formats
// (per-runtime versioned parsers):
//
//   - Parser: per-runtime transcript reader. Concrete parsers (ClaudeParserV2x,
//     OpenCodeParserV1, CodexParserV1) ship in sibling files in this package.
//   - RestartTrigger: callback the force-fire path invokes to trigger a
//     session restart. Implemented at daemon-init time by a thin adapter that
//     wraps the tmux RPC handler — defined in cmd/thrum so contextpoll does
//     not need to import internal/daemon/rpc.
//   - ContextProvider: read-only view that TeamHandler queries to render the
//     context-% column in `thrum team`.
//
// The Poller itself is declared here as a stub; its fields, methods, and
// supporting types are added in the CR.2 implementation task (thrum-6qmf.1.11).
package contextpoll

import (
	"context"
	"time"
)

// ContextUsage is one snapshot of an agent's context-window utilization,
// extracted from the agent's runtime transcript by a Parser.
//
// UsedPercentage is a 0-100 integer; the Poller compares it against the
// configured warn and auto thresholds to decide which callback to fire.
// Approximate is true when the parser reconstructs usage from a sum of
// per-message token counts (OpenCode) rather than a single cumulative field
// (Claude); downstream consumers may surface the distinction in UI but the
// threshold logic treats both the same.
type ContextUsage struct {
	UsedPercentage int       // 0-100 integer percentage
	ParserVersion  string    // e.g. "claude-v2x"
	SourcePath     string    // absolute path of the transcript polled
	Timestamp      time.Time // when the parse completed
	Approximate    bool      // true if sum-of-tokens reconstruction (e.g. OpenCode)
}

// Parser reads a runtime's session transcript and returns the current
// context-window utilization. Implementations are stateless; the Poller
// owns per-agent state and dispatches to the first parser whose Matches
// returns true for the resolved transcript path.
type Parser interface {
	// Version returns a human-readable version tag for log attribution.
	// Example: "claude-v2x", "opencode-v1".
	Version() string

	// Matches returns true if this parser handles the given transcript path.
	// Used for version dispatch: the first parser where Matches returns true
	// wins. Implementations should be cheap — typically a single open + read
	// of the file's first line.
	Matches(transcriptPath string) bool

	// Parse reads the transcript and returns the current context usage.
	// Returns an error if the transcript is unreadable or unparsable.
	// Callers must treat a non-nil error as "usage unknown"; they must NOT
	// treat it as a threshold-crossing.
	Parse(transcriptPath string) (ContextUsage, error)
}

// RestartTrigger is the narrow interface contextpoll exposes so the force-fire
// path can trigger a session restart without importing internal/daemon/rpc
// (cycle prevention). Implemented at daemon-init time by a thin adapter that
// wraps TmuxHandler.RestartSession(ctx, agentName, opts{Force: true}). The
// adapter lives in cmd/thrum/main.go, not in this package — keeping the
// dependency edge one-way (rpc → contextpoll via TeamHandler.SetContextProvider).
//
// This pattern mirrors agentdispatch.Restarter at
// internal/daemon/agentdispatch/respawn.go:32, which exists for the same
// cycle-prevention reason in B-B1's auto-respawn path.
//
// The reason argument is the human-readable cause of the restart — for the
// auto-restart path this looks like "automatic context-threshold restart at
// 82%". It flows through to FormatRestartSnapshot's YAML frontmatter so an
// operator (or the post-restart agent reading the resume plan) can see WHY
// the session was force-restarted, distinguishing this from a graceful
// /thrum:restart or an external operator-initiated restart.
type RestartTrigger interface {
	Restart(ctx context.Context, agentName, reason string) error
}

// RestartTriggerFunc adapts a plain function value into a RestartTrigger. The
// daemon wires the real adapter via this helper so cmd/thrum/main.go's
// boot-time block can construct the closure inline without declaring a
// dedicated type — matching the http.HandlerFunc / sort.SliceStable patterns
// that callers expect from Go's standard library.
type RestartTriggerFunc func(ctx context.Context, agentName, reason string) error

// Restart implements RestartTrigger by invoking the underlying function.
func (f RestartTriggerFunc) Restart(ctx context.Context, agentName, reason string) error {
	return f(ctx, agentName, reason)
}

// ContextProvider is the read-only surface TeamHandler uses to read cached
// poller state for `thrum team` rendering. The Poller implements it directly;
// TeamHandler receives a ContextProvider via SetContextProvider at daemon
// init, mirroring the existing SetPoller / SetPaneCapture / SetLifecycleStore
// setter pattern.
type ContextProvider interface {
	// ContextUsageFor returns the most recently cached ContextUsage for the
	// named agent. The second return value is false if the agent is not
	// currently enrolled with the Poller (i.e. no usage has ever been cached).
	ContextUsageFor(agentName string) (ContextUsage, bool)
}

// The Poller type, its supporting types (PollerConfig, AgentEnrollment,
// callback signatures), and its methods live in sibling file poller.go.
// Keeping the interfaces above isolated in this file makes the dependency
// surface easy to audit (no daemon/rpc imports, see acceptance criteria for
// thrum-6qmf.1.1 / .1.19 import-cycle guard).
