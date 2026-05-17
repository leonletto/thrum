// Package mirror is the runtime-aware mirror worker for the C-B1 skill
// substrate. Promoted skills under .thrum/skills/<name>/ are copied
// (mirrored) into every active worktree's per-runtime path (e.g.
// .claude/skills/<name>/) so the agent runtime's discovery loader can
// pick them up without restart.
//
// The shared event types live in the parent package
// (internal/skills/skill_types.go) per spec §6: MirrorEvent,
// MirrorEventKind, MirrorTrigger. Both watcher.go (parent) and
// worker.go (this package) reference them, so co-locating them here
// would create an import cycle.
//
// Dependency direction: internal/skills/mirror MAY import
// internal/skills; the reverse is forbidden. Adding any
// internal/skills/mirror import to a file under internal/skills/ is a
// regression — verify via `go list -deps internal/skills/mirror` and
// `go build ./internal/skills/...` after any change here.
package mirror

import "errors"

// Sentinel errors. Callers compare with errors.Is to distinguish
// "this worktree was never registered with the mirror" from "this
// runtime has no two-tier mirror surface in v0.11" from a real
// filesystem error.
var (
	// ErrUnknownWorktree fires when a caller asks the worker to
	// mirror to a worktree that was never Register()'d. Surface for
	// EnsureMirrored callers (B-B1 stage-3, thrum-non7 cobra) so a
	// missing-registration bug fails loud rather than silently
	// dropping events.
	ErrUnknownWorktree = errors.New("mirror: unknown worktree")

	// ErrNullAdapter fires when the worker resolves a runtime that
	// has no mirror surface in v0.11 (codex/opencode/kiro/cursor as
	// of plan v2). Treated as success-skip by callers — there's
	// nothing to mirror, but it's not an error condition.
	ErrNullAdapter = errors.New("mirror: null adapter")

	// ErrMirrorWrite wraps any filesystem-level write failure during
	// a mirror apply. Callers should not retry on this — the worker
	// re-runs reconcile at restart.
	ErrMirrorWrite = errors.New("mirror: write failed")

	// ErrUnknownRuntime fires when Adapter.Lookup is asked about a
	// runtime name not in the adapter table. Indicates a typo at the
	// CLI / config layer, not a missing v0.11 mirror surface (use
	// ErrNullAdapter for the latter).
	ErrUnknownRuntime = errors.New("mirror: unknown runtime")
)
