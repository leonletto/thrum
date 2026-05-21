package nudge_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/nudge"
	"github.com/stretchr/testify/require"
)

// syncBuf is a thread-safe wrapper around bytes.Buffer so the slog
// handler (writing from background goroutines) and the test goroutine
// (polling via String()) don't race on the same buffer.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

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

// TestDispatchTmux_SkipsSelfEcho is the thrum-1zfk regression guard.
// HandleSend now intentionally keeps the author in the recipients list
// (so the projector can stamp read_at on the self-delivery row), which
// reached the tmux-nudge path and surfaced as 'New message from @<self>'
// in the sender's own pane. DispatchTmux must filter sender out before
// firing, matching the spool-dispatcher guard in cmd/thrum/main.go.
//
// Capture slog to confirm: alice (the sender) is skipped synchronously
// with the 'tmux.skip self' log; bob (a non-self recipient without a
// real tmux session) reaches the resolution path. The test verifies the
// guard fires before any I/O attempt for the self recipient.
func TestDispatchTmux_SkipsSelfEcho(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	require.NoError(t, os.MkdirAll(identitiesDir, 0750))

	// Give alice a registered tmux session — without the guard,
	// DispatchTmux would resolve her identity and attempt to nudge,
	// producing 'nudge.DispatchTmux fire' for sender=alice recipient=alice.
	id := config.IdentityFile{TmuxSession: "alice-session:0.0"}
	idJSON, err := json.Marshal(id)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(identitiesDir, "alice.json"), idJSON, 0600))

	// Install a slog handler that writes to a thread-safe buffer so the
	// guard-path log (synchronous) and the bob goroutine's log don't
	// race on the same writer. Restore the previous default at end.
	var logBuf syncBuf
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	nudge.DispatchTmux(thrumDir, []string{"alice", "bob"}, "alice")

	// Drain background goroutines spawned for non-self recipients.
	// The bob goroutine will fall through to ResolveTarget (returns "")
	// and log 'no-target' — that's our proof bob's path was entered
	// while alice's was short-circuited.
	require.Eventually(t, func() bool {
		return strings.Contains(logBuf.String(), "no-target")
	}, 2*time.Second, 50*time.Millisecond, "bob's no-target log never appeared; background goroutine may not have run")

	out := logBuf.String()
	require.Contains(t, out, "tmux.skip self",
		"expected '[nudge] tmux.skip self' for sender=alice recipient=alice; full log:\n%s", out)
	require.NotContains(t, out, `"recipient":"alice"`+`,"target"`,
		"alice's path should be short-circuited BEFORE any target resolution; full log:\n%s", out)
}

// TestDispatchTmux_SkipsSelfEcho_AllSelfRecipients pins the edge case
// where every recipient is the sender. The guard fires for each entry,
// the loop ends with zero goroutines launched, and DispatchTmux exits
// cleanly. Together with TestDispatchTmux_SkipsSelfEcho this proves the
// guard is per-entry rather than depending on a non-self recipient
// being present in the slice.
func TestDispatchTmux_SkipsSelfEcho_AllSelfRecipients(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	require.NoError(t, os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750))

	var logBuf syncBuf
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// All three recipients equal the sender — every iteration must take
	// the guard branch.
	nudge.DispatchTmux(thrumDir, []string{"alice", "alice", "alice"}, "alice")

	out := logBuf.String()
	require.Equal(t, 3, strings.Count(out, "tmux.skip self"),
		"expected exactly 3 'tmux.skip self' lines (one per recipient); full log:\n%s", out)
	require.NotContains(t, out, "nudge.DispatchTmux fire",
		"no fire log should appear when every recipient is the sender; full log:\n%s", out)
	require.NotContains(t, out, "no-target",
		"no goroutine should reach ResolveTarget when every recipient is skipped; full log:\n%s", out)
}

// TestDispatchTmux_EmptySenderName_TriggersGuard pins the defensive
// behavior when senderName is the empty string. evt.AgentID is supposed
// to be non-empty in production (HandleSend always populates it from
// the resolved callerID), but the guard at the top of the loop will
// silently skip any "" recipient if senderName is also "". This is
// preferable to firing an unsigned nudge — it just means an upstream
// invariant is broken. The pin ensures future refactors don't change
// that behavior without an explicit decision.
func TestDispatchTmux_EmptySenderName_TriggersGuard(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	require.NoError(t, os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750))

	var logBuf syncBuf
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// senderName="" and one recipient also "". Equality holds, guard fires.
	nudge.DispatchTmux(thrumDir, []string{""}, "")

	out := logBuf.String()
	require.Contains(t, out, "tmux.skip self",
		"empty-string recipient with empty-string sender should hit the guard; full log:\n%s", out)
	require.NotContains(t, out, "nudge.DispatchTmux fire",
		"no fire log expected; full log:\n%s", out)
}

func TestHasLocalIdentity(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeIdentity := func(name string) {
		body := fmt.Sprintf(`{"version":1,"agent":{"name":%q}}`, name)
		_ = os.WriteFile(
			filepath.Join(thrumDir, "identities", name+".json"),
			[]byte(body),
			0o600,
		)
	}
	writeIdentity("bob")

	if !nudge.HasLocalIdentity(thrumDir, "bob") {
		t.Fatal("bob should be local")
	}
	if nudge.HasLocalIdentity(thrumDir, "nobody") {
		t.Fatal("nobody should not be local")
	}
}

func TestLocalAgentNames(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alice", "bob"} {
		body := fmt.Sprintf(`{"version":1,"agent":{"name":%q}}`, name)
		if err := os.WriteFile(filepath.Join(identitiesDir, name+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Also drop a non-json file that should be ignored.
	if err := os.WriteFile(filepath.Join(identitiesDir, "README.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := nudge.LocalAgentNames(thrumDir)
	if len(got) != 2 {
		t.Fatalf("want 2 agents, got %d: %v", len(got), got)
	}
	found := map[string]bool{}
	for _, n := range got {
		found[n] = true
	}
	if !found["alice"] || !found["bob"] {
		t.Errorf("missing expected agents: %v", got)
	}
}
