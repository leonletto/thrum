package worktree

import "errors"

// ErrPathExists is returned by Create in ephemeral mode when the
// target worktree path already exists on disk. Caller (B-B1 E6.1)
// maps this to "sweep then retry" per spec §3.5. Persistent mode
// never returns this error; it returns CreateResult{Reused: true}
// instead.
var ErrPathExists = errors.New("worktree: path already exists")

// ErrPersistentBranchMismatch is returned by Create in persistent
// mode when the target worktree path already exists AND its branch
// is NOT the expected agent/<AgentName>. Indicates operator-owned
// state squatting on the persistent path; caller must reconcile
// manually. Spec §3.5.
var ErrPersistentBranchMismatch = errors.New("worktree: persistent path has unexpected branch")

// ErrInvalidOpts is returned by Create when CreateOpts fails
// validation at API entry (missing required field, malformed name,
// path too long, JobID outside the ULID alphabet, etc.).
// Spec §3.4 + §3.5.
var ErrInvalidOpts = errors.New("worktree: invalid CreateOpts")
