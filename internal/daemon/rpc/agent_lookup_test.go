package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
)

func TestAgentLookup_Found(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	if err := os.MkdirAll(filepath.Join(syncDir, "messages"), 0o750); err != nil {
		t.Fatalf("mkdir messages: %v", err)
	}

	s, err := state.NewState(thrumDir, syncDir, "test_repo_agentlookup", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	agentHandler := NewAgentHandler(s)
	sessionHandler := NewSessionHandler(s)
	lookupHandler := NewAgentLookupHandler(s)

	regJSON, _ := json.Marshal(RegisterRequest{
		Role:    "implementer",
		Module:  "lookup",
		Display: "Lookup Test",
	})
	regResp, err := agentHandler.HandleRegister(ctx, regJSON)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	agentID := regResp.(*RegisterResponse).AgentID

	startJSON, _ := json.Marshal(SessionStartRequest{AgentID: agentID})
	if _, err := sessionHandler.HandleStart(ctx, startJSON); err != nil {
		t.Fatalf("session.start: %v", err)
	}

	reqJSON, _ := json.Marshal(AgentLookupRequest{Name: agentID})
	resp, err := lookupHandler.HandleLookup(ctx, reqJSON)
	if err != nil {
		t.Fatalf("HandleLookup: %v", err)
	}
	out := resp.(*AgentLookupResponse)
	if out.Member == nil {
		t.Fatal("Member is nil; want populated TeamMember")
	}
	if out.Member.AgentID != agentID {
		t.Errorf("AgentID = %q, want %q", out.Member.AgentID, agentID)
	}
	if out.Member.Role != "implementer" {
		t.Errorf("Role = %q, want implementer", out.Member.Role)
	}
	if out.Member.Module != "lookup" {
		t.Errorf("Module = %q, want lookup", out.Member.Module)
	}
	if out.Member.Status != "active" {
		t.Errorf("Status = %q, want active", out.Member.Status)
	}
	if out.Member.SessionID == "" {
		t.Error("SessionID empty; want populated from active session")
	}
	if !out.Member.IsLocal {
		t.Error("IsLocal = false; want true (no remote OriginDaemon)")
	}
}

func TestAgentLookup_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	if err := os.MkdirAll(filepath.Join(syncDir, "messages"), 0o750); err != nil {
		t.Fatalf("mkdir messages: %v", err)
	}
	s, err := state.NewState(thrumDir, syncDir, "test_repo_notfound", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	lookupHandler := NewAgentLookupHandler(s)

	reqJSON, _ := json.Marshal(AgentLookupRequest{Name: "does_not_exist"})
	resp, err := lookupHandler.HandleLookup(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("HandleLookup on missing agent: %v (want nil err with nil Member)", err)
	}
	out := resp.(*AgentLookupResponse)
	if out.Member != nil {
		t.Errorf("Member = %+v, want nil for unknown agent", out.Member)
	}
}

func TestAgentLookup_MissingName(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	if err := os.MkdirAll(filepath.Join(syncDir, "messages"), 0o750); err != nil {
		t.Fatalf("mkdir messages: %v", err)
	}
	s, err := state.NewState(thrumDir, syncDir, "test_repo_missingname", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	lookupHandler := NewAgentLookupHandler(s)

	reqJSON, _ := json.Marshal(AgentLookupRequest{Name: ""})
	_, err = lookupHandler.HandleLookup(context.Background(), reqJSON)
	if err == nil {
		t.Fatal("expected error for empty Name")
	}
}
