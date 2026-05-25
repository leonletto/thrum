package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
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
	teamHandler := NewTeamHandler(s, "", nil)

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

	teamHandler := NewTeamHandler(s, "", nil)
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
	teamHandler := NewTeamHandler(s, thrumDir, nil)

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

// TestTeamList_SingleFlightDeadAgentSelfHeal verifies thrum-1nkt.3:
// concurrent team.list callers that observe the same dead agent in
// Phase 1 must NOT each emit a redundant session.end in Phase 2. The
// h.selfHealing sync.Map gate ensures exactly one emit per session_id
// even when N goroutines race through Phase 2 at the same time. Without
// the gate, this test would write 8 session.end events for the same
// dead session (one per concurrent caller).
func TestTeamList_SingleFlightDeadAgentSelfHeal(t *testing.T) {
	// Make the concurrency guarantee explicit on CI machines that clamp
	// GOMAXPROCS — the race we are testing only manifests when goroutines
	// can actually overlap inside Phase 2.
	runtime.GOMAXPROCS(runtime.NumCPU())

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	messagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(messagesDir, 0o750); err != nil {
		t.Fatalf("create messages dir: %v", err)
	}

	s, err := state.NewState(thrumDir, syncDir, "test_repo_singleflight", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	agentHandler := NewAgentHandler(s)
	sessionHandler := NewSessionHandler(s)
	teamHandler := NewTeamHandler(s, "", nil)

	// Register one agent with a dead PID and start a session — same
	// fixture pattern as TestTeamList_SelfHealSkipsLiveFilePID, minus
	// the identity-file override that would skip the self-heal.
	reg := RegisterRequest{
		Role:     "implementer",
		Module:   "burst",
		AgentPID: 999999,
	}
	regJSON, _ := json.Marshal(reg)
	regResp, err := agentHandler.HandleRegister(ctx, regJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	agentID := regResp.(*RegisterResponse).AgentID

	startReq := SessionStartRequest{AgentID: agentID}
	startJSON, _ := json.Marshal(startReq)
	if _, err := sessionHandler.HandleStart(ctx, startJSON); err != nil {
		t.Fatalf("start session: %v", err)
	}

	var endBefore int
	if err := s.RawDB().QueryRow(
		"SELECT COUNT(*) FROM events WHERE type = 'agent.session.end'",
	).Scan(&endBefore); err != nil {
		t.Fatalf("query session.end count before: %v", err)
	}

	const callers = 8
	req := TeamListRequest{}
	reqJSON, _ := json.Marshal(req)

	var (
		wg       sync.WaitGroup
		startGun = make(chan struct{})
		offline  = make(chan bool, callers)
		errCh    = make(chan error, callers)
	)
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			<-startGun
			resp, err := teamHandler.HandleList(ctx, reqJSON)
			if err != nil {
				errCh <- err
				return
			}
			for _, m := range resp.(*TeamListResponse).Members {
				if m.AgentID == agentID {
					offline <- m.Status == "offline"
					return
				}
			}
			t.Errorf("agent %s not found in concurrent HandleList response", agentID)
			offline <- false
		}()
	}
	close(startGun)
	wg.Wait()
	close(offline)
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent HandleList: %v", err)
	}

	offlineCount := 0
	for ok := range offline {
		if ok {
			offlineCount++
		}
	}
	if offlineCount != callers {
		t.Errorf("offline status seen by %d/%d concurrent callers, want %d (Phase 3 must mark dead agents offline in every response)",
			offlineCount, callers, callers)
	}

	var endAfter int
	if err := s.RawDB().QueryRow(
		"SELECT COUNT(*) FROM events WHERE type = 'agent.session.end'",
	).Scan(&endAfter); err != nil {
		t.Fatalf("query session.end count after: %v", err)
	}
	if delta := endAfter - endBefore; delta != 1 {
		t.Errorf("session.end events emitted under %d-concurrent burst: got %d, want 1 (single-flight gate must collapse duplicates)",
			callers, delta)
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

	handler := NewTeamHandler(s, thrumDir, nil)
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

	handler := NewTeamHandler(s, thrumDir, nil)
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

	handler := NewTeamHandler(s, thrumDir, nil)

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

// TestHandleList_InjectsSupervisorWhenIncludeSystemTrue verifies that
// HandleList injects the virtual supervisor pseudo-agent when the
// request asks for system identities. Post-cleanup (Task 7) the
// supervisor has no identity file on disk, so the daemon must carry
// it in-memory via TeamHandler.supervisorIdentity.
func TestHandleList_InjectsSupervisorWhenIncludeSystemTrue(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	if err := os.MkdirAll(syncDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s, err := state.NewState(thrumDir, syncDir, "test_repo_sys_true", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = s.Close() }()

	supervisorIdentity := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind: "agent", Name: "supervisor_test_user",
			Role: "supervisor", Module: "daemon",
			Display: "Supervisor (test)",
		},
		Reserved: true,
	}
	h := NewTeamHandler(s, "", supervisorIdentity)

	reqJSON, _ := json.Marshal(TeamListRequest{IncludeSystem: true})
	rawResp, err := h.HandleList(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	resp, ok := rawResp.(*TeamListResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", rawResp)
	}

	var supervisorMember *TeamMember
	for i, m := range resp.Members {
		if m.AgentID == "supervisor_test_user" {
			supervisorMember = &resp.Members[i]
			break
		}
	}
	if supervisorMember == nil {
		t.Fatalf("supervisor not injected; got %+v", resp.Members)
	}
	if supervisorMember.Role != "supervisor" {
		t.Errorf("Role = %q, want supervisor", supervisorMember.Role)
	}
	if supervisorMember.Status != "reserved" {
		t.Errorf("Status = %q, want reserved", supervisorMember.Status)
	}
	if !supervisorMember.Reserved {
		t.Errorf("Reserved = false, want true")
	}
}

// TestHandleList_HidesSupervisorWhenIncludeSystemFalse verifies that
// the default listing (IncludeSystem=false) excludes the virtual
// supervisor even when the handler has one wired.
func TestHandleList_HidesSupervisorWhenIncludeSystemFalse(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	if err := os.MkdirAll(syncDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s, err := state.NewState(thrumDir, syncDir, "test_repo_sys_false", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = s.Close() }()

	supervisorIdentity := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind: "agent", Name: "supervisor_test_user",
			Role: "supervisor", Module: "daemon",
			Display: "Supervisor (test)",
		},
		Reserved: true,
	}
	h := NewTeamHandler(s, "", supervisorIdentity)

	reqJSON, _ := json.Marshal(TeamListRequest{IncludeSystem: false})
	rawResp, err := h.HandleList(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	resp, ok := rawResp.(*TeamListResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", rawResp)
	}

	for _, m := range resp.Members {
		if m.AgentID == "supervisor_test_user" {
			t.Fatalf("supervisor leaked into default listing: %+v", resp.Members)
		}
	}
}

// TestHandleList_WorktreeFallbackFromSessionRefs verifies that when a
// session has a `worktree` row in session_refs but no matching row in
// agent_work_contexts, HandleList still returns the worktree via the
// scalar-subquery fallback. Regression for thrum-naak: live daemons
// observed coordinator_main missing `worktree` from `team --json` even
// though its session_ref carried the value, because the team.list SQL
// read worktree_path exclusively from agent_work_contexts and omitempty
// dropped the empty string.
func TestHandleList_WorktreeFallbackFromSessionRefs(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	messagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(messagesDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s, err := state.NewState(thrumDir, syncDir, "test_repo_wt_fallback", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	agentHandler := NewAgentHandler(s)
	sessionHandler := NewSessionHandler(s)
	teamHandler := NewTeamHandler(s, "", nil)

	const wantWorktree = "/tmp/test-worktree-fallback"

	reg := RegisterRequest{Role: "implementer", Module: "fallback"}
	regJSON, _ := json.Marshal(reg)
	regResp, err := agentHandler.HandleRegister(ctx, regJSON)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	agentID := regResp.(*RegisterResponse).AgentID

	start := SessionStartRequest{
		AgentID: agentID,
		Refs:    []types.Ref{{Type: "worktree", Value: wantWorktree}},
	}
	startJSON, _ := json.Marshal(start)
	startResp, err := sessionHandler.HandleStart(ctx, startJSON)
	if err != nil {
		t.Fatalf("session.start: %v", err)
	}
	sessionID := startResp.(*SessionStartResponse).SessionID

	// Simulate the observed bug state: session_refs has the worktree row
	// (populated by HandleStart) but agent_work_contexts has no row
	// (e.g. a later code path cleared it, or a daemon-resurrect path
	// populated session_refs without seeding agent_work_contexts).
	if _, err := s.RawDB().ExecContext(ctx,
		`DELETE FROM agent_work_contexts WHERE session_id = ?`, sessionID); err != nil {
		t.Fatalf("delete agent_work_contexts row: %v", err)
	}

	reqJSON, _ := json.Marshal(TeamListRequest{})
	rawResp, err := teamHandler.HandleList(ctx, reqJSON)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	resp := rawResp.(*TeamListResponse)

	var got *TeamMember
	for i := range resp.Members {
		if resp.Members[i].AgentID == agentID {
			got = &resp.Members[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("agent %q not returned by team.list; got %+v", agentID, resp.Members)
	}
	if got.WorktreePath != wantWorktree {
		t.Errorf("WorktreePath = %q, want %q (fallback to session_refs should populate it when agent_work_contexts has no row)",
			got.WorktreePath, wantWorktree)
	}

	// JSON roundtrip check — the whole point of the fix is that the
	// `worktree` key survives marshaling for this shape of DB state.
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal member: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("unmarshal member: %v", err)
	}
	if v, ok := raw["worktree"]; !ok || v != wantWorktree {
		t.Errorf("json output missing or wrong worktree: got %v (present=%v), want %q", v, ok, wantWorktree)
	}
}

// TestHandleList_WorktreeAgentWorkContextsWins verifies that when both
// agent_work_contexts.worktree_path AND session_refs carry a worktree
// value, the agent_work_contexts row wins. agent_work_contexts is the
// authoritative source — session_refs is only a fallback.
func TestHandleList_WorktreeAgentWorkContextsWins(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	messagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(messagesDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s, err := state.NewState(thrumDir, syncDir, "test_repo_wt_precedence", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	agentHandler := NewAgentHandler(s)
	sessionHandler := NewSessionHandler(s)
	teamHandler := NewTeamHandler(s, "", nil)

	const refWorktree = "/tmp/from-session-ref"
	const authoritative = "/tmp/from-work-context"

	reg := RegisterRequest{Role: "implementer", Module: "precedence"}
	regJSON, _ := json.Marshal(reg)
	regResp, err := agentHandler.HandleRegister(ctx, regJSON)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	agentID := regResp.(*RegisterResponse).AgentID

	start := SessionStartRequest{
		AgentID: agentID,
		Refs:    []types.Ref{{Type: "worktree", Value: refWorktree}},
	}
	startJSON, _ := json.Marshal(start)
	startResp, err := sessionHandler.HandleStart(ctx, startJSON)
	if err != nil {
		t.Fatalf("session.start: %v", err)
	}
	sessionID := startResp.(*SessionStartResponse).SessionID

	// Overwrite agent_work_contexts.worktree_path with a distinct value
	// to verify precedence over the session_refs fallback.
	if _, err := s.RawDB().ExecContext(ctx,
		`UPDATE agent_work_contexts SET worktree_path = ? WHERE session_id = ?`,
		authoritative, sessionID); err != nil {
		t.Fatalf("update agent_work_contexts: %v", err)
	}

	reqJSON, _ := json.Marshal(TeamListRequest{})
	rawResp, err := teamHandler.HandleList(ctx, reqJSON)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	resp := rawResp.(*TeamListResponse)

	var got *TeamMember
	for i := range resp.Members {
		if resp.Members[i].AgentID == agentID {
			got = &resp.Members[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("agent %q not returned by team.list; got %+v", agentID, resp.Members)
	}
	if got.WorktreePath != authoritative {
		t.Errorf("WorktreePath = %q, want %q (agent_work_contexts must win over session_refs fallback)",
			got.WorktreePath, authoritative)
	}
}

// TestTeamList_IsLocalPopulated verifies the IsLocal bool on TeamMember:
//   - empty OriginDaemon  → IsLocal == true  (legacy/fixture rows)
//   - OriginDaemon == local daemon ID → IsLocal == true
//   - OriginDaemon == some other ID   → IsLocal == false  (remote peer)
//
// Heartbeats are DB-only and don't propagate across peer daemons (thrum-iyrt),
// so hint sources must not fire "may be idle" for remote-peer agents.
func TestTeamList_IsLocalPopulated(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	syncDir := filepath.Join(tmpDir, "sync")
	if err := os.MkdirAll(syncDir, 0o750); err != nil {
		t.Fatalf("create sync dir: %v", err)
	}
	const localDaemonID = "d_local_01"
	const remoteDaemonID = "d_peer_02"

	s, err := state.NewState(thrumDir, syncDir, "repo_islocal", localDaemonID)
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()

	// Insert three agents directly into the DB with controlled origin_daemon values.
	// (1) empty origin_daemon — legacy/fixture
	// (2) matches local daemon ID
	// (3) remote peer daemon ID
	_, err = s.RawDB().ExecContext(ctx, `INSERT INTO agents
		(agent_id, kind, role, module, origin_daemon, hostname, registered_at)
		VALUES
		('agent_legacy', 'agent', 'worker', 'test', '', 'host1', datetime('now')),
		('agent_local',  'agent', 'worker', 'test', ?, 'host1', datetime('now')),
		('agent_remote', 'agent', 'worker', 'test', ?, 'host2', datetime('now'))`,
		localDaemonID, remoteDaemonID)
	if err != nil {
		t.Fatalf("insert agents: %v", err)
	}

	handler := NewTeamHandler(s, "", nil)
	reqJSON, _ := json.Marshal(TeamListRequest{IncludeOffline: true})
	raw, err := handler.HandleList(ctx, reqJSON)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	resp := raw.(*TeamListResponse)

	byID := make(map[string]TeamMember, len(resp.Members))
	for _, m := range resp.Members {
		byID[m.AgentID] = m
	}

	for _, tc := range []struct {
		agentID string
		want    bool
	}{
		{"agent_legacy", true},
		{"agent_local", true},
		{"agent_remote", false},
	} {
		m, ok := byID[tc.agentID]
		if !ok {
			t.Errorf("agent %q not found in response", tc.agentID)
			continue
		}
		if m.IsLocal != tc.want {
			t.Errorf("agent %q: IsLocal = %v, want %v (OriginDaemon=%q)",
				tc.agentID, m.IsLocal, tc.want, m.OriginDaemon)
		}
	}
}
