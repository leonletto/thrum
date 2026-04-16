package nudge_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/nudge"
	"github.com/stretchr/testify/require"
)

// TestResolveTarget_FindsTmuxSession exercises the happy path: an agent
// with TmuxSession populated in its identity file is resolvable.
func TestResolveTarget_FindsTmuxSession(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	require.NoError(t, os.MkdirAll(identitiesDir, 0750))

	id := config.IdentityFile{
		TmuxSession: "permission-prompts:0.0",
	}
	idJSON, err := json.Marshal(id)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(identitiesDir, "impl_permission_prompts.json"),
		idJSON, 0600))

	got := nudge.ResolveTarget(thrumDir, "impl_permission_prompts")
	require.Equal(t, "permission-prompts:0.0", got)
}

// TestResolveTarget_MissingIdentity returns "" cleanly (no panic) when the
// identity file does not exist. This is the common case for non-tmux
// agents — their pane is just unreachable, not an error.
func TestResolveTarget_MissingIdentity(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	require.NoError(t, os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750))

	got := nudge.ResolveTarget(thrumDir, "nonexistent")
	require.Empty(t, got)
}

// TestResolveTarget_EmptyTmuxSession returns "" when the identity file
// exists but TmuxSession is unset. Mirrors a CLI-only agent that
// registered without a tmux pane.
func TestResolveTarget_EmptyTmuxSession(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	require.NoError(t, os.MkdirAll(identitiesDir, 0750))

	id := config.IdentityFile{} // TmuxSession deliberately empty
	idJSON, err := json.Marshal(id)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(identitiesDir, "cli_only_agent.json"),
		idJSON, 0600))

	got := nudge.ResolveTarget(thrumDir, "cli_only_agent")
	require.Empty(t, got)
}

// TestResolveTarget_MalformedJSON returns "" rather than panicking on a
// corrupt identity file.
func TestResolveTarget_MalformedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	require.NoError(t, os.MkdirAll(identitiesDir, 0750))

	require.NoError(t, os.WriteFile(
		filepath.Join(identitiesDir, "broken.json"),
		[]byte("{not valid json"), 0600))

	got := nudge.ResolveTarget(thrumDir, "broken")
	require.Empty(t, got)
}

// TestDispatchTmux_NoOpOnEmptyInputs proves DispatchTmux is safe to call
// with empty thrumDir or empty recipients — it just returns without
// attempting any I/O. This is the contract the SetOnEventWrite hook
// relies on for non-message events.
func TestDispatchTmux_NoOpOnEmptyInputs(t *testing.T) {
	// Should not panic, should not block. The function spawns goroutines
	// per recipient — with zero recipients it spawns nothing.
	nudge.DispatchTmux("", []string{"alice"}, "bob")
	nudge.DispatchTmux("/tmp/some/dir", nil, "bob")
	nudge.DispatchTmux("/tmp/some/dir", []string{}, "bob")
}

// TestDispatchTmux_SkipsUnresolvableRecipients ensures a recipient with
// no identity file is silently skipped (the hot-path expectation:
// broadcast messages have many recipients, only some have tmux panes).
func TestDispatchTmux_SkipsUnresolvableRecipients(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	require.NoError(t, os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750))

	// Two recipients; neither has an identity file. DispatchTmux should
	// spawn goroutines, find no targets, and complete silently. We can't
	// directly assert "no nudge fired" without injecting a mock ttmux,
	// but the test passes if no panic / no goroutine leak.
	nudge.DispatchTmux(thrumDir, []string{"ghost1", "ghost2"}, "alice")
	// No assertion needed — completing without panic is the test.
}
