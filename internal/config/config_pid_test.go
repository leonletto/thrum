package config

// Unit tests for PID-first identity resolution and adoption logic.
// These use the internal package to call loadIdentityFromDir directly,
// since process.FindClaudeAncestor() returns 0 in test environments
// (no "claude" ancestor process), making it impossible to exercise
// PID-first resolution through the public API alone.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// writeIdentityInternal writes an IdentityFile directly to dirPath/name.json,
// preserving explicit field values (unlike SaveIdentityFile which overwrites UpdatedAt).
func writeIdentityInternal(t *testing.T, dir, name string, id IdentityFile) {
	t.Helper()
	data, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("marshal identity %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0600); err != nil {
		t.Fatalf("write identity file %s: %v", name, err)
	}
}

// runGitCmdInternal runs a git command in the given directory for internal package tests.
func runGitCmdInternal(t *testing.T, dir string, args ...string) {
	t.Helper()
	fullArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", fullArgs...) // #nosec G204 -- dir is a t.TempDir() path
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

// TestLoadIdentity_PIDMatch verifies that when two identity files exist and one
// has a AgentPID matching the current process, resolution still succeeds and
// returns a valid identity.
//
// In a live Claude session: process.FindClaudeAncestor() returns the Claude PID,
// the PID-first pass matches agent_a, and agent_a is returned.
//
// In test/CI environments: process.FindClaudeAncestor() returns 0, so the PID
// pass is skipped. The test verifies that resolution still succeeds (worktree
// or most-recent fallback applies) and does not panic or error.
func TestLoadIdentity_PIDMatch(t *testing.T) {
	repoDir := t.TempDir()
	runGitCmdInternal(t, repoDir, "init")
	runGitCmdInternal(t, repoDir, "config", "user.name", "Test User")
	runGitCmdInternal(t, repoDir, "config", "user.email", "test@example.com")

	worktreeName := filepath.Base(repoDir)
	identitiesDir := filepath.Join(repoDir, ".thrum", "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		t.Fatalf("create identities dir: %v", err)
	}

	myPID := os.Getpid() // use test process PID as stand-in

	// agent_a has the current process PID and more recent UpdatedAt.
	writeIdentityInternal(t, identitiesDir, "agent_a", IdentityFile{
		Version:   3,
		AgentPID:  myPID,
		Agent:     AgentConfig{Kind: "agent", Name: "agent_a", Role: "implementer", Module: "test"},
		Worktree:  worktreeName,
		UpdatedAt: time.Now(),
	})
	// agent_b has a dead PID and older UpdatedAt.
	writeIdentityInternal(t, identitiesDir, "agent_b", IdentityFile{
		Version:   3,
		AgentPID:  999999, // dead PID
		Agent:     AgentConfig{Kind: "agent", Name: "agent_b", Role: "tester", Module: "test"},
		Worktree:  worktreeName,
		UpdatedAt: time.Now().Add(-time.Hour),
	})

	result, err := loadIdentityFromDir(identitiesDir, "")
	if err != nil {
		t.Fatalf("loadIdentityFromDir failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected a resolved identity, got nil")
	}

	// In a Claude session: PID-first pass returns agent_a.
	// In CI (FindClaudeAncestor=0): worktree pass runs, both match, agent_a wins
	// by most-recent UpdatedAt. Either way agent_a is returned.
	if result.Agent.Name != "agent_a" {
		t.Errorf("expected agent_a, got %s", result.Agent.Name)
	}
}

// TestLoadIdentity_NoPIDMatch_FallsThrough verifies that when all identity files
// have dead PIDs, the PID-first pass is skipped and resolution falls through to
// the existing worktree / most-recent UpdatedAt logic.
func TestLoadIdentity_NoPIDMatch_FallsThrough(t *testing.T) {
	repoDir := t.TempDir()
	runGitCmdInternal(t, repoDir, "init")
	runGitCmdInternal(t, repoDir, "config", "user.name", "Test User")
	runGitCmdInternal(t, repoDir, "config", "user.email", "test@example.com")

	worktreeName := filepath.Base(repoDir)
	identitiesDir := filepath.Join(repoDir, ".thrum", "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		t.Fatalf("create identities dir: %v", err)
	}

	// Both have dead PIDs and the same worktree — most-recent UpdatedAt should win.
	writeIdentityInternal(t, identitiesDir, "agent_a", IdentityFile{
		Version:   3,
		AgentPID:  999998, // dead PID
		Agent:     AgentConfig{Kind: "agent", Name: "agent_a", Role: "implementer", Module: "test"},
		Worktree:  worktreeName,
		UpdatedAt: time.Now().Add(-time.Hour), // older
	})
	writeIdentityInternal(t, identitiesDir, "agent_b", IdentityFile{
		Version:   3,
		AgentPID:  999999, // dead PID
		Agent:     AgentConfig{Kind: "agent", Name: "agent_b", Role: "tester", Module: "test"},
		Worktree:  worktreeName,
		UpdatedAt: time.Now(), // more recent
	})

	result, err := loadIdentityFromDir(identitiesDir, "")
	if err != nil {
		t.Fatalf("loadIdentityFromDir failed: %v", err)
	}

	// PID pass: claudePID=0 (not in Claude session) → skipped.
	// Worktree pass: both match → most-recent UpdatedAt wins → agent_b.
	if result.Agent.Name != "agent_b" {
		t.Errorf("expected agent_b (most recent UpdatedAt), got %s", result.Agent.Name)
	}
}

// TestLoadIdentity_AdoptsDeadPID verifies that a single identity file with a
// dead PID (999999) is loaded successfully. The adoption code path fires when
// FindClaudeAncestor() returns a non-zero PID (live Claude session). In test
// environments FindClaudeAncestor() returns 0 so adoption is skipped, but the
// identity is still returned without error (no blocking or panic).
func TestLoadIdentity_AdoptsDeadPID(t *testing.T) {
	dir := t.TempDir()

	writeIdentityInternal(t, dir, "agent_a", IdentityFile{
		Version:   3,
		AgentPID:  999999, // dead PID
		Agent:     AgentConfig{Kind: "agent", Name: "agent_a", Role: "implementer", Module: "test"},
		Worktree:  "main",
		UpdatedAt: time.Now(),
	})

	result, err := loadIdentityFromDir(dir, "")
	if err != nil {
		t.Fatalf("loadIdentityFromDir failed: %v", err)
	}

	// Single identity file: always loaded directly regardless of PID state.
	// Adoption fires if FindClaudeAncestor() > 0; in tests it returns 0 (no-op).
	if result.Agent.Name != "agent_a" {
		t.Errorf("expected agent_a, got %s", result.Agent.Name)
	}
}
