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
	// Ensure no env vars interfere
	_ = os.Unsetenv("THRUM_ROLE")
	_ = os.Unsetenv("THRUM_MODULE")
	defer func() {
		_ = os.Unsetenv("THRUM_ROLE")
		_ = os.Unsetenv("THRUM_MODULE")
	}()

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
	// No identity file in temp dir
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	_, err := config.Load("", "")
	if err == nil {
		t.Fatal("Expected error when role and module are missing, got nil")
	}
}

func TestLoad_ThrumNameEnvVar_SelectsSpecificIdentity(t *testing.T) {
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

func TestLoad_ThrumNameEnvVar_ErrorOnNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Create one identity file
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

	// Set THRUM_NAME to nonexistent agent
	t.Setenv("THRUM_NAME", "nonexistent")

	_, err := config.LoadWithPath(tmpDir, "", "")
	if err == nil {
		t.Fatal("Expected error when THRUM_NAME points to nonexistent file, got nil")
	}
}

func TestLoad_MultipleIdentities_ErrorWithoutThrumName(t *testing.T) {
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

	if loaded.Version != 3 {
		t.Errorf("Version = %d, want 3", loaded.Version)
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

	if reloaded.Version != 3 {
		t.Errorf("Version = %d, want 3", reloaded.Version)
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

func TestSaveIdentityFile_BumpsVersionTo3(t *testing.T) {
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
	if loaded.Version != 3 {
		t.Errorf("Version after save = %d, want 3", loaded.Version)
	}
}
