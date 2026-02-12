package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
)

func TestTeamHandleList(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	messagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(messagesDir, 0750); err != nil {
		t.Fatalf("create messages dir: %v", err)
	}

	s, err := state.NewState(thrumDir, syncDir, "test_repo_team")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	agentHandler := NewAgentHandler(s)
	sessionHandler := NewSessionHandler(s)
	teamHandler := NewTeamHandler(s)

	// Register two agents
	reg1 := RegisterRequest{Role: "implementer", Module: "auth"}
	reg1JSON, _ := json.Marshal(reg1)
	resp1, err := agentHandler.HandleRegister(ctx, reg1JSON)
	if err != nil {
		t.Fatalf("register agent1: %v", err)
	}
	agent1ID := resp1.(*RegisterResponse).AgentID

	reg2 := RegisterRequest{Role: "reviewer", Module: "all"}
	reg2JSON, _ := json.Marshal(reg2)
	resp2, err := agentHandler.HandleRegister(ctx, reg2JSON)
	if err != nil {
		t.Fatalf("register agent2: %v", err)
	}
	agent2ID := resp2.(*RegisterResponse).AgentID

	// Start sessions for both
	start1 := SessionStartRequest{AgentID: agent1ID}
	start1JSON, _ := json.Marshal(start1)
	_, err = sessionHandler.HandleStart(ctx, start1JSON)
	if err != nil {
		t.Fatalf("start session1: %v", err)
	}

	start2 := SessionStartRequest{AgentID: agent2ID}
	start2JSON, _ := json.Marshal(start2)
	startResp2, err := sessionHandler.HandleStart(ctx, start2JSON)
	if err != nil {
		t.Fatalf("start session2: %v", err)
	}
	session2ID := startResp2.(*SessionStartResponse).SessionID

	// Insert a message from agent1 (should appear in agent2's inbox)
	_, err = s.DB().Exec(`INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES (?, ?, ?, datetime('now'), 'markdown', 'hello team')`,
		"msg_test001", agent1ID, "ses_test001")
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}

	t.Run("default_active_only", func(t *testing.T) {
		req := TeamListRequest{}
		reqJSON, _ := json.Marshal(req)
		resp, err := teamHandler.HandleList(ctx, reqJSON)
		if err != nil {
			t.Fatalf("HandleList error: %v", err)
		}

		result := resp.(*TeamListResponse)
		if len(result.Members) != 2 {
			t.Fatalf("expected 2 members, got %d", len(result.Members))
		}

		// Find agent2 and check inbox
		for _, m := range result.Members {
			if m.AgentID == agent2ID {
				if m.InboxTotal != 1 {
					t.Errorf("agent2 inbox total: want 1, got %d", m.InboxTotal)
				}
				if m.InboxUnread != 1 {
					t.Errorf("agent2 inbox unread: want 1, got %d", m.InboxUnread)
				}
			}
			if m.Status != "active" {
				t.Errorf("expected active status for %s, got %s", m.AgentID, m.Status)
			}
		}
	})

	t.Run("after_ending_session", func(t *testing.T) {
		// End agent2's session
		endReq := SessionEndRequest{SessionID: session2ID, Reason: "done"}
		endJSON, _ := json.Marshal(endReq)
		_, err := sessionHandler.HandleEnd(ctx, endJSON)
		if err != nil {
			t.Fatalf("end session: %v", err)
		}

		// Default (active only) should return only 1
		req := TeamListRequest{}
		reqJSON, _ := json.Marshal(req)
		resp, err := teamHandler.HandleList(ctx, reqJSON)
		if err != nil {
			t.Fatalf("HandleList error: %v", err)
		}

		result := resp.(*TeamListResponse)
		if len(result.Members) != 1 {
			t.Fatalf("expected 1 active member, got %d", len(result.Members))
		}
		if result.Members[0].AgentID != agent1ID {
			t.Errorf("expected agent1, got %s", result.Members[0].AgentID)
		}
	})

	t.Run("include_offline", func(t *testing.T) {
		req := TeamListRequest{IncludeOffline: true}
		reqJSON, _ := json.Marshal(req)
		resp, err := teamHandler.HandleList(ctx, reqJSON)
		if err != nil {
			t.Fatalf("HandleList error: %v", err)
		}

		result := resp.(*TeamListResponse)
		if len(result.Members) != 2 {
			t.Fatalf("expected 2 members with --all, got %d", len(result.Members))
		}

		// Check one is active, one is offline
		statuses := map[string]string{}
		for _, m := range result.Members {
			statuses[m.AgentID] = m.Status
		}
		if statuses[agent1ID] != "active" {
			t.Errorf("agent1 should be active, got %s", statuses[agent1ID])
		}
		if statuses[agent2ID] != "offline" {
			t.Errorf("agent2 should be offline, got %s", statuses[agent2ID])
		}
	})
}

func TestResolveHostname(t *testing.T) {
	t.Run("env_override", func(t *testing.T) {
		t.Setenv("THRUM_HOSTNAME", "my-machine")
		h := resolveHostname()
		if h != "my-machine" {
			t.Errorf("expected 'my-machine', got '%s'", h)
		}
	})

	t.Run("default_hostname", func(t *testing.T) {
		t.Setenv("THRUM_HOSTNAME", "")
		h := resolveHostname()
		// Should return something non-empty on any machine
		if h == "" {
			t.Error("expected non-empty hostname")
		}
		// Should not end with .local
		if len(h) > 6 && h[len(h)-6:] == ".local" {
			t.Errorf("hostname should have .local stripped, got '%s'", h)
		}
	})
}

func TestTeamHandleList_EmptyDB(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_empty")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	teamHandler := NewTeamHandler(s)
	req := TeamListRequest{}
	reqJSON, _ := json.Marshal(req)
	resp, err := teamHandler.HandleList(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("HandleList error: %v", err)
	}

	result := resp.(*TeamListResponse)
	if len(result.Members) != 0 {
		t.Errorf("expected 0 members, got %d", len(result.Members))
	}
}
