package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
)

func TestSessionStart(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Register an agent first
	agentHandler := NewAgentHandler(s)
	registerReq := RegisterRequest{
		Role:   "implementer",
		Module: "test",
	}
	registerReqJSON, _ := json.Marshal(registerReq)
	registerResp, err := agentHandler.HandleRegister(context.Background(), registerReqJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	regResp, ok := registerResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", registerResp)
	}
	agentID := regResp.AgentID

	// Test session start
	sessionHandler := NewSessionHandler(s)

	tests := []struct {
		name    string
		request SessionStartRequest
		wantErr bool
	}{
		{
			name: "start_new_session",
			request: SessionStartRequest{
				AgentID: agentID,
			},
			wantErr: false,
		},
		{
			name: "missing_agent_id",
			request: SessionStartRequest{
				AgentID: "",
			},
			wantErr: true,
		},
		{
			name: "nonexistent_agent",
			request: SessionStartRequest{
				AgentID: "agent:nonexistent:ABC123",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqJSON, _ := json.Marshal(tt.request)
			resp, err := sessionHandler.HandleStart(context.Background(), reqJSON)

			if (err != nil) != tt.wantErr {
				t.Errorf("HandleStart() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			// Parse response
			startResp, ok := resp.(*SessionStartResponse)
			if !ok {
				t.Fatalf("response is not *SessionStartResponse, got %T", resp)
			}

			// Verify response
			if startResp.SessionID == "" {
				t.Error("SessionID should not be empty")
			}
			if startResp.AgentID != tt.request.AgentID {
				t.Errorf("AgentID = %s, want %s", startResp.AgentID, tt.request.AgentID)
			}
			if startResp.StartedAt == "" {
				t.Error("StartedAt should not be empty")
			}

			// Verify session was written to database
			var count int
			err = s.RawDB().QueryRow("SELECT COUNT(*) FROM sessions WHERE session_id = ?", startResp.SessionID).Scan(&count)
			if err != nil {
				t.Errorf("query session: %v", err)
			}
			if count == 0 {
				t.Error("Session not found in database after start")
			}
		})
	}
}

func TestSessionEnd(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Register an agent and start a session
	agentHandler := NewAgentHandler(s)
	registerReq := RegisterRequest{
		Role:   "implementer",
		Module: "test",
	}
	registerReqJSON, _ := json.Marshal(registerReq)
	registerResp, err := agentHandler.HandleRegister(context.Background(), registerReqJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	regResp, ok := registerResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", registerResp)
	}
	agentID := regResp.AgentID

	sessionHandler := NewSessionHandler(s)
	startReq := SessionStartRequest{
		AgentID: agentID,
	}
	startReqJSON, _ := json.Marshal(startReq)
	startResp, err := sessionHandler.HandleStart(context.Background(), startReqJSON)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	startRespTyped, ok := startResp.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", startResp)
	}
	sessionID := startRespTyped.SessionID

	// Test session end
	tests := []struct {
		name    string
		request SessionEndRequest
		wantErr bool
	}{
		{
			name: "end_session_with_reason",
			request: SessionEndRequest{
				SessionID: sessionID,
				Reason:    "normal",
			},
			wantErr: false,
		},
		{
			name: "missing_session_id",
			request: SessionEndRequest{
				SessionID: "",
			},
			wantErr: true,
		},
		{
			name: "nonexistent_session",
			request: SessionEndRequest{
				SessionID: "ses_NONEXISTENT",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqJSON, _ := json.Marshal(tt.request)
			resp, err := sessionHandler.HandleEnd(context.Background(), reqJSON)

			if (err != nil) != tt.wantErr {
				t.Errorf("HandleEnd() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			// Parse response
			endResp, ok := resp.(*SessionEndResponse)
			if !ok {
				t.Fatalf("response is not *SessionEndResponse, got %T", resp)
			}

			// Verify response
			if endResp.SessionID != tt.request.SessionID {
				t.Errorf("SessionID = %s, want %s", endResp.SessionID, tt.request.SessionID)
			}
			if endResp.EndedAt == "" {
				t.Error("EndedAt should not be empty")
			}
			if endResp.Duration < 0 {
				t.Errorf("Duration = %d, want >= 0", endResp.Duration)
			}

			// Verify session was updated in database
			var endedAt *string
			err = s.RawDB().QueryRow("SELECT ended_at FROM sessions WHERE session_id = ?", tt.request.SessionID).Scan(&endedAt)
			if err != nil {
				t.Errorf("query session: %v", err)
			}
			if endedAt == nil {
				t.Error("Session ended_at should not be NULL after end")
			}
		})
	}
}

func TestSessionCrashRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Register an agent
	agentHandler := NewAgentHandler(s)
	registerReq := RegisterRequest{
		Role:   "implementer",
		Module: "test",
	}
	registerReqJSON, _ := json.Marshal(registerReq)
	registerResp, err := agentHandler.HandleRegister(context.Background(), registerReqJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	regResp, ok := registerResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", registerResp)
	}
	agentID := regResp.AgentID

	// Create orphaned sessions by inserting directly into DB (simulating crashed sessions)
	orphanedSessions := []string{"ses_ORPHAN1", "ses_ORPHAN2"}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, sessionID := range orphanedSessions {
		_, err = s.RawDB().Exec(`
			INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
			VALUES (?, ?, ?, ?)
		`, sessionID, agentID, now, now)
		if err != nil {
			t.Fatalf("create orphaned session: %v", err)
		}
	}

	// Verify orphaned sessions exist with no end time
	var orphanedCount int
	err = s.RawDB().QueryRow("SELECT COUNT(*) FROM sessions WHERE agent_id = ? AND ended_at IS NULL", agentID).Scan(&orphanedCount)
	if err != nil {
		t.Fatalf("query orphaned sessions: %v", err)
	}
	if orphanedCount != 2 {
		t.Fatalf("Expected 2 orphaned sessions, got %d", orphanedCount)
	}

	// Start a new session (should trigger crash recovery)
	sessionHandler := NewSessionHandler(s)
	startReq := SessionStartRequest{
		AgentID: agentID,
	}
	startReqJSON, _ := json.Marshal(startReq)
	_, err = sessionHandler.HandleStart(context.Background(), startReqJSON)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	// Verify orphaned sessions were recovered (ended_at should now be set)
	var recoveredCount int
	err = s.RawDB().QueryRow(`
		SELECT COUNT(*) FROM sessions
		WHERE agent_id = ? AND ended_at IS NOT NULL AND end_reason = 'crash_recovered'
	`, agentID).Scan(&recoveredCount)
	if err != nil {
		t.Fatalf("query recovered sessions: %v", err)
	}
	if recoveredCount != 2 {
		t.Errorf("Expected 2 recovered sessions, got %d", recoveredCount)
	}

	// Verify new session was created
	var activeCount int
	err = s.RawDB().QueryRow("SELECT COUNT(*) FROM sessions WHERE agent_id = ? AND ended_at IS NULL", agentID).Scan(&activeCount)
	if err != nil {
		t.Fatalf("query active sessions: %v", err)
	}
	if activeCount != 1 {
		t.Errorf("Expected 1 active session, got %d", activeCount)
	}
}

func TestSessionHeartbeat(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Register an agent first
	agentHandler := NewAgentHandler(s)
	registerReq := RegisterRequest{
		Role:   "implementer",
		Module: "test",
	}
	registerReqJSON, _ := json.Marshal(registerReq)
	registerResp, err := agentHandler.HandleRegister(context.Background(), registerReqJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	regResp, ok := registerResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", registerResp)
	}
	agentID := regResp.AgentID

	// Start a session
	sessionHandler := NewSessionHandler(s)
	startReq := SessionStartRequest{
		AgentID: agentID,
	}
	startReqJSON, _ := json.Marshal(startReq)
	startResp, err := sessionHandler.HandleStart(context.Background(), startReqJSON)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	startRespTyped, ok := startResp.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", startResp)
	}
	sessionID := startRespTyped.SessionID

	t.Run("update_last_seen_at", func(t *testing.T) {
		// Get initial last_seen_at for session
		var initialLastSeen string
		err := s.RawDB().QueryRow("SELECT last_seen_at FROM sessions WHERE session_id = ?", sessionID).Scan(&initialLastSeen)
		if err != nil {
			t.Fatalf("query last_seen_at: %v", err)
		}

		// Get initial last_seen_at for agent (may be NULL)
		var initialAgentLastSeen sql.NullString
		err = s.RawDB().QueryRow("SELECT last_seen_at FROM agents WHERE agent_id = ?", agentID).Scan(&initialAgentLastSeen)
		if err != nil {
			t.Fatalf("query agent last_seen_at: %v", err)
		}

		// Wait a bit to ensure timestamp changes (intentional timing test)
		time.Sleep(10 * time.Millisecond)

		// Send heartbeat
		req := HeartbeatRequest{
			SessionID: sessionID,
		}
		reqJSON, _ := json.Marshal(req)
		resp, err := sessionHandler.HandleHeartbeat(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("HandleHeartbeat() error: %v", err)
		}

		hbResp, ok := resp.(*HeartbeatResponse)
		if !ok {
			t.Fatalf("response is not *HeartbeatResponse, got %T", resp)
		}

		// Verify response
		if hbResp.SessionID != sessionID {
			t.Errorf("SessionID = %s, want %s", hbResp.SessionID, sessionID)
		}
		if hbResp.LastSeenAt == "" {
			t.Error("LastSeenAt should not be empty")
		}

		// Verify session last_seen_at was updated
		var updatedLastSeen string
		err = s.RawDB().QueryRow("SELECT last_seen_at FROM sessions WHERE session_id = ?", sessionID).Scan(&updatedLastSeen)
		if err != nil {
			t.Fatalf("query updated last_seen_at: %v", err)
		}

		if updatedLastSeen == initialLastSeen {
			t.Error("session last_seen_at should have been updated")
		}

		// Verify agent last_seen_at was updated (should now be non-NULL)
		var updatedAgentLastSeen sql.NullString
		err = s.RawDB().QueryRow("SELECT last_seen_at FROM agents WHERE agent_id = ?", agentID).Scan(&updatedAgentLastSeen)
		if err != nil {
			t.Fatalf("query updated agent last_seen_at: %v", err)
		}

		if !updatedAgentLastSeen.Valid {
			t.Error("agent last_seen_at should not be NULL after heartbeat")
		}

		if initialAgentLastSeen.Valid && updatedAgentLastSeen.String == initialAgentLastSeen.String {
			t.Error("agent last_seen_at should have been updated")
		}
	})

	t.Run("add_scopes", func(t *testing.T) {
		req := HeartbeatRequest{
			SessionID: sessionID,
			AddScopes: []types.Scope{
				{Type: "module", Value: "auth"},
				{Type: "file", Value: "src/main.go"},
			},
		}
		reqJSON, _ := json.Marshal(req)
		_, err := sessionHandler.HandleHeartbeat(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("HandleHeartbeat() error: %v", err)
		}

		// Verify scopes were added
		var scopeCount int
		err = s.RawDB().QueryRow("SELECT COUNT(*) FROM session_scopes WHERE session_id = ?", sessionID).Scan(&scopeCount)
		if err != nil {
			t.Fatalf("query scopes: %v", err)
		}
		if scopeCount != 2 {
			t.Errorf("Expected 2 scopes, got %d", scopeCount)
		}

		// Verify specific scope
		var scopeValue string
		err = s.RawDB().QueryRow("SELECT scope_value FROM session_scopes WHERE session_id = ? AND scope_type = ?",
			sessionID, "module").Scan(&scopeValue)
		if err != nil {
			t.Fatalf("query scope value: %v", err)
		}
		if scopeValue != "auth" {
			t.Errorf("Expected scope_value 'auth', got '%s'", scopeValue)
		}
	})

	t.Run("add_refs", func(t *testing.T) {
		req := HeartbeatRequest{
			SessionID: sessionID,
			AddRefs: []types.Ref{
				{Type: "issue", Value: "beads-123"},
				{Type: "commit", Value: "abc123"},
			},
		}
		reqJSON, _ := json.Marshal(req)
		_, err := sessionHandler.HandleHeartbeat(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("HandleHeartbeat() error: %v", err)
		}

		// Verify refs were added
		var refCount int
		err = s.RawDB().QueryRow("SELECT COUNT(*) FROM session_refs WHERE session_id = ?", sessionID).Scan(&refCount)
		if err != nil {
			t.Fatalf("query refs: %v", err)
		}
		if refCount != 2 {
			t.Errorf("Expected 2 refs, got %d", refCount)
		}
	})

	t.Run("remove_scopes", func(t *testing.T) {
		req := HeartbeatRequest{
			SessionID: sessionID,
			RemoveScopes: []types.Scope{
				{Type: "file", Value: "src/main.go"},
			},
		}
		reqJSON, _ := json.Marshal(req)
		_, err := sessionHandler.HandleHeartbeat(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("HandleHeartbeat() error: %v", err)
		}

		// Verify scope was removed
		var scopeCount int
		err = s.RawDB().QueryRow("SELECT COUNT(*) FROM session_scopes WHERE session_id = ?", sessionID).Scan(&scopeCount)
		if err != nil {
			t.Fatalf("query scopes: %v", err)
		}
		if scopeCount != 1 {
			t.Errorf("Expected 1 scope (after removal), got %d", scopeCount)
		}
	})

	t.Run("remove_refs", func(t *testing.T) {
		req := HeartbeatRequest{
			SessionID: sessionID,
			RemoveRefs: []types.Ref{
				{Type: "commit", Value: "abc123"},
			},
		}
		reqJSON, _ := json.Marshal(req)
		_, err := sessionHandler.HandleHeartbeat(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("HandleHeartbeat() error: %v", err)
		}

		// Verify ref was removed
		var refCount int
		err = s.RawDB().QueryRow("SELECT COUNT(*) FROM session_refs WHERE session_id = ?", sessionID).Scan(&refCount)
		if err != nil {
			t.Fatalf("query refs: %v", err)
		}
		if refCount != 1 {
			t.Errorf("Expected 1 ref (after removal), got %d", refCount)
		}
	})

	t.Run("missing_session_id", func(t *testing.T) {
		req := HeartbeatRequest{
			SessionID: "",
		}
		reqJSON, _ := json.Marshal(req)
		_, err := sessionHandler.HandleHeartbeat(context.Background(), reqJSON)
		if err == nil {
			t.Error("Expected error for missing session_id")
		}
	})

	t.Run("nonexistent_session", func(t *testing.T) {
		req := HeartbeatRequest{
			SessionID: "ses_NONEXISTENT",
		}
		reqJSON, _ := json.Marshal(req)
		_, err := sessionHandler.HandleHeartbeat(context.Background(), reqJSON)
		if err == nil {
			t.Error("Expected error for nonexistent session")
		}
	})

	t.Run("ended_session", func(t *testing.T) {
		// End the session first
		endReq := SessionEndRequest{
			SessionID: sessionID,
			Reason:    "normal",
		}
		endReqJSON, _ := json.Marshal(endReq)
		_, err := sessionHandler.HandleEnd(context.Background(), endReqJSON)
		if err != nil {
			t.Fatalf("end session: %v", err)
		}

		// Try to send heartbeat to ended session
		req := HeartbeatRequest{
			SessionID: sessionID,
		}
		reqJSON, _ := json.Marshal(req)
		_, err = sessionHandler.HandleHeartbeat(context.Background(), reqJSON)
		if err == nil {
			t.Error("Expected error for ended session")
		}
	})
}

func TestHeartbeat_WorkContext(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_456")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Register an agent
	agentHandler := NewAgentHandler(s)
	registerReq := RegisterRequest{
		Role:   "implementer",
		Module: "test",
	}
	registerReqJSON, _ := json.Marshal(registerReq)
	registerResp, err := agentHandler.HandleRegister(context.Background(), registerReqJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	regResp, ok := registerResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", registerResp)
	}
	agentID := regResp.AgentID

	// Setup a git repository for testing
	gitRepo := setupTestGitRepo(t)

	// Start session with worktree ref
	sessionHandler := NewSessionHandler(s)
	startReq := SessionStartRequest{
		AgentID: agentID,
		Refs: []types.Ref{
			{Type: "worktree", Value: gitRepo},
		},
	}
	startReqJSON, _ := json.Marshal(startReq)
	startResp, err := sessionHandler.HandleStart(context.Background(), startReqJSON)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	startRespTyped, ok := startResp.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", startResp)
	}
	sessionID := startRespTyped.SessionID

	// Verify worktree ref was stored
	var storedWorktree string
	err = s.RawDB().QueryRow(`
		SELECT ref_value FROM session_refs WHERE session_id = ? AND ref_type = 'worktree'
	`, sessionID).Scan(&storedWorktree)
	if err != nil {
		t.Fatalf("verify worktree ref stored: %v", err)
	}
	if storedWorktree != gitRepo {
		t.Errorf("Expected worktree ref '%s', got '%s'", gitRepo, storedWorktree)
	}

	// Send heartbeat (should extract and store work context)
	heartbeatReq := HeartbeatRequest{
		SessionID: sessionID,
	}
	heartbeatReqJSON, _ := json.Marshal(heartbeatReq)
	_, err = sessionHandler.HandleHeartbeat(context.Background(), heartbeatReqJSON)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	// Wait for async work context extraction to complete
	time.Sleep(50 * time.Millisecond)

	// Verify work context was stored
	var branch string
	err = s.RawDB().QueryRow(`
		SELECT branch FROM agent_work_contexts WHERE session_id = ?
	`, sessionID).Scan(&branch)
	if err != nil {
		t.Fatalf("query work context: %v (check if gitctx.ExtractWorkContext succeeded)", err)
	}

	if branch != "main" {
		t.Errorf("Expected branch 'main', got '%s'", branch)
	}

	// Verify other fields were populated
	var worktreePath, gitUpdatedAt string
	err = s.RawDB().QueryRow(`
		SELECT worktree_path, git_updated_at FROM agent_work_contexts WHERE session_id = ?
	`, sessionID).Scan(&worktreePath, &gitUpdatedAt)
	if err != nil {
		t.Fatalf("query work context fields: %v", err)
	}

	if worktreePath == "" {
		t.Error("worktree_path should not be empty")
	}
	if gitUpdatedAt == "" {
		t.Error("git_updated_at should not be empty")
	}
}

func TestHeartbeat_NoWorktreeRef(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_789")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Register an agent
	agentHandler := NewAgentHandler(s)
	registerReq := RegisterRequest{
		Role:   "implementer",
		Module: "test",
	}
	registerReqJSON, _ := json.Marshal(registerReq)
	registerResp, err := agentHandler.HandleRegister(context.Background(), registerReqJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	regResp, ok := registerResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", registerResp)
	}
	agentID := regResp.AgentID

	// Start session WITHOUT worktree ref
	sessionHandler := NewSessionHandler(s)
	startReq := SessionStartRequest{
		AgentID: agentID,
	}
	startReqJSON, _ := json.Marshal(startReq)
	startResp, err := sessionHandler.HandleStart(context.Background(), startReqJSON)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	startRespTyped, ok := startResp.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", startResp)
	}
	sessionID := startRespTyped.SessionID

	// Send heartbeat (should gracefully skip work context extraction)
	heartbeatReq := HeartbeatRequest{
		SessionID: sessionID,
	}
	heartbeatReqJSON, _ := json.Marshal(heartbeatReq)
	_, err = sessionHandler.HandleHeartbeat(context.Background(), heartbeatReqJSON)
	if err != nil {
		t.Fatalf("heartbeat should not error without worktree ref: %v", err)
	}

	// Verify no work context was created
	var count int
	err = s.RawDB().QueryRow(`
		SELECT COUNT(*) FROM agent_work_contexts WHERE session_id = ?
	`, sessionID).Scan(&count)
	if err != nil {
		t.Fatalf("query work context count: %v", err)
	}

	if count != 0 {
		t.Errorf("Expected 0 work contexts, got %d", count)
	}
}

// setupTestGitRepo creates a minimal git repository for testing.
func setupTestGitRepo(t *testing.T) string {
	t.Helper()

	repoDir := t.TempDir()

	// Initialize git repo
	runGitCmd(t, repoDir, "init")
	runGitCmd(t, repoDir, "config", "user.name", "Test User")
	runGitCmd(t, repoDir, "config", "user.email", "test@example.com")

	// Create initial commit on main
	runGitCmd(t, repoDir, "checkout", "-b", "main")
	writeTestFile(t, repoDir, "README.md", "# Test Repo")
	runGitCmd(t, repoDir, "add", "README.md")
	runGitCmd(t, repoDir, "commit", "-m", "Initial commit")

	return repoDir
}

// runGitCmd runs a git command in the specified directory.
func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\nOutput: %s", args, err, output)
	}
}

// writeTestFile writes content to a file.
func writeTestFile(t *testing.T, dir, filename, content string) {
	t.Helper()

	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}

func TestSetIntent(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_setintent")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Register an agent
	agentHandler := NewAgentHandler(s)
	registerReq := RegisterRequest{
		Role:   "implementer",
		Module: "test",
	}
	registerReqJSON, _ := json.Marshal(registerReq)
	registerResp, err := agentHandler.HandleRegister(context.Background(), registerReqJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	regResp, ok := registerResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", registerResp)
	}
	agentID := regResp.AgentID

	// Start session
	sessionHandler := NewSessionHandler(s)
	startReq := SessionStartRequest{
		AgentID: agentID,
	}
	startReqJSON, _ := json.Marshal(startReq)
	startResp, err := sessionHandler.HandleStart(context.Background(), startReqJSON)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	startRespTyped, ok := startResp.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", startResp)
	}
	sessionID := startRespTyped.SessionID

	// Test setting intent
	t.Run("set_intent_success", func(t *testing.T) {
		req := SetIntentRequest{
			SessionID: sessionID,
			Intent:    "Refactoring auth flow",
		}
		reqJSON, _ := json.Marshal(req)
		resp, err := sessionHandler.HandleSetIntent(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("set intent: %v", err)
		}

		intentResp, ok := resp.(*SetIntentResponse)
		if !ok {
			t.Fatalf("response is not *SetIntentResponse, got %T", resp)
		}

		if intentResp.SessionID != sessionID {
			t.Errorf("Expected sessionID '%s', got '%s'", sessionID, intentResp.SessionID)
		}
		if intentResp.Intent != "Refactoring auth flow" {
			t.Errorf("Expected intent 'Refactoring auth flow', got '%s'", intentResp.Intent)
		}
		if intentResp.IntentUpdatedAt == "" {
			t.Error("IntentUpdatedAt should not be empty")
		}

		// Verify in database
		var dbIntent string
		err = s.RawDB().QueryRow(`SELECT intent FROM agent_work_contexts WHERE session_id = ?`, sessionID).Scan(&dbIntent)
		if err != nil {
			t.Fatalf("query intent from db: %v", err)
		}
		if dbIntent != "Refactoring auth flow" {
			t.Errorf("Expected db intent 'Refactoring auth flow', got '%s'", dbIntent)
		}
	})

	t.Run("set_intent_clear", func(t *testing.T) {
		req := SetIntentRequest{
			SessionID: sessionID,
			Intent:    "", // Clear intent
		}
		reqJSON, _ := json.Marshal(req)
		resp, err := sessionHandler.HandleSetIntent(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("set intent: %v", err)
		}

		intentRespTyped, ok := resp.(*SetIntentResponse)
		if !ok {
			t.Fatalf("expected *SetIntentResponse, got %T", resp)
		}
		if intentRespTyped.Intent != "" {
			t.Errorf("Expected empty intent, got '%s'", intentRespTyped.Intent)
		}
	})

	t.Run("set_intent_invalid_session", func(t *testing.T) {
		req := SetIntentRequest{
			SessionID: "ses_NONEXISTENT",
			Intent:    "test",
		}
		reqJSON, _ := json.Marshal(req)
		_, err := sessionHandler.HandleSetIntent(context.Background(), reqJSON)
		if err == nil {
			t.Error("Expected error for invalid session")
		}
	})
}

func TestSetTask(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_settask")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Register an agent
	agentHandler := NewAgentHandler(s)
	registerReq := RegisterRequest{
		Role:   "implementer",
		Module: "test",
	}
	registerReqJSON, _ := json.Marshal(registerReq)
	registerResp, err := agentHandler.HandleRegister(context.Background(), registerReqJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	regResp, ok := registerResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", registerResp)
	}
	agentID := regResp.AgentID

	// Start session
	sessionHandler := NewSessionHandler(s)
	startReq := SessionStartRequest{
		AgentID: agentID,
	}
	startReqJSON, _ := json.Marshal(startReq)
	startResp, err := sessionHandler.HandleStart(context.Background(), startReqJSON)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	startRespTyped, ok := startResp.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", startResp)
	}
	sessionID := startRespTyped.SessionID

	// Test setting task
	t.Run("set_task_success", func(t *testing.T) {
		req := SetTaskRequest{
			SessionID:   sessionID,
			CurrentTask: "beads:thrum-xyz",
		}
		reqJSON, _ := json.Marshal(req)
		resp, err := sessionHandler.HandleSetTask(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("set task: %v", err)
		}

		taskResp, ok := resp.(*SetTaskResponse)
		if !ok {
			t.Fatalf("response is not *SetTaskResponse, got %T", resp)
		}

		if taskResp.SessionID != sessionID {
			t.Errorf("Expected sessionID '%s', got '%s'", sessionID, taskResp.SessionID)
		}
		if taskResp.CurrentTask != "beads:thrum-xyz" {
			t.Errorf("Expected task 'beads:thrum-xyz', got '%s'", taskResp.CurrentTask)
		}
		if taskResp.TaskUpdatedAt == "" {
			t.Error("TaskUpdatedAt should not be empty")
		}

		// Verify in database
		var dbTask string
		err = s.RawDB().QueryRow(`SELECT current_task FROM agent_work_contexts WHERE session_id = ?`, sessionID).Scan(&dbTask)
		if err != nil {
			t.Fatalf("query task from db: %v", err)
		}
		if dbTask != "beads:thrum-xyz" {
			t.Errorf("Expected db task 'beads:thrum-xyz', got '%s'", dbTask)
		}
	})

	t.Run("set_task_clear", func(t *testing.T) {
		req := SetTaskRequest{
			SessionID:   sessionID,
			CurrentTask: "", // Clear task
		}
		reqJSON, _ := json.Marshal(req)
		resp, err := sessionHandler.HandleSetTask(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("set task: %v", err)
		}

		taskRespTyped, ok := resp.(*SetTaskResponse)
		if !ok {
			t.Fatalf("expected *SetTaskResponse, got %T", resp)
		}
		if taskRespTyped.CurrentTask != "" {
			t.Errorf("Expected empty task, got '%s'", taskRespTyped.CurrentTask)
		}
	})

	t.Run("set_task_invalid_session", func(t *testing.T) {
		req := SetTaskRequest{
			SessionID:   "ses_NONEXISTENT",
			CurrentTask: "test",
		}
		reqJSON, _ := json.Marshal(req)
		_, err := sessionHandler.HandleSetTask(context.Background(), reqJSON)
		if err == nil {
			t.Error("Expected error for invalid session")
		}
	})

	// Test setting task on ended session
	t.Run("set_task_ended_session", func(t *testing.T) {
		// End the session first
		endReq := SessionEndRequest{
			SessionID: sessionID,
			Reason:    "normal",
		}
		endReqJSON, _ := json.Marshal(endReq)
		_, err := sessionHandler.HandleEnd(context.Background(), endReqJSON)
		if err != nil {
			t.Fatalf("end session: %v", err)
		}

		// Try to set task on ended session
		req := SetTaskRequest{
			SessionID:   sessionID,
			CurrentTask: "should-fail",
		}
		reqJSON, _ := json.Marshal(req)
		_, err = sessionHandler.HandleSetTask(context.Background(), reqJSON)
		if err == nil {
			t.Error("Expected error for ended session")
		}
	})
}

func TestSessionList(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_list")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Register an agent
	agentHandler := NewAgentHandler(s)
	registerReq := RegisterRequest{
		Role:   "implementer",
		Module: "test",
	}
	registerReqJSON, _ := json.Marshal(registerReq)
	registerResp, err := agentHandler.HandleRegister(context.Background(), registerReqJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	regResp, ok := registerResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", registerResp)
	}
	agentID := regResp.AgentID

	// Register a second agent
	registerReq2 := RegisterRequest{
		Role:   "reviewer",
		Module: "test2",
	}
	registerReq2JSON, _ := json.Marshal(registerReq2)
	registerResp2, err := agentHandler.HandleRegister(context.Background(), registerReq2JSON)
	if err != nil {
		t.Fatalf("register second agent: %v", err)
	}
	regResp2, ok := registerResp2.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", registerResp2)
	}
	agentID2 := regResp2.AgentID

	sessionHandler := NewSessionHandler(s)

	// Start first session for agentID
	startReq1 := SessionStartRequest{
		AgentID: agentID,
	}
	startReq1JSON, _ := json.Marshal(startReq1)
	startResp1, err := sessionHandler.HandleStart(context.Background(), startReq1JSON)
	if err != nil {
		t.Fatalf("start session 1: %v", err)
	}
	startResp1Typed, ok := startResp1.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", startResp1)
	}
	sessionID1 := startResp1Typed.SessionID

	// Wait to ensure different timestamps (intentional timing test)
	time.Sleep(10 * time.Millisecond)

	// Start second session for agentID
	startReq2 := SessionStartRequest{
		AgentID: agentID,
	}
	startReq2JSON, _ := json.Marshal(startReq2)
	startResp2, err := sessionHandler.HandleStart(context.Background(), startReq2JSON)
	if err != nil {
		t.Fatalf("start session 2: %v", err)
	}
	startResp2Typed, ok := startResp2.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", startResp2)
	}
	sessionID2 := startResp2Typed.SessionID

	// Start third session for agentID2
	startReq3 := SessionStartRequest{
		AgentID: agentID2,
	}
	startReq3JSON, _ := json.Marshal(startReq3)
	startResp3, err := sessionHandler.HandleStart(context.Background(), startReq3JSON)
	if err != nil {
		t.Fatalf("start session 3: %v", err)
	}
	startResp3Typed, ok := startResp3.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", startResp3)
	}
	sessionID3 := startResp3Typed.SessionID

	// End the first session
	endReq := SessionEndRequest{
		SessionID: sessionID1,
		Reason:    "normal",
	}
	endReqJSON, _ := json.Marshal(endReq)
	_, err = sessionHandler.HandleEnd(context.Background(), endReqJSON)
	if err != nil {
		t.Fatalf("end session 1: %v", err)
	}

	// Test 1: List all sessions (no filters)
	t.Run("list_all_sessions", func(t *testing.T) {
		listReq := ListSessionsRequest{}
		listReqJSON, _ := json.Marshal(listReq)
		resp, err := sessionHandler.HandleList(context.Background(), listReqJSON)
		if err != nil {
			t.Fatalf("list sessions: %v", err)
		}

		listResp, ok := resp.(*ListSessionsResponse)
		if !ok {
			t.Fatalf("response is not *ListSessionsResponse, got %T", resp)
		}

		// Should return all 3 sessions
		if len(listResp.Sessions) != 3 {
			t.Errorf("Expected 3 sessions, got %d", len(listResp.Sessions))
		}

		// Verify sessions are ordered by started_at DESC (most recent first)
		foundSession2 := false
		for _, s := range listResp.Sessions {
			if s.SessionID == sessionID2 {
				foundSession2 = true
				if s.Status != "active" {
					t.Errorf("Session 2 should be active, got %s", s.Status)
				}
			}
			if s.SessionID == sessionID1 {
				if s.Status != "ended" {
					t.Errorf("Session 1 should be ended, got %s", s.Status)
				}
				if s.EndReason != "normal" {
					t.Errorf("Session 1 end reason should be 'normal', got %s", s.EndReason)
				}
			}
		}
		if !foundSession2 {
			t.Error("Session 2 not found in list")
		}
	})

	// Test 2: List only active sessions
	t.Run("list_active_only", func(t *testing.T) {
		listReq := ListSessionsRequest{
			ActiveOnly: true,
		}
		listReqJSON, _ := json.Marshal(listReq)
		resp, err := sessionHandler.HandleList(context.Background(), listReqJSON)
		if err != nil {
			t.Fatalf("list active sessions: %v", err)
		}

		listRespTyped, ok := resp.(*ListSessionsResponse)
		if !ok {
			t.Fatalf("expected *ListSessionsResponse, got %T", resp)
		}
		listResp := listRespTyped

		// Should return only 2 active sessions (session2 and session3)
		if len(listResp.Sessions) != 2 {
			t.Errorf("Expected 2 active sessions, got %d", len(listResp.Sessions))
		}

		for _, s := range listResp.Sessions {
			if s.Status != "active" {
				t.Errorf("Expected all sessions to be active, got %s", s.Status)
			}
			if s.SessionID == sessionID1 {
				t.Error("Session 1 (ended) should not be in active-only list")
			}
		}
	})

	// Test 3: List sessions for specific agent
	t.Run("list_by_agent_id", func(t *testing.T) {
		listReq := ListSessionsRequest{
			AgentID: agentID,
		}
		listReqJSON, _ := json.Marshal(listReq)
		resp, err := sessionHandler.HandleList(context.Background(), listReqJSON)
		if err != nil {
			t.Fatalf("list sessions by agent: %v", err)
		}

		listRespTyped, ok := resp.(*ListSessionsResponse)
		if !ok {
			t.Fatalf("expected *ListSessionsResponse, got %T", resp)
		}
		listResp := listRespTyped

		// Should return only 2 sessions for agentID
		if len(listResp.Sessions) != 2 {
			t.Errorf("Expected 2 sessions for agentID, got %d", len(listResp.Sessions))
		}

		for _, s := range listResp.Sessions {
			if s.AgentID != agentID {
				t.Errorf("Expected all sessions for agent %s, got %s", agentID, s.AgentID)
			}
			if s.SessionID == sessionID3 {
				t.Error("Session 3 (from agentID2) should not be in this list")
			}
		}
	})

	// Test 4: List active sessions for specific agent
	t.Run("list_active_by_agent_id", func(t *testing.T) {
		listReq := ListSessionsRequest{
			AgentID:    agentID,
			ActiveOnly: true,
		}
		listReqJSON, _ := json.Marshal(listReq)
		resp, err := sessionHandler.HandleList(context.Background(), listReqJSON)
		if err != nil {
			t.Fatalf("list active sessions by agent: %v", err)
		}

		listRespTyped, ok := resp.(*ListSessionsResponse)
		if !ok {
			t.Fatalf("expected *ListSessionsResponse, got %T", resp)
		}
		listResp := listRespTyped

		// Should return only 1 active session for agentID (session2)
		if len(listResp.Sessions) != 1 {
			t.Errorf("Expected 1 active session for agentID, got %d", len(listResp.Sessions))
		}

		if len(listResp.Sessions) > 0 {
			s := listResp.Sessions[0]
			if s.SessionID != sessionID2 {
				t.Errorf("Expected session2, got %s", s.SessionID)
			}
			if s.AgentID != agentID {
				t.Errorf("Expected agent %s, got %s", agentID, s.AgentID)
			}
			if s.Status != "active" {
				t.Errorf("Expected active status, got %s", s.Status)
			}
		}
	})

	// Test 5: List with non-existent agent ID
	t.Run("list_nonexistent_agent", func(t *testing.T) {
		listReq := ListSessionsRequest{
			AgentID: "agent:nonexistent:XYZ123",
		}
		listReqJSON, _ := json.Marshal(listReq)
		resp, err := sessionHandler.HandleList(context.Background(), listReqJSON)
		if err != nil {
			t.Fatalf("list sessions for nonexistent agent: %v", err)
		}

		listRespTyped, ok := resp.(*ListSessionsResponse)
		if !ok {
			t.Fatalf("expected *ListSessionsResponse, got %T", resp)
		}
		listResp := listRespTyped

		// Should return empty list
		if len(listResp.Sessions) != 0 {
			t.Errorf("Expected 0 sessions for nonexistent agent, got %d", len(listResp.Sessions))
		}
	})
}

func TestSessionSetIntent(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_setintent2")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Register an agent
	agentHandler := NewAgentHandler(s)
	registerReq := RegisterRequest{
		Role:   "implementer",
		Module: "test",
	}
	registerReqJSON, _ := json.Marshal(registerReq)
	registerResp, err := agentHandler.HandleRegister(context.Background(), registerReqJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	regResp, ok := registerResp.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", registerResp)
	}
	agentID := regResp.AgentID

	// Start session
	sessionHandler := NewSessionHandler(s)
	startReq := SessionStartRequest{
		AgentID: agentID,
	}
	startReqJSON, _ := json.Marshal(startReq)
	startResp, err := sessionHandler.HandleStart(context.Background(), startReqJSON)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	startRespTyped, ok := startResp.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", startResp)
	}
	sessionID := startRespTyped.SessionID

	// Test 1: Set intent and verify it appears in session list
	t.Run("set_and_verify_intent", func(t *testing.T) {
		// Set intent
		setIntentReq := SetIntentRequest{
			SessionID: sessionID,
			Intent:    "Refactoring authentication module",
		}
		setIntentReqJSON, _ := json.Marshal(setIntentReq)
		resp, err := sessionHandler.HandleSetIntent(context.Background(), setIntentReqJSON)
		if err != nil {
			t.Fatalf("set intent: %v", err)
		}

		intentResp, ok := resp.(*SetIntentResponse)
		if !ok {
			t.Fatalf("response is not *SetIntentResponse, got %T", resp)
		}

		if intentResp.SessionID != sessionID {
			t.Errorf("Expected sessionID '%s', got '%s'", sessionID, intentResp.SessionID)
		}
		if intentResp.Intent != "Refactoring authentication module" {
			t.Errorf("Expected intent 'Refactoring authentication module', got '%s'", intentResp.Intent)
		}
		if intentResp.IntentUpdatedAt == "" {
			t.Error("IntentUpdatedAt should not be empty")
		}

		// List sessions and verify intent is visible
		listReq := ListSessionsRequest{}
		listReqJSON, _ := json.Marshal(listReq)
		listResp, err := sessionHandler.HandleList(context.Background(), listReqJSON)
		if err != nil {
			t.Fatalf("list sessions: %v", err)
		}

		listRespTyped, ok := listResp.(*ListSessionsResponse)
		if !ok {
			t.Fatalf("expected *ListSessionsResponse, got %T", listResp)
		}
		listResult := listRespTyped
		found := false
		for _, s := range listResult.Sessions {
			if s.SessionID == sessionID {
				found = true
				if s.Intent != "Refactoring authentication module" {
					t.Errorf("Expected intent 'Refactoring authentication module' in list, got '%s'", s.Intent)
				}
			}
		}
		if !found {
			t.Error("Session not found in list")
		}
	})

	// Test 2: Update intent with different value
	t.Run("update_intent", func(t *testing.T) {
		setIntentReq := SetIntentRequest{
			SessionID: sessionID,
			Intent:    "Fixing bug in login flow",
		}
		setIntentReqJSON, _ := json.Marshal(setIntentReq)
		resp, err := sessionHandler.HandleSetIntent(context.Background(), setIntentReqJSON)
		if err != nil {
			t.Fatalf("update intent: %v", err)
		}

		intentRespTyped, ok := resp.(*SetIntentResponse)
		if !ok {
			t.Fatalf("expected *SetIntentResponse, got %T", resp)
		}
		intentResp := intentRespTyped
		if intentResp.Intent != "Fixing bug in login flow" {
			t.Errorf("Expected updated intent 'Fixing bug in login flow', got '%s'", intentResp.Intent)
		}

		// Verify in database
		var dbIntent string
		err = s.RawDB().QueryRow(`SELECT intent FROM agent_work_contexts WHERE session_id = ?`, sessionID).Scan(&dbIntent)
		if err != nil {
			t.Fatalf("query intent from db: %v", err)
		}
		if dbIntent != "Fixing bug in login flow" {
			t.Errorf("Expected db intent 'Fixing bug in login flow', got '%s'", dbIntent)
		}
	})

	// Test 3: Set intent on ended session should fail
	t.Run("set_intent_ended_session", func(t *testing.T) {
		// End the session
		endReq := SessionEndRequest{
			SessionID: sessionID,
			Reason:    "normal",
		}
		endReqJSON, _ := json.Marshal(endReq)
		_, err := sessionHandler.HandleEnd(context.Background(), endReqJSON)
		if err != nil {
			t.Fatalf("end session: %v", err)
		}

		// Try to set intent on ended session
		setIntentReq := SetIntentRequest{
			SessionID: sessionID,
			Intent:    "should-fail",
		}
		setIntentReqJSON, _ := json.Marshal(setIntentReq)
		_, err = sessionHandler.HandleSetIntent(context.Background(), setIntentReqJSON)
		if err == nil {
			t.Error("Expected error for ended session")
		}
	})
}
