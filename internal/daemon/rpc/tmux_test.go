package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

func TestTmuxHandler_HandleStatus_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750)

	handler := NewTmuxHandler(thrumDir, nil)

	result, err := handler.HandleStatus(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("HandleStatus: %v", err)
	}

	resp, ok := result.(*TmuxStatusResponse)
	if !ok {
		t.Fatalf("expected *TmuxStatusResponse, got %T", result)
	}

	if len(resp.Sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(resp.Sessions))
	}
}

func TestTmuxHandler_HandleStatus_WithIdentities(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	os.MkdirAll(identitiesDir, 0750)

	// Create an identity with tmux_session set (session won't exist — should be "dead")
	idFile := config.IdentityFile{
		Version:     4,
		TmuxSession: "thrum-unit-test-nonexistent-session:0.0",
		Runtime:     "claude",
		Agent: config.AgentConfig{
			Name:   "test_agent",
			Role:   "implementer",
			Module: "api",
		},
		Branch: "feature/test",
	}
	data, _ := json.MarshalIndent(idFile, "", "  ")
	os.WriteFile(filepath.Join(identitiesDir, "test_agent.json"), data, 0600)

	handler := NewTmuxHandler(thrumDir, nil)
	result, err := handler.HandleStatus(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("HandleStatus: %v", err)
	}

	resp := result.(*TmuxStatusResponse)
	if len(resp.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(resp.Sessions))
	}

	info := resp.Sessions[0]
	if info.Name != "thrum-unit-test-nonexistent-session" {
		t.Errorf("Name = %q, want %q", info.Name, "thrum-unit-test-nonexistent-session")
	}
	if info.Agent != "test_agent" {
		t.Errorf("Agent = %q, want %q", info.Agent, "test_agent")
	}
	if info.State != "dead" {
		t.Errorf("State = %q, want %q (session doesn't exist)", info.State, "dead")
	}
	if info.Runtime != "claude" {
		t.Errorf("Runtime = %q, want %q", info.Runtime, "claude")
	}
}

func TestTmuxHandler_HandleStatus_NoIdentitiesDir(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	// Don't create identities dir

	handler := NewTmuxHandler(thrumDir, nil)
	result, err := handler.HandleStatus(context.Background(), json.RawMessage("{}"))
	if err != nil {
		t.Fatalf("HandleStatus: %v", err)
	}

	resp := result.(*TmuxStatusResponse)
	if len(resp.Sessions) != 0 {
		t.Errorf("expected 0 sessions with no identities dir, got %d", len(resp.Sessions))
	}
}

func TestTmuxHandler_HandleCreate_MissingFields(t *testing.T) {
	handler := NewTmuxHandler(t.TempDir(), nil)

	// Missing name
	_, err := handler.HandleCreate(context.Background(), json.RawMessage(`{"cwd":"/tmp"}`))
	if err == nil {
		t.Error("expected error for missing name")
	}

	// Missing cwd
	_, err = handler.HandleCreate(context.Background(), json.RawMessage(`{"name":"test"}`))
	if err == nil {
		t.Error("expected error for missing cwd")
	}
}

func TestTmuxHandler_HandleCreate_MissingQuickstartFlags(t *testing.T) {
	handler := NewTmuxHandler(t.TempDir(), nil)
	// No agent_name, role, module, no no_agent flag
	params := json.RawMessage(`{"name":"test-session","cwd":"` + t.TempDir() + `"}`)
	_, err := handler.HandleCreate(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing quickstart flags")
	}
	if !strings.Contains(err.Error(), "quickstart flags required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTmuxHandler_HandleCreate_NoAgentSkipsValidation(t *testing.T) {
	handler := NewTmuxHandler(t.TempDir(), nil)
	cwd := t.TempDir()
	params, _ := json.Marshal(map[string]any{
		"name":     "test-session",
		"cwd":      cwd,
		"no_agent": true,
	})
	// This will fail at CreateSession (no real tmux) but should pass
	// quickstart validation — error must NOT be about missing flags
	_, err := handler.HandleCreate(context.Background(), json.RawMessage(params))
	if err != nil && strings.Contains(err.Error(), "quickstart flags required") {
		t.Error("should not require quickstart flags when no_agent=true")
	}
	// Verify no redirect was created (--no-agent skips redirect setup)
	if _, statErr := os.Stat(filepath.Join(cwd, ".thrum", "redirect")); !os.IsNotExist(statErr) {
		t.Error(".thrum/redirect should not be created with --no-agent")
	}
}

func TestTmuxHandler_HandleCreate_NotAWorktree(t *testing.T) {
	handler := NewTmuxHandler(t.TempDir(), nil)
	// cwd is a regular dir (no .git file), not a worktree
	cwd := t.TempDir()
	params, _ := json.Marshal(map[string]any{
		"name":       "test-session",
		"cwd":        cwd,
		"agent_name": "test_agent",
		"role":       "implementer",
		"module":     "test",
	})
	_, err := handler.HandleCreate(context.Background(), json.RawMessage(params))
	if err == nil {
		t.Fatal("expected error for non-worktree cwd")
	}
	if !strings.Contains(err.Error(), "is not a git worktree") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTmuxHandler_HandleLaunch_MissingName(t *testing.T) {
	handler := NewTmuxHandler(t.TempDir(), nil)

	_, err := handler.HandleLaunch(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestTmuxHandler_HandleLaunch_NoSession(t *testing.T) {
	handler := NewTmuxHandler(t.TempDir(), nil)

	_, err := handler.HandleLaunch(context.Background(), json.RawMessage(`{"name":"nonexistent"}`))
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestTmuxHandler_ClearTmuxFromIdentities(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	identitiesDir := filepath.Join(thrumDir, "identities")
	os.MkdirAll(identitiesDir, 0750)

	// Create identity with tmux_session set
	idFile := config.IdentityFile{
		Version:     4,
		TmuxSession: "target-session:0.0",
		Runtime:     "claude",
		Agent:       config.AgentConfig{Name: "agent1", Role: "impl", Module: "test"},
	}
	data, _ := json.MarshalIndent(idFile, "", "  ")
	os.WriteFile(filepath.Join(identitiesDir, "agent1.json"), data, 0600)

	handler := NewTmuxHandler(thrumDir, nil)
	handler.clearTmuxFromIdentities("target-session")

	// Verify tmux_session was cleared
	updated, _ := os.ReadFile(filepath.Join(identitiesDir, "agent1.json"))
	var reloaded config.IdentityFile
	json.Unmarshal(updated, &reloaded)

	if reloaded.TmuxSession != "" {
		t.Errorf("TmuxSession should be empty after clear, got %q", reloaded.TmuxSession)
	}
	if reloaded.Runtime != "" {
		t.Errorf("Runtime should be empty after clear, got %q", reloaded.Runtime)
	}
}
