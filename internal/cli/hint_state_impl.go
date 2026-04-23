package cli

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/tmux"
)

// LiveStateAccessor is the production StateAccessor: agent lookups go through
// the daemon RPC client; tmux liveness through safecmd-wrapped tmux;
// filesystem checks through cli.IsGitWorktree and config.LoadIdentityFromWorktree.
//
// Pure reads; never mutates state. Honors the best-effort contract — errors
// propagate so hint sources can silently skip, but the command path is never
// broken by an accessor failure because hint sources swallow these errors.
type LiveStateAccessor struct {
	Client *Client
}

// NewLiveStateAccessor wraps a connected Client.
func NewLiveStateAccessor(c *Client) *LiveStateAccessor {
	return &LiveStateAccessor{Client: c}
}

// AgentByName looks up a registered agent by name via the team.list RPC.
// Returns (nil, nil) when the agent is not registered (not an error — a
// legitimate "no such agent" answer). Returns (nil, err) when the RPC fails.
//
// Uses team.list rather than agent.list because team.list includes
// TmuxSession and TmuxState — both are required for send.recipient-stale's
// "reprime" option to render an actionable command. Include offline agents
// so stale recipients (the exact case this lookup exists to serve) are
// discoverable.
//
// NOTE: fetches the full team list on every call. Pilot-acceptable — hint
// sources call this at most once per command invocation. Add a server-side
// name filter if this becomes hot.
func (s *LiveStateAccessor) AgentByName(name string) (*AgentSummary, error) {
	if s == nil || s.Client == nil {
		return nil, nil
	}
	req := TeamListRequest{IncludeOffline: true, IncludeSystem: false}
	var resp TeamListResponse
	if err := s.Client.Call("team.list", req, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Members {
		m := &resp.Members[i]
		if m.AgentID == name {
			alive := m.TmuxState == "alive"
			return &AgentSummary{
				AgentID:     m.AgentID,
				Role:        m.Role,
				Module:      m.Module,
				Display:     m.Display,
				Worktree:    m.WorktreePath,
				Branch:      m.Branch,
				Intent:      m.Intent,
				SessionID:   m.SessionID,
				UpdatedAt:   m.LastSeen,
				Source:      "daemon",
				Status:      m.Status,
				Host:        m.Hostname,
				PID:         m.AgentPID,
				TmuxSession: m.TmuxSession,
				TmuxAlive:   alive,
				IsLocal:     m.IsLocal,
			}, nil
		}
	}
	return nil, nil
}

// TmuxSessionExists reports whether a tmux session by that name is live.
// Uses the existing tmux.HasSession helper (safecmd.TmuxRun under the hood).
// HasSession returns bool only, so we never surface an error — if tmux is
// unavailable HasSession returns false, which is the right answer for
// "is the session alive".
func (s *LiveStateAccessor) TmuxSessionExists(name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	return tmux.HasSession(name), nil
}

// IsGitWorktree wraps the existing cli.IsGitWorktree helper and drops the
// mainRepoRoot return (StateAccessor only cares about the yes/no answer).
//
// Normalizes one quirk of the underlying helper: when the path is not a git
// repository at all (e.g. /tmp), cli.IsGitWorktree returns
// (false, "", ErrNotGitRepo). That error is actually a *definitive* answer
// — the path is not a worktree — so we flatten it to (false, nil). Other
// errors (safecmd failures, unexpected git output) propagate so hint sources
// can silently skip.
func (s *LiveStateAccessor) IsGitWorktree(path string) (bool, error) {
	return normalizedIsGitWorktree(path)
}

// IdentityStatus inspects <worktreePath>/.thrum/identities/ and classifies
// the worktree's agent-identity state:
//
//   - IdentityNone when the identities dir is missing or empty.
//   - IdentityLive when an identity file exists AND its referenced tmux
//     session is alive.
//   - IdentityStale when an identity file exists AND its tmux session is
//     not alive (or the identity doesn't record a tmux session).
//
// The returned *AgentSummary, when non-nil, carries the agent's name (from
// the identity file) and its tmux session name, so hint Options can render
// correct `thrum tmux connect <name>` suggestions.
//
// Uses only FS access — no daemon RPC — so the logic is shared with
// FSOnlyStateAccessor via identityStatusFromPath.
func (s *LiveStateAccessor) IdentityStatus(worktreePath string) (IdentityStatus, *AgentSummary, error) {
	return identityStatusFromPath(worktreePath)
}

// identityStatusFromPath is the FS-only identity-status implementation
// shared by LiveStateAccessor and FSOnlyStateAccessor. Kept package-private
// so neither accessor can accidentally invoke the other's IdentityStatus
// through a freshly-allocated zero value (which previously relied on nil-
// Client tolerance in AgentByName — brittle coupling).
func identityStatusFromPath(worktreePath string) (IdentityStatus, *AgentSummary, error) {
	if worktreePath == "" {
		return IdentityNone, nil, nil
	}

	idFile, err := config.LoadIdentityFromWorktree(worktreePath)
	if err != nil {
		if errIsNoIdentity(err) {
			return IdentityNone, nil, nil
		}
		return IdentityNone, nil, err
	}
	if idFile == nil {
		return IdentityNone, nil, nil
	}

	// Determine the tmux session from the identity file; fall back to the
	// agent name when not recorded (legacy identities).
	sessionName := idFile.TmuxSession
	if sessionName == "" {
		sessionName = idFile.Agent.Name
	}

	alive := false
	if sessionName != "" {
		alive = tmux.HasSession(sessionName)
	}

	agent := &AgentSummary{
		AgentID:      idFile.Agent.Name,
		Role:         idFile.Agent.Role,
		Module:       idFile.Agent.Module,
		TmuxSession:  sessionName,
		TmuxAlive:    alive,
		IdentityFile: filepath.Join(worktreePath, ".thrum", "identities", idFile.Agent.Name+".json"),
		Source:       "file",
	}

	if alive {
		return IdentityLive, agent, nil
	}
	return IdentityStale, agent, nil
}

// normalizedIsGitWorktree is the FS-only IsGitWorktree shared by both
// accessors. Flattens ErrNotGitRepo to (false, nil) — a non-repo path is a
// definitive "not a worktree" answer, not an "unknowable" error.
//
// Only accepts errors.Is(err, ErrNotGitRepo). If a code path wraps the
// "not a git repository" message without using the typed sentinel, the
// hint won't fire — the TestIsGitWorktreeErrorAlwaysWrapsSentinel test
// below catches the class of drift where internal/cli/init.go stops
// using the sentinel.
func normalizedIsGitWorktree(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	ok, _, err := IsGitWorktree(path)
	if err == nil {
		return ok, nil
	}
	if errors.Is(err, ErrNotGitRepo) {
		return false, nil
	}
	return ok, err
}

// errIsNoIdentity reports whether err from LoadIdentityFromWorktree means
// "no identities in this worktree" rather than a real FS problem. The loader
// returns wrapped errors; we treat two cases as "none":
//   - identities dir missing (wrapped fs.ErrNotExist)
//   - dir present but no .json files (message "no identity files in ...")
func errIsNoIdentity(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	return strings.Contains(err.Error(), "no identity files")
}

// FSOnlyStateAccessor is a StateAccessor for commands that run before the
// daemon connection exists (e.g. `thrum init`). Daemon-backed methods return
// empty; FS-based methods (IsGitWorktree, IdentityStatus) work normally.
type FSOnlyStateAccessor struct{}

// NewFSOnlyStateAccessor returns an accessor safe to call without a daemon.
func NewFSOnlyStateAccessor() *FSOnlyStateAccessor { return &FSOnlyStateAccessor{} }

func (FSOnlyStateAccessor) AgentByName(string) (*AgentSummary, error) { return nil, nil }
func (FSOnlyStateAccessor) TmuxSessionExists(name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	return tmux.HasSession(name), nil
}
func (FSOnlyStateAccessor) IsGitWorktree(path string) (bool, error) {
	return normalizedIsGitWorktree(path)
}
func (FSOnlyStateAccessor) IdentityStatus(path string) (IdentityStatus, *AgentSummary, error) {
	return identityStatusFromPath(path)
}
