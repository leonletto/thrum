package identitybanner

import (
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

// TestCompose_FullIdentity pins the full 4-line shape when every field is
// set. Order matters for human readability: agent → role → worktree →
// branch — the call site relies on this ordering for the banner to read
// like the xupf+2qe2 SessionStart-injected banner.
func TestCompose_FullIdentity(t *testing.T) {
	id := &config.IdentityFile{
		Agent: config.AgentConfig{
			Name: "impl_team_fix",
			Role: "implementer",
		},
		Worktree: "/Users/leon/.workspaces/thrum/team-fix",
		Branch:   "fix/tmux-start-restart-identity-banner",
	}
	got := Compose(id)
	want := "Agent: @impl_team_fix\n" +
		"Role:  implementer\n" +
		"Worktree: /Users/leon/.workspaces/thrum/team-fix\n" +
		"Branch: fix/tmux-start-restart-identity-banner\n"
	if got != want {
		t.Errorf("Compose mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

// TestCompose_OmitsEmptyOptionalFields verifies that worktree/branch are
// omitted when empty — a fresh registration (pre-tmux-launch identity
// write) may not have either yet, and emitting "Worktree: \n" would mislead
// the human reading the banner.
func TestCompose_OmitsEmptyOptionalFields(t *testing.T) {
	id := &config.IdentityFile{
		Agent: config.AgentConfig{
			Name: "fresh_agent",
			Role: "implementer",
		},
		// Worktree + Branch unset.
	}
	got := Compose(id)
	want := "Agent: @fresh_agent\n" +
		"Role:  implementer\n"
	if got != want {
		t.Errorf("Compose mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

// TestCompose_NilIdentityReturnsEmpty pins the degraded fall-through: a
// nil identity (whoami failed, identity file missing) yields no banner so
// the daemon can route around the tmux send-keys without emitting a
// half-rendered "Agent: @\n" header.
func TestCompose_NilIdentityReturnsEmpty(t *testing.T) {
	if got := Compose(nil); got != "" {
		t.Errorf("Compose(nil) = %q, want empty", got)
	}
}

// TestCompose_EmptyAgentNameReturnsEmpty mirrors NilIdentity for the
// other degraded shape — IdentityFile present but Agent.Name unset.
func TestCompose_EmptyAgentNameReturnsEmpty(t *testing.T) {
	id := &config.IdentityFile{}
	if got := Compose(id); got != "" {
		t.Errorf("Compose(empty-name) = %q, want empty", got)
	}
}

// TestShellCommand_QuotesEachLine pins the printf-with-single-quoted-args
// shape. Each banner line must be its own positional arg so a future
// formatting tweak (e.g. adding a leading "* " bullet) can land without
// re-counting %s slots in the format string. The trailing empty string
// arg adds a blank line for visual separation from runtime output.
func TestShellCommand_QuotesEachLine(t *testing.T) {
	id := &config.IdentityFile{
		Agent:    config.AgentConfig{Name: "impl", Role: "implementer"},
		Worktree: "/path",
		Branch:   "main",
	}
	got := ShellCommand(id)
	if !strings.HasPrefix(got, "printf '%s\\n' ") {
		t.Errorf("ShellCommand should start with printf format string, got: %s", got)
	}
	for _, needle := range []string{
		"'Agent: @impl'",
		"'Role:  implementer'",
		"'Worktree: /path'",
		"'Branch: main'",
		"''", // trailing blank line
	} {
		if !strings.Contains(got, needle) {
			t.Errorf("ShellCommand missing arg %q\n--- got ---\n%s", needle, got)
		}
	}
}

// TestShellCommand_EscapesEmbeddedSingleQuote pins the safety property:
// a worktree path or branch name containing a literal single quote (rare
// but possible — e.g. someone's "Leon's repo" path) must not break out
// of the shell quoting. The standard '"'"' Bourne idiom should appear
// inline in the rendered command.
func TestShellCommand_EscapesEmbeddedSingleQuote(t *testing.T) {
	id := &config.IdentityFile{
		Agent:    config.AgentConfig{Name: "impl", Role: "implementer"},
		Worktree: "/Leon's path/repo",
	}
	got := ShellCommand(id)
	// Contains the escaped form (single-quote → '"'"').
	if !strings.Contains(got, `'Worktree: /Leon'"'"'s path/repo'`) {
		t.Errorf("ShellCommand should escape embedded single quote via '\"'\"' idiom\n--- got ---\n%s", got)
	}
}

// TestShellCommand_NilReturnsEmpty matches Compose's degraded shape — a
// missing identity yields no shell command at all, so the daemon's
// SendKeys call is skipped entirely rather than running an empty printf
// that would still consume a line in the pane.
func TestShellCommand_NilReturnsEmpty(t *testing.T) {
	if got := ShellCommand(nil); got != "" {
		t.Errorf("ShellCommand(nil) = %q, want empty", got)
	}
}

// TestShellCommand_EmptyAgentNameReturnsEmpty parallels
// TestCompose_EmptyAgentNameReturnsEmpty for ShellCommand. Although
// ShellCommand delegates to Compose so the property holds transitively,
// pinning it explicitly defends against a future refactor that fans out
// the empty-name guard between the two functions.
func TestShellCommand_EmptyAgentNameReturnsEmpty(t *testing.T) {
	id := &config.IdentityFile{}
	if got := ShellCommand(id); got != "" {
		t.Errorf("ShellCommand(empty-name) = %q, want empty", got)
	}
}
