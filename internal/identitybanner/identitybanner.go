// Package identitybanner composes the pane-side identity banner that
// `thrum tmux start` and `thrum tmux restart` emit before launching the
// runtime. The banner gives the human watching the tmux pane immediate
// orientation (agent / role / worktree / branch) regardless of whether the
// runtime itself surfaces identity in its UI. Companion to the
// claude-plugin / cursor-plugin SessionStart auto-injection banner
// (thrum-xupf + thrum-2qe2) which lives inside the agent's context window;
// this banner is for the human-watching-tmux side of the same orientation
// problem (thrum-6hqy).
//
// Single source of truth for both call sites (HandleLaunch + HandleRestart)
// so future field additions or formatting tweaks land in one place.
package identitybanner

import (
	"fmt"
	"strings"

	"github.com/leonletto/thrum/internal/config"
)

// Compose returns the multi-line banner text rendered for the given
// identity, or "" if the identity is nil / has no agent name (no banner is
// emitted in that degraded case — the daemon should fall through silently
// rather than ship a half-rendered "Agent: @" line). Lines:
//
//	Agent: @<agent_id>
//	Role:  <role>
//	Worktree: <worktree>          (omitted if empty)
//	Branch: <branch>              (omitted if empty)
//
// Trailing newline included so callers can splice the value directly into
// printf-style output.
func Compose(id *config.IdentityFile) string {
	if id == nil || id.Agent.Name == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Agent: @%s\n", id.Agent.Name)
	if id.Agent.Role != "" {
		fmt.Fprintf(&b, "Role:  %s\n", id.Agent.Role)
	}
	if id.Worktree != "" {
		fmt.Fprintf(&b, "Worktree: %s\n", id.Worktree)
	}
	if id.Branch != "" {
		fmt.Fprintf(&b, "Branch: %s\n", id.Branch)
	}
	return b.String()
}

// ShellCommand returns a single-line shell `printf` invocation that, when
// executed at a shell prompt via `tmux send-keys`, prints the banner from
// Compose to the pane. Returns "" when the identity wouldn't produce a
// banner (matches Compose).
//
// We use printf rather than echo so a single send-keys hop covers all
// banner lines (multiple echo+Enter pairs would fight tmux's keystroke-
// pacing for noisy renders), and we shell-quote each line with single
// quotes (escaping any embedded single quotes) so user-controlled values
// like worktree paths can't introduce shell metacharacters into the
// command. The trailing empty-string arg adds a blank line after the
// banner so subsequent runtime-launch output is visually separated.
func ShellCommand(id *config.IdentityFile) string {
	body := Compose(id)
	if body == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	args := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		args = append(args, shellSingleQuote(l))
	}
	// Trailing blank line for visual separation from runtime output.
	args = append(args, "''")
	return "printf '%s\\n' " + strings.Join(args, " ")
}

// shellSingleQuote returns s wrapped in single quotes with any embedded
// single quote escaped via the standard '"'"' Bourne-shell idiom. Safe
// for any byte string — no metacharacters can escape the quoting.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
