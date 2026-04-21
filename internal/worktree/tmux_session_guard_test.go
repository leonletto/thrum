package worktree

import (
	"path/filepath"
	"testing"
)

// TestPaneTargetForIdentity_MatchPasses — caller's pane session
// matches the target worktree's basename → return paneTarget unchanged
// so reconcileDrift / quickstart can write it as TmuxSession.
func TestPaneTargetForIdentity_MatchPasses(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "enforce-identity")
	got := PaneTargetForIdentity("enforce-identity:0.0", wt, SafeTmuxOpts{})
	if got != "enforce-identity:0.0" {
		t.Errorf("got %q, want %q (pane session matches worktree basename)", got, "enforce-identity:0.0")
	}
}

// TestPaneTargetForIdentity_MismatchRefuses — thrum-l9s1 core case.
// Coordinator runs `thrum` from their own pane (session=thrum) with
// cwd resolving into a different worktree (enforce-identity). The
// helper must return "" so the caller skips the TmuxSession write —
// otherwise the agent's identity gets the coordinator's session and
// every future nudge to that agent is misrouted to coordinator's
// pane (the actual S44 production bug).
func TestPaneTargetForIdentity_MismatchRefuses(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "enforce-identity")
	got := PaneTargetForIdentity("thrum:0.0", wt, SafeTmuxOpts{})
	if got != "" {
		t.Errorf("got %q, want \"\" (cross-worktree write must be refused)", got)
	}
}

// TestPaneTargetForIdentity_AllowCrossWorktree — explicit override
// for legitimate daemon-side paths (e.g. agent registration RPC that
// writes into a sibling worktree). Mirrors the 182j EnforceOpts
// AllowCrossWorktree pattern.
func TestPaneTargetForIdentity_AllowCrossWorktree(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "enforce-identity")
	got := PaneTargetForIdentity("thrum:0.0", wt, SafeTmuxOpts{AllowCrossWorktree: true})
	if got != "thrum:0.0" {
		t.Errorf("got %q, want %q (AllowCrossWorktree=true must bypass gate)", got, "thrum:0.0")
	}
}

// TestPaneTargetForIdentity_EmptyPaneTargetSilent — pre-checked by
// caller (`if ttmux.InTmux()`). When paneTarget is empty the helper
// must return "" silently — no log noise from non-tmux callers.
func TestPaneTargetForIdentity_EmptyPaneTargetSilent(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "enforce-identity")
	got := PaneTargetForIdentity("", wt, SafeTmuxOpts{})
	if got != "" {
		t.Errorf("got %q, want \"\" for empty paneTarget", got)
	}
}

// TestPaneTargetForIdentity_EmptyWorktreeRefuses — defensive: an
// empty worktree path means we can't validate. Refuse the write
// rather than blindly accept (default-deny on ambiguous input).
func TestPaneTargetForIdentity_EmptyWorktreeRefuses(t *testing.T) {
	got := PaneTargetForIdentity("thrum:0.0", "", SafeTmuxOpts{})
	if got != "" {
		t.Errorf("got %q, want \"\" for empty worktreePath", got)
	}
}

// TestPaneTargetForIdentity_SanitizesWorktreeName — worktree paths
// can have characters that get sanitized in tmux session names. The
// helper must compare the SANITIZED form so a worktree at
// /path/to/feature.x.y is correctly compared against session
// "feature-x-y:0.0" (dots → hyphens via SanitizeSessionName).
func TestPaneTargetForIdentity_SanitizesWorktreeName(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "feature.x.y")
	got := PaneTargetForIdentity("feature-x-y:0.0", wt, SafeTmuxOpts{})
	if got != "feature-x-y:0.0" {
		t.Errorf("got %q, want %q (sanitized comparison)", got, "feature-x-y:0.0")
	}
}

// TestPaneTargetForIdentity_MalformedPaneTargetRefuses — paneTarget
// without a session:window.pane shape (no colon) is unparseable.
// Refuse rather than silently accept.
func TestPaneTargetForIdentity_MalformedPaneTargetRefuses(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "enforce-identity")
	got := PaneTargetForIdentity("notatarget", wt, SafeTmuxOpts{})
	if got != "" {
		t.Errorf("got %q, want \"\" for malformed paneTarget", got)
	}
}
