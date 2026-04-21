package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// writeSnapshotTestIdentity writes a minimal identity file that the
// snapshot save command can load without ambiguity. Load-bearing fields:
//   - agent.name: used to select the identity file by THRUM_NAME
//   - worktree: used by the mtime fallback to derive the project-dir slug
//   - agent_pid: used by restart.FindSessionJSONL; pass 0 to force the
//     no-pid path with agent.list also returning no-match, or a high
//     guaranteed-dead PID (99999999) to force the no-jsonl path.
//   - runtime: must equal "claude" since snapshot save today only works
//     for the Claude runtime.
//
// repoDir is the agent's worktree root (where .thrum/identities/ is
// written). Name is the agent name the subprocess will resolve via
// THRUM_NAME. The identity file lands at
// <repoDir>/.thrum/identities/<name>.json.
func writeSnapshotTestIdentity(t *testing.T, repoDir, name string, pid int) {
	t.Helper()
	identitiesDir := filepath.Join(repoDir, ".thrum", "identities")
	if err := os.MkdirAll(identitiesDir, 0o750); err != nil {
		t.Fatalf("mkdir identities: %v", err)
	}
	identity := map[string]any{
		"version":   5,
		"repo_id":   "test-repo",
		"agent":     map[string]any{"name": name, "role": "impl", "module": "testmod"},
		"worktree":  repoDir,
		"agent_pid": pid,
		"runtime":   "claude",
	}
	b, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(identitiesDir, name+".json"), b, 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}
}

// TestIntegration_SnapshotSaveNoJSONL_TextMode verifies the
// snapshot.save.no-jsonl hint lands on stderr when `thrum tmux snapshot save`
// cannot resolve a Claude JSONL for the recorded agent PID.
//
// Fixture: minimal .thrum/identities/<name>.json with agent_pid pointed at a
// PID that does not have a matching ~/.claude/sessions/<pid>.json (we use a
// fresh HOME tmpdir so the file cannot exist regardless of host state).
// The PID value itself is arbitrary — 99999999 is chosen to be outside
// normal PID ranges to reduce flake on systems that assigned it to a real
// process. Either way the sessions/<pid>.json lookup fails in the fresh HOME.
func TestIntegration_SnapshotSaveNoJSONL_TextMode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)

	repoDir := t.TempDir()
	fakeHome := t.TempDir()
	writeSnapshotTestIdentity(t, repoDir, "testagent", 99999999)

	cmd := exec.Command(bin, "--repo", repoDir, "tmux", "snapshot", "save")
	cmd.Env = append(filterEnvVars(os.Environ(), "THRUM_HOME", "HOME", "THRUM_NAME"),
		"HOME="+fakeHome,
		"THRUM_NAME=testagent",
	)
	_, errBuf := captureOut(cmd)
	runErr := cmd.Run()
	if runErr == nil {
		t.Fatal("expected non-zero exit when no JSONL is resolvable")
	}

	stderr := errBuf.String()
	if !strings.Contains(stderr, "[snapshot.save.no-jsonl]") {
		t.Errorf("stderr missing hint code [snapshot.save.no-jsonl], got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "warn") {
		t.Errorf("stderr missing 'warn' severity label, got:\n%s", stderr)
	}
	// Remediation must include a copy/paste-ready ls command so the operator
	// can verify which of (a) sessions/<pid>.json, (b) projects/ dir is missing.
	if !strings.Contains(stderr, "99999999.json") {
		t.Errorf("stderr should reference sessions/<pid>.json path, got:\n%s", stderr)
	}
}

// TestIntegration_SnapshotSaveNoJSONL_ThrumNoHintsSuppresses verifies the env
// kill-switch suppresses the hint trailer even when the failure condition
// fires. The underlying error still returns non-zero — suppression is for
// the hint text only.
func TestIntegration_SnapshotSaveNoJSONL_ThrumNoHintsSuppresses(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)

	repoDir := t.TempDir()
	fakeHome := t.TempDir()
	writeSnapshotTestIdentity(t, repoDir, "testagent", 99999999)

	cmd := exec.Command(bin, "--repo", repoDir, "tmux", "snapshot", "save")
	cmd.Env = append(filterEnvVars(os.Environ(), "THRUM_HOME", "HOME", "THRUM_NAME", "THRUM_NO_HINTS"),
		"HOME="+fakeHome,
		"THRUM_NAME=testagent",
		"THRUM_NO_HINTS=1",
	)
	_, errBuf := captureOut(cmd)
	_ = cmd.Run()

	stderr := errBuf.String()
	if strings.Contains(stderr, "[snapshot.save.no-jsonl]") {
		t.Errorf("THRUM_NO_HINTS=1 must suppress hint code, got stderr:\n%s", stderr)
	}
}

// filterEnvVars returns env minus any entries whose key matches one of remove.
// Used to drop inherited test-parent THRUM_HOME / HOME etc. so the subprocess
// sees only the explicit overrides set by the test.
func filterEnvVars(env []string, remove ...string) []string {
	skip := make(map[string]bool, len(remove))
	for _, k := range remove {
		skip[k] = true
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			out = append(out, kv)
			continue
		}
		if skip[key] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// TestIntegration_SnapshotSaveMtimeFallback_UsesProjectDir verifies the
// layer-2 fallback kicks in when PID-based lookup fails (no sessions/<pid>.json)
// but the per-worktree project directory holds at least one .jsonl. The save
// should complete successfully using the newest-mtime JSONL — no hint emits,
// and the extracted conversation lands in the snapshot.
func TestIntegration_SnapshotSaveMtimeFallback_UsesProjectDir(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)

	repoDir := t.TempDir()
	fakeHome := t.TempDir()
	// agent_pid deliberately unresolvable — forces the fallback path.
	writeSnapshotTestIdentity(t, repoDir, "testagent", 99999999)

	// Mirror encodeCwd from internal/restart: strip leading '/', replace
	// '/' and '.' with '-', prepend '-'. repoDir from t.TempDir() has no
	// dots so we only need the slash substitution.
	slug := "-" + strings.ReplaceAll(strings.TrimPrefix(repoDir, "/"), "/", "-")
	slug = strings.ReplaceAll(slug, ".", "-")
	projectDir := filepath.Join(fakeHome, ".claude", "projects", slug)
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	// Write two JSONL files with different mtimes; save must pick the newer.
	olderPath := filepath.Join(projectDir, "older.jsonl")
	newerPath := filepath.Join(projectDir, "newer.jsonl")
	olderLine := `{"type":"user","isSidechain":false,"message":{"role":"user","content":[{"type":"text","text":"OLDER content"}]}}` + "\n"
	newerLine := `{"type":"user","isSidechain":false,"message":{"role":"user","content":[{"type":"text","text":"NEWER content"}]}}` + "\n"
	if err := os.WriteFile(olderPath, []byte(olderLine), 0o600); err != nil {
		t.Fatalf("write older: %v", err)
	}
	if err := os.WriteFile(newerPath, []byte(newerLine), 0o600); err != nil {
		t.Fatalf("write newer: %v", err)
	}
	// Force older to actually be older in case of 1s filesystem resolution.
	past := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(olderPath, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	cmd := exec.Command(bin, "--repo", repoDir, "tmux", "snapshot", "save")
	cmd.Env = append(filterEnvVars(os.Environ(), "THRUM_HOME", "HOME", "THRUM_NAME"),
		"HOME="+fakeHome,
		"THRUM_NAME=testagent",
	)
	outBuf, errBuf := captureOut(cmd)
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected success via mtime fallback; err=%v\nstdout:%s\nstderr:%s", err, outBuf.String(), errBuf.String())
	}

	snapshotPath := filepath.Join(repoDir, ".thrum", "restart", "testagent.md")
	data, err := os.ReadFile(snapshotPath) // #nosec G304 -- test-controlled path
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	if !strings.Contains(string(data), "NEWER content") {
		t.Errorf("snapshot should contain newer JSONL content; got:\n%s", string(data))
	}
	if strings.Contains(string(data), "OLDER content") {
		t.Errorf("snapshot should NOT contain older JSONL content (mtime picked wrong file); got:\n%s", string(data))
	}
	if strings.Contains(errBuf.String(), "[snapshot.save.no-jsonl]") {
		t.Errorf("fallback success path must not emit no-jsonl hint; stderr:\n%s", errBuf.String())
	}
}

// TestIntegration_SnapshotSaveExplicitJSONL_BypassesAutoDetect verifies that
// `--jsonl <path>` short-circuits both PID-based lookup and the mtime
// fallback — letting the caller directly supply the conversation file even
// when auto-detect can't find it. The file must still be a real readable
// file (ExtractConversation will fail otherwise), so we write a minimal
// JSONL with a single user message that extracts to a non-empty snapshot.
func TestIntegration_SnapshotSaveExplicitJSONL_BypassesAutoDetect(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)

	repoDir := t.TempDir()
	fakeHome := t.TempDir()
	// PID stays unresolvable — proving the flag bypasses auto-detect, not
	// that auto-detect happened to work.
	writeSnapshotTestIdentity(t, repoDir, "testagent", 99999999)

	// Craft a minimal JSONL. ExtractConversation filters to user+assistant
	// message rows with text content; we supply one user row.
	jsonlPath := filepath.Join(t.TempDir(), "override-session.jsonl")
	line := `{"type":"user","isSidechain":false,"message":{"role":"user","content":[{"type":"text","text":"hello from explicit JSONL"}]}}` + "\n"
	if err := os.WriteFile(jsonlPath, []byte(line), 0o600); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	cmd := exec.Command(bin, "--repo", repoDir, "tmux", "snapshot", "save", "--jsonl", jsonlPath)
	cmd.Env = append(filterEnvVars(os.Environ(), "THRUM_HOME", "HOME", "THRUM_NAME"),
		"HOME="+fakeHome,
		"THRUM_NAME=testagent",
	)
	outBuf, errBuf := captureOut(cmd)
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected success with --jsonl override; err=%v\nstdout:%s\nstderr:%s", err, outBuf.String(), errBuf.String())
	}

	// Success path prints the saved-line count; snapshot should land under
	// repoDir/.thrum/restart/testagent.md.
	snapshotPath := filepath.Join(repoDir, ".thrum", "restart", "testagent.md")
	data, err := os.ReadFile(snapshotPath) // #nosec G304 -- test-controlled path
	if err != nil {
		t.Fatalf("snapshot file not written at %s: %v", snapshotPath, err)
	}
	if !strings.Contains(string(data), "hello from explicit JSONL") {
		t.Errorf("snapshot missing extracted content; got:\n%s", string(data))
	}
	// Stderr must NOT carry the no-jsonl hint when the override succeeded.
	if strings.Contains(errBuf.String(), "[snapshot.save.no-jsonl]") {
		t.Errorf("--jsonl success path must not emit no-jsonl hint; stderr:\n%s", errBuf.String())
	}
}


// TestIntegration_SnapshotSaveNoPID_TextMode verifies the snapshot.save.no-pid
// hint wires end-to-end when the identity file has agent_pid=0 AND the daemon
// lookup returns no-match. The daemon lookup branch in saveCmd gracefully
// tolerates a dial failure (if err != nil { ... }), so the test simply
// leaves no daemon running — the loop over `agents` finds nothing and the
// pid==0 refusal fires with the hint attached.
func TestIntegration_SnapshotSaveNoPID_TextMode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)

	repoDir := t.TempDir()
	fakeHome := t.TempDir()
	// agent_pid=0 triggers the fallback path; with no daemon the lookup
	// returns empty and the pid==0 error returns with the no-pid hint.
	writeSnapshotTestIdentity(t, repoDir, "testagent", 0)

	cmd := exec.Command(bin, "--repo", repoDir, "tmux", "snapshot", "save")
	cmd.Env = append(filterEnvVars(os.Environ(), "THRUM_HOME", "HOME", "THRUM_NAME"),
		"HOME="+fakeHome,
		"THRUM_NAME=testagent",
	)
	_, errBuf := captureOut(cmd)
	if err := cmd.Run(); err == nil {
		t.Fatal("expected non-zero exit when no PID is resolvable")
	}

	stderr := errBuf.String()
	if !strings.Contains(stderr, "[snapshot.save.no-pid]") {
		t.Errorf("stderr missing hint code [snapshot.save.no-pid], got:\n%s", stderr)
	}
	// Remediation must reference the specific agent name so the re-register
	// command is copy/paste-ready.
	if !strings.Contains(stderr, "testagent") {
		t.Errorf("stderr should name the agent in the remediation command; got:\n%s", stderr)
	}
}

// TestIntegration_SnapshotSaveExtractFailed_TextMode verifies the
// snapshot.save.extract-failed hint fires when the supplied JSONL is
// present (passes os.Stat pre-flight) but ExtractConversation's
// os.Open returns an error.
//
// Note on trigger choice: ExtractConversation uses bufio.Scanner with a
// "skip unmarshal errors" policy, so feeding corrupt content would just
// produce an empty snapshot without error. The reliable portable trigger
// for an Open error when Stat succeeds is a chmod-000 file — Stat reads
// the dir entry (no read perm on file needed), Open fails EACCES.
// Skipped on Windows (chmod semantics differ).
func TestIntegration_SnapshotSaveExtractFailed_TextMode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	if os.Geteuid() == 0 {
		t.Skip("chmod-000 is bypassed by root; test requires non-root uid")
	}
	bin := buildTestBinary(t)

	repoDir := t.TempDir()
	fakeHome := t.TempDir()
	writeSnapshotTestIdentity(t, repoDir, "testagent", 99999999)

	unreadablePath := filepath.Join(t.TempDir(), "unreadable.jsonl")
	if err := os.WriteFile(unreadablePath, []byte(`{"type":"user"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	if err := os.Chmod(unreadablePath, 0o000); err != nil {
		t.Fatalf("chmod 000: %v", err)
	}
	// Restore perms after test so t.TempDir cleanup can delete the file.
	t.Cleanup(func() { _ = os.Chmod(unreadablePath, 0o600) })

	cmd := exec.Command(bin, "--repo", repoDir, "tmux", "snapshot", "save", "--jsonl", unreadablePath)
	cmd.Env = append(filterEnvVars(os.Environ(), "THRUM_HOME", "HOME", "THRUM_NAME"),
		"HOME="+fakeHome,
		"THRUM_NAME=testagent",
	)
	_, errBuf := captureOut(cmd)
	if err := cmd.Run(); err == nil {
		t.Fatal("expected non-zero exit when JSONL can't be opened")
	}

	stderr := errBuf.String()
	if !strings.Contains(stderr, "[snapshot.save.extract-failed]") {
		t.Errorf("stderr missing hint code [snapshot.save.extract-failed], got:\n%s", stderr)
	}
	// The hint should name the path for copy/paste remediation (inspect/ls).
	if !strings.Contains(stderr, unreadablePath) {
		t.Errorf("stderr should reference the JSONL path, got:\n%s", stderr)
	}
}

// TestIntegration_SnapshotSaveJSONLNotFound_TextMode verifies the pre-flight
// os.Stat on --jsonl detects typo'd paths and fires the distinct
// snapshot.save.jsonl-not-found hint — distinguishing "path doesn't exist"
// from "path exists but can't be read/parsed" (the extract-failed case).
func TestIntegration_SnapshotSaveJSONLNotFound_TextMode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)

	repoDir := t.TempDir()
	fakeHome := t.TempDir()
	writeSnapshotTestIdentity(t, repoDir, "testagent", 99999999)

	bogusPath := filepath.Join(t.TempDir(), "does-not-exist.jsonl")
	// Explicitly do NOT create bogusPath — os.Stat must report ENOENT.

	cmd := exec.Command(bin, "--repo", repoDir, "tmux", "snapshot", "save", "--jsonl", bogusPath)
	cmd.Env = append(filterEnvVars(os.Environ(), "THRUM_HOME", "HOME", "THRUM_NAME"),
		"HOME="+fakeHome,
		"THRUM_NAME=testagent",
	)
	_, errBuf := captureOut(cmd)
	if err := cmd.Run(); err == nil {
		t.Fatal("expected non-zero exit when --jsonl path is missing")
	}

	stderr := errBuf.String()
	if !strings.Contains(stderr, "[snapshot.save.jsonl-not-found]") {
		t.Errorf("stderr missing hint code [snapshot.save.jsonl-not-found], got:\n%s", stderr)
	}
	// Crucial: the extract-failed hint must NOT also fire — that would
	// re-introduce the confusion the new code exists to avoid.
	if strings.Contains(stderr, "[snapshot.save.extract-failed]") {
		t.Errorf("jsonl-not-found path must not ALSO emit extract-failed hint; stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, bogusPath) {
		t.Errorf("stderr should echo the bad path for copy/paste fix; got:\n%s", stderr)
	}
}

// TestIntegration_SnapshotSaveNoJSONL_WorktreeMissing_TextMode verifies the
// WorktreeMissing context variant: when the identity file lacks a worktree
// field, the mtime fallback can't run. The hint must render the
// "missing the 'worktree' field" message so operators know where to look
// instead of hunting for sessions/<pid>.json or project dirs.
func TestIntegration_SnapshotSaveNoJSONL_WorktreeMissing_TextMode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)

	repoDir := t.TempDir()
	fakeHome := t.TempDir()

	// Hand-craft an identity that deliberately omits 'worktree' to trigger
	// the WorktreeMissing branch. writeSnapshotTestIdentity always populates
	// worktree, so this test bypasses the helper.
	identitiesDir := filepath.Join(repoDir, ".thrum", "identities")
	if err := os.MkdirAll(identitiesDir, 0o750); err != nil {
		t.Fatalf("mkdir identities: %v", err)
	}
	identity := map[string]any{
		"version":   5,
		"repo_id":   "test-repo",
		"agent":     map[string]any{"name": "testagent", "role": "impl", "module": "testmod"},
		"agent_pid": 99999999,
		"runtime":   "claude",
		// worktree field intentionally OMITTED
	}
	b, _ := json.MarshalIndent(identity, "", "  ")
	if err := os.WriteFile(filepath.Join(identitiesDir, "testagent.json"), b, 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}

	cmd := exec.Command(bin, "--repo", repoDir, "tmux", "snapshot", "save")
	cmd.Env = append(filterEnvVars(os.Environ(), "THRUM_HOME", "HOME", "THRUM_NAME"),
		"HOME="+fakeHome,
		"THRUM_NAME=testagent",
	)
	_, errBuf := captureOut(cmd)
	if err := cmd.Run(); err == nil {
		t.Fatal("expected non-zero exit when worktree is missing and auto-detect fails")
	}

	stderr := errBuf.String()
	if !strings.Contains(stderr, "[snapshot.save.no-jsonl]") {
		t.Errorf("stderr missing hint code [snapshot.save.no-jsonl], got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "missing the 'worktree' field") {
		t.Errorf("stderr should explain worktree field is missing (context-specific message); got:\n%s", stderr)
	}
}
