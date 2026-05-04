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
