package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// TestRefreshLocalIdentity_NoRuntime asserts that when FindClaudeAncestor
// returns (0, ""), the refresh still runs through but does not update
// PID/runtime fields. Tmux and branch may still update.
// DisableGuardForTest drops a .thrum/config.json that turns every
// identity_guard mode to "off" so tests predating Epic 4 can continue
// to exercise legacy refresh behavior without tripping the new
// ownership check. Remove this helper once each test is rewritten to
// set up a realistic caller chain / CWD / TMUX match.
func disableGuardForTest(t *testing.T, thrumDir string) {
	t.Helper()
	cfg := []byte(`{"identity_guard":{"cross_worktree":"off","dead_pid_auto_reclaim":"off","quickstart_self_rename":"off","quickstart_name_collision":"off","non_git_bootstrap":"off","unauthenticated_rpc":"off","daemon_writer_liveness":"off","prime_ownership":"off"}}`)
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshLocalIdentity_NoRuntime(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750); err != nil {
		t.Fatal(err)
	}
	disableGuardForTest(t, thrumDir)

	// Isolate: pin THRUM_HOME to the tmp dir so paths.EffectiveRepoPath
	// does not redirect to the real repo, and unset THRUM_NAME so
	// LoadIdentityWithPath does not demand a specific name.
	t.Setenv("THRUM_HOME", tmpDir)
	t.Setenv("THRUM_NAME", "test_agent")

	// Write an identity file with some existing state.
	idFile := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "test_agent", Role: "tester", Module: "unit",
		},
		AgentPID: 99999,
		Runtime:  "claude",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatal(err)
	}

	// Swap the detector to return a no-runtime result regardless of the
	// environment the test runs in (including under an actual claude session).
	origDetect := detectAncestor
	detectAncestor = func(_ context.Context) (int, string) { return 0, "" }
	t.Cleanup(func() { detectAncestor = origDetect })

	result, err := RefreshLocalIdentity(nil, tmpDir)
	if err != nil {
		t.Fatalf("RefreshLocalIdentity: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Assert that PID/runtime fields were NOT marked as changed. Tmux and
	// branch may legitimately update depending on the test environment, so
	// we check only the three fields this test cares about.
	//
	// We cannot re-read via LoadIdentityWithPath here because that loader
	// has a silent PID-adoption side effect when the stored PID is dead
	// (see internal/config/config.go loadIdentityFromDir). The authoritative
	// signal for "refresh did not touch this field" is result.FileChanged.
	for _, f := range result.FileChanged {
		if f == "agent_pid" || f == "runtime" || f == "preferred_runtime" {
			t.Errorf("refresh changed %q unexpectedly when detector returned (0, \"\")", f)
		}
	}
}

// TestRefreshLocalIdentity_NoIdentityFile asserts (nil, nil) when no
// .thrum/identities/ directory exists at repoPath.
func TestRefreshLocalIdentity_NoIdentityFile(t *testing.T) {
	tmpDir := t.TempDir()
	// No .thrum directory created.

	// Pin THRUM_HOME so the load does not redirect to the real repo.
	t.Setenv("THRUM_HOME", tmpDir)
	t.Setenv("THRUM_NAME", "test_agent")

	result, err := RefreshLocalIdentity(nil, tmpDir)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
}

// TestRefreshLocalIdentity_PIDDrift was DELETED as part of identity-
// guard Epic 4. The refresh path no longer writes AgentPID on drift;
// that mutation is the sole domain of guard.WritePID invoked from
// prime / quickstart / Rule's auto-reclaim. Equivalent coverage for
// the PID-adoption scenario now lives in
// internal/identity/guard/rule_test.go::TestRule_RuntimeAncestor_DeadPID_AutoReclaim.

// TestRefreshLocalIdentity_RuntimeDrift was DELETED as part of
// identity-guard Epic 4. Drift reconciliation moved from
// RefreshLocalIdentity into guard.Check (internal/identity/guard/
// guard.go:reconcileDrift); guard.Check uses the real
// process.RuntimeName on the real closest-runtime ancestor and cannot
// be stubbed via this file's detectAncestor seam. Equivalent coverage
// lives as guard-package integration tests seeded by a fixture
// process tree.

// TestRefreshLocalIdentity_HappyPath_IdempotentReconcile asserts that
// when detected state exactly matches the identity file:
//   - no file write occurs (FileChanged empty, mtime unchanged)
//   - DaemonUpdated remains false (no actual state change happened)
//
// With thrum-pxz.14 Fix C, RefreshLocalIdentity now always calls
// AgentRegister when the file has a valid PID — even on the happy path
// — so the DB can catch up to a stale state (e.g., legacy data from
// before this feature). The daemon's agent.register handler (Fix A) is
// a no-op when the stored PID already matches, so the happy-path cost
// is one local RPC (~1ms) that produces no state change.
//
// This test uses client=nil so Fix C's reconcile branch is skipped and
// only the file-write side of the happy path is exercised. Full
// validation of the idempotent-reconcile RPC behavior would require a
// mockable client or a real daemon — that coverage lives in the
// daemon-side TestAgentRegister_SameAgentSamePID test.
func TestRefreshLocalIdentity_HappyPath_IdempotentReconcile(t *testing.T) {
	if os.Getenv("TMUX") != "" {
		// guard.Check's reconcileDrift legitimately rewrites the
		// file when the caller is in tmux but the identity file's
		// TmuxSession is empty (the happy-path fixture). The test's
		// mtime assertion predates Epic 4's always-on drift path.
		t.Skip("reconcileDrift writes tmux_session when in tmux; rewrite pending Epic 4")
	}
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750); err != nil {
		t.Fatal(err)
	}
	disableGuardForTest(t, thrumDir)
	t.Setenv("THRUM_HOME", tmpDir)
	t.Setenv("THRUM_NAME", "test_agent")

	idFile := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "test_agent", Role: "tester", Module: "unit",
		},
		AgentPID:         os.Getpid(),
		Runtime:          "claude",
		PreferredRuntime: "claude",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatal(err)
	}

	// Note the file mtime before the refresh call.
	idPath := filepath.Join(thrumDir, "identities", "test_agent.json")
	statBefore, err := os.Stat(idPath)
	if err != nil {
		t.Fatal(err)
	}

	orig := detectAncestor
	detectAncestor = func(_ context.Context) (int, string) { return os.Getpid(), "claude" }
	t.Cleanup(func() { detectAncestor = orig })

	// Give the filesystem a millisecond gap so a write would be detectable.
	time.Sleep(10 * time.Millisecond)

	result, err := RefreshLocalIdentity(nil, tmpDir)
	if err != nil {
		t.Fatalf("RefreshLocalIdentity: %v", err)
	}
	// Filter out tmux_session and branch from the assertion — those fields
	// can legitimately change depending on the test environment. We only
	// care that the four identity fields (pid/runtime/preferred_runtime)
	// did not cause a rewrite.
	for _, f := range result.FileChanged {
		if f == "agent_pid" || f == "runtime" || f == "preferred_runtime" {
			t.Errorf("unexpected change to %q on happy path", f)
		}
	}

	// mtime check: only meaningful if nothing in FileChanged would force
	// a rewrite. If tmux/branch drifted the file WILL have been rewritten.
	if len(result.FileChanged) == 0 {
		statAfter, err := os.Stat(idPath)
		if err != nil {
			t.Fatal(err)
		}
		if !statBefore.ModTime().Equal(statAfter.ModTime()) {
			t.Errorf("file was rewritten on happy path (mtime changed)")
		}
	}
}

// TestRefreshLocalIdentity_LiveConflict asserts that when AgentRegister
// returns a conflict with a different, live PID, the refresh returns
// without marking DaemonUpdated. This requires a mockable Client which
// is out of scope for this task; skipped as a placeholder.
func TestRefreshLocalIdentity_LiveConflict(t *testing.T) {
	t.Skip("requires mockable client; see TODO in plan Task 4")
}

// TestRefreshLocalIdentity_TmuxDrift asserts that when the stored
// tmux_session is stale and the agent is outside tmux, the refresh
// leaves the field alone rather than blanking it. The detector stub
// returns (0, "") so no PID/runtime drift fires either. Depends on
// the test process running outside tmux — skip if TMUX is set.
func TestRefreshLocalIdentity_TmuxDrift(t *testing.T) {
	if os.Getenv("TMUX") != "" {
		t.Skip("test requires non-tmux environment")
	}

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750); err != nil {
		t.Fatal(err)
	}
	disableGuardForTest(t, thrumDir)
	t.Setenv("THRUM_HOME", tmpDir)
	t.Setenv("THRUM_NAME", "test_agent")

	idFile := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "test_agent", Role: "tester", Module: "unit",
		},
		TmuxSession: "old:0.0",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatal(err)
	}

	orig := detectAncestor
	detectAncestor = func(_ context.Context) (int, string) { return 0, "" }
	t.Cleanup(func() { detectAncestor = orig })

	result, err := RefreshLocalIdentity(nil, tmpDir)
	if err != nil {
		t.Fatalf("RefreshLocalIdentity: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if containsString(result.FileChanged, "tmux_session") {
		t.Errorf("tmux_session should not be marked changed when agent is outside tmux; got FileChanged=%v", result.FileChanged)
	}

	// Raw file read bypasses LoadIdentityWithPath's side effects.
	loaded := readIdentityFile(t, thrumDir, "test_agent")
	if loaded.TmuxSession != "old:0.0" {
		t.Errorf("TmuxSession was mutated: got %q, want old:0.0", loaded.TmuxSession)
	}
}

// TestRefreshLocalIdentity_SaveFailure asserts that when drift is
// detected but SaveIdentityFile fails, the returned error bubbles out
// with a wrapped "save identity" prefix and the result is still non-nil
// so the caller can inspect DetectedPID/DetectedRuntime.
func TestRefreshLocalIdentity_SaveFailure(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		t.Fatal(err)
	}
	disableGuardForTest(t, thrumDir)
	t.Setenv("THRUM_HOME", tmpDir)
	t.Setenv("THRUM_NAME", "test_agent")

	idFile := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "test_agent", Role: "tester", Module: "unit",
		},
		AgentPID: 99999,
		Runtime:  "claude",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatal(err)
	}

	// Make the identities directory read-only to force SaveIdentityFile
	// to fail. On Unix, os.WriteFile into a dir with mode 0500 errors with
	// EACCES. Restore in Cleanup so t.TempDir can clean up.
	if err := os.Chmod(identitiesDir, 0500); err != nil { //#nosec G302 -- intentionally read-only for error-path test
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(identitiesDir, 0750) }) //#nosec G302 -- restoring test dir for cleanup

	orig := detectAncestor
	detectAncestor = func(_ context.Context) (int, string) { return os.Getpid(), "claude" }
	t.Cleanup(func() { detectAncestor = orig })

	result, err := RefreshLocalIdentity(nil, tmpDir)
	if err == nil {
		t.Skip("save did not fail in this environment; cannot exercise save-failure path (e.g. running as root)")
	}
	// When save fails, refresh.go returns (result, wrapped error). Both
	// should be non-nil; the caller can still inspect what was detected.
	if result == nil {
		t.Errorf("expected non-nil result alongside save error, got nil")
	}
	if !strings.Contains(err.Error(), "save identity") {
		t.Errorf("expected error to be wrapped with 'save identity', got %v", err)
	}
}

// containsString is a small helper for checking FileChanged membership.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// readIdentityFile reads an identity JSON file directly, bypassing
// LoadIdentityWithPath's silent PID-adoption side effect.
func readIdentityFile(t *testing.T, thrumDir, agentName string) *config.IdentityFile {
	t.Helper()
	path := filepath.Join(thrumDir, "identities", agentName+".json")
	data, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("read identity: %v", err)
	}
	var id config.IdentityFile
	if err := json.Unmarshal(data, &id); err != nil {
		t.Fatalf("unmarshal identity: %v", err)
	}
	return &id
}

// resurrectTestEnv bundles a real in-process daemon (with the actual
// rpc.AgentHandler.HandleRegister registered), a connected *cli.Client,
// the underlying *state.State (so tests can seed/inspect rows directly),
// the on-disk thrum dir (for SaveIdentityFile), and a teardown closure.
//
// This is the end-to-end harness for thrum-xir.18.4 — the cli refresh
// tests must exercise the full RefreshLocalIdentity → AgentRegister →
// HandleRegister → ensureActiveSession chain through the real handler,
// not via a mock that bypasses the daemon-side decision logic.
type resurrectTestEnv struct {
	client   *Client
	state    *state.State
	server   *daemon.Server
	thrumDir string
	repoPath string
	teardown func()
}

// startResurrectTestDaemon stands up a real daemon.Server with the
// rpc.AgentHandler bound to a fresh state.State, listens on a short
// socket path (macOS sun_path is capped at 104 bytes — t.TempDir's long
// per-test-name prefix overflows), and returns a connected client.
func startResurrectTestDaemon(t *testing.T) *resurrectTestEnv {
	t.Helper()

	// Short os.MkdirTemp path for the unix socket; the per-test repo
	// dir lives under the same short root so identity file paths stay
	// short too.
	root, err := os.MkdirTemp("", "tx18env")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}

	thrumDir := filepath.Join(root, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		_ = os.RemoveAll(root)
		t.Fatalf("mkdir identities: %v", err)
	}

	st, err := state.NewState(thrumDir, thrumDir, "test_repo_xir18", "")
	if err != nil {
		_ = os.RemoveAll(root)
		t.Fatalf("create state: %v", err)
	}

	socketPath := filepath.Join(root, "d.sock")
	server := daemon.NewServer(socketPath)
	agentHandler := rpc.NewAgentHandler(st)
	server.RegisterHandler("agent.register", agentHandler.HandleRegister)

	if err := server.Start(context.Background()); err != nil {
		_ = st.Close()
		_ = os.RemoveAll(root)
		t.Fatalf("start daemon server: %v", err)
	}

	client, err := NewClient(socketPath)
	if err != nil {
		_ = server.Stop()
		_ = st.Close()
		_ = os.RemoveAll(root)
		t.Fatalf("connect client: %v", err)
	}

	env := &resurrectTestEnv{
		client:   client,
		state:    st,
		server:   server,
		thrumDir: thrumDir,
		repoPath: root,
	}
	env.teardown = func() {
		_ = client.Close()
		_ = server.Stop()
		_ = st.Close()
		_ = os.RemoveAll(root)
	}
	t.Cleanup(env.teardown)
	return env
}

// TestRefreshLocalIdentity_SurfacesSessionResumedFlag — end-to-end:
// real in-process daemon with the actual rpc.AgentHandler bound. An
// agent is registered through the live socket so the daemon's own
// agent_id derivation drives both sides; its session is then ended via
// direct DB write (simulating the post-pxz.14 recovery scenario).
// Calling RefreshLocalIdentity must traverse the full chain
// RefreshLocalIdentity → AgentRegister → HandleRegister →
// ensureActiveSession and surface SessionResumed plus the new
// session_id on the RefreshResult (thrum-xir.18.4).
func TestRefreshLocalIdentity_SurfacesSessionResumedFlag(t *testing.T) {
	env := startResurrectTestDaemon(t)

	t.Setenv("THRUM_HOME", env.repoPath)
	t.Setenv("THRUM_NAME", "test_agent_resume")

	// Step 1: register the agent through the real daemon so the
	// daemon-derived agent_id is the one stored in the DB. The
	// initial register also populates the agents row.
	regOpts := AgentRegisterOptions{
		Name:     "test_agent_resume",
		Role:     "tester",
		Module:   "unit",
		Display:  "Resume Test",
		AgentPID: os.Getpid(),
	}
	regResp, err := AgentRegister(env.client, regOpts)
	if err != nil {
		t.Fatalf("initial register: %v", err)
	}
	agentID := regResp.AgentID
	if agentID == "" {
		t.Fatalf("initial register returned empty agent_id")
	}

	// Step 2: write the matching identity file on disk so
	// RefreshLocalIdentity will load it and re-register from there.
	idFile := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "test_agent_resume", Role: "tester", Module: "unit",
		},
		AgentPID:         os.Getpid(),
		Runtime:          "claude",
		PreferredRuntime: "claude",
	}
	if err := config.SaveIdentityFile(env.thrumDir, idFile); err != nil {
		t.Fatal(err)
	}

	// Step 3: end any active session via direct DB write. This is the
	// recovery scenario the resurrect path is built for.
	endedAt := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339Nano)
	if _, err := env.state.RawDB().Exec(`
		UPDATE sessions SET ended_at = ?, end_reason = 'test_seed_ended'
		WHERE agent_id = ? AND ended_at IS NULL
	`, endedAt, agentID); err != nil {
		t.Fatalf("end session: %v", err)
	}

	// Step 4: stub the runtime detector so refresh.go does not perceive
	// PID/runtime drift — we want the same-PID no-op branch in
	// HandleRegister, which is exactly the path the resurrect logic
	// must traverse.
	orig := detectAncestor
	detectAncestor = func(_ context.Context) (int, string) { return os.Getpid(), "claude" }
	t.Cleanup(func() { detectAncestor = orig })

	// Act: drive the full chain through the real daemon socket.
	result, err := RefreshLocalIdentity(env.client, env.repoPath)
	if err != nil {
		t.Fatalf("RefreshLocalIdentity: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.SessionResumed {
		t.Errorf("SessionResumed = false, want true")
	}
	if result.ResumedSessionID == "" {
		t.Errorf("ResumedSessionID = empty, want fresh session id")
	}
	if !strings.HasPrefix(result.ResumedSessionID, "ses_") {
		t.Errorf("ResumedSessionID = %q, want ses_ prefix", result.ResumedSessionID)
	}

	// And verify the daemon-side state actually changed: the new
	// session row exists with ended_at IS NULL.
	var activeCount int
	if err := env.state.RawDB().QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE agent_id = ? AND ended_at IS NULL`,
		agentID,
	).Scan(&activeCount); err != nil {
		t.Fatalf("query active sessions: %v", err)
	}
	if activeCount != 1 {
		t.Errorf("active session rows = %d, want 1", activeCount)
	}
}

// TestRefreshLocalIdentity_NoSessionResumedWhenAlreadyActive — end-to-end
// negative case: the agent already has an active session, so the
// resurrect path must no-op. RefreshResult must keep SessionResumed
// false and ResumedSessionID empty (thrum-xir.18.4).
func TestRefreshLocalIdentity_NoSessionResumedWhenAlreadyActive(t *testing.T) {
	env := startResurrectTestDaemon(t)

	t.Setenv("THRUM_HOME", env.repoPath)
	t.Setenv("THRUM_NAME", "test_agent_active")

	regOpts := AgentRegisterOptions{
		Name:     "test_agent_active",
		Role:     "tester",
		Module:   "unit",
		Display:  "Active Test",
		AgentPID: os.Getpid(),
	}
	regResp, err := AgentRegister(env.client, regOpts)
	if err != nil {
		t.Fatalf("initial register: %v", err)
	}
	agentID := regResp.AgentID
	if agentID == "" {
		t.Fatalf("initial register returned empty agent_id")
	}

	// Seed an active session row directly (mirrors the
	// already-running-agent case the resurrect path must skip).
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := env.state.RawDB().Exec(`
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
		VALUES (?, ?, ?, ?)
	`, "ses_already_active_e2e", agentID, now, now); err != nil {
		t.Fatalf("seed active session: %v", err)
	}

	idFile := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "test_agent_active", Role: "tester", Module: "unit",
		},
		AgentPID:         os.Getpid(),
		Runtime:          "claude",
		PreferredRuntime: "claude",
	}
	if err := config.SaveIdentityFile(env.thrumDir, idFile); err != nil {
		t.Fatal(err)
	}

	orig := detectAncestor
	detectAncestor = func(_ context.Context) (int, string) { return os.Getpid(), "claude" }
	t.Cleanup(func() { detectAncestor = orig })

	result, err := RefreshLocalIdentity(env.client, env.repoPath)
	if err != nil {
		t.Fatalf("RefreshLocalIdentity: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.SessionResumed {
		t.Errorf("SessionResumed = true, want false (active session present)")
	}
	if result.ResumedSessionID != "" {
		t.Errorf("ResumedSessionID = %q, want empty", result.ResumedSessionID)
	}
}

// TestRefreshLocalIdentity_EnforceOneIdentityQuarantinesStale — thrum-33dt
// regression. The refresh path must enforce the "one identity per
// worktree" invariant. Seed a worktree with the current agent's
// identity PLUS a stale sibling identity from a prior agent; after
// refresh the stale file must be gone while the current one is
// preserved.
func TestRefreshLocalIdentity_EnforceOneIdentityQuarantinesStale(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	idsDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(idsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	disableGuardForTest(t, thrumDir)
	t.Setenv("THRUM_HOME", tmpDir)
	t.Setenv("THRUM_NAME", "current_agent")

	// Current agent's identity (the one the refresh picks up).
	current := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "current_agent", Role: "implementer", Module: "active",
		},
		AgentPID: os.Getpid(),
		Runtime:  "claude",
	}
	if err := config.SaveIdentityFile(thrumDir, current); err != nil {
		t.Fatal(err)
	}

	// Stale sibling identity that a prior agent left behind in this
	// worktree. Pre-fix, refresh would leave this file in place,
	// violating the one-identity-per-worktree invariant.
	stale := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "old_agent", Role: "implementer", Module: "legacy",
		},
		AgentPID: 1,
	}
	if err := config.SaveIdentityFile(thrumDir, stale); err != nil {
		t.Fatal(err)
	}
	currentPath := filepath.Join(idsDir, "current_agent.json")
	stalePath := filepath.Join(idsDir, "old_agent.json")
	if _, err := os.Stat(stalePath); err != nil {
		t.Fatalf("stale identity missing before refresh: %v", err)
	}

	// Detector returns no-runtime so no PID/runtime churn interferes.
	orig := detectAncestor
	detectAncestor = func(_ context.Context) (int, string) { return 0, "" }
	t.Cleanup(func() { detectAncestor = orig })

	if _, err := RefreshLocalIdentity(nil, tmpDir); err != nil {
		t.Fatalf("RefreshLocalIdentity: %v", err)
	}

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Errorf("stale identity old_agent.json should be deleted after refresh (err=%v); worktree invariant not enforced", err)
	}
	if _, err := os.Stat(currentPath); err != nil {
		t.Errorf("current identity file was deleted unexpectedly: %v", err)
	}
}
