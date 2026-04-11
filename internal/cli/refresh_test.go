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
)

// TestRefreshLocalIdentity_NoRuntime asserts that when FindClaudeAncestor
// returns (0, ""), the refresh still runs through but does not update
// PID/runtime fields. Tmux and branch may still update.
func TestRefreshLocalIdentity_NoRuntime(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750); err != nil {
		t.Fatal(err)
	}

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

// TestRefreshLocalIdentity_PIDDrift asserts that when the detected PID
// differs from the identity file, the file is updated and result reports it.
func TestRefreshLocalIdentity_PIDDrift(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("THRUM_HOME", tmpDir)
	t.Setenv("THRUM_NAME", "test_agent")

	idFile := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "test_agent", Role: "tester", Module: "unit",
		},
		AgentPID:         99999,
		Runtime:          "claude",
		PreferredRuntime: "claude",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatal(err)
	}

	orig := detectAncestor
	detectAncestor = func(_ context.Context) (int, string) { return os.Getpid(), "claude" }
	t.Cleanup(func() { detectAncestor = orig })

	result, err := RefreshLocalIdentity(nil, tmpDir)
	if err != nil {
		t.Fatalf("RefreshLocalIdentity: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !containsString(result.FileChanged, "agent_pid") {
		t.Errorf("expected FileChanged to contain agent_pid, got %v", result.FileChanged)
	}
	if result.DetectedPID != os.Getpid() {
		t.Errorf("DetectedPID = %d, want %d", result.DetectedPID, os.Getpid())
	}

	// Read the raw file to verify the on-disk PID, bypassing the
	// silent PID-adoption side effect in loadIdentityFromDir.
	loaded := readIdentityFile(t, thrumDir, "test_agent")
	if loaded.AgentPID != os.Getpid() {
		t.Errorf("file AgentPID = %d, want %d", loaded.AgentPID, os.Getpid())
	}
}

// TestRefreshLocalIdentity_RuntimeDrift asserts runtime field updates.
func TestRefreshLocalIdentity_RuntimeDrift(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750); err != nil {
		t.Fatal(err)
	}
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

	orig := detectAncestor
	detectAncestor = func(_ context.Context) (int, string) { return os.Getpid(), "codex" }
	t.Cleanup(func() { detectAncestor = orig })

	result, err := RefreshLocalIdentity(nil, tmpDir)
	if err != nil {
		t.Fatalf("RefreshLocalIdentity: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !containsString(result.FileChanged, "runtime") {
		t.Errorf("expected runtime in FileChanged, got %v", result.FileChanged)
	}
	if !containsString(result.FileChanged, "preferred_runtime") {
		t.Errorf("expected preferred_runtime in FileChanged, got %v", result.FileChanged)
	}

	loaded := readIdentityFile(t, thrumDir, "test_agent")
	if loaded.Runtime != "codex" {
		t.Errorf("loaded.Runtime = %q, want codex", loaded.Runtime)
	}
	if loaded.PreferredRuntime != "codex" {
		t.Errorf("loaded.PreferredRuntime = %q, want codex", loaded.PreferredRuntime)
	}
}

// TestRefreshLocalIdentity_HappyPath asserts no file write when detected
// state exactly matches the identity file.
func TestRefreshLocalIdentity_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750); err != nil {
		t.Fatal(err)
	}
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
	if err := os.Chmod(identitiesDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(identitiesDir, 0750) })

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
