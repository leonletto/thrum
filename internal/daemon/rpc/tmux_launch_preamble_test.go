package rpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

// TestClearStalePIDForLaunch_DeadPID_Clears covers the happy path for
// HandleLaunch's preamble: an identity file with a dead stored PID
// gets nulled so writeTmuxToIdentity's Pass 0 G4 skip applies.
func TestClearStalePIDForLaunch_DeadPID_Clears(t *testing.T) {
	cwd := t.TempDir()
	idPath := writeTestIdentityFile(t, cwd, "impl_test", 2147483646, "") // guaranteed-dead PID

	h := NewTmuxHandler(t.TempDir(), nil)
	h.sessionMu.Lock()
	h.sessionCwds = map[string]string{"test-session": cwd}
	h.sessionMu.Unlock()

	h.clearStalePIDForLaunch("test-session")

	id := readTestIdentityFile(t, idPath)
	if id.AgentPID != 0 {
		t.Errorf("expected AgentPID cleared to 0, got %d", id.AgentPID)
	}
}

// TestClearStalePIDForLaunch_LivePID_Noop: live PID must be preserved.
// The dead-PID clear is specifically for the tmux-create subshell PID;
// a legitimately-running runtime should not be molested.
func TestClearStalePIDForLaunch_LivePID_Noop(t *testing.T) {
	cwd := t.TempDir()
	livePID := os.Getpid()
	idPath := writeTestIdentityFile(t, cwd, "impl_test", livePID, "claude")

	h := NewTmuxHandler(t.TempDir(), nil)
	h.sessionMu.Lock()
	h.sessionCwds = map[string]string{"test-session": cwd}
	h.sessionMu.Unlock()

	h.clearStalePIDForLaunch("test-session")

	id := readTestIdentityFile(t, idPath)
	if id.AgentPID != livePID {
		t.Errorf("expected AgentPID unchanged (%d), got %d", livePID, id.AgentPID)
	}
}

// TestClearStalePIDForLaunch_NoSession_Noop verifies the preamble is a
// silent no-op when sessionCwds has no entry for this session (can
// happen if HandleLaunch runs without a prior HandleCreate on the same
// daemon instance).
func TestClearStalePIDForLaunch_NoSession_Noop(t *testing.T) {
	h := NewTmuxHandler(t.TempDir(), nil)
	// Intentionally no sessionCwds entry
	h.clearStalePIDForLaunch("test-session") // must not panic / error
}

// TestClearStalePIDForLaunch_MultipleIdentityFiles_Noop verifies the
// preamble bails out silently when the worktree has >1 identity files
// (EnforceOneIdentity invariant violated). Pass 0's multi-file warn in
// writeTmuxByWorktreeCwd is the canonical operator signal for that
// case; the preamble is intentionally a silent best-effort.
func TestClearStalePIDForLaunch_MultipleIdentityFiles_Noop(t *testing.T) {
	cwd := t.TempDir()
	writeTestIdentityFile(t, cwd, "impl_a", 2147483646, "")
	writeTestIdentityFile(t, cwd, "impl_b", 2147483646, "")

	h := NewTmuxHandler(t.TempDir(), nil)
	h.sessionMu.Lock()
	h.sessionCwds = map[string]string{"test-session": cwd}
	h.sessionMu.Unlock()

	h.clearStalePIDForLaunch("test-session") // no panic, no error; neither file touched
}

// TestWarnIfTmuxSessionEmpty_Empty_Warns exercises the regression-guard
// post-write inspection. We can't easily assert the slog.Warn fired
// from a unit test, so this test verifies the code path runs without
// panicking for the empty-tmux-session case. The visible signal is the
// log emission in daemon stderr during a real HandleLaunch.
func TestWarnIfTmuxSessionEmpty_Empty_Warns(t *testing.T) {
	cwd := t.TempDir()
	_ = writeTestIdentityFile(t, cwd, "impl_test", 0, "") // TmuxSession empty

	h := NewTmuxHandler(t.TempDir(), nil)
	h.sessionMu.Lock()
	h.sessionCwds = map[string]string{"test-session": cwd}
	h.sessionMu.Unlock()

	h.warnIfTmuxSessionEmpty("test-session") // exercise path
}

// --- Test helpers ---

// writeTestIdentityFile writes an identity file into cwd/.thrum/identities/<name>.json.
func writeTestIdentityFile(t *testing.T, cwd, name string, pid int, tmuxSession string) string {
	t.Helper()
	idDir := filepath.Join(cwd, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	id := config.IdentityFile{
		Version: 4,
		RepoID:  "test-repo",
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    name,
			Role:    "implementer",
			Module:  "test",
			Display: name,
		},
		Worktree:    cwd,
		AgentPID:    pid,
		TmuxSession: tmuxSession,
	}
	b, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(idDir, name+".json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func readTestIdentityFile(t *testing.T, path string) config.IdentityFile {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var id config.IdentityFile
	if err := json.Unmarshal(b, &id); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return id
}
