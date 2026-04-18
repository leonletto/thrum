package guard

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfig_DefaultsWhenNoFiles confirms that when neither the
// repo-level nor daemon-level config files exist, LoadConfig returns
// DefaultConfig (every guard strict) — enforcement defaults on.
func TestLoadConfig_DefaultsWhenNoFiles(t *testing.T) {
	thrumDir := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}
	got := LoadConfig(thrumDir)
	want := DefaultConfig()
	if got != want {
		t.Errorf("LoadConfig with no files = %+v, want DefaultConfig %+v", got, want)
	}
}

// TestLoadConfig_RepoOverridesDefaults confirms that values in the
// repo-level .thrum/config.json override the DefaultConfig baseline.
func TestLoadConfig_RepoOverridesDefaults(t *testing.T) {
	thrumDir := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeRepoConfig(t, thrumDir, `{"identity_guard":{"cross_worktree":"warn","prime_ownership":"off"}}`)

	got := LoadConfig(thrumDir)
	if got.CrossWorktree != ModeWarn {
		t.Errorf("CrossWorktree = %q, want warn", got.CrossWorktree)
	}
	if got.PrimeOwnership != ModeOff {
		t.Errorf("PrimeOwnership = %q, want off", got.PrimeOwnership)
	}
	// Unset fields fall through to default strict.
	if got.UnauthenticatedRPC != ModeStrict {
		t.Errorf("UnauthenticatedRPC = %q, want strict (from default)", got.UnauthenticatedRPC)
	}
}

// TestLoadConfig_DaemonOverridesRepo confirms the daemon-level override
// file takes precedence over repo-level. Per-guard modes must merge
// independently: DeadReclaimMode=warn on repo + strict on daemon
// resolves to strict (daemon wins for that field only).
func TestLoadConfig_DaemonOverridesRepo(t *testing.T) {
	thrumDir := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "var"), 0o750); err != nil {
		t.Fatal(err)
	}
	// Repo says cross_worktree=warn, dead_pid_auto_reclaim=warn.
	writeRepoConfig(t, thrumDir, `{"identity_guard":{"cross_worktree":"warn","dead_pid_auto_reclaim":"warn"}}`)
	// Daemon override flips dead_pid_auto_reclaim to strict; leaves
	// cross_worktree unset so repo's warn persists.
	writeDaemonConfig(t, thrumDir, `{"identity_guard":{"dead_pid_auto_reclaim":"strict"}}`)

	got := LoadConfig(thrumDir)
	if got.CrossWorktree != ModeWarn {
		t.Errorf("CrossWorktree = %q, want warn (repo wins, daemon unset)", got.CrossWorktree)
	}
	if got.DeadPIDAutoReclaim != ModeStrict {
		t.Errorf("DeadPIDAutoReclaim = %q, want strict (daemon overrides repo)", got.DeadPIDAutoReclaim)
	}
}

// TestLoadConfig_MalformedRepoFallsBackToDefaults confirms that a
// malformed repo config doesn't fail-open — we return DefaultConfig
// so enforcement stays on. Matches LoadConfigFromDir's existing
// behavior.
func TestLoadConfig_MalformedRepoFallsBackToDefaults(t *testing.T) {
	thrumDir := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeRepoConfig(t, thrumDir, `{not valid json`)

	got := LoadConfig(thrumDir)
	if got != DefaultConfig() {
		t.Errorf("malformed repo config should fall back to DefaultConfig, got %+v", got)
	}
}

// TestLoadConfig_MalformedDaemonSkipsOverlay confirms that a malformed
// daemon override is silently ignored — we fall back to repo+defaults,
// not to DefaultConfig alone. Operator-side corruption of the override
// shouldn't lose repo settings.
func TestLoadConfig_MalformedDaemonSkipsOverlay(t *testing.T) {
	thrumDir := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "var"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeRepoConfig(t, thrumDir, `{"identity_guard":{"cross_worktree":"warn"}}`)
	writeDaemonConfig(t, thrumDir, `{not valid json`)

	got := LoadConfig(thrumDir)
	if got.CrossWorktree != ModeWarn {
		t.Errorf("CrossWorktree = %q, want warn (repo persists through malformed daemon overlay)", got.CrossWorktree)
	}
}

func writeRepoConfig(t *testing.T, thrumDir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write repo config: %v", err)
	}
}

func writeDaemonConfig(t *testing.T, thrumDir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(thrumDir, "var"), 0o750); err != nil {
		t.Fatalf("mkdir var: %v", err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "var", "guard-daemon.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write daemon config: %v", err)
	}
}
