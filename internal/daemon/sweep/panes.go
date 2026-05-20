package sweep

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/tmux"
)

// AgentRegistry yields the set of agents the daemon knows about,
// each with the tmux session their pane lives in. Real impl wraps
// the daemon state.State agent listing + identity-file lookup;
// tests stub with a fixed slice.
//
// The interface is intentionally minimal — production code may have
// richer AgentInfo somewhere, but the sweep only needs Name +
// TmuxSession. Anything else (PID, role, etc.) is observable
// downstream from the reminders row, not at sweep-time.
type AgentRegistry interface {
	LiveAgents(ctx context.Context) ([]AgentInfo, error)
}

// AgentInfo describes one registered agent for sweep purposes.
type AgentInfo struct {
	Name        string
	TmuxSession string // from identity.tmux_session; "" → no pane to sweep
}

// SessionExistsFn checks whether a tmux session exists. Function type
// (not interface) mirrors the CapturePaneFn pattern from sweep.go —
// production wraps tmux.HasSession; tests substitute closures.
type SessionExistsFn func(name string) bool

// WindowActivityFn returns the tmux #{window_activity} format value
// for a session (unix seconds string). Function type for testability.
type WindowActivityFn func(ctx context.Context, session string) (string, error)

// DaemonPaneSource satisfies PaneSource by walking the daemon's agent
// registry and probing each agent's tmux session for activity.
//
// Three injected collaborators keep the unit testable without spinning
// up tmux or the daemon: AgentRegistry yields agent names + sessions,
// SessionExistsFn filters dead sessions, WindowActivityFn returns the
// raw tmux activity string for parsing.
type DaemonPaneSource struct {
	registry       AgentRegistry
	sessionExists  SessionExistsFn
	windowActivity WindowActivityFn
}

// NewDaemonPaneSource wires the production collaborators
// (tmux.HasSession + safecmd.Tmux display-message). Tests should use
// NewDaemonPaneSourceWithDeps to inject custom function values.
func NewDaemonPaneSource(registry AgentRegistry) *DaemonPaneSource {
	return NewDaemonPaneSourceWithDeps(
		registry,
		tmux.HasSession,
		defaultWindowActivity,
	)
}

// NewDaemonPaneSourceWithDeps is the test-friendly constructor.
func NewDaemonPaneSourceWithDeps(
	registry AgentRegistry,
	sessionExists SessionExistsFn,
	windowActivity WindowActivityFn,
) *DaemonPaneSource {
	return &DaemonPaneSource{
		registry:       registry,
		sessionExists:  sessionExists,
		windowActivity: windowActivity,
	}
}

// LivePanes returns one Pane per agent whose tmux session is still
// alive. Agents with no tmux session OR a session tmux no longer
// reports are silently filtered — they can't be swept (no pane to
// capture).
//
// LastActivity comes from tmux's #{window_activity} format
// (unix-seconds integer). Empty / unparseable values fall back to
// time.Now() — that prevents a freshly-created session (which may
// report an empty activity stamp before any input lands) from
// false-positive tripping the staleness threshold immediately.
func (s *DaemonPaneSource) LivePanes(ctx context.Context) ([]Pane, error) {
	agents, err := s.registry.LiveAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("LivePanes: list agents: %w", err)
	}
	var panes []Pane
	for _, a := range agents {
		if a.TmuxSession == "" {
			// Headless agent, remote peer, or pre-attach state — no
			// pane to sweep.
			continue
		}
		if !s.sessionExists(a.TmuxSession) {
			// Session in registry but tmux says it's gone (process
			// died, operator killed the window, etc.). Stale identity
			// data; nothing to sweep.
			continue
		}
		raw, err := s.windowActivity(ctx, a.TmuxSession)
		if err != nil {
			// tmux unhappy for this one session — typically transient
			// (display-message has a tiny race window mid-teardown).
			// Skip this agent rather than aborting the whole batch.
			continue
		}
		panes = append(panes, Pane{
			AgentName:    a.Name,
			TmuxTarget:   a.TmuxSession + ":0.0",
			LastActivity: parseWindowActivity(raw),
		})
	}
	return panes, nil
}

// parseWindowActivity converts tmux's #{window_activity} format to
// a UTC time.Time. Empty / unparseable → time.Now() (conservative
// fallback so freshly-created sessions don't trip the threshold).
//
// tmux emits the timestamp followed by a trailing newline; TrimSpace
// handles that plus any whitespace operators have configured in
// custom display-message formats.
func parseWindowActivity(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Now().UTC()
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Now().UTC()
	}
	return time.Unix(n, 0).UTC()
}

// defaultWindowActivity wraps safecmd.Tmux for production. Mirrors
// the safe-exec discipline enforced project-wide (feedback_safecmd_safedb).
func defaultWindowActivity(ctx context.Context, session string) (string, error) {
	out, err := safecmd.Tmux(ctx, "display-message", "-t", session, "-p", "#{window_activity}")
	if err != nil {
		return "", fmt.Errorf("tmux display-message %s: %w", session, err)
	}
	return string(out), nil
}

// Compile-time check that DaemonPaneSource satisfies PaneSource.
var _ PaneSource = (*DaemonPaneSource)(nil)
