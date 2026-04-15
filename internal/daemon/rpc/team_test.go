package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
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

	s, err := state.NewState(thrumDir, syncDir, "test_repo_team", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	agentHandler := NewAgentHandler(s)
	sessionHandler := NewSessionHandler(s)
	teamHandler := NewTeamHandler(s, "")

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

	// Insert a directed message from agent1 to agent2 (mention ref)
	_, err = s.RawDB().Exec(`INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES (?, ?, ?, datetime('now'), 'markdown', 'hey agent2')`,
		"msg_directed001", agent1ID, "ses_test001")
	if err != nil {
		t.Fatalf("insert directed message: %v", err)
	}
	_, err = s.RawDB().Exec(`INSERT INTO message_refs (message_id, ref_type, ref_value) VALUES (?, 'mention', ?)`,
		"msg_directed001", agent2ID)
	if err != nil {
		t.Fatalf("insert mention ref: %v", err)
	}

	// Insert a broadcast message (no mention refs, no group scopes — shows in shared footer)
	_, err = s.RawDB().Exec(`INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES (?, ?, ?, datetime('now'), 'markdown', 'hello everyone')`,
		"msg_broadcast001", agent1ID, "ses_test001")
	if err != nil {
		t.Fatalf("insert broadcast message: %v", err)
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

		// Find agent2 and check directed inbox (should only count the mention, not broadcast)
		for _, m := range result.Members {
			if m.AgentID == agent2ID {
				if m.InboxTotal != 1 {
					t.Errorf("agent2 inbox total: want 1 (directed only), got %d", m.InboxTotal)
				}
				if m.InboxUnread != 1 {
					t.Errorf("agent2 inbox unread: want 1, got %d", m.InboxUnread)
				}
			}
			if m.AgentID == agent1ID {
				if m.InboxTotal != 0 {
					t.Errorf("agent1 inbox total: want 0 (no messages directed to agent1), got %d", m.InboxTotal)
				}
			}
			if m.Status != "active" {
				t.Errorf("expected active status for %s, got %s", m.AgentID, m.Status)
			}
		}

		// Check shared messages footer
		if result.SharedMessages == nil {
			t.Fatal("expected shared messages in response")
		}
		if result.SharedMessages.BroadcastTotal != 1 {
			t.Errorf("broadcast total: want 1, got %d", result.SharedMessages.BroadcastTotal)
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

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_empty", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	teamHandler := NewTeamHandler(s, "")
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

// TestTeamList_EnrichesFromWorktreeIdentityFile asserts that team.list
// enriches members from identity files scanned across all worktrees via
// ReadIdentitiesAcrossWorktrees. Full coverage requires a daemon test
// harness with DB fixtures and fake worktrees; skipped as a placeholder.
// The manual smoke test in plan task thrum-pxz.13 is the real gate.
func TestTeamList_EnrichesFromWorktreeIdentityFile(t *testing.T) {
	t.Skip("requires daemon test harness with DB + fake worktree fixtures")
}

// TestTeamList_SelfHealSkipsLiveFilePID covers Fix B from thrum-pxz.14:
// when the DB reports an agent's PID as dead but the identity file
// reports a different, live PID, the self-heal must NOT emit session.end
// and the agent must remain active. This is the first-deploy bootstrap
// scenario: legacy DB state that predates the refresh feature.
func TestTeamList_SelfHealSkipsLiveFilePID(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	messagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(messagesDir, 0750); err != nil {
		t.Fatalf("create messages dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750); err != nil {
		t.Fatalf("create identities dir: %v", err)
	}

	s, err := state.NewState(thrumDir, syncDir, "test_repo_team", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	agentHandler := NewAgentHandler(s)
	sessionHandler := NewSessionHandler(s)
	teamHandler := NewTeamHandler(s, thrumDir)

	// Register an agent with a DEAD PID (simulating legacy/stale DB state).
	// PID 999999 is nearly guaranteed to not be running on a developer box.
	reg := RegisterRequest{
		Role:     "implementer",
		Module:   "selfheal",
		Display:  "Self-Heal Test Agent",
		AgentPID: 999999,
	}
	regJSON, _ := json.Marshal(reg)
	regResp, err := agentHandler.HandleRegister(ctx, regJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	agentID := regResp.(*RegisterResponse).AgentID

	// Start a session so the agent appears as "active" in team.list.
	startReq := SessionStartRequest{AgentID: agentID}
	startJSON, _ := json.Marshal(startReq)
	if _, err := sessionHandler.HandleStart(ctx, startJSON); err != nil {
		t.Fatalf("start session: %v", err)
	}

	// Write an identity file reporting a LIVE, different PID (os.Getpid()).
	// This simulates the "agent restarted under a new PID and wrote the
	// new PID to its local file, but the DB is still stuck on the old
	// legacy PID" scenario that triggered thrum-pxz.14.
	idFile := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind:    "agent",
			Name:    agentID,
			Role:    "implementer",
			Module:  "selfheal",
			Display: "Self-Heal Test Agent",
		},
		AgentPID: os.Getpid(),
		Runtime:  "claude",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity file: %v", err)
	}

	// Snapshot session.end event count before.
	var endBefore int
	if err := s.RawDB().QueryRow("SELECT COUNT(*) FROM events WHERE type = 'agent.session.end'").Scan(&endBefore); err != nil {
		t.Fatalf("query session.end count before: %v", err)
	}

	// Call team.list — self-heal must be skipped for this agent.
	req := TeamListRequest{}
	reqJSON, _ := json.Marshal(req)
	resp, err := teamHandler.HandleList(ctx, reqJSON)
	if err != nil {
		t.Fatalf("HandleList error: %v", err)
	}
	result := resp.(*TeamListResponse)

	var got *TeamMember
	for i := range result.Members {
		if result.Members[i].AgentID == agentID {
			got = &result.Members[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("agent %s missing from team list", agentID)
	}
	if got.Status != "active" {
		t.Errorf("Status = %q, want active (self-heal should have skipped this agent)", got.Status)
	}

	// Verify NO session.end event was emitted.
	var endAfter int
	if err := s.RawDB().QueryRow("SELECT COUNT(*) FROM events WHERE type = 'agent.session.end'").Scan(&endAfter); err != nil {
		t.Fatalf("query session.end count after: %v", err)
	}
	if endAfter != endBefore {
		t.Errorf("session.end event count changed: before=%d after=%d (expected no new event)", endBefore, endAfter)
	}
}

// TestTeamList_HidesReservedByDefault verifies that the default
// team.list call does NOT surface identities marked Reserved=true
// in their identity file. This is the hiding half of Task 7.2:
// @supervisor_<project> and similar daemon-internal pseudo-agents
// are not workers and should not clutter the default listing.
func TestTeamList_HidesReservedByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("create identities dir: %v", err)
	}
	s, err := state.NewState(thrumDir, thrumDir, "r_HIDETEST", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Seed two identity files: one Reserved=true (pseudo-agent),
	// one normal (real worker).
	reserved := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "supervisor_test",
			Role:   "supervisor",
			Module: "daemon",
		},
		Reserved: true,
	}
	if err := config.SaveIdentityFile(thrumDir, reserved); err != nil {
		t.Fatalf("save reserved identity: %v", err)
	}
	normal := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "researcher_alice",
			Role:   "researcher",
			Module: "exploration",
		},
	}
	if err := config.SaveIdentityFile(thrumDir, normal); err != nil {
		t.Fatalf("save normal identity: %v", err)
	}

	handler := NewTeamHandler(s, thrumDir)
	ctx := context.Background()

	reqJSON, _ := json.Marshal(TeamListRequest{IncludeOffline: true})
	resp, err := handler.HandleList(ctx, reqJSON)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	result := resp.(*TeamListResponse)

	for _, m := range result.Members {
		if m.AgentID == "supervisor_test" {
			t.Errorf("default team list should hide reserved agent, but found %+v", m)
		}
	}
}

// TestTeamList_SystemFlagShowsReserved verifies that --system
// surfaces Reserved=true identities synthesized from identity files
// that don't have an agents-table row. This is the showing half of
// Task 7.2.
func TestTeamList_SystemFlagShowsReserved(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("create identities dir: %v", err)
	}
	s, err := state.NewState(thrumDir, thrumDir, "r_SYSTEST", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	reserved := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "supervisor_test",
			Role:   "supervisor",
			Module: "daemon",
		},
		Reserved: true,
	}
	if err := config.SaveIdentityFile(thrumDir, reserved); err != nil {
		t.Fatalf("save reserved identity: %v", err)
	}

	handler := NewTeamHandler(s, thrumDir)
	ctx := context.Background()

	reqJSON, _ := json.Marshal(TeamListRequest{
		IncludeOffline: true,
		IncludeSystem:  true,
	})
	resp, err := handler.HandleList(ctx, reqJSON)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	result := resp.(*TeamListResponse)

	var found *TeamMember
	for i := range result.Members {
		if result.Members[i].AgentID == "supervisor_test" {
			found = &result.Members[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("--system should surface reserved agent, got %d members without it", len(result.Members))
	}
	if !found.Reserved {
		t.Error("synthesized member should carry Reserved=true")
	}
	if found.Role != "supervisor" {
		t.Errorf("synthesized Role = %q, want supervisor", found.Role)
	}
	if found.Status != "reserved" {
		t.Errorf("synthesized Status = %q, want reserved", found.Status)
	}
}

// TestTeamList_SystemFlagMarksExistingReserved covers the defensive
// path where an agent IS in the agents table but its identity file
// has Reserved=true. Default call hides it; --system shows it with
// the Reserved flag set.
func TestTeamList_SystemFlagMarksExistingReserved(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("create identities dir: %v", err)
	}
	s, err := state.NewState(thrumDir, thrumDir, "r_MARKTEST", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Register a real agent with a matching identity file that
	// carries Reserved=true. Ordinarily reserved agents aren't
	// registered — this test covers the defensive path.
	ctx := context.Background()
	agentHandler := NewAgentHandler(s)
	sessionHandler := NewSessionHandler(s)

	reg := RegisterRequest{Role: "supervisor", Module: "daemon"}
	regJSON, _ := json.Marshal(reg)
	regResp, err := agentHandler.HandleRegister(ctx, regJSON)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	agentID := regResp.(*RegisterResponse).AgentID

	startJSON, _ := json.Marshal(SessionStartRequest{AgentID: agentID})
	if _, err := sessionHandler.HandleStart(ctx, startJSON); err != nil {
		t.Fatalf("start session: %v", err)
	}

	// Seed an identity file with Reserved=true for this agent.
	idFile := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   agentID,
			Role:   "supervisor",
			Module: "daemon",
		},
		Reserved: true,
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity: %v", err)
	}

	handler := NewTeamHandler(s, thrumDir)

	// Default: should NOT include the reserved agent even though
	// it has a live agents-table row.
	defaultJSON, _ := json.Marshal(TeamListRequest{})
	defaultResp, _ := handler.HandleList(ctx, defaultJSON)
	for _, m := range defaultResp.(*TeamListResponse).Members {
		if m.AgentID == agentID {
			t.Errorf("default list should filter reserved agent %s, got %+v", agentID, m)
		}
	}

	// --system: should include it and Reserved must be true.
	sysJSON, _ := json.Marshal(TeamListRequest{IncludeSystem: true})
	sysResp, _ := handler.HandleList(ctx, sysJSON)
	var found *TeamMember
	for i := range sysResp.(*TeamListResponse).Members {
		if sysResp.(*TeamListResponse).Members[i].AgentID == agentID {
			found = &sysResp.(*TeamListResponse).Members[i]
			break
		}
	}
	if found == nil {
		t.Fatal("--system should include the reserved real-agent")
	}
	if !found.Reserved {
		t.Error("member should carry Reserved=true after enrichment")
	}
}
