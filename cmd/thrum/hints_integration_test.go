package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// L4 integration test coverage for the 8-code pilot catalog.
//
// Catalog coverage matrix:
//
//   tmux.create.not-a-worktree       — COVERED (4 cases: text/json/env/quiet)
//   tmux.create.session-exists       — CARVE-OUT: requires a pre-existing
//                                       tmux session in a fresh temp worktree.
//                                       Host fixture fragility + daemon
//                                       coupling (thrum tmux create calls
//                                       getClient() before reaching the
//                                       hint). L1 covers it deterministically
//                                       via MockState.
//   tmux.create.identity-exists-alive — CARVE-OUT: requires a live registered
//                                        agent on a fresh worktree fixture.
//                                        Tight daemon + tmux + identity-file
//                                        coupling. L1 covers.
//   tmux.create.identity-exists-stale — CARVE-OUT: same as above, minus live
//                                        session. FS fixture is buildable
//                                        but the success path still runs the
//                                        RPC. L1 covers.
//   tmux.create.next-launch          — CARVE-OUT: requires tmux.create to
//                                       succeed end-to-end, which needs a
//                                       real worktree + running daemon + live
//                                       tmux backend. L1 covers (post=true,
//                                       Result marker shape assertion).
//   tmux.create.identity-replaced    — CARVE-OUT: requires --force on a
//                                       pre-existing stale-identity worktree.
//                                       Depends on next-launch success path.
//                                       L1 covers via Result marker.
//   send.recipient-stale             — CARVE-OUT (documented in plan Task
//                                       13.2): requires daemon fixture with
//                                       an agent whose last_seen is backdated.
//                                       Tight RPC coupling. L1 covers all
//                                       threshold variants + bare-name +
//                                       at-prefix parsing.
//   init.next-quickstart             — COVERED below (init uses
//                                       FSOnlyStateAccessor; daemon-free).
//
// The carved-out cases are structurally daemon-or-fixture coupled. The L1
// tier exercises each with MockState so firing semantics are verified
// deterministically. The L4 tier here validates the rendering pipeline
// end-to-end (Shape B stderr, Shape C JSON, suppression) using the codes
// that CAN be driven without a daemon-shaped fixture.

// TestIntegration_NotAWorktree_TextMode verifies the canonical
// tmux.create.not-a-worktree hint lands on stderr and the command exits
// non-zero when --cwd is not a git worktree.
func TestIntegration_NotAWorktree_TextMode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)
	cmd := exec.Command(bin, "tmux", "create", "foo", "--cwd", "/tmp")
	_, errBuf := captureOut(cmd)
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit, got success")
	}
	stderr := errBuf.String()
	if !strings.Contains(stderr, "[tmux.create.not-a-worktree]") {
		t.Errorf("stderr missing hint code [tmux.create.not-a-worktree], got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "warn") {
		t.Errorf("stderr missing 'warn' severity label, got:\n%s", stderr)
	}
}

// TestIntegration_NotAWorktree_ThrumNoHintsSuppresses verifies the
// env-var kill-switch suppresses the hint code from stderr even when the
// underlying condition fires. Abort still happens but quietly (no hint).
func TestIntegration_NotAWorktree_ThrumNoHintsSuppresses(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)
	cmd := exec.Command(bin, "tmux", "create", "foo", "--cwd", "/tmp")
	cmd.Env = append(cmd.Environ(), "THRUM_NO_HINTS=1")
	_, errBuf := captureOut(cmd)
	_ = cmd.Run()
	stderr := errBuf.String()
	if strings.Contains(stderr, "[tmux.create.not-a-worktree]") {
		t.Errorf("THRUM_NO_HINTS=1 must suppress hint code, got stderr:\n%s", stderr)
	}
}

// TestIntegration_NotAWorktree_JSONMode verifies --json produces a parseable
// body with hints[] populated, and that THRUM_NO_HINTS suppresses hints
// from the JSON body too.
func TestIntegration_NotAWorktree_JSONMode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)

	// Positive: hints present.
	cmd := exec.Command(bin, "--json", "tmux", "create", "foo", "--cwd", "/tmp")
	outBuf, _ := captureOut(cmd)
	_ = cmd.Run()
	var body map[string]any
	if err := json.Unmarshal(outBuf.Bytes(), &body); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, outBuf.String())
	}
	hints, ok := body["hints"].([]any)
	if !ok {
		t.Fatalf("hints field missing or wrong type: %+v", body["hints"])
	}
	if len(hints) == 0 {
		t.Fatal("hints array empty")
	}
	first, _ := hints[0].(map[string]any)
	if first["code"] != "tmux.create.not-a-worktree" {
		t.Errorf("first hint code = %v, want tmux.create.not-a-worktree", first["code"])
	}
	if first["severity"] != "warn" {
		t.Errorf("first hint severity = %v, want warn", first["severity"])
	}

	// Suppression: hints should NOT appear with THRUM_NO_HINTS=1.
	cmd2 := exec.Command(bin, "--json", "tmux", "create", "foo", "--cwd", "/tmp")
	cmd2.Env = append(cmd2.Environ(), "THRUM_NO_HINTS=1")
	outBuf2, _ := captureOut(cmd2)
	_ = cmd2.Run()
	var body2 map[string]any
	if err := json.Unmarshal(outBuf2.Bytes(), &body2); err != nil {
		t.Fatalf("suppressed stdout not JSON: %v\n%s", err, outBuf2.String())
	}
	if _, present := body2["hints"]; present {
		t.Errorf("THRUM_NO_HINTS=1 must omit 'hints' key from JSON body, got: %+v", body2)
	}
}

// TestIntegration_NotAWorktree_QuietSuppressesText verifies --quiet
// suppresses the stderr hint trailer. The command still aborts (returns
// non-zero) — the suppression applies only to the hint text itself.
func TestIntegration_NotAWorktree_QuietSuppressesText(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)
	cmd := exec.Command(bin, "--quiet", "tmux", "create", "foo", "--cwd", "/tmp")
	_, errBuf := captureOut(cmd)
	_ = cmd.Run()
	stderr := errBuf.String()
	if strings.Contains(stderr, "[tmux.create.not-a-worktree]") {
		t.Errorf("--quiet must suppress hint text, got stderr:\n%s", stderr)
	}
}

// TestIntegration_InitNextQuickstart_TextMode verifies init.next-quickstart
// fires on stderr after a successful `thrum init` in a fresh temp git repo
// where no agent identity is registered yet.
//
// Daemon-free: thrum init uses FSOnlyStateAccessor, which doesn't touch the
// daemon for identity-status checks. We still avoid --runtime selection by
// passing --runtime cli-only which writes no config.
func TestIntegration_InitNextQuickstart_TextMode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)

	// Set up a throwaway git repo (isolated from any real .thrum state via
	// THRUM_HOME pointing at a temp dir).
	repoDir := t.TempDir()
	thrumHome := t.TempDir()
	if err := runInDir(t, repoDir, "git", "init", "-q"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := runInDir(t, repoDir, "git", "commit", "--allow-empty", "-m", "init", "--quiet"); err != nil {
		// commit may need config user.* — tolerate; most CI envs have it
		t.Logf("git commit failed (tolerating): %v", err)
	}

	cmd := exec.Command(bin, "--repo", repoDir, "init", "--runtime", "cli-only", "--force")
	cmd.Env = append(cmd.Environ(), "THRUM_HOME="+thrumHome)
	_, errBuf := captureOut(cmd)
	_ = cmd.Run()

	stderr := errBuf.String()
	if !strings.Contains(stderr, "[init.next-quickstart]") {
		t.Errorf("stderr missing hint code [init.next-quickstart], got:\n%s", stderr)
	}
	// Verify the stderr trailer points at quickstart.
	if !strings.Contains(stderr, "thrum quickstart") {
		t.Errorf("stderr missing 'thrum quickstart' suggestion, got:\n%s", stderr)
	}
}

// runInDir runs a command in a specific working directory. Test helper
// for fixture setup (git init etc).
func runInDir(t *testing.T, dir, name string, args ...string) error {
	t.Helper()
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Env = os.Environ()
	c.Stdout = nil
	c.Stderr = nil
	return c.Run()
}

