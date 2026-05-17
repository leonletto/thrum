package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// newArchiveTestHandler wires up a SessionArchiveHandler against a
// fresh state + thrumDir under t.TempDir() and registers one agent.
// Returns the handler, the agent_id, and the snapshot path the test
// should write to.
func newArchiveTestHandler(t *testing.T, mode string) (*SessionArchiveHandler, string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o700); err != nil {
		t.Fatalf("mkdir thrumDir: %v", err)
	}

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_archive", "")
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Register agent with the requested mode so the SessionsDir routing
	// + AgentRegistry.Lookup both have something to find.
	identity := "long_lived"
	if mode == "ephemeral" {
		identity = "ephemeral"
	}
	registerReq := RegisterRequest{
		Role:        "implementer",
		Module:      "test",
		Mode:        mode,
		Identity:    identity,
		AutoRespawn: false,
	}
	registerJSON, _ := json.Marshal(registerReq)
	registerResp, err := NewAgentHandler(s).HandleRegister(context.Background(), registerJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	regResp, ok := registerResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", registerResp)
	}
	agentID := regResp.AgentID

	// HandleRegister inserts the agents-table row but production
	// identity-file writing happens in the CLI/quickstart/tmux flows,
	// not the RPC. resolveWorktreeThrumDir scans identity files, so
	// the test must provide one. Single-worktree fixture: identity
	// file lives in the same thrumDir.
	idFile := &config.IdentityFile{
		Version:   5,
		RepoID:    "test_repo_archive",
		Agent:     config.AgentConfig{Name: agentID, Role: "implementer", Module: "test"},
		Worktree:  thrumDir,
		UpdatedAt: time.Now().UTC(),
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity file: %v", err)
	}

	h := NewSessionArchiveHandler(s, thrumDir)

	srcPath := filepath.Join(thrumDir, "restart", agentID+".md")
	return h, agentID, srcPath
}

// writeArchiveSnapshot creates a snapshot file with YAML frontmatter +
// §1 Big picture body at path.
func writeArchiveSnapshot(t *testing.T, path, savedAt, bigPicture string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir snapshot parent: %v", err)
	}
	content := "---\nagent: x\nsession_id: ses_x\nsaved_at: " + savedAt + "\nreason: manual\nmachine_id: t\n---\n\n## 1. Big picture — what shipped this session\n\n" + bigPicture + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
}

func TestHandleSessionArchive_EmptyAgentID_ReturnsRequiredError(t *testing.T) {
	h, _, _ := newArchiveTestHandler(t, "persistent")

	params, _ := json.Marshal(SessionArchiveRequest{AgentID: ""})
	_, err := h.HandleArchive(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for empty agent_id, got nil")
	}
	if !strings.Contains(err.Error(), "agent_id is required") {
		t.Errorf("expected 'agent_id is required' in error, got: %v", err)
	}
}

func TestHandleSessionArchive_UnknownAgent_ReturnsNotRegistered(t *testing.T) {
	h, _, _ := newArchiveTestHandler(t, "persistent")

	params, _ := json.Marshal(SessionArchiveRequest{AgentID: "ghost-agent"})
	_, err := h.HandleArchive(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for unknown agent, got nil")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Errorf("expected 'not registered' in error, got: %v", err)
	}
}

func TestHandleSessionArchive_PersistentAgent_NoSnapshot_ReturnsNullNull(t *testing.T) {
	h, agentID, _ := newArchiveTestHandler(t, "persistent")

	params, _ := json.Marshal(SessionArchiveRequest{AgentID: agentID})
	resp, err := h.HandleArchive(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, ok := resp.(*SessionArchiveResponse)
	if !ok {
		t.Fatalf("expected *SessionArchiveResponse, got %T", resp)
	}
	if r.ArchivedPath != nil || r.BigPicture != nil {
		t.Errorf("expected null/null for missing snapshot, got %+v", r)
	}
}

func TestHandleSessionArchive_PersistentAgent_ValidSnapshot_Archives(t *testing.T) {
	h, agentID, srcPath := newArchiveTestHandler(t, "persistent")
	writeArchiveSnapshot(t, srcPath, "2026-05-17T15:32:18.421Z", "Locked the spec.")

	params, _ := json.Marshal(SessionArchiveRequest{AgentID: agentID})
	resp, err := h.HandleArchive(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := resp.(*SessionArchiveResponse)

	if r.ArchivedPath == nil {
		t.Fatal("expected ArchivedPath, got nil")
	}
	expectedDir := filepath.Join(h.thrumDir, "agents", agentID, "sessions")
	if !strings.HasPrefix(*r.ArchivedPath, expectedDir+string(filepath.Separator)) {
		t.Errorf("ArchivedPath %q not under expected dir %q", *r.ArchivedPath, expectedDir)
	}
	if r.BigPicture == nil || *r.BigPicture != "Locked the spec." {
		t.Errorf("BigPicture: got %v, want 'Locked the spec.'", r.BigPicture)
	}
	// Content (Task 7 addition) carries the pre-archive file bytes
	// so the CLI prime flow can emit them in place of the prior
	// restart.ConsumeInPrime read.
	if r.Content == nil {
		t.Fatal("expected Content for valid snapshot, got nil")
	}
	if !strings.Contains(*r.Content, "Locked the spec.") {
		t.Errorf("Content missing body: %q", *r.Content)
	}
	if !strings.Contains(*r.Content, "saved_at: 2026-05-17T15:32:18.421Z") {
		t.Errorf("Content missing frontmatter: %q", *r.Content)
	}
	// Source removed
	if _, err := os.Stat(srcPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("source not removed: %v", err)
	}
}

// TestHandleSessionArchive_NoSnapshot_NullContent confirms the Content
// field is JSON null when no snapshot existed at call time —
// idempotency contract preserves the "{archived_path:null, ...}"
// response shape for missing-source.
func TestHandleSessionArchive_NoSnapshot_NullContent(t *testing.T) {
	h, agentID, _ := newArchiveTestHandler(t, "persistent")

	params, _ := json.Marshal(SessionArchiveRequest{AgentID: agentID})
	resp, err := h.HandleArchive(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := resp.(*SessionArchiveResponse)
	if r.Content != nil {
		t.Errorf("expected nil Content for missing snapshot, got %v", *r.Content)
	}
	if r.DiscoveryHint != nil {
		t.Errorf("expected nil DiscoveryHint for missing snapshot + empty sessions/, got %v", *r.DiscoveryHint)
	}
}

// TestHandleSessionArchive_PopulatesDiscoveryHint covers the Task 8
// addition: after a successful archive, the response should include
// the rendered "Past sessions: ..." hint. Just-archived snapshots
// count toward the sessions tally — N=1 after a fresh archive,
// since the freshly-moved file IS the lone past session.
func TestHandleSessionArchive_PopulatesDiscoveryHint(t *testing.T) {
	h, agentID, srcPath := newArchiveTestHandler(t, "persistent")
	writeArchiveSnapshot(t, srcPath, "2026-05-17T15:32:18.421Z", "Locked the spec.")

	params, _ := json.Marshal(SessionArchiveRequest{AgentID: agentID})
	resp, err := h.HandleArchive(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := resp.(*SessionArchiveResponse)

	if r.DiscoveryHint == nil {
		t.Fatal("expected DiscoveryHint after archive, got nil")
	}
	hint := *r.DiscoveryHint
	if !strings.Contains(hint, "Past sessions: 1 saved") {
		t.Errorf("hint missing 'Past sessions: 1 saved': %q", hint)
	}
	if !strings.Contains(hint, "Last big picture: Locked the spec.") {
		t.Errorf("hint missing line 2: %q", hint)
	}
}

func TestHandleSessionArchive_EphemeralAgent_ValidSnapshot_Archives(t *testing.T) {
	h, agentID, srcPath := newArchiveTestHandler(t, "ephemeral")
	writeArchiveSnapshot(t, srcPath, "2026-05-17T15:32:18.421Z", "Ephemeral body.")

	params, _ := json.Marshal(SessionArchiveRequest{AgentID: agentID})
	resp, err := h.HandleArchive(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r := resp.(*SessionArchiveResponse)

	if r.ArchivedPath == nil {
		t.Fatal("expected ArchivedPath, got nil")
	}
	// Single-worktree test setup: worktreeThrumDir == h.thrumDir, so the
	// ephemeral and persistent destinations are the same physical tree
	// here. We assert the agents/<id>/sessions/ shape regardless.
	expectedDir := filepath.Join(h.thrumDir, "agents", agentID, "sessions")
	if !strings.HasPrefix(*r.ArchivedPath, expectedDir+string(filepath.Separator)) {
		t.Errorf("ArchivedPath %q not under expected dir %q", *r.ArchivedPath, expectedDir)
	}
	if r.BigPicture == nil || *r.BigPicture != "Ephemeral body." {
		t.Errorf("BigPicture: got %v, want 'Ephemeral body.'", r.BigPicture)
	}
}

func TestHandleSessionArchive_InvalidJSON_ReturnsError(t *testing.T) {
	h, _, _ := newArchiveTestHandler(t, "persistent")

	_, err := h.HandleArchive(context.Background(), json.RawMessage(`not-valid-json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "invalid request") {
		t.Errorf("expected 'invalid request' in error, got: %v", err)
	}
}
