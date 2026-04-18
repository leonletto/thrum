package rpc

import (
	"os"
	"path/filepath"
	"testing"
)

// writeGuardOffConfig drops a .thrum/config.json under repoDir that turns
// every identity_guard mode off. Useful in unit tests written before the
// guard landed: those tests exercise handler semantics (group creation,
// message send) with no CallerAgentID and no peercred, which the guard
// now refuses by default. Disabling the guard preserves test intent
// without rewriting every call site to supply a CallerAgentID.
func writeGuardOffConfig(t *testing.T, repoDir string) {
	t.Helper()
	thrumDir := filepath.Join(repoDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir .thrum: %v", err)
	}
	cfg := []byte(`{"identity_guard":{"cross_worktree":"off","dead_pid_auto_reclaim":"off","quickstart_self_rename":"off","quickstart_name_collision":"off","non_git_bootstrap":"off","unauthenticated_rpc":"off","daemon_writer_liveness":"off","prime_ownership":"off"}}`)
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), cfg, 0o600); err != nil {
		t.Fatalf("write guard-off config: %v", err)
	}
}
