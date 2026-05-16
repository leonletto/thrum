package rpc

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// thrum-51cg: writeTmuxToIdentity must update identity files in the worktree
// whose cwd matches the session being launched, even when the agent name does
// not match the session name and the stale TmuxSession value points at a
// different session. HandleCreate populates sessionCwds[name]=cwd; the new
// worktree-path pass uses that mapping to find the right identity file.

func setupTmuxWorktreeTest(t *testing.T) (handler *TmuxHandler, thrumDir, worktreeDir, identityPath string) {
	t.Helper()

	tmpDir := t.TempDir()
	thrumDir = filepath.Join(tmpDir, "main", ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("mkdir thrumDir: %v", err)
	}

	worktreeDir = filepath.Join(tmpDir, "worktreeA")
	worktreeIdentitiesDir := filepath.Join(worktreeDir, ".thrum", "identities")
	if err := os.MkdirAll(worktreeIdentitiesDir, 0o750); err != nil {
		t.Fatalf("mkdir worktree identities: %v", err)
	}

	// Identity whose stale TmuxSession points at a *different* session than
	// the one we're about to launch and whose agent name does NOT sanitize
	// to the new session name. This is the post-γ-reset shape.
	idFile := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "impl_skills",
			Role:   "implementer",
			Module: "skills",
		},
		AgentPID:    0, // pre-prime skips G4
		TmuxSession: "orchestrator-skills:0.0",
		Runtime:     "claude",
		Worktree:    "worktreeA",
		UpdatedAt:   time.Now().Add(-1 * time.Hour),
	}
	identityPath = filepath.Join(worktreeIdentitiesDir, "impl_skills.json")
	data, err := json.MarshalIndent(idFile, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(identityPath, data, 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}

	st, err := state.NewState(thrumDir, thrumDir, "r_51CG_TEST", "")
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	handler = NewTmuxHandler(thrumDir, st)
	return
}

func readIdentity(t *testing.T, path string) config.IdentityFile {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test fixture
	if err != nil {
		t.Fatalf("read identity %s: %v", path, err)
	}
	var got config.IdentityFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return got
}

// Case 1: sessionCwds[name]=worktreeA populated, stale TmuxSession in
// worktreeA's identity. Both Pass 1 (session mismatch) and Pass 2 (agent
// name mismatch) would fail; the new worktree pass must update the stale
// value to the new target.
func TestWriteTmuxToIdentity_WorktreeMatch_UpdatesStaleSession(t *testing.T) {
	h, _, worktreeDir, identityPath := setupTmuxWorktreeTest(t)

	h.sessionMu.Lock()
	h.sessionCwds["plugin-skills-slate"] = worktreeDir
	h.sessionMu.Unlock()

	// Simulate HandleLaunch writing the new binding. sessionName does not
	// match the agent name nor the stale TmuxSession prefix.
	h.writeTmuxToIdentity("plugin-skills-slate", "plugin-skills-slate:0.0", "codex")

	got := readIdentity(t, identityPath)
	if got.TmuxSession != "plugin-skills-slate:0.0" {
		t.Errorf("TmuxSession = %q, want plugin-skills-slate:0.0 (worktree-path pass should update stale value)", got.TmuxSession)
	}
	if got.Runtime != "codex" {
		t.Errorf("Runtime = %q, want codex", got.Runtime)
	}
}

// Case 2: sessionCwds empty for this name. Must fall back to the existing
// Pass 1/2 behavior — no worktree-path shortcut taken.
func TestWriteTmuxToIdentity_NoSessionCwd_FallsBackToPass1And2(t *testing.T) {
	h, _, _, identityPath := setupTmuxWorktreeTest(t)

	// sessionCwds intentionally not populated for "plugin-skills-slate".
	h.writeTmuxToIdentity("plugin-skills-slate", "plugin-skills-slate:0.0", "codex")

	got := readIdentity(t, identityPath)
	// Worktree-path pass should have been skipped. Pass 1 fails (stale
	// TmuxSession points at orchestrator-skills, not plugin-skills-slate).
	// Pass 2 fails (agent name impl_skills != session name plugin-skills-slate).
	// And AllIdentityDirs in the test env (not a git repo) only returns the
	// primary thrumDir — which is empty. So no write should occur.
	if got.TmuxSession != "orchestrator-skills:0.0" {
		t.Errorf("TmuxSession = %q, want unchanged orchestrator-skills:0.0 (no matching pass should succeed)", got.TmuxSession)
	}
	if got.Runtime != "claude" {
		t.Errorf("Runtime = %q, want unchanged claude", got.Runtime)
	}
}

// Case 3: EnforceOneIdentity invariant violation (>1 identity file in the
// worktree). The worktree-path pass must fall back to Pass 1/2 with a
// warning log rather than updating all files — avoids mass-flapping.
func TestWriteTmuxToIdentity_WorktreeMultipleIdentities_FallsBackWithWarning(t *testing.T) {
	h, _, worktreeDir, _ := setupTmuxWorktreeTest(t)

	// Inject a second identity file into the same worktree's identities dir.
	// This violates the EnforceOneIdentity invariant and should trigger fallback.
	extraIdentity := &config.IdentityFile{
		Agent:       config.AgentConfig{Kind: "agent", Name: "impl_extra", Role: "implementer", Module: "other"},
		AgentPID:    0,
		TmuxSession: "some-other-session:0.0",
		Runtime:     "claude",
		Worktree:    "worktreeA",
	}
	extraData, _ := json.MarshalIndent(extraIdentity, "", "  ")
	extraPath := filepath.Join(worktreeDir, ".thrum", "identities", "impl_extra.json")
	if err := os.WriteFile(extraPath, extraData, 0o600); err != nil {
		t.Fatalf("write extra identity: %v", err)
	}

	h.sessionMu.Lock()
	h.sessionCwds["plugin-skills-slate"] = worktreeDir
	h.sessionMu.Unlock()

	// Capture log output so we can assert the warning fired.
	var logBuf bytes.Buffer
	origOutput := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(origOutput) })

	h.writeTmuxToIdentity("plugin-skills-slate", "plugin-skills-slate:0.0", "codex")

	// Neither impl_skills nor impl_extra should have their TmuxSession
	// overwritten — worktree-pass fell back; Pass 1/2 can't match either file.
	skills := readIdentity(t, filepath.Join(worktreeDir, ".thrum", "identities", "impl_skills.json"))
	extra := readIdentity(t, extraPath)

	if skills.TmuxSession != "orchestrator-skills:0.0" {
		t.Errorf("impl_skills TmuxSession = %q, want unchanged orchestrator-skills:0.0", skills.TmuxSession)
	}
	if extra.TmuxSession != "some-other-session:0.0" {
		t.Errorf("impl_extra TmuxSession = %q, want unchanged some-other-session:0.0", extra.TmuxSession)
	}

	logText := logBuf.String()
	if !strings.Contains(logText, "51cg") && !strings.Contains(logText, "multiple identit") {
		t.Errorf("expected warning log about multiple identities, got: %q", logText)
	}
}

// Case 5 (thrum-51cg Option B): team.list enrichment must self-heal a stale
// TmuxSession that points at a dead tmux session. The clearing is defense-
// in-depth alongside Option A — covers external kills (γ reset via raw
// `tmux kill-session`) that bypass HandleKill's clearTmuxFromIdentities.
func TestTeamListEnrichment_ClearsDeadTmuxSession(t *testing.T) {
	_, _, worktreeDir, identityPath := setupTmuxWorktreeTest(t)

	// Pre-fix: identity already has TmuxSession pointing at
	// orchestrator-skills (which the test env confirms doesn't exist as
	// a real tmux session). ttmux.HasSession("orchestrator-skills") returns
	// false; team.list enrichment must notice this and clear the field.
	if err := clearDeadTmuxSessionInIdentity(identityPath); err != nil {
		t.Fatalf("clear dead tmux session: %v", err)
	}

	got := readIdentity(t, identityPath)
	if got.TmuxSession != "" {
		t.Errorf("TmuxSession = %q, want cleared (session is dead)", got.TmuxSession)
	}
	// Worktree field must be preserved — only tmux-related fields are cleared.
	if got.Worktree != "worktreeA" {
		t.Errorf("Worktree = %q, want unchanged worktreeA", got.Worktree)
	}
	// Runtime should also be cleared alongside TmuxSession to match
	// clearTmuxFromIdentitiesInDir semantics (line 1010-1011).
	if got.Runtime != "" {
		t.Errorf("Runtime = %q, want cleared alongside TmuxSession", got.Runtime)
	}

	_ = worktreeDir
}

// Case 4: Sanity check — when only one identity exists in the worktree AND
// Pass 1 would succeed (stale TmuxSession matches current session), the
// worktree-path pass should still work correctly (no regression in the
// existing happy path).
func TestWriteTmuxToIdentity_WorktreeMatch_Pass1HappyPathStillWorks(t *testing.T) {
	h, _, worktreeDir, identityPath := setupTmuxWorktreeTest(t)

	// Overwrite identity so TmuxSession already matches the session we're
	// about to launch — Pass 1 equivalent, but now also worktree-mapped.
	idFile := readIdentity(t, identityPath)
	idFile.TmuxSession = "plugin-skills-slate:0.0"
	idFile.Runtime = "claude"
	data, _ := json.MarshalIndent(idFile, "", "  ")
	if err := os.WriteFile(identityPath, data, 0o600); err != nil {
		t.Fatalf("rewrite identity: %v", err)
	}

	h.sessionMu.Lock()
	h.sessionCwds["plugin-skills-slate"] = worktreeDir
	h.sessionMu.Unlock()

	// Re-launch with a new runtime — should rewrite TmuxSession/Runtime.
	h.writeTmuxToIdentity("plugin-skills-slate", "plugin-skills-slate:0.0", "codex")

	got := readIdentity(t, identityPath)
	if got.Runtime != "codex" {
		t.Errorf("Runtime = %q, want codex after re-launch", got.Runtime)
	}
}

func TestRestoreBinding_PopulatesBothMaps(t *testing.T) {
	h := &TmuxHandler{
		sessionCwds: make(map[string]string),
		cwdSessions: make(map[string]string),
	}
	h.RestoreBinding("session-a", "/path/to/worktree-a")
	if got := h.sessionCwds["session-a"]; got != "/path/to/worktree-a" {
		t.Fatalf("sessionCwds[session-a] = %q, want %q", got, "/path/to/worktree-a")
	}
	if got := h.cwdSessions["/path/to/worktree-a"]; got != "session-a" {
		t.Fatalf("cwdSessions[/path/to/worktree-a] = %q, want %q", got, "session-a")
	}
}

// TestRestoreBinding_StripsTargetSuffix pins thrum-8dl3's downstream fix:
// identity files store `tmux_session` as the full tmux target ("name:N.M")
// but every reader of sessionCwds / cwdSessions uses the bare session name.
// Boot reconcile passes the verbatim identity-file value into RestoreBinding,
// so without the suffix-strip the map keys diverge from caller expectations
// and emitIdentityBanner / HandleRestart / HandleKill all silently miss the
// entry. Caught end-to-end on email-brainstorm during rc.8 verification.
func TestRestoreBinding_StripsTargetSuffix(t *testing.T) {
	h := &TmuxHandler{
		sessionCwds: make(map[string]string),
		cwdSessions: make(map[string]string),
	}
	h.RestoreBinding("foo:0.0", "/path/foo")

	// Map key must be the bare session name — that's the canonical form
	// every reader uses (HandleCreate stores `name` not `name:0.0` so
	// reconcile-side writes must match).
	if got, ok := h.sessionCwds["foo"]; !ok || got != "/path/foo" {
		t.Errorf("sessionCwds[foo] = (%q, ok=%v), want (%q, true) after RestoreBinding stripped the :0.0 suffix",
			got, ok, "/path/foo")
	}
	// The suffixed form must NOT be present — that would mean the strip
	// silently failed and we'd have stale + correct entries side-by-side.
	if _, ok := h.sessionCwds["foo:0.0"]; ok {
		t.Errorf("sessionCwds[foo:0.0] still present; expected RestoreBinding to strip :N.M before writing")
	}
	// cwdSessions VALUE (not key) must also be normalized — readers that
	// look up "what session owns this cwd" expect the bare name.
	if got := h.cwdSessions["/path/foo"]; got != "foo" {
		t.Errorf("cwdSessions[/path/foo] = %q, want %q (bare session name, no :N.M suffix)", got, "foo")
	}
}

// TestRestoreBinding_PaneSuffix covers the less-common ":0.N" form
// (window 0, pane N) — the strip must clip at the first colon so both
// shapes normalize identically.
func TestRestoreBinding_PaneSuffix(t *testing.T) {
	h := &TmuxHandler{
		sessionCwds: make(map[string]string),
		cwdSessions: make(map[string]string),
	}
	h.RestoreBinding("bar:0.3", "/path/bar")
	if got := h.sessionCwds["bar"]; got != "/path/bar" {
		t.Errorf("sessionCwds[bar] = %q, want %q", got, "/path/bar")
	}
}
