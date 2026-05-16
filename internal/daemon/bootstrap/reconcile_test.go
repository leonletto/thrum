package bootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/rpc"
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

func TestReconcile_TmuxBindingRestoredWhenAlive(t *testing.T) {
	dirA := t.TempDir()
	thrumDir := filepath.Join(dirA, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, filepath.Join(thrumDir, "identities"), config.IdentityFile{
		Version: 5, RepoID: "test",
		Agent:       config.AgentConfig{Kind: "agent", Name: "agent_t", Role: "tester"},
		Worktree:    dirA,
		TmuxSession: "live-session:0.0",
		UpdatedAt:   time.Now().UTC(),
	})
	st := newTestState(t, thrumDir)
	th := rpc.NewTestTmuxHandlerForBootstrap()

	stats, err := Reconcile(context.Background(), Deps{
		State: st, ThrumDir: thrumDir, TmuxHandler: th, Now: time.Now,
		NewSessionID: func() string { return "ses_T" },
		TmuxAlive:    func(name string) bool { return name == "live-session:0.0" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.TmuxBindingsRestored != 1 {
		t.Fatalf("expected TmuxBindingsRestored=1, got %+v", stats)
	}
	// Lookup MUST use the bare session name (no :N.M suffix). The identity
	// file stores tmux_session as "live-session:0.0" but every reader of
	// sessionCwds (HandleCreate / HandleRestart / emitIdentityBanner /
	// HandleKill) uses the bare name. RestoreBinding strips the suffix at
	// the API boundary; this assertion pins that canonicalization end-to-end
	// through the reconcile pass. Pre-thrum-8dl3 this test asserted on the
	// suffixed key, which silently agreed with the buggy storage and let
	// emitIdentityBanner miss on every post-restart pane.
	if got := rpc.TmuxHandlerGetBindingForTest(th, "live-session"); got != dirA {
		t.Fatalf("binding not restored under bare session key: got %q, want %q", got, dirA)
	}
	// Defense-in-depth: the suffixed form must NOT be present — that would
	// mean the strip silently failed and the bug regressed.
	if got := rpc.TmuxHandlerGetBindingForTest(th, "live-session:0.0"); got != "" {
		t.Fatalf("suffixed binding leaked through RestoreBinding: got %q, want \"\"", got)
	}
}

func TestReconcile_TmuxBindingSkippedWhenDead(t *testing.T) {
	dirA := t.TempDir()
	thrumDir := filepath.Join(dirA, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, filepath.Join(thrumDir, "identities"), config.IdentityFile{
		Version: 5, RepoID: "test",
		Agent:       config.AgentConfig{Kind: "agent", Name: "agent_d", Role: "tester"},
		Worktree:    dirA,
		TmuxSession: "dead-session:0.0",
		UpdatedAt:   time.Now().UTC(),
	})
	st := newTestState(t, thrumDir)
	th := rpc.NewTestTmuxHandlerForBootstrap()

	stats, err := Reconcile(context.Background(), Deps{
		State: st, ThrumDir: thrumDir, TmuxHandler: th, Now: time.Now,
		NewSessionID: func() string { return "ses_D" },
		TmuxAlive:    func(string) bool { return false }, // dead
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.TmuxBindingsRestored != 0 {
		t.Fatalf("expected 0 tmux bindings, got %+v", stats)
	}
	if got := rpc.TmuxHandlerGetBindingForTest(th, "dead-session:0.0"); got != "" {
		t.Fatalf("dead session binding leaked: %q", got)
	}
}

func TestReconcile_PerIdentityErrorContinuesLoop(t *testing.T) {
	dirA := t.TempDir()
	thrumDir := filepath.Join(dirA, ".thrum")
	idDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(idDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// File 1: corrupt JSON.
	if err := os.WriteFile(filepath.Join(idDir, "corrupt.json"),
		[]byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	// File 2: valid identity, must still be processed.
	writeIdentity(t, idDir, config.IdentityFile{
		Version: 5, RepoID: "test",
		Agent:     config.AgentConfig{Kind: "agent", Name: "agent_ok", Role: "tester"},
		Worktree:  dirA,
		UpdatedAt: time.Now().UTC(),
	})
	st := newTestState(t, thrumDir)

	stats, err := Reconcile(context.Background(), Deps{
		State: st, ThrumDir: thrumDir, Now: time.Now,
		NewSessionID: func() string { return "ses_OK" },
		TmuxAlive:    func(string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}

	if stats.Errors != 1 {
		t.Fatalf("expected stats.Errors=1 from corrupt file, got %d", stats.Errors)
	}
	if stats.SessionsCreated != 1 {
		t.Fatalf("expected SessionsCreated=1 from valid file, got %+v", stats)
	}
}

// ulidLikeForTest returns a unique session ID per call. Helper kept local
// to tests; production uses ulid.Make().String().
var testSeq int

func ulidLikeForTest() string {
	testSeq++
	return "ses_TEST_" + time.Now().Format("150405.000000000") + "_" + string(rune('A'+testSeq%26))
}

func TestReconcile_Idempotent(t *testing.T) {
	dirA := t.TempDir()
	thrumDir := filepath.Join(dirA, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, filepath.Join(thrumDir, "identities"), config.IdentityFile{
		Version: 5, RepoID: "test",
		Agent:     config.AgentConfig{Kind: "agent", Name: "agent_i", Role: "tester"},
		Worktree:  dirA,
		UpdatedAt: time.Now().UTC(),
	})
	st := newTestState(t, thrumDir)

	deps := Deps{
		State: st, ThrumDir: thrumDir, Now: time.Now,
		NewSessionID: ulidLikeForTest,
		TmuxAlive:    func(string) bool { return false },
	}

	s1, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if s1.SessionsCreated != 1 {
		t.Fatalf("first run: got %+v", s1)
	}

	s2, err := Reconcile(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if s2.SessionsCreated != 0 || s2.RefsCreated != 0 {
		t.Fatalf("second run not idempotent: %+v", s2)
	}
}

func TestReconcile_TransactionRollbackOnRefInsertFailure(t *testing.T) {
	dirA := t.TempDir()
	thrumDir := filepath.Join(dirA, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeIdentity(t, filepath.Join(thrumDir, "identities"), config.IdentityFile{
		Version: 5, RepoID: "test",
		Agent:     config.AgentConfig{Kind: "agent", Name: "agent_r", Role: "tester"},
		Worktree:  dirA,
		UpdatedAt: time.Now().UTC(),
	})
	st := newTestState(t, thrumDir)

	// Force a ref-insert failure by dropping the session_refs table after
	// state init but before reconcile runs. The transaction wrapping the
	// (sessions, session_refs) pair must roll back the sessions insert
	// when the session_refs insert errors.
	if _, err := st.DB().ExecContext(context.Background(),
		`DROP TABLE session_refs`); err != nil {
		t.Fatal(err)
	}

	stats, err := Reconcile(context.Background(), Deps{
		State: st, ThrumDir: thrumDir, Now: time.Now,
		NewSessionID: func() string { return "ses_R_ROLLBACK" },
		TmuxAlive:    func(string) bool { return false },
	})
	if err != nil {
		t.Fatalf("Reconcile returned fatal err: %v", err)
	}
	if stats.Errors == 0 {
		t.Fatalf("expected at least 1 error from dropped session_refs table, got %+v", stats)
	}
	if stats.SessionsCreated != 0 {
		t.Fatalf("transaction did NOT roll back: SessionsCreated=%d (expected 0)", stats.SessionsCreated)
	}

	// Confirm no orphan session row remains in the sessions table.
	var n int
	if err := st.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM sessions WHERE session_id='ses_R_ROLLBACK'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("orphan session row left after rollback: count=%d", n)
	}
}
