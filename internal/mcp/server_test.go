package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// createTestIdentity sets up a minimal identity file for testing.
func createTestIdentity(t *testing.T, repoPath, name, role, module string) {
	t.Helper()

	identDir := filepath.Join(repoPath, ".thrum", "identities")
	if err := os.MkdirAll(identDir, 0o750); err != nil {
		t.Fatalf("create identities dir: %v", err)
	}

	identity := map[string]any{
		"version": 1,
		"repo_id": "test-repo-123",
		"agent": map[string]any{
			"kind":    "agent",
			"name":    name,
			"role":    role,
			"module":  module,
			"display": name,
		},
		"worktree":     "test",
		"confirmed_by": "test",
		"updated_at":   time.Now().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}

	if err := os.WriteFile(filepath.Join(identDir, name+".json"), data, 0o600); err != nil {
		t.Fatalf("write identity file: %v", err)
	}
}

func TestNewServer(t *testing.T) {
	repoPath := t.TempDir()
	createTestIdentity(t, repoPath, "testbot", "implementer", "core")

	s, err := NewServer(repoPath)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if s.agentName != "testbot" {
		t.Errorf("expected agentName 'testbot', got %q", s.agentName)
	}
	if s.agentRole != "implementer" {
		t.Errorf("expected agentRole 'implementer', got %q", s.agentRole)
	}
	if s.agentID != "testbot" {
		t.Errorf("expected agentID 'testbot', got %q", s.agentID)
	}
	if s.version != "dev" {
		t.Errorf("expected default version 'dev', got %q", s.version)
	}
	if s.server == nil {
		t.Fatal("expected MCP server to be created")
	}
}

func TestNewServerWithVersion(t *testing.T) {
	repoPath := t.TempDir()
	createTestIdentity(t, repoPath, "testbot", "reviewer", "api")

	s, err := NewServer(repoPath, WithVersion("1.0.0"))
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if s.version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", s.version)
	}
}

func TestNewServerNoIdentity(t *testing.T) {
	repoPath := t.TempDir()

	_, err := NewServer(repoPath)
	if err == nil {
		t.Fatal("expected error when no identity file exists")
	}
}

func TestNewServerMultipleIdentities(t *testing.T) {
	repoPath := t.TempDir()
	createTestIdentity(t, repoPath, "alice", "implementer", "core")
	createTestIdentity(t, repoPath, "bob", "reviewer", "api")

	// Without THRUM_NAME, should fail with ambiguous identity
	_, err := NewServer(repoPath)
	if err == nil {
		t.Fatal("expected error with multiple identities and no THRUM_NAME")
	}

	// With THRUM_NAME, should select the right identity
	t.Setenv("THRUM_NAME", "alice")
	s, err := NewServer(repoPath)
	if err != nil {
		t.Fatalf("NewServer with THRUM_NAME failed: %v", err)
	}
	if s.agentName != "alice" {
		t.Errorf("expected agentName 'alice', got %q", s.agentName)
	}
}

func TestNewServerSocketPath(t *testing.T) {
	repoPath := t.TempDir()
	createTestIdentity(t, repoPath, "testbot", "implementer", "core")

	s, err := NewServer(repoPath)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Socket path should end with var/thrum.sock
	expected := filepath.Join(repoPath, ".thrum", "var", "thrum.sock")
	if s.socketPath != expected {
		t.Errorf("expected socketPath %q, got %q", expected, s.socketPath)
	}
}

func TestNewDaemonClient(t *testing.T) {
	repoPath := t.TempDir()
	createTestIdentity(t, repoPath, "testbot", "implementer", "core")

	s, err := NewServer(repoPath)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// newDaemonClient should fail because there's no daemon running
	_, err = s.newDaemonClient()
	if err == nil {
		t.Fatal("expected error when no daemon socket exists")
	}
}
