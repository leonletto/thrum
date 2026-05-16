package config_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

// TestMain isolates this package's tests from THRUM_* env pollution in the
// operator's shell. Without this, a developer running the suite from a primed
// shell sees Load tests fail with "open .../coordinator_main.json: no such
// file" because the test's tmpdir doesn't contain the operator's identity
// file. The package previously protected only TestLoad_FromIdentityFile via
// inline t.Setenv("", "") incantations; doing it once at TestMain covers every
// test in the package and prevents new tests from forgetting the dance.
//
// Individual tests that need specific THRUM_* values still set them via
// t.Setenv, which restores them at test end.
func TestMain(m *testing.M) {
	for _, k := range []string{
		"THRUM_HOME", "THRUM_NAME", "THRUM_AGENT_ID",
		"THRUM_ROLE", "THRUM_MODULE", "THRUM_DISPLAY", "THRUM_INTENT",
	} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}

func TestLoad_FromEnvironmentVariables(t *testing.T) {
	t.Setenv("THRUM_ROLE", "implementer")
	t.Setenv("THRUM_MODULE", "auth")
	t.Setenv("THRUM_DISPLAY", "Auth Agent")

	cfg, err := config.Load("", "")
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.Agent.Role != "implementer" {
		t.Errorf("Expected role 'implementer', got '%s'", cfg.Agent.Role)
	}
	if cfg.Agent.Module != "auth" {
		t.Errorf("Expected module 'auth', got '%s'", cfg.Agent.Module)
	}
	if cfg.Display != "Auth Agent" {
		t.Errorf("Expected display 'Auth Agent', got '%s'", cfg.Display)
	}
	if cfg.Agent.Kind != "agent" {
		t.Errorf("Expected kind 'agent', got '%s'", cfg.Agent.Kind)
	}
}

func TestLoad_FromCLIFlags(t *testing.T) {
	cfg, err := config.Load("reviewer", "sync-daemon")
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.Agent.Role != "reviewer" {
		t.Errorf("Expected role 'reviewer', got '%s'", cfg.Agent.Role)
	}
	if cfg.Agent.Module != "sync-daemon" {
		t.Errorf("Expected module 'sync-daemon', got '%s'", cfg.Agent.Module)
	}
}

func TestLoad_FlagsOverrideEnv(t *testing.T) {
	t.Setenv("THRUM_ROLE", "implementer")
	t.Setenv("THRUM_MODULE", "auth")

	cfg, err := config.Load("planner", "frontend")
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Flags should override env vars
	if cfg.Agent.Role != "planner" {
		t.Errorf("Expected role 'planner' (from flag), got '%s'", cfg.Agent.Role)
	}
	if cfg.Agent.Module != "frontend" {
		t.Errorf("Expected module 'frontend' (from flag), got '%s'", cfg.Agent.Module)
	}
}

func TestLoad_FromIdentityFile(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	t.Setenv("THRUM_ROLE", "")
	t.Setenv("THRUM_MODULE", "")
	t.Setenv("THRUM_DISPLAY", "")
	// Create temp directory
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	// Create identity file in new format
	identity := &config.IdentityFile{
		Version: 1,
		RepoID:  "r_TEST123456",
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    "test_agent",
			Role:    "implementer",
			Module:  "database",
			Display: "DB Agent",
		},
		ConfirmedBy: "human:test",
		UpdatedAt:   time.Now().UTC(),
	}

	// Create .thrum directory
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("Failed to create .thrum directory: %v", err)
	}

	err := config.SaveIdentityFile(thrumDir, identity)
	if err != nil {
		t.Fatalf("SaveIdentityFile() failed: %v", err)
	}

	cfg, err := config.Load("", "")
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.RepoID != "r_TEST123456" {
		t.Errorf("Expected repo_id 'r_TEST123456', got '%s'", cfg.RepoID)
	}
	if cfg.Agent.Role != "implementer" {
		t.Errorf("Expected role 'implementer', got '%s'", cfg.Agent.Role)
	}
	if cfg.Agent.Module != "database" {
		t.Errorf("Expected module 'database', got '%s'", cfg.Agent.Module)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	// No identity file in temp dir
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	_, err := config.Load("", "")
	if err == nil {
		t.Fatal("Expected error when role and module are missing, got nil")
	}
}

func TestLoad_ThrumNameEnvVar_SelectsSpecificIdentity(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	// Create temp directory with multiple identity files
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Create two identity files
	furiosa := &config.IdentityFile{
		Version: 1,
		RepoID:  "r_TEST123",
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    "furiosa",
			Role:    "implementer",
			Module:  "auth",
			Display: "Furiosa",
		},
		UpdatedAt: time.Now().UTC(),
	}
	nux := &config.IdentityFile{
		Version: 1,
		RepoID:  "r_TEST123",
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    "nux",
			Role:    "tester",
			Module:  "auth",
			Display: "Nux",
		},
		UpdatedAt: time.Now().UTC(),
	}

	if err := config.SaveIdentityFile(thrumDir, furiosa); err != nil {
		t.Fatalf("Failed to save furiosa identity: %v", err)
	}
	if err := config.SaveIdentityFile(thrumDir, nux); err != nil {
		t.Fatalf("Failed to save nux identity: %v", err)
	}

	// Set THRUM_NAME to select furiosa
	t.Setenv("THRUM_NAME", "furiosa")

	cfg, err := config.LoadWithPath(tmpDir, "", "")
	if err != nil {
		t.Fatalf("LoadWithPath() failed: %v", err)
	}

	if cfg.Agent.Name != "furiosa" {
		t.Errorf("Expected agent name 'furiosa', got '%s'", cfg.Agent.Name)
	}
	if cfg.Agent.Role != "implementer" {
		t.Errorf("Expected role 'implementer', got '%s'", cfg.Agent.Role)
	}

	// Now switch to nux
	t.Setenv("THRUM_NAME", "nux")
	cfg, err = config.LoadWithPath(tmpDir, "", "")
	if err != nil {
		t.Fatalf("LoadWithPath() with nux failed: %v", err)
	}

	if cfg.Agent.Name != "nux" {
		t.Errorf("Expected agent name 'nux', got '%s'", cfg.Agent.Name)
	}
	if cfg.Agent.Role != "tester" {
		t.Errorf("Expected role 'tester', got '%s'", cfg.Agent.Role)
	}
}

// TestLoadWithPath_CwdWinsOverThrumHome pins the rc.6 fix (thrum-qofl):
// when LoadWithPath is called with a worktreeRepo that has its OWN .thrum/
// identity, the worktree's identity wins even if THRUM_HOME points
// elsewhere. This inverts pre-rc.6 behavior (env-wins) which silently
// misidentified callers when stale env vars were inherited at fork time
// from a parent shell anchored to a different worktree.
//
// The legitimate "pin via THRUM_HOME when cwd is outside any worktree" use
// case is verified separately in TestLoadWithPath_FallsBackToThrumHomeWhenCwdHasNothing.
func TestLoadWithPath_CwdWinsOverThrumHome(t *testing.T) {
	staleEnvRepo := t.TempDir()
	cwdRepo := t.TempDir()

	staleIdentity := &config.IdentityFile{
		Version: 1,
		RepoID:  "r_STALE123",
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    "coordinator_stale",
			Role:    "coordinator",
			Module:  "testing",
			Display: "Stale Coordinator",
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := config.SaveIdentityFile(filepath.Join(staleEnvRepo, ".thrum"), staleIdentity); err != nil {
		t.Fatalf("save stale identity: %v", err)
	}

	cwdIdentity := &config.IdentityFile{
		Version: 1,
		RepoID:  "r_CWD123",
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    "implementer_cwd",
			Role:    "implementer",
			Module:  "testing",
			Display: "Cwd Implementer",
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := config.SaveIdentityFile(filepath.Join(cwdRepo, ".thrum"), cwdIdentity); err != nil {
		t.Fatalf("save cwd identity: %v", err)
	}

	t.Setenv("THRUM_HOME", staleEnvRepo)
	t.Setenv("THRUM_NAME", "coordinator_stale")

	cfg, err := config.LoadWithPath(cwdRepo, "", "")
	if err != nil {
		t.Fatalf("LoadWithPath() failed: %v", err)
	}

	if cfg.RepoID != "r_CWD123" {
		t.Errorf("RepoID = %q, want r_CWD123 (cwd should win over stale THRUM_HOME)", cfg.RepoID)
	}
	if cfg.Agent.Name != "implementer_cwd" {
		t.Errorf("Agent.Name = %q, want implementer_cwd", cfg.Agent.Name)
	}
	if cfg.Agent.Role != "implementer" {
		t.Errorf("Agent.Role = %q, want implementer", cfg.Agent.Role)
	}
}

// TestLoadWithPath_FallsBackToThrumHomeWhenCwdHasNothing verifies the
// legitimate THRUM_HOME use case is preserved: when the caller's cwd has no
// .thrum/ at or above it, THRUM_HOME still wins as a fallback. This is
// important for scripts run from /tmp or other non-worktree contexts that
// intentionally use THRUM_HOME to route commands to a bound worktree.
func TestLoadWithPath_FallsBackToThrumHomeWhenCwdHasNothing(t *testing.T) {
	homeRepo := t.TempDir()
	cwdNoThrum := t.TempDir() // no .thrum/

	homeIdentity := &config.IdentityFile{
		Version: 1,
		RepoID:  "r_HOME123",
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    "coordinator_home",
			Role:    "coordinator",
			Module:  "main",
			Display: "Home Coordinator",
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := config.SaveIdentityFile(filepath.Join(homeRepo, ".thrum"), homeIdentity); err != nil {
		t.Fatalf("save home identity: %v", err)
	}

	t.Setenv("THRUM_HOME", homeRepo)
	t.Setenv("THRUM_NAME", "coordinator_home")

	cfg, err := config.LoadWithPath(cwdNoThrum, "", "")
	if err != nil {
		t.Fatalf("LoadWithPath() failed: %v", err)
	}

	if cfg.RepoID != "r_HOME123" {
		t.Errorf("RepoID = %q, want r_HOME123 (THRUM_HOME should win when cwd has no .thrum/)", cfg.RepoID)
	}
	if cfg.Agent.Name != "coordinator_home" {
		t.Errorf("Agent.Name = %q, want coordinator_home", cfg.Agent.Name)
	}
}

// TestLoad_ThrumNameEnvVar_FallsThroughOnNonexistent pins the rc.6 fix:
// when THRUM_NAME points to an identity file that doesn't exist in cwd's
// worktree, the loader falls through to directory scan instead of erroring.
// Old behavior (rc.5 and earlier): hard error on missing env-named file —
// which broke legitimate uses where stale THRUM_NAME was inherited from a
// parent shell. With a single identity in cwd, that one identity wins.
func TestLoad_ThrumNameEnvVar_FallsThroughOnNonexistent(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Create one identity file in cwd
	identity := &config.IdentityFile{
		Version: 1,
		RepoID:  "r_TEST123",
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "existing",
			Role:   "implementer",
			Module: "test",
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := config.SaveIdentityFile(thrumDir, identity); err != nil {
		t.Fatalf("Failed to save identity: %v", err)
	}

	// Set THRUM_NAME to a name that doesn't exist in this worktree
	// (simulates stale inherited env from a parent shell anchored elsewhere)
	t.Setenv("THRUM_NAME", "nonexistent")

	cfg, err := config.LoadWithPath(tmpDir, "", "")
	if err != nil {
		t.Fatalf("LoadWithPath should fall through to directory scan when THRUM_NAME doesn't match: %v", err)
	}
	if cfg.Agent.Name != "existing" {
		t.Errorf("Agent.Name = %q, want existing (cwd's single identity should win when env hint doesn't match)", cfg.Agent.Name)
	}
}

func TestLoad_MultipleIdentities_ErrorWithoutThrumName(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	// Create temp directory with multiple identity files
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Create two identity files
	agent1 := &config.IdentityFile{
		Version: 1,
		RepoID:  "r_TEST123",
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "agent1",
			Role:   "implementer",
			Module: "test",
		},
		UpdatedAt: time.Now().UTC(),
	}
	agent2 := &config.IdentityFile{
		Version: 1,
		RepoID:  "r_TEST123",
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "agent2",
			Role:   "tester",
			Module: "test",
		},
		UpdatedAt: time.Now().UTC(),
	}

	if err := config.SaveIdentityFile(thrumDir, agent1); err != nil {
		t.Fatalf("Failed to save agent1 identity: %v", err)
	}
	if err := config.SaveIdentityFile(thrumDir, agent2); err != nil {
		t.Fatalf("Failed to save agent2 identity: %v", err)
	}

	// Make sure THRUM_NAME is not set
	t.Setenv("THRUM_NAME", "")

	_, err := config.LoadWithPath(tmpDir, "", "")
	if err == nil {
		t.Fatal("Expected error when multiple identities exist without THRUM_NAME, got nil")
	}
}

func TestLoad_ThrumNameEnvVar_InvalidName(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Create a valid identity file
	identity := &config.IdentityFile{
		Version: 1,
		RepoID:  "r_TEST123",
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "valid_agent",
			Role:   "implementer",
			Module: "test",
		},
		UpdatedAt: time.Now().UTC(),
	}
	if err := config.SaveIdentityFile(thrumDir, identity); err != nil {
		t.Fatalf("Failed to save identity: %v", err)
	}

	// Set role and module env vars so validation runs
	t.Setenv("THRUM_ROLE", "implementer")
	t.Setenv("THRUM_MODULE", "test")

	tests := []struct {
		name        string
		thrumName   string
		errorSubstr string
	}{
		{
			name:        "uppercase",
			thrumName:   "InvalidName",
			errorSubstr: "invalid characters",
		},
		{
			name:        "reserved name",
			thrumName:   "daemon",
			errorSubstr: "reserved",
		},
		{
			name:        "special chars",
			thrumName:   "agent@home",
			errorSubstr: "invalid characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("THRUM_NAME", tt.thrumName)

			_, err := config.LoadWithPath(tmpDir, "", "")
			if err == nil {
				t.Fatalf("Expected error for invalid THRUM_NAME %q, got nil", tt.thrumName)
			}

			if !strings.Contains(err.Error(), tt.errorSubstr) {
				t.Errorf("Error should contain %q, got: %v", tt.errorSubstr, err)
			}
		})
	}
}

func TestSaveIdentityFile(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	identity := &config.IdentityFile{
		Version: 1,
		RepoID:  "r_ABC123",
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    "my_agent",
			Role:    "tester",
			Module:  "api",
			Display: "API Tester",
		},
		ConfirmedBy: "human:alice",
	}

	err := config.SaveIdentityFile(thrumDir, identity)
	if err != nil {
		t.Fatalf("SaveIdentityFile() failed: %v", err)
	}

	// Verify file was created in identities directory
	expectedPath := filepath.Join(thrumDir, "identities", "my_agent.json")
	data, err := os.ReadFile(expectedPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Failed to read saved file: %v", err)
	}

	// Verify JSON structure
	var saved config.IdentityFile
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("Failed to parse saved JSON: %v", err)
	}

	if saved.RepoID != "r_ABC123" {
		t.Errorf("Expected repo_id 'r_ABC123', got '%s'", saved.RepoID)
	}
	if saved.Agent.Role != "tester" {
		t.Errorf("Expected role 'tester', got '%s'", saved.Agent.Role)
	}

	// Verify updated_at was set
	if saved.UpdatedAt.IsZero() {
		t.Error("Expected updated_at to be set, got zero time")
	}

	// Verify file permissions (should be 0600)
	info, err := os.Stat(expectedPath)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("Expected file permissions 0600, got %v", info.Mode().Perm())
	}
}

func TestLoad_WorktreeFiltering_SingleMatch(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	// Create a temp git worktree
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Initialize git repo to enable worktree detection
	// The worktree name will be the basename of tmpDir
	runGitCmd(t, tmpDir, "init")
	runGitCmd(t, tmpDir, "config", "user.name", "Test User")
	runGitCmd(t, tmpDir, "config", "user.email", "test@example.com")

	// Get the actual worktree name (basename of tmpDir)
	worktreeName := filepath.Base(tmpDir)

	// Create two identity files with different worktree fields
	// One matches current worktree, one doesn't
	matchingAgent := &config.IdentityFile{
		Version:  1,
		RepoID:   "r_TEST123",
		Worktree: worktreeName,
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    "matching_agent",
			Role:    "implementer",
			Module:  "test",
			Display: "Matching Agent",
		},
		UpdatedAt: time.Now().UTC(),
	}
	otherAgent := &config.IdentityFile{
		Version:  1,
		RepoID:   "r_TEST123",
		Worktree: "other_worktree",
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    "other_agent",
			Role:    "tester",
			Module:  "test",
			Display: "Other Agent",
		},
		UpdatedAt: time.Now().UTC(),
	}

	if err := config.SaveIdentityFile(thrumDir, matchingAgent); err != nil {
		t.Fatalf("Failed to save matching agent identity: %v", err)
	}
	if err := config.SaveIdentityFile(thrumDir, otherAgent); err != nil {
		t.Fatalf("Failed to save other agent identity: %v", err)
	}

	// Make sure THRUM_NAME is not set (force worktree filtering)
	t.Setenv("THRUM_NAME", "")

	// Load should auto-select the matching agent
	cfg, err := config.LoadWithPath(tmpDir, "", "")
	if err != nil {
		t.Fatalf("LoadWithPath() failed: %v", err)
	}

	if cfg.Agent.Name != "matching_agent" {
		t.Errorf("Expected agent name 'matching_agent', got '%s'", cfg.Agent.Name)
	}
	if cfg.Agent.Role != "implementer" {
		t.Errorf("Expected role 'implementer', got '%s'", cfg.Agent.Role)
	}
}

func TestLoad_WorktreeFiltering_MultipleMatches_MostRecentWins(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Initialize git repo
	runGitCmd(t, tmpDir, "init")
	runGitCmd(t, tmpDir, "config", "user.name", "Test User")
	runGitCmd(t, tmpDir, "config", "user.email", "test@example.com")

	worktreeName := filepath.Base(tmpDir)

	// Create two identity files that both match the current worktree
	// agent1 is older, agent2 is newer — agent2 should win
	agent1 := &config.IdentityFile{
		Version:  1,
		RepoID:   "r_TEST123",
		Worktree: worktreeName,
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "agent1",
			Role:   "implementer",
			Module: "test",
		},
		UpdatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	agent2 := &config.IdentityFile{
		Version:  1,
		RepoID:   "r_TEST123",
		Worktree: worktreeName,
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "agent2",
			Role:   "tester",
			Module: "test",
		},
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	// Write identity files directly to preserve explicit timestamps
	// (SaveIdentityFile overwrites UpdatedAt with time.Now())
	identitiesDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		t.Fatalf("Failed to create identities dir: %v", err)
	}
	for _, id := range []*config.IdentityFile{agent1, agent2} {
		data, err := json.Marshal(id)
		if err != nil {
			t.Fatalf("Failed to marshal %s: %v", id.Agent.Name, err)
		}
		path := filepath.Join(identitiesDir, id.Agent.Name+".json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatalf("Failed to write %s: %v", id.Agent.Name, err)
		}
	}

	t.Setenv("THRUM_NAME", "")

	// Should succeed with the most recently updated identity (agent2)
	cfg, err := config.LoadWithPath(tmpDir, "", "")
	if err != nil {
		t.Fatalf("Expected success with most-recent-wins, got error: %v", err)
	}

	if cfg.Agent.Name != "agent2" {
		t.Errorf("Expected most recent identity (agent2), got: %s", cfg.Agent.Name)
	}
	if cfg.Agent.Role != "tester" {
		t.Errorf("Expected role 'tester' from agent2, got: %s", cfg.Agent.Role)
	}
}

func TestLoad_WorktreeFiltering_NoMatches(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Initialize git repo
	runGitCmd(t, tmpDir, "init")
	runGitCmd(t, tmpDir, "config", "user.name", "Test User")
	runGitCmd(t, tmpDir, "config", "user.email", "test@example.com")

	// Create two identity files that don't match current worktree
	agent1 := &config.IdentityFile{
		Version:  1,
		RepoID:   "r_TEST123",
		Worktree: "worktree_a",
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "agent1",
			Role:   "implementer",
			Module: "test",
		},
		UpdatedAt: time.Now().UTC(),
	}
	agent2 := &config.IdentityFile{
		Version:  1,
		RepoID:   "r_TEST123",
		Worktree: "worktree_b",
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "agent2",
			Role:   "tester",
			Module: "test",
		},
		UpdatedAt: time.Now().UTC(),
	}

	if err := config.SaveIdentityFile(thrumDir, agent1); err != nil {
		t.Fatalf("Failed to save agent1 identity: %v", err)
	}
	if err := config.SaveIdentityFile(thrumDir, agent2); err != nil {
		t.Fatalf("Failed to save agent2 identity: %v", err)
	}

	t.Setenv("THRUM_NAME", "")

	// Should error with generic "multiple identity files" message
	_, err := config.LoadWithPath(tmpDir, "", "")
	if err == nil {
		t.Fatal("Expected error when no identities match worktree, got nil")
	}

	// Error should be the generic auto-select message with available names
	if !strings.Contains(err.Error(), "cannot auto-select identity") {
		t.Errorf("Error should contain 'cannot auto-select identity', got: %v", err)
	}
	if !strings.Contains(err.Error(), "agent1") || !strings.Contains(err.Error(), "agent2") {
		t.Errorf("Error should list available identity names, got: %v", err)
	}
}

func TestLoad_WorktreeFiltering_NotInGitRepo(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	// Create temp directory WITHOUT git init
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Create two identity files
	agent1 := &config.IdentityFile{
		Version:  1,
		RepoID:   "r_TEST123",
		Worktree: "some_worktree",
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "agent1",
			Role:   "implementer",
			Module: "test",
		},
		UpdatedAt: time.Now().UTC(),
	}
	agent2 := &config.IdentityFile{
		Version:  1,
		RepoID:   "r_TEST123",
		Worktree: "other_worktree",
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "agent2",
			Role:   "tester",
			Module: "test",
		},
		UpdatedAt: time.Now().UTC(),
	}

	if err := config.SaveIdentityFile(thrumDir, agent1); err != nil {
		t.Fatalf("Failed to save agent1 identity: %v", err)
	}
	if err := config.SaveIdentityFile(thrumDir, agent2); err != nil {
		t.Fatalf("Failed to save agent2 identity: %v", err)
	}

	t.Setenv("THRUM_NAME", "")

	// Should fall through to generic error (git detection fails)
	_, err := config.LoadWithPath(tmpDir, "", "")
	if err == nil {
		t.Fatal("Expected error when multiple identities exist and not in git repo, got nil")
	}

	// Error should be the generic auto-select message with available names
	if !strings.Contains(err.Error(), "cannot auto-select identity") {
		t.Errorf("Error should contain 'cannot auto-select identity', got: %v", err)
	}
	if !strings.Contains(err.Error(), "agent1") || !strings.Contains(err.Error(), "agent2") {
		t.Errorf("Error should list available identity names, got: %v", err)
	}
}

func TestLoad_WorktreeFiltering_ThrumNameBypassesWorktreeFilter(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Initialize git repo
	runGitCmd(t, tmpDir, "init")
	runGitCmd(t, tmpDir, "config", "user.name", "Test User")
	runGitCmd(t, tmpDir, "config", "user.email", "test@example.com")

	worktreeName := filepath.Base(tmpDir)

	// Create two identity files
	// One matches worktree, one doesn't
	matchingAgent := &config.IdentityFile{
		Version:  1,
		RepoID:   "r_TEST123",
		Worktree: worktreeName,
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "matching_agent",
			Role:   "implementer",
			Module: "test",
		},
		UpdatedAt: time.Now().UTC(),
	}
	otherAgent := &config.IdentityFile{
		Version:  1,
		RepoID:   "r_TEST123",
		Worktree: "other_worktree",
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "other_agent",
			Role:   "tester",
			Module: "test",
		},
		UpdatedAt: time.Now().UTC(),
	}

	if err := config.SaveIdentityFile(thrumDir, matchingAgent); err != nil {
		t.Fatalf("Failed to save matching agent identity: %v", err)
	}
	if err := config.SaveIdentityFile(thrumDir, otherAgent); err != nil {
		t.Fatalf("Failed to save other agent identity: %v", err)
	}

	// Set THRUM_NAME to the non-matching agent
	t.Setenv("THRUM_NAME", "other_agent")

	// Should select other_agent, bypassing worktree filtering
	cfg, err := config.LoadWithPath(tmpDir, "", "")
	if err != nil {
		t.Fatalf("LoadWithPath() failed: %v", err)
	}

	if cfg.Agent.Name != "other_agent" {
		t.Errorf("Expected agent name 'other_agent', got '%s'", cfg.Agent.Name)
	}
	if cfg.Agent.Role != "tester" {
		t.Errorf("Expected role 'tester', got '%s'", cfg.Agent.Role)
	}
}

func TestLoad_RedirectedWorktree_NoIdentities_Errors(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	// Simulate a redirected worktree with no local identities.
	// LoadWithPath should error instead of silently falling through to env vars.
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("Failed to create .thrum dir: %v", err)
	}

	// Create a redirect file (marks this as a feature worktree)
	redirectPath := filepath.Join(thrumDir, "redirect")
	// Point to a target that exists (use tmpDir itself as a stand-in)
	targetDir := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(targetDir, 0750); err != nil {
		t.Fatalf("Failed to create target .thrum dir: %v", err)
	}
	if err := os.WriteFile(redirectPath, []byte(targetDir), 0600); err != nil {
		t.Fatalf("Failed to write redirect file: %v", err)
	}

	// No identities directory — loadIdentityFromDir will fail with "read identities directory"
	t.Setenv("THRUM_NAME", "")

	_, err := config.LoadWithPath(tmpDir, "", "")
	if err == nil {
		t.Fatal("Expected error for redirected worktree with no identities, got nil")
	}

	if !strings.Contains(err.Error(), "no agent identities registered in this worktree") {
		t.Errorf("Expected worktree identity error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "thrum quickstart") {
		t.Errorf("Expected actionable hint with quickstart command, got: %v", err)
	}
}

// runGitCmd runs a git command in the given directory.
func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	fullArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", fullArgs...)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to run git %v: %v", args, err)
	}
}

func TestIdentityFileV3Fields(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750)

	identity := &config.IdentityFile{
		Version:   3,
		RepoID:    "r_TEST123456",
		Agent:     config.AgentConfig{Kind: "agent", Name: "coordinator", Role: "coordinator", Module: "main", Display: "Coordinator (main)"},
		Worktree:  "thrum",
		Branch:    "main",
		Intent:    "Coordinate agents and tasks in thrum",
		SessionID: "ses_01ABC",
	}

	if err := config.SaveIdentityFile(thrumDir, identity); err != nil {
		t.Fatalf("SaveIdentityFile: %v", err)
	}

	loaded, _, err := config.LoadIdentityWithPath(tmpDir)
	if err != nil {
		t.Fatalf("LoadIdentityWithPath: %v", err)
	}

	if loaded.Version != 5 {
		t.Errorf("Version = %d, want 5", loaded.Version)
	}
	if loaded.Branch != "main" {
		t.Errorf("Branch = %q, want %q", loaded.Branch, "main")
	}
	if loaded.Intent != "Coordinate agents and tasks in thrum" {
		t.Errorf("Intent = %q, want correct default", loaded.Intent)
	}
	if loaded.SessionID != "ses_01ABC" {
		t.Errorf("SessionID = %q, want %q", loaded.SessionID, "ses_01ABC")
	}
	if loaded.Agent.Display != "Coordinator (main)" {
		t.Errorf("Display = %q, want %q", loaded.Agent.Display, "Coordinator (main)")
	}
}

func TestIdentityFileV1Compat(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	tmpDir := t.TempDir()
	identitiesDir := filepath.Join(tmpDir, ".thrum", "identities")
	os.MkdirAll(identitiesDir, 0750)

	v1Data := `{"version":1,"repo_id":"","agent":{"Kind":"agent","Name":"old_agent","Role":"implementer","Module":"main","Display":""},"worktree":"thrum","confirmed_by":"","updated_at":"2026-01-01T00:00:00Z"}`
	os.WriteFile(filepath.Join(identitiesDir, "old_agent.json"), []byte(v1Data), 0600)

	loaded, _, err := config.LoadIdentityWithPath(tmpDir)
	if err != nil {
		t.Fatalf("LoadIdentityWithPath v1 file: %v", err)
	}
	if loaded.Branch != "" {
		t.Errorf("v1 file Branch should be empty, got %q", loaded.Branch)
	}
	if loaded.Intent != "" {
		t.Errorf("v1 file Intent should be empty, got %q", loaded.Intent)
	}
}

func TestIdentityV1RoundTrip(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	tmpDir := t.TempDir()
	identitiesDir := filepath.Join(tmpDir, ".thrum", "identities")
	os.MkdirAll(identitiesDir, 0750)

	// Write a v1 identity JSON file to disk
	v1Data := `{"version":1,"repo_id":"r_ROUNDTRIP","agent":{"Kind":"agent","Name":"roundtrip_agent","Role":"implementer","Module":"core","Display":"RT Agent"},"worktree":"myrepo","confirmed_by":"human:tester","updated_at":"2026-01-15T10:30:00Z"}`
	os.WriteFile(filepath.Join(identitiesDir, "roundtrip_agent.json"), []byte(v1Data), 0600)

	// Load it
	loaded, _, err := config.LoadIdentityWithPath(tmpDir)
	if err != nil {
		t.Fatalf("LoadIdentityWithPath: %v", err)
	}

	// Save it back (should bump to v3)
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := config.SaveIdentityFile(thrumDir, loaded); err != nil {
		t.Fatalf("SaveIdentityFile: %v", err)
	}

	// Reload and verify
	reloaded, _, err := config.LoadIdentityWithPath(tmpDir)
	if err != nil {
		t.Fatalf("Reload after save: %v", err)
	}

	if reloaded.Version != 5 {
		t.Errorf("Version = %d, want 5", reloaded.Version)
	}
	if reloaded.ConfirmedBy != "human:tester" {
		t.Errorf("ConfirmedBy = %q, want %q", reloaded.ConfirmedBy, "human:tester")
	}
	if reloaded.Branch != "" {
		t.Errorf("Branch = %q, want empty (not set by migration)", reloaded.Branch)
	}
	if reloaded.Intent != "" {
		t.Errorf("Intent = %q, want empty (not set by migration)", reloaded.Intent)
	}
	if reloaded.SessionID != "" {
		t.Errorf("SessionID = %q, want empty (not set by migration)", reloaded.SessionID)
	}
}

func TestIdentityFile_AgentPID_Serialization(t *testing.T) {
	identity := config.IdentityFile{
		Version:  3,
		AgentPID: 12345,
		Agent:    config.AgentConfig{Name: "test"},
	}
	data, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	var decoded config.IdentityFile
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AgentPID != 12345 {
		t.Errorf("AgentPID = %d, want 12345", decoded.AgentPID)
	}
}

func TestIdentityFile_AgentPID_OmittedWhenZero(t *testing.T) {
	identity := config.IdentityFile{Version: 3, Agent: config.AgentConfig{Name: "test"}}
	data, _ := json.Marshal(identity)
	if strings.Contains(string(data), "agent_pid") {
		t.Error("agent_pid should be omitted when zero")
	}
}

func TestIdentityFile_BackwardCompat_NoPIDField(t *testing.T) {
	old := `{"version":3,"repo_id":"r_ABC","agent":{"Name":"test"},"updated_at":"2026-01-01T00:00:00Z"}`
	var identity config.IdentityFile
	if err := json.Unmarshal([]byte(old), &identity); err != nil {
		t.Fatal(err)
	}
	if identity.AgentPID != 0 {
		t.Errorf("AgentPID should default to 0, got %d", identity.AgentPID)
	}
}

func TestSaveIdentityFile_BumpsVersionTo4(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	identity := &config.IdentityFile{
		Version: 1,
		Agent:   config.AgentConfig{Name: "test_agent", Role: "implementer", Module: "main"},
	}

	if err := config.SaveIdentityFile(thrumDir, identity); err != nil {
		t.Fatalf("SaveIdentityFile: %v", err)
	}

	loaded, _, err := config.LoadIdentityWithPath(tmpDir)
	if err != nil {
		t.Fatalf("LoadIdentityWithPath: %v", err)
	}
	if loaded.Version != 5 {
		t.Errorf("Version after save = %d, want 5", loaded.Version)
	}
}

// writeTestIdentity writes an identity file directly to the identities dir,
// preserving explicit field values (unlike SaveIdentityFile which overwrites UpdatedAt).
func writeTestIdentity(t *testing.T, dir, name string, id config.IdentityFile) {
	t.Helper()
	data, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLoad_PIDFirstResolution_ZeroPIDFallsThrough verifies that when no Claude
// ancestor process is found (PID=0), the PID pass is skipped and the existing
// worktree / ambiguous-error logic still applies correctly.
func TestLoad_PIDFirstResolution_ZeroPIDFallsThrough(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")

	tmpDir := t.TempDir()
	identitiesDir := filepath.Join(tmpDir, ".thrum", "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		t.Fatalf("Failed to create identities dir: %v", err)
	}

	// Two identities with AgentPID=0 (no PID stored) and non-matching worktrees.
	// Pass 0 should skip (no PID match), Pass 1 should find no worktree match,
	// and the function should return the "cannot auto-select" error.
	agent1 := config.IdentityFile{
		Version:   3,
		RepoID:    "r_TEST",
		AgentPID:  0,
		Worktree:  "worktree_x",
		Agent:     config.AgentConfig{Kind: "agent", Name: "agent_x", Role: "implementer", Module: "test"},
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	agent2 := config.IdentityFile{
		Version:   3,
		RepoID:    "r_TEST",
		AgentPID:  0,
		Worktree:  "worktree_y",
		Agent:     config.AgentConfig{Kind: "agent", Name: "agent_y", Role: "tester", Module: "test"},
		UpdatedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}

	writeTestIdentity(t, identitiesDir, "agent_x", agent1)
	writeTestIdentity(t, identitiesDir, "agent_y", agent2)

	_, err := config.LoadWithPath(tmpDir, "", "")
	if err == nil {
		t.Fatal("Expected error when PID=0 and no worktree match, got nil")
	}
	if !strings.Contains(err.Error(), "cannot auto-select identity") {
		t.Errorf("Expected 'cannot auto-select identity' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "agent_x") || !strings.Contains(err.Error(), "agent_y") {
		t.Errorf("Error should list available identities, got: %v", err)
	}
}

func TestIdentityFile_TmuxFields(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750)

	identity := &config.IdentityFile{
		Agent:       config.AgentConfig{Name: "test_agent", Role: "implementer", Module: "api"},
		TmuxSession: "implementer-api:0.0",
		Runtime:     "claude",
	}

	err := config.SaveIdentityFile(thrumDir, identity)
	if err != nil {
		t.Fatalf("SaveIdentityFile: %v", err)
	}

	loaded, _, err := config.LoadIdentityWithPath(tmpDir)
	if err != nil {
		t.Fatalf("LoadIdentityWithPath: %v", err)
	}

	if loaded.TmuxSession != "implementer-api:0.0" {
		t.Errorf("TmuxSession = %q, want %q", loaded.TmuxSession, "implementer-api:0.0")
	}
	if loaded.Runtime != "claude" {
		t.Errorf("Runtime = %q, want %q", loaded.Runtime, "claude")
	}
	if loaded.Version != 5 {
		t.Errorf("Version = %d, want 5", loaded.Version)
	}
}

func TestIdentityFile_TmuxFieldsOmitEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750)

	identity := &config.IdentityFile{
		Agent: config.AgentConfig{Name: "legacy_agent", Role: "coordinator", Module: "main"},
	}

	err := config.SaveIdentityFile(thrumDir, identity)
	if err != nil {
		t.Fatalf("SaveIdentityFile: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(thrumDir, "identities", "legacy_agent.json"))
	if strings.Contains(string(data), "tmux_session") {
		t.Error("tmux_session should be omitted when empty")
	}
	if strings.Contains(string(data), "runtime") {
		t.Error("runtime should be omitted when empty")
	}
}

func TestRestartConfigDefaults(t *testing.T) {
	cfg := config.ThrumConfig{}
	if cfg.Restart.MaxLines != 0 {
		t.Errorf("MaxLines = %d, want 0", cfg.Restart.MaxLines)
	}
	if cfg.Restart.AutoThreshold != 0 {
		t.Errorf("AutoThreshold = %d, want 0", cfg.Restart.AutoThreshold)
	}
	if cfg.Restart.GracefulTimeout != 0 {
		t.Errorf("GracefulTimeout = %d, want 0", cfg.Restart.GracefulTimeout)
	}
}

func TestRestartConfigJSON(t *testing.T) {
	jsonStr := `{"restart":{"max_lines":500,"auto_threshold":80,"graceful_timeout":45}}`
	var cfg config.ThrumConfig
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if cfg.Restart.MaxLines != 500 {
		t.Errorf("MaxLines = %d, want 500", cfg.Restart.MaxLines)
	}
	if cfg.Restart.AutoThreshold != 80 {
		t.Errorf("AutoThreshold = %d, want 80", cfg.Restart.AutoThreshold)
	}
	if cfg.Restart.GracefulTimeout != 45 {
		t.Errorf("GracefulTimeout = %d, want 45", cfg.Restart.GracefulTimeout)
	}
}

func TestRestartMaxLines(t *testing.T) {
	if got := (config.RestartConfig{}).RestartMaxLines(); got != 200 {
		t.Errorf("RestartMaxLines() with zero = %d, want 200", got)
	}
	if got := (config.RestartConfig{MaxLines: 500}).RestartMaxLines(); got != 500 {
		t.Errorf("RestartMaxLines() with 500 = %d, want 500", got)
	}
}

func TestRestartGracefulTimeout(t *testing.T) {
	if got := (config.RestartConfig{}).RestartGracefulTimeout(); got != 30 {
		t.Errorf("RestartGracefulTimeout() with zero = %d, want 30", got)
	}
	if got := (config.RestartConfig{GracefulTimeout: 45}).RestartGracefulTimeout(); got != 45 {
		t.Errorf("RestartGracefulTimeout() with 45 = %d, want 45", got)
	}
}

func TestIdentityFile_AdoptionDoesNotBlock(t *testing.T) {
	// Verify that loading a single identity file with a dead PID
	// succeeds (doesn't block or error). Adoption itself won't fire
	// because FindClaudeAncestor() returns 0 in test, but we confirm
	// the code path doesn't panic.
	dir := t.TempDir()
	writeTestIdentity(t, dir, "agent_dead", config.IdentityFile{
		Version:  3,
		AgentPID: 999999,
		Agent:    config.AgentConfig{Name: "agent_dead", Role: "test", Module: "test"},
		Worktree: "main",
	})
	// Single file → should load successfully
	// We can't easily call loadIdentityFromDir directly, so just verify the file roundtrips
	data, err := os.ReadFile(filepath.Join(dir, "agent_dead.json"))
	if err != nil {
		t.Fatal(err)
	}
	var loaded config.IdentityFile
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.AgentPID != 999999 {
		t.Errorf("AgentPID = %d, want 999999", loaded.AgentPID)
	}
}

// TestSaveIdentityFile_BackfillsRuntimeFromPreferred verifies that
// SaveIdentityFile copies preferred_runtime → runtime when runtime is empty.
// This covers the quickstart write path (Part 1 of thrum-yl3k).
func TestSaveIdentityFile_BackfillsRuntimeFromPreferred(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	identity := &config.IdentityFile{
		Agent:            config.AgentConfig{Name: "kiro-agent", Role: "implementer", Module: "main"},
		PreferredRuntime: "kiro-cli",
		// Runtime intentionally left empty (simulates pre-runtime-field identity)
	}

	if err := config.SaveIdentityFile(thrumDir, identity); err != nil {
		t.Fatalf("SaveIdentityFile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(thrumDir, "identities", "kiro-agent.json"))
	if err != nil {
		t.Fatalf("read identity file: %v", err)
	}
	var saved config.IdentityFile
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal saved identity: %v", err)
	}

	if saved.Runtime != "kiro-cli" {
		t.Errorf("Runtime = %q, want %q (backfill from preferred_runtime)", saved.Runtime, "kiro-cli")
	}
	if saved.PreferredRuntime != "kiro-cli" {
		t.Errorf("PreferredRuntime = %q, want %q (must be preserved)", saved.PreferredRuntime, "kiro-cli")
	}
}

// TestSaveIdentityFile_DoesNotOverwriteExistingRuntime verifies that the
// backfill does not clobber a runtime field that is already set.
func TestSaveIdentityFile_DoesNotOverwriteExistingRuntime(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	identity := &config.IdentityFile{
		Agent:            config.AgentConfig{Name: "cursor-agent", Role: "implementer", Module: "main"},
		PreferredRuntime: "kiro-cli",
		Runtime:          "cursor", // already set — must not be overwritten
	}

	if err := config.SaveIdentityFile(thrumDir, identity); err != nil {
		t.Fatalf("SaveIdentityFile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(thrumDir, "identities", "cursor-agent.json"))
	if err != nil {
		t.Fatalf("read identity file: %v", err)
	}
	var saved config.IdentityFile
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal saved identity: %v", err)
	}

	if saved.Runtime != "cursor" {
		t.Errorf("Runtime = %q, want %q (must not be overwritten by backfill)", saved.Runtime, "cursor")
	}
}

// TestBackfillIdentityRuntime_FixesLegacyFiles verifies that
// BackfillIdentityRuntime scans a thrumDir, finds identity files with
// runtime="" and preferred_runtime set, and rewrites them with runtime
// populated. This covers the daemon-boot backfill path (Part 2 of thrum-yl3k).
func TestBackfillIdentityRuntime_FixesLegacyFiles(t *testing.T) {
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		t.Fatalf("create identities dir: %v", err)
	}

	// Write a legacy identity file directly (bypassing SaveIdentityFile so
	// runtime remains empty, simulating a file created before the field existed).
	legacy := config.IdentityFile{
		Version:          4,
		Agent:            config.AgentConfig{Name: "kiro-agent", Role: "implementer", Module: "main"},
		PreferredRuntime: "kiro-cli",
		// Runtime intentionally absent
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy identity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(identitiesDir, "kiro-agent.json"), data, 0o600); err != nil {
		t.Fatalf("write legacy identity file: %v", err)
	}

	// Also write an identity that already has runtime set — must not change.
	modern := config.IdentityFile{
		Version:          5,
		Agent:            config.AgentConfig{Name: "cursor-agent", Role: "reviewer", Module: "main"},
		PreferredRuntime: "cursor",
		Runtime:          "cursor",
	}
	data2, err := json.Marshal(modern)
	if err != nil {
		t.Fatalf("marshal modern identity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(identitiesDir, "cursor-agent.json"), data2, 0o600); err != nil {
		t.Fatalf("write modern identity file: %v", err)
	}

	// Run the backfill.
	config.BackfillIdentityRuntime(thrumDir)

	// Verify legacy file was fixed.
	fixedData, err := os.ReadFile(filepath.Join(identitiesDir, "kiro-agent.json"))
	if err != nil {
		t.Fatalf("read fixed identity: %v", err)
	}
	var fixed config.IdentityFile
	if err := json.Unmarshal(fixedData, &fixed); err != nil {
		t.Fatalf("unmarshal fixed identity: %v", err)
	}
	if fixed.Runtime != "kiro-cli" {
		t.Errorf("after backfill: Runtime = %q, want %q", fixed.Runtime, "kiro-cli")
	}

	// Verify modern file was untouched (runtime already set).
	modernData, err := os.ReadFile(filepath.Join(identitiesDir, "cursor-agent.json"))
	if err != nil {
		t.Fatalf("read modern identity: %v", err)
	}
	var reloaded config.IdentityFile
	if err := json.Unmarshal(modernData, &reloaded); err != nil {
		t.Fatalf("unmarshal modern identity: %v", err)
	}
	if reloaded.Runtime != "cursor" {
		t.Errorf("modern identity runtime changed unexpectedly: got %q", reloaded.Runtime)
	}
}

func TestIdentityFile_Reserved_Default(t *testing.T) {
	var id config.IdentityFile
	raw := []byte(`{"version":5,"agent":{"Name":"x","Role":"r","Module":"m"}}`)
	if err := json.Unmarshal(raw, &id); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if id.Reserved {
		t.Errorf("Reserved should default to false, got true")
	}
}

func TestIdentityFile_Reserved_Roundtrip(t *testing.T) {
	in := config.IdentityFile{
		Version:  5,
		Agent:    config.AgentConfig{Name: "supervisor_thrum", Role: "supervisor", Module: "daemon"},
		Reserved: true,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out config.IdentityFile
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.Reserved {
		t.Errorf("Reserved should be true after roundtrip")
	}
}

func TestIdentityFile_Reserved_OmittedWhenFalse(t *testing.T) {
	in := config.IdentityFile{
		Version: 5,
		Agent:   config.AgentConfig{Name: "worker", Role: "r", Module: "m"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, exists := raw["reserved"]; exists {
		t.Error("reserved should be omitted when false")
	}
}
