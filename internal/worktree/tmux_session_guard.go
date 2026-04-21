package worktree

import (
	"log/slog"
	"path/filepath"
	"strings"

	ttmux "github.com/leonletto/thrum/internal/tmux"
)

// SafeTmuxOpts configures the tmux-session safety check used when
// writing the TmuxSession field of a worktree-bound identity file.
// The zero value yields the default-deny behavior: a paneTarget whose
// session does not match the target worktree's basename is refused.
type SafeTmuxOpts struct {
	// AllowCrossWorktree, when true, bypasses the worktree-name match
	// and returns the supplied paneTarget verbatim. Use only on paths
	// that legitimately operate across worktrees (e.g. a daemon RPC
	// that binds a fresh agent's identity to its newly-created tmux
	// session by design). Mirrors the EnforceOpts pattern from
	// thrum-182j EnforceOneIdentityWith.
	AllowCrossWorktree bool

	// Logger is the *slog.Logger used for the structured warning
	// emitted on cross-worktree refusal. nil falls back to
	// slog.Default().
	Logger *slog.Logger
}

// PaneTargetForIdentity returns the tmux target string that is safe
// to write as the TmuxSession field of an identity file living under
// worktreePath. When the caller's pane (paneTarget) belongs to the
// same worktree as the target identity, the input is returned
// unchanged. When it belongs to a different worktree — the
// thrum-l9s1 misroute path — the helper returns "" so the caller
// skips the write, and emits a structured slog.Warn.
//
// The match rule is "session-name of paneTarget == sanitized basename
// of worktreePath". SanitizeSessionName matches the rule used by
// `thrum tmux create` so a worktree at
// /Users/leon/.workspaces/thrum/enforce-identity matches a tmux
// session named "enforce-identity" exactly.
//
// Default-deny on ambiguity: empty paneTarget returns "" silently
// (the caller has already gated on `ttmux.InTmux()`); empty
// worktreePath, malformed paneTarget without a "session:..." shape,
// or any other unparseable input also returns "" — better to skip
// the write than blindly accept whatever the caller's $TMUX
// happens to be.
func PaneTargetForIdentity(paneTarget, worktreePath string, opts SafeTmuxOpts) string {
	if paneTarget == "" {
		return ""
	}
	if opts.AllowCrossWorktree {
		return paneTarget
	}
	if worktreePath == "" {
		return ""
	}
	// ParseTarget returns session=paneTarget when no colon is present,
	// which the mismatch branch below would misreport as a
	// "cross-worktree" refusal. Catch malformed input first and refuse
	// silently — it's a parse failure, not a routing decision.
	if !strings.Contains(paneTarget, ":") {
		return ""
	}
	session, _, _ := ttmux.ParseTarget(paneTarget)
	if session == "" {
		return ""
	}
	expected := ttmux.SanitizeSessionName(filepath.Base(strings.TrimRight(worktreePath, "/")))
	if session == expected {
		return paneTarget
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("worktree.PaneTargetForIdentity refused: caller pane belongs to a different worktree",
		slog.String("caller_pane", paneTarget),
		slog.String("caller_session", session),
		slog.String("target_worktree", worktreePath),
		slog.String("expected_session", expected))
	return ""
}
