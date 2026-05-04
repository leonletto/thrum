package bootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
)

func writeIdentity(t *testing.T, dir string, identity config.IdentityFile) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, identity.Agent.Name+".json")
	data, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// newTestState opens an isolated state.State on a fresh SQLite DB.
// thrumDir is the .thrum/ directory (var/messages.db inside it).
//
// state.NewState signature: NewState(thrumDir, syncDir, repoID, daemonID string).
// For these unit tests syncDir/daemonID are empty strings — reconcile never
// writes events and never touches sync state, so the empty values are safe.
func newTestState(t *testing.T, thrumDir string) *state.State {
	t.Helper()
	syncDir := filepath.Join(thrumDir, "var")
	if err := os.MkdirAll(syncDir, 0o750); err != nil {
		t.Fatal(err)
	}
	st, err := state.NewState(thrumDir, syncDir, "test-repo-id", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestReconcile_SkipsNonAbsoluteWorktreePath(t *testing.T) {
	// Mirror the resilience fixture: identity file with worktree="test".
	thrumDir := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, filepath.Join(thrumDir, "identities"), config.IdentityFile{
		Version:   5,
		RepoID:    "test",
		Agent:     config.AgentConfig{Kind: "agent", Name: "stub_0", Role: "tester"},
		Worktree:  "test", // <-- non-absolute, must be skipped
		UpdatedAt: time.Now().UTC(),
	})

	st := newTestState(t, thrumDir)

	deps := Deps{
		State:        st,
		ThrumDir:     thrumDir,
		Now:          time.Now,
		NewSessionID: func() string { return "ses_TEST_NEVER_USED" },
		TmuxAlive:    func(string) bool { return false },
	}
	stats, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if stats.SessionsCreated != 0 || stats.RefsCreated != 0 {
		t.Fatalf("expected zero rows created for non-absolute worktree, got %+v", stats)
	}
	if stats.Errors != 1 {
		t.Fatalf("expected stats.Errors=1, got %d", stats.Errors)
	}

	// Confirm DB has no rows.
	var n int
	if err := st.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM session_refs WHERE ref_value='test'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 session_refs rows with ref_value='test', got %d", n)
	}
}

func TestReconcile_RespectsStateRepoPath_NotTHRUMHome(t *testing.T) {
	// dirA: real identity worktree.
	dirA := t.TempDir()
	thrumDir := filepath.Join(dirA, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, filepath.Join(thrumDir, "identities"), config.IdentityFile{
		Version: 5, RepoID: "test",
		Agent:     config.AgentConfig{Kind: "agent", Name: "agent_a", Role: "tester"},
		Worktree:  dirA,
		UpdatedAt: time.Now().UTC(),
	})

	// dirB: bogus THRUM_HOME pointing at an unrelated temp dir.
	dirB := t.TempDir()
	t.Setenv("THRUM_HOME", dirB)

	st := newTestState(t, thrumDir)

	stats, err := Reconcile(context.Background(), Deps{
		State:        st,
		ThrumDir:     thrumDir, // sourced from daemonRun local, not env
		Now:          time.Now,
		NewSessionID: func() string { return "ses_TEST_A" },
		TmuxAlive:    func(string) bool { return false },
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if stats.RefsCreated != 1 {
		t.Fatalf("expected 1 ref created, got %+v", stats)
	}

	var refValue string
	if err := st.DB().QueryRowContext(context.Background(),
		"SELECT ref_value FROM session_refs WHERE ref_type='worktree' LIMIT 1").Scan(&refValue); err != nil {
		t.Fatal(err)
	}
	if refValue == dirB {
		t.Fatalf("THRUM_HOME hijack: ref_value=%q matches dirB=%q", refValue, dirB)
	}
	if refValue != dirA {
		t.Fatalf("ref_value=%q, want dirA=%q", refValue, dirA)
	}
}

func TestReconcile_NoopWhenActiveSessionExists(t *testing.T) {
	dirA := t.TempDir()
	thrumDir := filepath.Join(dirA, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, filepath.Join(thrumDir, "identities"), config.IdentityFile{
		Version: 5, RepoID: "test",
		Agent:     config.AgentConfig{Kind: "agent", Name: "agent_n", Role: "tester"},
		Worktree:  dirA,
		UpdatedAt: time.Now().UTC(),
	})
	st := newTestState(t, thrumDir)

	// Pre-populate an active session+ref to mimic prior session.start.
	pre := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO sessions(session_id, agent_id, started_at, last_seen_at) VALUES (?, ?, ?, ?)`,
		"ses_PRE", "agent_n", pre, pre); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO session_refs(session_id, ref_type, ref_value, added_at) VALUES (?, 'worktree', ?, ?)`,
		"ses_PRE", dirA, pre); err != nil {
		t.Fatal(err)
	}

	deps := Deps{
		State: st, ThrumDir: thrumDir, Now: time.Now,
		NewSessionID: func() string { return "ses_NEW_SHOULD_NOT_APPEAR" },
		TmuxAlive:    func(string) bool { return false },
	}
	stats, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if stats.SessionsCreated != 0 || stats.RefsCreated != 0 {
		t.Fatalf("reconcile inserted rows when already active: %+v", stats)
	}

	var n int
	if err := st.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM sessions WHERE agent_id='agent_n'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("sessions count for agent_n = %d, want 1", n)
	}
}
