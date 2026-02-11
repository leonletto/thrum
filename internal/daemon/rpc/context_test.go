package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
)

func setupContextTest(t *testing.T) (*ContextHandler, string) {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return NewContextHandler(s), thrumDir
}

func TestContextHandleSave(t *testing.T) {
	handler, thrumDir := setupContextTest(t)

	req := ContextSaveRequest{
		AgentName: "test_agent",
		Content:   []byte("# Test\nSome context here."),
	}
	reqJSON, _ := json.Marshal(req)

	result, err := handler.HandleSave(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("HandleSave error: %v", err)
	}

	resp, ok := result.(*ContextSaveResponse)
	if !ok {
		t.Fatalf("expected *ContextSaveResponse, got %T", result)
	}
	if resp.AgentName != "test_agent" {
		t.Errorf("agent name: got %q, want %q", resp.AgentName, "test_agent")
	}

	// Verify file was written
	data, err := os.ReadFile(filepath.Join(thrumDir, "context", "test_agent.md")) //nolint:gosec // G304 - test helper reading temp file
	if err != nil {
		t.Fatalf("read context file: %v", err)
	}
	if string(data) != "# Test\nSome context here." {
		t.Errorf("content mismatch: got %q", data)
	}
}

func TestContextHandleSaveMissingName(t *testing.T) {
	handler, _ := setupContextTest(t)

	req := ContextSaveRequest{Content: []byte("data")}
	reqJSON, _ := json.Marshal(req)

	_, err := handler.HandleSave(context.Background(), reqJSON)
	if err == nil {
		t.Fatal("expected error for missing agent_name")
	}
}

func TestContextHandleShow(t *testing.T) {
	handler, _ := setupContextTest(t)

	// Save first
	saveReq := ContextSaveRequest{
		AgentName: "agent1",
		Content:   []byte("context data"),
	}
	saveJSON, _ := json.Marshal(saveReq)
	if _, err := handler.HandleSave(context.Background(), saveJSON); err != nil {
		t.Fatal(err)
	}

	// Show
	showReq := ContextShowRequest{AgentName: "agent1"}
	showJSON, _ := json.Marshal(showReq)

	result, err := handler.HandleShow(context.Background(), showJSON)
	if err != nil {
		t.Fatalf("HandleShow error: %v", err)
	}

	resp, ok := result.(*ContextShowResponse)
	if !ok {
		t.Fatalf("expected *ContextShowResponse, got %T", result)
	}
	if !resp.HasContext {
		t.Error("expected HasContext=true")
	}
	if string(resp.Content) != "context data" {
		t.Errorf("content: got %q", resp.Content)
	}
	if resp.Size == 0 {
		t.Error("expected non-zero size")
	}
	if resp.UpdatedAt == "" {
		t.Error("expected non-empty UpdatedAt")
	}
}

func TestContextHandleShowNoContext(t *testing.T) {
	handler, _ := setupContextTest(t)

	req := ContextShowRequest{AgentName: "nonexistent"}
	reqJSON, _ := json.Marshal(req)

	result, err := handler.HandleShow(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("HandleShow error: %v", err)
	}

	resp := result.(*ContextShowResponse)
	if resp.HasContext {
		t.Error("expected HasContext=false for missing context")
	}
}

func TestContextHandleClear(t *testing.T) {
	handler, thrumDir := setupContextTest(t)

	// Save first
	saveReq := ContextSaveRequest{
		AgentName: "agent1",
		Content:   []byte("data"),
	}
	saveJSON, _ := json.Marshal(saveReq)
	if _, err := handler.HandleSave(context.Background(), saveJSON); err != nil {
		t.Fatal(err)
	}

	// Clear
	clearReq := ContextClearRequest{AgentName: "agent1"}
	clearJSON, _ := json.Marshal(clearReq)

	result, err := handler.HandleClear(context.Background(), clearJSON)
	if err != nil {
		t.Fatalf("HandleClear error: %v", err)
	}

	resp := result.(*ContextClearResponse)
	if resp.AgentName != "agent1" {
		t.Errorf("agent name: got %q", resp.AgentName)
	}

	// Verify file is gone
	path := filepath.Join(thrumDir, "context", "agent1.md")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("context file should be deleted after clear")
	}
}

func TestContextHandleClearIdempotent(t *testing.T) {
	handler, _ := setupContextTest(t)

	// Clear non-existent context should not error
	req := ContextClearRequest{AgentName: "nonexistent"}
	reqJSON, _ := json.Marshal(req)

	_, err := handler.HandleClear(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("Clear should be idempotent: %v", err)
	}
}
