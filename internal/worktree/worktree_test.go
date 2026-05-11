package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- EnsureRedirects tests ---

func TestEnsureRedirects_CreatesThrum(t *testing.T) {
	mainRepo := t.TempDir()
	mainThrumDir := filepath.Join(mainRepo, ".thrum")
	if err := os.MkdirAll(mainThrumDir, 0750); err != nil {
		t.Fatal(err)
	}

	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	redirect, err := os.ReadFile(filepath.Join(wt, ".thrum", "redirect"))
	if err != nil {
		t.Fatal("redirect file not created")
	}
	if string(redirect) != mainThrumDir+"\n" {
		t.Errorf("redirect = %q, want %q", string(redirect), mainThrumDir+"\n")
	}

	if _, err := os.Stat(filepath.Join(wt, ".thrum", "identities")); err != nil {
		t.Error("identities dir not created")
	}
	if _, err := os.Stat(filepath.Join(wt, ".thrum", "context")); err != nil {
		t.Error("context dir not created")
	}
}

func TestEnsureRedirects_CreatesBeads(t *testing.T) {
	mainRepo := t.TempDir()
	os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0750)
	os.MkdirAll(filepath.Join(mainRepo, ".beads"), 0750)

	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0600)

	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	redirect, err := os.ReadFile(filepath.Join(wt, ".beads", "redirect"))
	if err != nil {
		t.Fatal("beads redirect not created")
	}
	expected := filepath.Join(mainRepo, ".beads") + "\n"
	if string(redirect) != expected {
		t.Errorf("beads redirect = %q, want %q", string(redirect), expected)
	}
}

func TestEnsureRedirects_SkipsBeadsWhenNotPresent(t *testing.T) {
	mainRepo := t.TempDir()
	os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0750)

	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0600)

	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wt, ".beads")); !os.IsNotExist(err) {
		t.Error(".beads should not exist when main repo has no .beads/")
	}
}

// TestEnsureRedirects_CopiesHookScripts pins thrum-nne1: gitignored hook
// scripts in the main repo's scripts/ dir must be copied into the worktree
// at redirect-setup time so Claude Code SessionStart/PostToolUse hooks
// (which dereference ${CLAUDE_PROJECT_DIR}/scripts/<name>.sh) don't fire
// against a missing file in every worktree-created subdir.
func TestEnsureRedirects_CopiesHookScripts(t *testing.T) {
	mainRepo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0o750); err != nil {
		t.Fatal(err)
	}
	mainScriptsDir := filepath.Join(mainRepo, "scripts")
	if err := os.MkdirAll(mainScriptsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	startupBody := []byte("#!/usr/bin/env bash\necho startup\n")
	inboxBody := []byte("#!/usr/bin/env bash\necho inbox\n")
	if err := os.WriteFile(filepath.Join(mainScriptsDir, "thrum-startup.sh"), startupBody, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainScriptsDir, "thrum-check-inbox.sh"), inboxBody, 0o755); err != nil {
		t.Fatal(err)
	}

	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for name, want := range map[string][]byte{
		"thrum-startup.sh":     startupBody,
		"thrum-check-inbox.sh": inboxBody,
	} {
		path := filepath.Join(wt, "scripts", name)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected %s to be copied: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s content mismatch: got %q, want %q", name, got, want)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s missing user-exec bit: %v", name, info.Mode().Perm())
		}
	}
}

// TestEnsureRedirects_HookScripts_NoOpWhenSourceMissing verifies that an
// operator who has removed a hook script from the main repo deliberately
// does not break worktree setup.
func TestEnsureRedirects_HookScripts_NoOpWhenSourceMissing(t *testing.T) {
	mainRepo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0o750); err != nil {
		t.Fatal(err)
	}
	// No scripts/ in main repo.

	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// scripts/ dir gets created but stays empty; that's fine — the
	// init flow on the main repo will eventually materialize them and
	// the next EnsureRedirects call will copy them in.
	if entries, err := os.ReadDir(filepath.Join(wt, "scripts")); err != nil {
		t.Fatalf("scripts dir should exist: %v", err)
	} else if len(entries) != 0 {
		t.Errorf("scripts dir should be empty when main has no sources, got %d entries", len(entries))
	}
}

func TestEnsureRedirects_FixesBrokenRedirect(t *testing.T) {
	mainRepo := t.TempDir()
	os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0750)

	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0600)

	os.MkdirAll(filepath.Join(wt, ".thrum"), 0750)
	os.WriteFile(filepath.Join(wt, ".thrum", "redirect"), []byte("/nonexistent/path/.thrum\n"), 0600)

	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	redirect, _ := os.ReadFile(filepath.Join(wt, ".thrum", "redirect"))
	expected := filepath.Join(mainRepo, ".thrum") + "\n"
	if string(redirect) != expected {
		t.Errorf("redirect not fixed: got %q, want %q", string(redirect), expected)
	}
}

func TestEnsureRedirects_ErrorNoMainThrum(t *testing.T) {
	mainRepo := t.TempDir()
	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0600)

	err := EnsureRedirects(wt, mainRepo)
	if err == nil {
		t.Fatal("expected error when main repo has no .thrum/")
	}
}

func TestEnsureRedirects_ErrorWorktreeNotFound(t *testing.T) {
	err := EnsureRedirects("/nonexistent/path", t.TempDir())
	if err == nil {
		t.Fatal("expected error for nonexistent worktree")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnsureRedirects_Idempotent(t *testing.T) {
	mainRepo := t.TempDir()
	os.MkdirAll(filepath.Join(mainRepo, ".thrum"), 0750)

	wt := t.TempDir()
	os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "test")+"\n"), 0600)

	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := EnsureRedirects(wt, mainRepo); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

// --- EnforceOneIdentity tests ---

// TestEnforceOneIdentity_QuarantinesOthers — thrum-ajmd. Sibling
// identities are MOVED to .thrum/identities/.quarantine/, not deleted.
// This preserves recourse when EnforceOneIdentity fires incorrectly.
func TestEnforceOneIdentity_QuarantinesOthers(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	os.MkdirAll(idDir, 0750)

	os.WriteFile(filepath.Join(idDir, "old_agent.json"), []byte(`{"agent":{"name":"old_agent"}}`), 0600)
	os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{"agent":{"name":"new_agent"}}`), 0600)

	quarantined := EnforceOneIdentity(dir, "new_agent")

	if _, err := os.Stat(filepath.Join(idDir, "old_agent.json")); !os.IsNotExist(err) {
		t.Error("old_agent.json should no longer be at top level")
	}
	if _, err := os.Stat(filepath.Join(idDir, "new_agent.json")); err != nil {
		t.Error("new_agent.json should survive")
	}
	if len(quarantined) != 1 || quarantined[0] != "old_agent" {
		t.Errorf("quarantined = %v, want [old_agent]", quarantined)
	}

	// Quarantine directory exists and holds a timestamped copy of the
	// original file — not a delete, so recovery is possible.
	qDir := filepath.Join(idDir, ".quarantine")
	entries, err := os.ReadDir(qDir)
	if err != nil {
		t.Fatalf("quarantine dir should exist: %v", err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "old_agent.json.") {
			found = true
			data, _ := os.ReadFile(filepath.Join(qDir, e.Name()))
			if !strings.Contains(string(data), "old_agent") {
				t.Errorf("quarantined file contents missing: %s", data)
			}
		}
	}
	if !found {
		t.Errorf("quarantined file not found in %s (entries: %v)", qDir, entries)
	}
}

func TestEnforceOneIdentity_PreservesContext(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	ctxDir := filepath.Join(dir, ".thrum", "context")
	os.MkdirAll(idDir, 0750)
	os.MkdirAll(ctxDir, 0750)

	os.WriteFile(filepath.Join(idDir, "old_agent.json"), []byte(`{}`), 0600)
	os.WriteFile(filepath.Join(ctxDir, "old_agent.md"), []byte("# Notes"), 0600)

	EnforceOneIdentity(dir, "new_agent")

	if _, err := os.Stat(filepath.Join(idDir, "old_agent.json")); !os.IsNotExist(err) {
		t.Error("old identity should no longer be at top level (it's in .quarantine/)")
	}
	if _, err := os.Stat(filepath.Join(ctxDir, "old_agent.md")); err != nil {
		t.Error("context file should be preserved")
	}
}

func TestEnforceOneIdentity_NoIdentitiesDir(t *testing.T) {
	dir := t.TempDir()
	quarantined := EnforceOneIdentity(dir, "agent")
	if len(quarantined) != 0 {
		t.Error("expected no quarantines")
	}
}

// TestEnforceOneIdentity_MultipleKeepers — thrum-dw06. Variadic keep
// list must preserve every named identity, not just the first. The
// daemon-side enforceWorktreeIdentity hook passes both the newly
// registered agent's name AND the peercred-resolved caller's name so
// neither gets quarantined. Without this, registering a differently
// named agent from an existing agent's cwd (e.g. the E2E harness
// registering short-lived test agents from the coordinator dir)
// quarantines the caller's own identity file.
func TestEnforceOneIdentity_MultipleKeepers(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(idDir, "caller.json"), []byte(`{"agent":{"name":"caller"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{"agent":{"name":"new_agent"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "stale.json"), []byte(`{"agent":{"name":"stale"}}`), 0600); err != nil {
		t.Fatal(err)
	}

	quarantined := EnforceOneIdentity(dir, "new_agent", "caller")

	// Both named keepers survive.
	if _, err := os.Stat(filepath.Join(idDir, "new_agent.json")); err != nil {
		t.Errorf("new_agent.json must survive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(idDir, "caller.json")); err != nil {
		t.Errorf("caller.json must survive (regression: caller's own identity was quarantined): %v", err)
	}
	// Unkept sibling is quarantined.
	if _, err := os.Stat(filepath.Join(idDir, "stale.json")); !os.IsNotExist(err) {
		t.Errorf("stale.json should be quarantined, still at top level")
	}
	if len(quarantined) != 1 || quarantined[0] != "stale" {
		t.Errorf("quarantined = %v, want [stale]", quarantined)
	}
}

// TestEnforceOneIdentity_EmptyKeeperIgnored — a zero-length keep arg
// (e.g. peercred resolved.AgentID is "" for anonymous callers) must be
// skipped, not matched against unnamed sibling files.
func TestEnforceOneIdentity_EmptyKeeperIgnored(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "stale.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	quarantined := EnforceOneIdentity(dir, "new_agent", "")

	if _, err := os.Stat(filepath.Join(idDir, "stale.json")); !os.IsNotExist(err) {
		t.Errorf("stale.json should be quarantined even with empty second keeper")
	}
	if len(quarantined) != 1 {
		t.Errorf("want 1 quarantined, got %v", quarantined)
	}
}

// TestEnforceOneIdentityWith_LiveAgentPreserved — thrum-182j defensive
// invariant. When a sibling identity file is NOT in the keeper list
// BUT its AgentPID is alive per the injected IsPIDAlive callback,
// quarantine must be refused. This models the peercred-mis-resolution
// cascade: the caller's keeper list is incomplete (daemon DB is stale
// or peercred resolved the wrong caller), so a live agent's identity
// file would otherwise be quarantined as a "stale sibling". The
// callback-verified liveness check is the defense-in-depth gate that
// keeps the file intact.
func TestEnforceOneIdentityWith_LiveAgentPreserved(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Victim: a live agent's identity file — NOT in the keeper list
	// below, but its PID is reported alive.
	victim := filepath.Join(idDir, "victim.json")
	if err := os.WriteFile(victim, []byte(`{"agent":{"name":"victim"},"agent_pid":4242}`), 0600); err != nil {
		t.Fatal(err)
	}
	// A truly stale sibling with a dead PID — should still be quarantined.
	stale := filepath.Join(idDir, "stale.json")
	if err := os.WriteFile(stale, []byte(`{"agent":{"name":"stale"},"agent_pid":9999}`), 0600); err != nil {
		t.Fatal(err)
	}
	// The registrant we are keeping.
	keeper := filepath.Join(idDir, "new_agent.json")
	if err := os.WriteFile(keeper, []byte(`{"agent":{"name":"new_agent"}}`), 0600); err != nil {
		t.Fatal(err)
	}

	opts := EnforceOpts{
		IsPIDAlive: func(pid int) bool { return pid == 4242 }, // victim alive, stale dead
	}
	quarantined := EnforceOneIdentityWith(dir, opts, "new_agent")

	// Victim must survive (live agent, even though not in keep list).
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("victim.json must survive live-PID check: %v", err)
	}
	// Stale must be quarantined.
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale.json should be quarantined, still at top level")
	}
	// Keeper must survive.
	if _, err := os.Stat(keeper); err != nil {
		t.Errorf("new_agent.json must survive: %v", err)
	}
	if len(quarantined) != 1 || quarantined[0] != "stale" {
		t.Errorf("quarantined = %v, want [stale]", quarantined)
	}
}

// TestEnforceOneIdentityWith_ZeroPIDStillQuarantined — pre-prime
// identity files (AgentPID == 0) are not protected by liveness. G4
// has the same pre-prime carveout because a zero PID means "no live
// process asserted this file yet"; it is safe to quarantine.
func TestEnforceOneIdentityWith_ZeroPIDStillQuarantined(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "pre_prime.json"), []byte(`{"agent":{"name":"pre_prime"},"agent_pid":0}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	// IsPIDAlive would return true if called with a live PID, but we
	// never want it consulted for zero: assert by failing the test if
	// it's called with pid == 0.
	opts := EnforceOpts{
		IsPIDAlive: func(pid int) bool {
			if pid == 0 {
				t.Errorf("IsPIDAlive called with pid=0 — the implementation should short-circuit")
			}
			return true
		},
	}
	quarantined := EnforceOneIdentityWith(dir, opts, "new_agent")

	if len(quarantined) != 1 || quarantined[0] != "pre_prime" {
		t.Errorf("quarantined = %v, want [pre_prime]", quarantined)
	}
}

// TestEnforceOneIdentityWith_UnreadableFileQuarantined — if a sibling
// file cannot be parsed for AgentPID (corrupt JSON, missing field),
// treat as PID=0 and fall through to the normal keeper check. This
// keeps the function deterministic: liveness protection applies only
// to files that positively assert a live owner.
func TestEnforceOneIdentityWith_UnreadableFileQuarantined(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "corrupt.json"), []byte(`{not valid json`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	opts := EnforceOpts{IsPIDAlive: func(pid int) bool { return true }}
	quarantined := EnforceOneIdentityWith(dir, opts, "new_agent")

	if len(quarantined) != 1 || quarantined[0] != "corrupt" {
		t.Errorf("quarantined = %v, want [corrupt] (unreadable files fall through)", quarantined)
	}
}

// TestEnforceOneIdentityWith_NilCallbackLegacyBehavior — zero-value
// opts (no IsPIDAlive) is equivalent to the legacy EnforceOneIdentity
// behavior. EnforceOneIdentity itself calls into EnforceOneIdentityWith
// with EnforceOpts{}, so this is also a regression guard against a
// future where the thin wrapper diverges.
func TestEnforceOneIdentityWith_NilCallbackLegacyBehavior(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// Even a file asserting a PID must be quarantined when the callback
	// is nil — callers that don't opt into liveness get legacy semantics.
	if err := os.WriteFile(filepath.Join(idDir, "with_pid.json"), []byte(`{"agent_pid":4242}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	quarantined := EnforceOneIdentityWith(dir, EnforceOpts{}, "new_agent")

	if len(quarantined) != 1 || quarantined[0] != "with_pid" {
		t.Errorf("quarantined = %v, want [with_pid] (nil callback = legacy semantics)", quarantined)
	}
}

// TestEnforceOneIdentityWith_CWDMismatchRefuses — thrum-182j static
// CWD-match invariant (Leon's design rule: "that script should never
// operate outside of its own work tree"). When CallerCwd resolves to
// a different git worktree than the target, EnforceOneIdentityWith
// refuses the whole call — no identity file is touched, no
// .quarantine/ dir is created. This closes the temporal blind spot
// in the liveness gate (old PID dead, new claude not yet written)
// through which a caller with an arbitrary target worktree could
// still quarantine a victim.
func TestEnforceOneIdentityWith_CWDMismatchRefuses(t *testing.T) {
	callerWT := initGitWorktree(t)
	targetWT := initGitWorktree(t)

	targetIDs := filepath.Join(targetWT, ".thrum", "identities")
	if err := os.MkdirAll(targetIDs, 0o750); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(targetIDs, "stale.json")
	if err := os.WriteFile(stale, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	quarantined := EnforceOneIdentityWith(targetWT, EnforceOpts{
		CallerCwd: callerWT,
	}, "new_agent")

	if len(quarantined) != 0 {
		t.Errorf("cross-worktree enforcement must refuse; got quarantined=%v", quarantined)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("stale.json must NOT be touched when CWD mismatch refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetIDs, ".quarantine")); err == nil {
		t.Errorf(".quarantine/ must NOT be created when CWD mismatch refused")
	}
}

// TestEnforceOneIdentityWith_CWDMatchProceeds — when CallerCwd and
// target resolve to the same git worktree root, enforcement proceeds
// normally. CallerCwd may be a SUBDIRECTORY within the worktree;
// `git rev-parse --show-toplevel` walks up to the worktree root for
// the comparison.
func TestEnforceOneIdentityWith_CWDMatchProceeds(t *testing.T) {
	wt := initGitWorktree(t)
	sub := filepath.Join(wt, "sub", "dir")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}

	idDir := filepath.Join(wt, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "stale.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	quarantined := EnforceOneIdentityWith(wt, EnforceOpts{
		CallerCwd: sub, // subdir of the same worktree — must resolve to same toplevel
	}, "new_agent")

	if len(quarantined) != 1 || quarantined[0] != "stale" {
		t.Errorf("quarantined = %v, want [stale]", quarantined)
	}
}

// TestEnforceOneIdentityWith_AllowCrossWorktreeBypassesCheck — the
// escape hatch for legitimate cross-worktree callers. HandleCreate
// in daemon/rpc/tmux.go legitimately operates on a target worktree
// that differs from the caller's own cwd; AllowCrossWorktree=true
// disables the gate while preserving keeper-list + liveness defenses.
func TestEnforceOneIdentityWith_AllowCrossWorktreeBypassesCheck(t *testing.T) {
	callerWT := initGitWorktree(t)
	targetWT := initGitWorktree(t)

	targetIDs := filepath.Join(targetWT, ".thrum", "identities")
	if err := os.MkdirAll(targetIDs, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetIDs, "stale.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	quarantined := EnforceOneIdentityWith(targetWT, EnforceOpts{
		CallerCwd:          callerWT, // different worktree
		AllowCrossWorktree: true,     // explicit bypass
	}, "new_agent")

	if len(quarantined) != 1 || quarantined[0] != "stale" {
		t.Errorf("quarantined = %v, want [stale] (AllowCrossWorktree=true must bypass gate)", quarantined)
	}
}

// TestEnforceOneIdentityWith_EmptyCallerCwdSkipsGate — legacy-friendly
// behavior: callers that never populate CallerCwd (the old
// EnforceOneIdentity(path, keep...) wrapper passes zero-value opts)
// are NOT subjected to the CWD-match gate. This preserves the
// existing API for tests and any future non-daemon callers.
func TestEnforceOneIdentityWith_EmptyCallerCwdSkipsGate(t *testing.T) {
	targetWT := initGitWorktree(t)
	targetIDs := filepath.Join(targetWT, ".thrum", "identities")
	if err := os.MkdirAll(targetIDs, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetIDs, "stale.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	quarantined := EnforceOneIdentityWith(targetWT, EnforceOpts{
		// CallerCwd deliberately empty.
	}, "new_agent")

	if len(quarantined) != 1 || quarantined[0] != "stale" {
		t.Errorf("empty CallerCwd must skip gate; want [stale], got %v", quarantined)
	}
}

// TestEnforceOneIdentityWith_NonGitCallerCwdRefuses — if CallerCwd is
// populated but does not resolve to a git worktree (git rev-parse
// fails), enforcement must refuse. Better to warn-and-noop than to
// proceed under an unverifiable assertion.
func TestEnforceOneIdentityWith_NonGitCallerCwdRefuses(t *testing.T) {
	targetWT := initGitWorktree(t)
	targetIDs := filepath.Join(targetWT, ".thrum", "identities")
	if err := os.MkdirAll(targetIDs, 0o750); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(targetIDs, "stale.json")
	if err := os.WriteFile(stale, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	// TempDir is not inside a git repo here.
	nonGit := t.TempDir()

	quarantined := EnforceOneIdentityWith(targetWT, EnforceOpts{
		CallerCwd: nonGit,
	}, "new_agent")

	if len(quarantined) != 0 {
		t.Errorf("non-git CallerCwd must refuse; got %v", quarantined)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("stale.json must NOT be touched when caller-cwd cannot be resolved: %v", err)
	}
}

// initGitWorktree creates a temp directory that is a real minimal git
// repository so `git rev-parse --show-toplevel` succeeds on it. Used
// by the CWD-match tests — they need real worktree roots to compare.
// Returns the canonicalized path (EvalSymlinks-resolved) so
// comparisons against the function-under-test's canonicalized output
// match on macOS where /tmp → /private/tmp.
func initGitWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// TestEnforceOneIdentity_QuarantineSkipped — the .quarantine/ dir
// itself must never be scanned as an identity file. Files already
// inside .quarantine/ stay there.
func TestEnforceOneIdentity_QuarantineSkipped(t *testing.T) {
	dir := t.TempDir()
	idDir := filepath.Join(dir, ".thrum", "identities")
	qDir := filepath.Join(idDir, ".quarantine")
	os.MkdirAll(qDir, 0o750)

	os.WriteFile(filepath.Join(idDir, "new_agent.json"), []byte(`{}`), 0600)
	os.WriteFile(filepath.Join(qDir, "ancient.json.20260101T000000Z"), []byte(`{}`), 0600)

	quarantined := EnforceOneIdentity(dir, "new_agent")
	if len(quarantined) != 0 {
		t.Errorf("quarantine dir contents must not be re-quarantined, got %v", quarantined)
	}
	if _, err := os.Stat(filepath.Join(qDir, "ancient.json.20260101T000000Z")); err != nil {
		t.Errorf("existing quarantine file must remain: %v", err)
	}
}

// --- BuildQuickstartCmd tests ---

func TestBuildQuickstartCmd_Basic(t *testing.T) {
	cmd := BuildQuickstartCmd("", "impl_api", "implementer", "api", "", "", false)
	expected := "thrum quickstart --name 'impl_api' --role 'implementer' --module 'api' --force"
	if cmd != expected {
		t.Errorf("got %q, want %q", cmd, expected)
	}
}

func TestBuildQuickstartCmd_WithIntent(t *testing.T) {
	cmd := BuildQuickstartCmd("", "impl_api", "implementer", "api", "Build the API endpoints", "claude", false)
	if !strings.Contains(cmd, "--intent 'Build the API endpoints'") {
		t.Errorf("missing intent: %s", cmd)
	}
	if !strings.Contains(cmd, "--runtime 'claude'") {
		t.Errorf("missing runtime: %s", cmd)
	}
}

func TestBuildQuickstartCmd_QuotesSpecialChars(t *testing.T) {
	cmd := BuildQuickstartCmd("", "impl_api", "implementer", "api", "Build API; handle auth", "", false)
	if !strings.Contains(cmd, "'Build API; handle auth'") {
		t.Errorf("intent not safely quoted: %s", cmd)
	}
}

func TestBuildQuickstartCmd_EscapesSingleQuoteInIntent(t *testing.T) {
	cmd := BuildQuickstartCmd("", "impl_api", "implementer", "api", "Build API's auth", "", false)
	if strings.Contains(cmd, "'Build API's auth'") {
		t.Errorf("unescaped single quote would break shell: %s", cmd)
	}
	if !strings.Contains(cmd, `'Build API'\''s auth'`) {
		t.Errorf("expected escaped single quote: %s", cmd)
	}
}

func TestBuildQuickstartCmd_WithNoAgentPID_AppendsFlag(t *testing.T) {
	cmd := BuildQuickstartCmd("", "impl_api", "implementer", "api", "", "", true)
	if !strings.Contains(cmd, "--no-agent-pid") {
		t.Errorf("expected --no-agent-pid flag when noAgentPID=true, got: %s", cmd)
	}
}

func TestBuildQuickstartCmd_WithoutNoAgentPID_OmitsFlag(t *testing.T) {
	cmd := BuildQuickstartCmd("", "impl_api", "implementer", "api", "", "", false)
	if strings.Contains(cmd, "--no-agent-pid") {
		t.Errorf("did not expect --no-agent-pid flag when noAgentPID=false, got: %s", cmd)
	}
}

// thrum-tc4w: when repoPath is non-empty, --repo <path> is prepended BEFORE
// the quickstart subcommand so EffectiveRepoPath in PersistentPreRunE
// (cobra root) doesn't substitute THRUM_HOME for the inline-quickstart
// path. Without --repo, daemon-spawned tmux panes that inherit THRUM_HOME
// from the daemon would write the new agent's identity into THRUM_HOME's
// .thrum/identities/ instead of the calling worktree.
func TestBuildQuickstartCmd_WithRepoPath_PrependsRepoFlag(t *testing.T) {
	cmd := BuildQuickstartCmd("/path/to/worktree", "impl_api", "implementer", "api", "", "", false)
	if !strings.HasPrefix(cmd, "thrum --repo '/path/to/worktree' quickstart ") {
		t.Errorf("expected --repo before quickstart, got: %s", cmd)
	}
}

func TestBuildQuickstartCmd_RepoPathQuotesSpecialChars(t *testing.T) {
	cmd := BuildQuickstartCmd("/path with spaces/worktree", "impl_api", "implementer", "api", "", "", false)
	if !strings.Contains(cmd, "--repo '/path with spaces/worktree'") {
		t.Errorf("repo path not shell-quoted: %s", cmd)
	}
}
