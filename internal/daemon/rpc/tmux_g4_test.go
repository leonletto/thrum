package rpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// TestWriteTmuxToIdentity_G4_RefusesDeadPID proves G4 blocks the daemon
// from mutating an identity file whose AgentPID is no longer alive.
// Closes the race where an agent crashes mid-session and a stale
// tmux-notify RPC lands on its file, silently repointing a dead owner.
func TestWriteTmuxToIdentity_G4_RefusesDeadPID(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	idFile := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "impl_g4",
			Role:   "implementer",
			Module: "mod",
		},
		AgentPID:    999999, // guaranteed dead on any sane system
		TmuxSession: "old-session:0.0",
		Runtime:     "claude",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity: %v", err)
	}

	st, err := state.NewState(thrumDir, thrumDir, "r_G4_TMUX", "")
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	h := NewTmuxHandler(thrumDir, st)
	// Session "old-session" matches the stored TmuxSession prefix → pass-1 hit.
	h.writeTmuxToIdentity("old-session", "new-target:0.0", "codex")

	// Re-read the identity file; fields must NOT have been overwritten.
	path := filepath.Join(thrumDir, "identities", "impl_g4.json")
	data, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("read identity: %v", err)
	}
	var got config.IdentityFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.TmuxSession != "old-session:0.0" {
		t.Errorf("TmuxSession = %q, want unchanged old-session:0.0 (G4 should have blocked the write)", got.TmuxSession)
	}
	if got.Runtime != "claude" {
		t.Errorf("Runtime = %q, want unchanged claude", got.Runtime)
	}
}

// TestWriteTmuxToIdentity_G4_OffModeAllowsDeadPID confirms opt-out works:
// with identity_guard.daemon_writer_liveness=off the write proceeds even
// against a dead PID, preserving the pre-guard behavior during migration.
func TestWriteTmuxToIdentity_G4_OffModeAllowsDeadPID(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeGuardOffConfig(t, tmpDir)

	idFile := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "impl_g4_off",
			Role:   "implementer",
			Module: "mod",
		},
		AgentPID:    999999,
		TmuxSession: "old-session:0.0",
		Runtime:     "claude",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity: %v", err)
	}

	st, err := state.NewState(thrumDir, thrumDir, "r_G4_OFF", "")
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	h := NewTmuxHandler(thrumDir, st)
	h.writeTmuxToIdentity("old-session", "new-target:0.0", "codex")

	path := filepath.Join(thrumDir, "identities", "impl_g4_off.json")
	data, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("read identity: %v", err)
	}
	var got config.IdentityFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.TmuxSession != "new-target:0.0" {
		t.Errorf("TmuxSession = %q, want new-target:0.0 (off mode should have written)", got.TmuxSession)
	}
	if got.Runtime != "codex" {
		t.Errorf("Runtime = %q, want codex", got.Runtime)
	}
}
