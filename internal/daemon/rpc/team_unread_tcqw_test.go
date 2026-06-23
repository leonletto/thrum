package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
)

// TestTeamUnread_UsesDeliveriesReadAt is thrum-b6qw T4 (message_reads
// retirement). HandleGetTeam's per-member unread query must derive read-state
// from message_deliveries.read_at — the unified read-truth — NOT from the
// retired message_reads table. The fixture stamps a delivery row read with NO
// message_reads row: before the migration the team query consulted
// message_reads (no row → counted unread → InboxUnread=1, RED); after it
// consults message_deliveries.read_at (set → InboxUnread=0, GREEN).
func TestTeamUnread_UsesDeliveriesReadAt(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	if err := os.MkdirAll(filepath.Join(syncDir, "messages"), 0o750); err != nil {
		t.Fatalf("create messages dir: %v", err)
	}

	s, err := state.NewState(thrumDir, syncDir, "test_repo_team_unread", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	agentHandler := NewAgentHandler(s)
	sessionHandler := NewSessionHandler(s)
	teamHandler := NewTeamHandler(s, "", nil)

	reg, _ := json.Marshal(RegisterRequest{Role: "reviewer", Module: "all"})
	resp, err := agentHandler.HandleRegister(ctx, reg)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	agentID := resp.(*RegisterResponse).AgentID

	startJSON, _ := json.Marshal(SessionStartRequest{AgentID: agentID})
	if _, err := sessionHandler.HandleStart(ctx, startJSON); err != nil {
		t.Fatalf("start session: %v", err)
	}

	// A message that mentions the agent (so it counts toward InboxTotal).
	if _, err := s.RawDB().Exec(`INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES (?, ?, ?, datetime('now'), 'markdown', 'hey reviewer')`,
		"msg_t4", "agent:author:OTH", "ses_t4"); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if _, err := s.RawDB().Exec(`INSERT INTO message_refs (message_id, ref_type, ref_value) VALUES (?, 'mention', ?)`,
		"msg_t4", agentID); err != nil {
		t.Fatalf("insert mention ref: %v", err)
	}
	// Read-stamped delivery row — and deliberately NO message_reads row.
	if _, err := s.RawDB().Exec(`INSERT INTO message_deliveries (message_id, recipient_agent_id, delivered_at, seen_at, read_at)
		VALUES (?, ?, datetime('now'), datetime('now'), datetime('now'))`,
		"msg_t4", agentID); err != nil {
		t.Fatalf("insert read delivery: %v", err)
	}

	reqJSON, _ := json.Marshal(TeamListRequest{})
	listResp, err := teamHandler.HandleList(ctx, reqJSON)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	result := listResp.(*TeamListResponse)
	var found bool
	for _, m := range result.Members {
		if m.AgentID == agentID {
			found = true
			if m.InboxTotal != 1 {
				t.Errorf("InboxTotal: want 1 (mention), got %d", m.InboxTotal)
			}
			if m.InboxUnread != 0 {
				t.Errorf("InboxUnread: want 0 (delivery read_at set; team must read message_deliveries not message_reads), got %d", m.InboxUnread)
			}
		}
	}
	if !found {
		t.Fatalf("agent %s not in team result", agentID)
	}
}
