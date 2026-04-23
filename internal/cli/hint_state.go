package cli

// StateAccessor is the narrow read-only interface hint sources use to query
// daemon/FS state. Keep this surface minimal — hint sources that need richer
// state should negotiate additions via spec revision, not local shortcuts.
//
// All methods are best-effort: when state is unknowable (daemon unreachable,
// FS EACCES), implementations return an error; hint sources silently skip
// the condition rather than breaking the command path.
type StateAccessor interface {
	// AgentByName returns a summary of the named agent, or (nil, nil) when
	// no such agent is registered. An error signals the query itself failed;
	// nil-summary with nil-error means "definitely no such agent".
	AgentByName(name string) (*AgentSummary, error)

	// TmuxSessionExists reports whether a tmux session by that name is live.
	TmuxSessionExists(name string) (bool, error)

	// IsGitWorktree reports whether the path is inside a git worktree.
	IsGitWorktree(path string) (bool, error)

	// IdentityStatus returns the agent-identity state of a worktree path.
	// When status is IdentityLive or IdentityStale, the *AgentSummary
	// describes the registered agent. When IdentityNone, returns
	// (IdentityNone, nil, nil).
	IdentityStatus(worktreePath string) (IdentityStatus, *AgentSummary, error)
}
