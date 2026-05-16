package worktree

// CreateOpts configures a worktree creation. Callers populate the
// fields appropriate to their mode; see field comments for which
// combinations are valid. Spec §3.1.
type CreateOpts struct {
	// RepoPath is the absolute path to the main repository root
	// (the directory containing the real .git/ directory, NOT a
	// worktree's .git file). Required.
	RepoPath string

	// BasePath, when non-empty, overrides the default worktree base
	// path. Use the empty string to fall back to
	// config.Worktrees.BasePath and then InferBasePath(RepoPath).
	BasePath string

	// AgentName is the canonical thrum agent name owning this
	// worktree. Required.
	AgentName string

	// JobID is the scheduled-agent job identifier. Required when
	// Persistent == false (ephemeral mode); ignored when
	// Persistent == true.
	JobID string

	// WakeTimestamp is the unix-epoch second when the wake fired.
	// Required when Persistent == false; ignored when
	// Persistent == true.
	WakeTimestamp int64

	// BaseBranch is the branch the new worktree's branch is forked
	// from. Defaults to "main" when empty.
	BaseBranch string

	// Persistent, when true, creates (or reuses) a long-lived
	// worktree at <BasePath>/<AgentName> with branch
	// agent/<AgentName>. Re-invocation returns Reused == true when
	// the worktree already exists at the expected path with the
	// expected branch.
	Persistent bool

	// BranchOverride, when non-empty, overrides the default
	// branch-naming convention from §3.4. Used by the cobra
	// wrapper. Daemon scheduler callers always leave this empty.
	BranchOverride string
}

// CreateResult describes a successful creation. Spec §3.1.
type CreateResult struct {
	// Path is the absolute path of the new (or reused) worktree.
	Path string

	// Branch is the name of the branch the worktree is checked
	// out on. Equals opts.BranchOverride when non-empty; otherwise
	// the §3.4 convention applies.
	Branch string

	// Reused is true when Persistent == true AND the worktree
	// already existed at Path with the expected branch.
	Reused bool
}

// DestroyResult describes a completed destruction. Spec §3.2.
type DestroyResult struct {
	// BranchDeleted is true when opts.Branch was non-empty AND the
	// `git branch -D` call succeeded. Callers (cobra wrapper) can
	// use this to print accurate confirmation text instead of
	// assuming success from a nil error (branch deletion is
	// best-effort and logs but does not return on failure).
	BranchDeleted bool
}

// DestroyOpts configures a worktree destruction. Spec §3.2.
type DestroyOpts struct {
	// RepoPath is the absolute path to the main repository root.
	// Required.
	RepoPath string

	// WorktreePath is the absolute path of the worktree to remove.
	// Required.
	WorktreePath string

	// Branch is the branch name to delete after the worktree is
	// removed. Optional; empty string skips branch deletion.
	//
	// Cobra layer (per Leon Q2 lock, 2026-05-15) resolves from
	// --delete-branch flag via `git rev-parse --abbrev-ref HEAD`
	// on the worktree path. B-B1 scheduler caller populates
	// unconditionally with the branch it created at stage 3.
	Branch string

	// Force, when true, passes --force to `git worktree remove`.
	// Required for ephemeral-mode teardown.
	Force bool
}
