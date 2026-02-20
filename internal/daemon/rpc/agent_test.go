package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
)

func TestAgentRegister(t *testing.T) {
	tests := []struct {
		name         string
		request      RegisterRequest
		wantStatus   string
		wantConflict bool
		wantErr      bool
		setupFunc    func(*state.State) error // Optional setup before test
	}{
		{
			name: "new_agent_registration",
			request: RegisterRequest{
				Role:    "implementer",
				Module:  "auth",
				Display: "Auth Implementer",
			},
			wantStatus:   "registered",
			wantConflict: false,
			wantErr:      false,
		},
		{
			name: "same_agent_returning",
			request: RegisterRequest{
				Role:    "implementer",
				Module:  "auth",
				Display: "Auth Implementer",
			},
			wantStatus:   "registered",
			wantConflict: false,
			wantErr:      false,
			setupFunc: func(s *state.State) error {
				// Register agent first
				h := NewAgentHandler(s)
				req := RegisterRequest{
					Role:   "implementer",
					Module: "auth",
				}
				reqJSON, _ := json.Marshal(req)
				_, err := h.HandleRegister(context.Background(), reqJSON)
				return err
			},
		},
		{
			name: "same_agent_re_register",
			request: RegisterRequest{
				Role:       "implementer",
				Module:     "auth",
				Display:    "Auth Implementer Updated",
				ReRegister: true,
			},
			wantStatus:   "updated",
			wantConflict: false,
			wantErr:      false,
			setupFunc: func(s *state.State) error {
				// Register agent first
				h := NewAgentHandler(s)
				req := RegisterRequest{
					Role:   "implementer",
					Module: "auth",
				}
				reqJSON, _ := json.Marshal(req)
				_, err := h.HandleRegister(context.Background(), reqJSON)
				return err
			},
		},
		{
			name: "conflict_different_agent_same_role_module",
			request: RegisterRequest{
				Role:    "implementer",
				Module:  "auth",
				Display: "Different Agent",
			},
			wantStatus:   "conflict",
			wantConflict: true,
			wantErr:      false,
			setupFunc: func(s *state.State) error {
				// Register different module first to create different agent
				h := NewAgentHandler(s)
				req := RegisterRequest{
					Role:   "implementer",
					Module: "other",
				}
				reqJSON, _ := json.Marshal(req)
				_, err := h.HandleRegister(context.Background(), reqJSON)
				if err != nil {
					return err
				}

				// Now manually insert a conflicting agent (different agent_id, same role+module)
				// This simulates the conflict scenario
				_, err = s.RawDB().Exec(`
					INSERT INTO agents (agent_id, kind, role, module, display, registered_at)
					VALUES (?, ?, ?, ?, ?, ?)
				`, "agent:implementer:CONFLICT00", "agent", "implementer", "auth", "", "2026-01-01T00:00:00Z")
				return err
			},
		},
		{
			name: "force_override_conflict",
			request: RegisterRequest{
				Role:    "implementer",
				Module:  "auth",
				Display: "New Agent",
				Force:   true,
			},
			wantStatus:   "registered",
			wantConflict: false,
			wantErr:      false,
			setupFunc: func(s *state.State) error {
				// Insert conflicting agent
				_, err := s.RawDB().Exec(`
					INSERT INTO agents (agent_id, kind, role, module, display, registered_at)
					VALUES (?, ?, ?, ?, ?, ?)
				`, "agent:implementer:CONFLICT00", "agent", "implementer", "auth", "", "2026-01-01T00:00:00Z")
				return err
			},
		},
		{
			name: "missing_role",
			request: RegisterRequest{
				Module: "auth",
			},
			wantErr: true,
		},
		{
			name: "missing_module",
			request: RegisterRequest{
				Role: "implementer",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory and state
			tmpDir := t.TempDir()
			thrumDir := filepath.Join(tmpDir, ".thrum")

			s, err := state.NewState(thrumDir, thrumDir, "test_repo_123")
			if err != nil {
				t.Fatalf("create state: %v", err)
			}
			defer func() { _ = s.Close() }()

			// Run setup if provided
			if tt.setupFunc != nil {
				if err := tt.setupFunc(s); err != nil {
					t.Fatalf("setup failed: %v", err)
				}
			}

			// Create handler and execute request
			handler := NewAgentHandler(s)
			reqJSON, err := json.Marshal(tt.request)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}

			resp, err := handler.HandleRegister(context.Background(), reqJSON)

			// Check error expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("HandleRegister() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return // Expected error, test passed
			}

			// Parse response
			regResp, ok := resp.(*RegisterResponse)
			if !ok {
				t.Fatalf("response is not *RegisterResponse, got %T", resp)
			}

			// Check status
			if regResp.Status != tt.wantStatus {
				t.Errorf("Status = %s, want %s", regResp.Status, tt.wantStatus)
			}

			// Check conflict info
			hasConflict := regResp.Conflict != nil
			if hasConflict != tt.wantConflict {
				t.Errorf("HasConflict = %v, want %v", hasConflict, tt.wantConflict)
			}

			// For successful registrations, verify agent_id is set
			if !tt.wantConflict && regResp.AgentID == "" {
				t.Error("AgentID should not be empty for successful registration")
			}

			// Verify agent was written to database (except for conflicts)
			if !tt.wantConflict && regResp.Status != "conflict" {
				var count int
				err = s.RawDB().QueryRow("SELECT COUNT(*) FROM agents WHERE agent_id = ?", regResp.AgentID).Scan(&count)
				if err != nil {
					t.Errorf("query agent: %v", err)
				}
				if count == 0 {
					t.Error("Agent not found in database after registration")
				}
			}
		})
	}
}

func TestAgentList(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Register multiple agents
	handler := NewAgentHandler(s)
	agents := []RegisterRequest{
		{Role: "implementer", Module: "auth", Display: "Auth Impl"},
		{Role: "implementer", Module: "sync", Display: "Sync Impl"},
		{Role: "planner", Module: "arch", Display: "Architect"},
	}

	for _, agent := range agents {
		reqJSON, _ := json.Marshal(agent)
		_, err := handler.HandleRegister(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("register agent: %v", err)
		}
	}

	tests := []struct {
		name      string
		request   ListAgentsRequest
		wantCount int
	}{
		{
			name:      "list_all_agents",
			request:   ListAgentsRequest{},
			wantCount: 3,
		},
		{
			name:      "filter_by_role",
			request:   ListAgentsRequest{Role: "implementer"},
			wantCount: 2,
		},
		{
			name:      "filter_by_module",
			request:   ListAgentsRequest{Module: "auth"},
			wantCount: 1,
		},
		{
			name:      "filter_by_role_and_module",
			request:   ListAgentsRequest{Role: "implementer", Module: "sync"},
			wantCount: 1,
		},
		{
			name:      "no_matches",
			request:   ListAgentsRequest{Role: "nonexistent"},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqJSON, _ := json.Marshal(tt.request)
			resp, err := handler.HandleList(context.Background(), reqJSON)
			if err != nil {
				t.Fatalf("HandleList() error = %v", err)
			}

			listResp, ok := resp.(*ListAgentsResponse)
			if !ok {
				t.Fatalf("response is not *ListAgentsResponse, got %T", resp)
			}

			if len(listResp.Agents) != tt.wantCount {
				t.Errorf("Agent count = %d, want %d", len(listResp.Agents), tt.wantCount)
			}

			// Verify all agents have required fields
			for _, agent := range listResp.Agents {
				if agent.AgentID == "" {
					t.Error("Agent has empty AgentID")
				}
				if agent.Role == "" {
					t.Error("Agent has empty Role")
				}
				if agent.Module == "" {
					t.Error("Agent has empty Module")
				}
			}
		})
	}
}

func TestAgentWhoami(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Set environment variables for identity
	t.Setenv("THRUM_ROLE", "implementer")
	t.Setenv("THRUM_MODULE", "test")

	handler := NewAgentHandler(s)

	t.Run("whoami_without_session", func(t *testing.T) {
		resp, err := handler.HandleWhoami(context.Background(), json.RawMessage("{}"))
		if err != nil {
			t.Fatalf("HandleWhoami() error = %v", err)
		}

		whoamiResp, ok := resp.(*WhoamiResponse)
		if !ok {
			t.Fatalf("response is not *WhoamiResponse, got %T", resp)
		}

		if whoamiResp.Role != "implementer" {
			t.Errorf("Role = %s, want implementer", whoamiResp.Role)
		}
		if whoamiResp.Module != "test" {
			t.Errorf("Module = %s, want test", whoamiResp.Module)
		}
		if whoamiResp.Source != "environment" {
			t.Errorf("Source = %s, want environment", whoamiResp.Source)
		}
		if whoamiResp.SessionID != "" {
			t.Error("SessionID should be empty when no active session")
		}
	})

	t.Run("whoami_with_active_session", func(t *testing.T) {
		// Register agent and start session
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
		sessResp, ok := startResp.(*SessionStartResponse)
		if !ok {
			t.Fatalf("expected *SessionStartResponse, got %T", startResp)
		}
		sessionID := sessResp.SessionID

		// Now call whoami
		resp, err := handler.HandleWhoami(context.Background(), json.RawMessage("{}"))
		if err != nil {
			t.Fatalf("HandleWhoami() error = %v", err)
		}

		whoamiResp, ok := resp.(*WhoamiResponse)
		if !ok {
			t.Fatalf("response is not *WhoamiResponse, got %T", resp)
		}

		if whoamiResp.SessionID != sessionID {
			t.Errorf("SessionID = %s, want %s", whoamiResp.SessionID, sessionID)
		}
		if whoamiResp.SessionStart == "" {
			t.Error("SessionStart should not be empty when active session exists")
		}
	})
}

func TestListContext(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_listctx")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Setup: Register agents and create work contexts
	agentHandler := NewAgentHandler(s)
	sessionHandler := NewSessionHandler(s)

	// Register agent 1
	reg1 := RegisterRequest{Role: "planner", Module: "test"}
	reg1JSON, _ := json.Marshal(reg1)
	resp1, _ := agentHandler.HandleRegister(context.Background(), reg1JSON)
	regResp1, ok := resp1.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", resp1)
	}
	agent1ID := regResp1.AgentID

	// Register agent 2
	reg2 := RegisterRequest{Role: "implementer", Module: "test"}
	reg2JSON, _ := json.Marshal(reg2)
	resp2, _ := agentHandler.HandleRegister(context.Background(), reg2JSON)
	regResp2, ok := resp2.(*RegisterResponse)
	if !ok {
		t.Fatalf("expected *RegisterResponse, got %T", resp2)
	}
	agent2ID := regResp2.AgentID

	// Start session 1
	start1 := SessionStartRequest{AgentID: agent1ID}
	start1JSON, _ := json.Marshal(start1)
	startResp1, _ := sessionHandler.HandleStart(context.Background(), start1JSON)
	sessResp1, ok := startResp1.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", startResp1)
	}
	session1ID := sessResp1.SessionID

	// Start session 2
	start2 := SessionStartRequest{AgentID: agent2ID}
	start2JSON, _ := json.Marshal(start2)
	startResp2, _ := sessionHandler.HandleStart(context.Background(), start2JSON)
	sessResp2, ok := startResp2.(*SessionStartResponse)
	if !ok {
		t.Fatalf("expected *SessionStartResponse, got %T", startResp2)
	}
	session2ID := sessResp2.SessionID

	// Create work contexts manually
	_, err = s.RawDB().Exec(`
		INSERT INTO agent_work_contexts (
			session_id, agent_id, branch, worktree_path,
			unmerged_commits, uncommitted_files, changed_files, git_updated_at,
			current_task, intent
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, session1ID, agent1ID, "feature/auth", "/path/to/repo1",
		`[{"sha":"abc123","message":"Add auth","files":["auth.go"]}]`,
		`["auth.go","user.go"]`,
		`["auth.go"]`,
		"2026-01-01T12:00:00Z",
		"beads:thrum-123",
		"Implementing authentication")
	if err != nil {
		t.Fatalf("insert work context 1: %v", err)
	}

	_, err = s.RawDB().Exec(`
		INSERT INTO agent_work_contexts (
			session_id, agent_id, branch, worktree_path,
			unmerged_commits, uncommitted_files, changed_files, git_updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, session2ID, agent2ID, "feature/ui", "/path/to/repo2",
		`[]`,
		`["ui.go"]`,
		`["ui.go"]`,
		"2026-01-01T13:00:00Z")

	if err != nil {
		t.Fatalf("insert work contexts: %v", err)
	}

	t.Run("list_all", func(t *testing.T) {
		req := ListContextRequest{}
		reqJSON, _ := json.Marshal(req)
		resp, err := agentHandler.HandleListContext(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("HandleListContext() error = %v", err)
		}

		listResp, ok := resp.(*ListContextResponse)
		if !ok {
			t.Fatalf("response is not *ListContextResponse, got %T", resp)
		}

		if len(listResp.Contexts) != 2 {
			t.Errorf("Expected 2 contexts, got %d", len(listResp.Contexts))
		}

		// Verify first context (most recent by git_updated_at)
		if len(listResp.Contexts) > 0 {
			ctx := listResp.Contexts[0]
			if ctx.Branch != "feature/ui" {
				t.Errorf("Expected first context branch 'feature/ui', got '%s'", ctx.Branch)
			}
		}
	})

	t.Run("filter_by_agent", func(t *testing.T) {
		req := ListContextRequest{AgentID: agent1ID}
		reqJSON, _ := json.Marshal(req)
		resp, err := agentHandler.HandleListContext(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("HandleListContext() error = %v", err)
		}

		listResp, ok := resp.(*ListContextResponse)
		if !ok {
			t.Fatalf("expected *ListContextResponse, got %T", resp)
		}
		if len(listResp.Contexts) != 1 {
			t.Errorf("Expected 1 context for agent1, got %d", len(listResp.Contexts))
		}

		if len(listResp.Contexts) > 0 {
			if listResp.Contexts[0].AgentID != agent1ID {
				t.Errorf("Expected agent %s, got %s", agent1ID, listResp.Contexts[0].AgentID)
			}
		}
	})

	t.Run("filter_by_branch", func(t *testing.T) {
		req := ListContextRequest{Branch: "feature/auth"}
		reqJSON, _ := json.Marshal(req)
		resp, err := agentHandler.HandleListContext(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("HandleListContext() error = %v", err)
		}

		listResp, ok := resp.(*ListContextResponse)
		if !ok {
			t.Fatalf("expected *ListContextResponse, got %T", resp)
		}
		if len(listResp.Contexts) != 1 {
			t.Errorf("Expected 1 context for branch feature/auth, got %d", len(listResp.Contexts))
		}

		if len(listResp.Contexts) > 0 {
			if listResp.Contexts[0].Branch != "feature/auth" {
				t.Errorf("Expected branch 'feature/auth', got '%s'", listResp.Contexts[0].Branch)
			}
		}
	})

	t.Run("filter_by_file", func(t *testing.T) {
		req := ListContextRequest{File: "auth.go"}
		reqJSON, _ := json.Marshal(req)
		resp, err := agentHandler.HandleListContext(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("HandleListContext() error = %v", err)
		}

		listResp, ok := resp.(*ListContextResponse)
		if !ok {
			t.Fatalf("expected *ListContextResponse, got %T", resp)
		}
		if len(listResp.Contexts) != 1 {
			t.Errorf("Expected 1 context with auth.go, got %d", len(listResp.Contexts))
		}

		if len(listResp.Contexts) > 0 {
			ctx := listResp.Contexts[0]
			// Verify the context has auth.go in either changed or uncommitted
			hasAuth := false
			for _, f := range ctx.ChangedFiles {
				if f == "auth.go" {
					hasAuth = true
					break
				}
			}
			for _, f := range ctx.UncommittedFiles {
				if f == "auth.go" {
					hasAuth = true
					break
				}
			}
			if !hasAuth {
				t.Error("Expected context to have auth.go in files")
			}
		}
	})

	t.Run("empty_result", func(t *testing.T) {
		req := ListContextRequest{AgentID: "agent:nonexistent:ABC"}
		reqJSON, _ := json.Marshal(req)
		resp, err := agentHandler.HandleListContext(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("HandleListContext() error = %v", err)
		}

		listResp, ok := resp.(*ListContextResponse)
		if !ok {
			t.Fatalf("expected *ListContextResponse, got %T", resp)
		}
		if len(listResp.Contexts) != 0 {
			t.Errorf("Expected 0 contexts for nonexistent agent, got %d", len(listResp.Contexts))
		}
	})

	t.Run("verify_fields", func(t *testing.T) {
		req := ListContextRequest{AgentID: agent1ID}
		reqJSON, _ := json.Marshal(req)
		resp, err := agentHandler.HandleListContext(context.Background(), reqJSON)
		if err != nil {
			t.Fatalf("HandleListContext() error = %v", err)
		}

		listResp, ok := resp.(*ListContextResponse)
		if !ok {
			t.Fatalf("expected *ListContextResponse, got %T", resp)
		}
		if len(listResp.Contexts) == 0 {
			t.Fatal("Expected at least 1 context")
		}

		ctx := listResp.Contexts[0]
		if ctx.CurrentTask != "beads:thrum-123" {
			t.Errorf("Expected task 'beads:thrum-123', got '%s'", ctx.CurrentTask)
		}
		if ctx.Intent != "Implementing authentication" {
			t.Errorf("Expected intent 'Implementing authentication', got '%s'", ctx.Intent)
		}
		if len(ctx.UnmergedCommits) != 1 {
			t.Errorf("Expected 1 unmerged commit, got %d", len(ctx.UnmergedCommits))
		} else {
			if ctx.UnmergedCommits[0].SHA != "abc123" {
				t.Errorf("Expected commit SHA 'abc123', got '%s'", ctx.UnmergedCommits[0].SHA)
			}
		}
	})
}

func TestAgentDelete(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	repoPath := tmpDir

	// Create .thrum structure
	thrumDir := filepath.Join(repoPath, ".thrum")
	syncDir := filepath.Join(thrumDir, "sync")
	identitiesDir := filepath.Join(thrumDir, "identities")
	messagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		t.Fatalf("Failed to create identities directory: %v", err)
	}
	if err := os.MkdirAll(messagesDir, 0750); err != nil {
		t.Fatalf("Failed to create messages directory: %v", err)
	}

	// Create state
	st, err := state.NewState(thrumDir, syncDir, "test-repo")
	if err != nil {
		t.Fatalf("Failed to create state: %v", err)
	}
	defer func() { _ = st.Close() }()

	agentHandler := NewAgentHandler(st)

	// Register a test agent
	registerReq := RegisterRequest{
		Name:    "test_agent",
		Role:    "tester",
		Module:  "cleanup",
		Display: "Test Agent",
	}
	registerJSON, _ := json.Marshal(registerReq)
	_, err = agentHandler.HandleRegister(context.Background(), registerJSON)
	if err != nil {
		t.Fatalf("Failed to register test agent: %v", err)
	}

	// Create identity and message files manually (normally created by CLI and message sending)
	identityPath := filepath.Join(identitiesDir, "test_agent.json")
	messagePath := filepath.Join(messagesDir, "test_agent.jsonl")

	// Create identity file
	identityData := []byte(`{"version":2,"repo_id":"test-repo","agent":{"kind":"agent","name":"test_agent","role":"tester","module":"cleanup"},"worktree":"test"}`)
	if err := os.WriteFile(identityPath, identityData, 0600); err != nil {
		t.Fatalf("Failed to create identity file: %v", err)
	}

	// Create message file
	if err := os.WriteFile(messagePath, []byte{}, 0600); err != nil {
		t.Fatalf("Failed to create message file: %v", err)
	}

	t.Run("delete_existing_agent", func(t *testing.T) {
		deleteReq := DeleteAgentRequest{
			Name: "test_agent",
		}
		deleteJSON, _ := json.Marshal(deleteReq)

		resp, err := agentHandler.HandleDelete(context.Background(), deleteJSON)
		if err != nil {
			t.Fatalf("HandleDelete() error = %v", err)
		}

		deleteResp, ok := resp.(*DeleteAgentResponse)
		if !ok {
			t.Fatalf("expected *DeleteAgentResponse, got %T", resp)
		}
		if !deleteResp.Deleted {
			t.Errorf("Expected Deleted = true, got false")
		}
		if deleteResp.AgentID != "test_agent" {
			t.Errorf("Expected AgentID = 'test_agent', got '%s'", deleteResp.AgentID)
		}

		// Verify identity file was deleted
		if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
			t.Errorf("Identity file still exists after deletion: %s", identityPath)
		}

		// Verify message file was deleted
		if _, err := os.Stat(messagePath); !os.IsNotExist(err) {
			t.Errorf("Message file still exists after deletion: %s", messagePath)
		}

		// Verify agent was removed from database
		var count int
		err = st.RawDB().QueryRow("SELECT COUNT(*) FROM agents WHERE agent_id = ?", "test_agent").Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query database: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 agents in database, got %d", count)
		}
	})

	t.Run("delete_nonexistent_agent", func(t *testing.T) {
		deleteReq := DeleteAgentRequest{
			Name: "nonexistent",
		}
		deleteJSON, _ := json.Marshal(deleteReq)

		_, err := agentHandler.HandleDelete(context.Background(), deleteJSON)
		if err == nil {
			t.Error("Expected error when deleting nonexistent agent")
		}
		if err != nil && err.Error() != "agent not found: nonexistent" {
			t.Errorf("Expected 'agent not found' error, got: %v", err)
		}
	})

	t.Run("delete_missing_name", func(t *testing.T) {
		deleteReq := DeleteAgentRequest{
			Name: "",
		}
		deleteJSON, _ := json.Marshal(deleteReq)

		_, err := agentHandler.HandleDelete(context.Background(), deleteJSON)
		if err == nil {
			t.Error("Expected error when name is missing")
		}
		if err != nil && err.Error() != "agent name is required" {
			t.Errorf("Expected 'agent name is required' error, got: %v", err)
		}
	})

	t.Run("delete_invalid_name", func(t *testing.T) {
		deleteReq := DeleteAgentRequest{
			Name: "invalid-name-with-hyphens",
		}
		deleteJSON, _ := json.Marshal(deleteReq)

		_, err := agentHandler.HandleDelete(context.Background(), deleteJSON)
		if err == nil {
			t.Error("Expected error for invalid agent name")
		}
	})
}

func TestHandleCleanup_DryRun(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	agentHandler := NewAgentHandler(s)
	ctx := context.Background()

	// Register an agent (no identity file → will be detected as orphan)
	registerReq := RegisterRequest{Role: "tester", Module: "test"}
	registerJSON, _ := json.Marshal(registerReq)
	_, err = agentHandler.HandleRegister(ctx, registerJSON)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}

	t.Run("dry_run_no_agents_orphaned", func(t *testing.T) {
		// With identities dir present and identity file created during register,
		// agents won't be orphaned unless identity file is missing
		cleanupReq := CleanupAgentRequest{DryRun: true, Threshold: 9999}
		cleanupJSON, _ := json.Marshal(cleanupReq)
		resp, err := agentHandler.HandleCleanup(ctx, cleanupJSON)
		if err != nil {
			t.Fatalf("HandleCleanup: %v", err)
		}
		result, ok := resp.(*CleanupAgentResponse)
		if !ok {
			t.Fatalf("expected *CleanupAgentResponse, got %T", resp)
		}
		if !result.DryRun {
			t.Error("Expected dry_run=true in response")
		}
	})

	t.Run("dry_run_detects_missing_identity", func(t *testing.T) {
		// Register another agent, then remove its identity file
		registerReq2 := RegisterRequest{Role: "reviewer", Module: "test"}
		registerJSON2, _ := json.Marshal(registerReq2)
		resp, err := agentHandler.HandleRegister(ctx, registerJSON2)
		if err != nil {
			t.Fatalf("register agent 2: %v", err)
		}
		regResp, ok := resp.(*RegisterResponse)
		if !ok {
			t.Fatalf("expected *RegisterResponse, got %T", resp)
		}
		agentID := regResp.AgentID

		// Remove the identity file to make it an orphan
		identityPath := filepath.Join(thrumDir, "identities", agentID+".json")
		_ = os.Remove(identityPath)

		cleanupReq := CleanupAgentRequest{DryRun: true, Threshold: 30}
		cleanupJSON, _ := json.Marshal(cleanupReq)
		cleanupResp, err := agentHandler.HandleCleanup(ctx, cleanupJSON)
		if err != nil {
			t.Fatalf("HandleCleanup: %v", err)
		}
		result, ok := cleanupResp.(*CleanupAgentResponse)
		if !ok {
			t.Fatalf("expected *CleanupAgentResponse, got %T", cleanupResp)
		}
		if len(result.Orphans) == 0 {
			t.Error("Expected at least 1 orphan with missing identity")
		}
	})

	t.Run("non_force_returns_orphans", func(t *testing.T) {
		cleanupReq := CleanupAgentRequest{DryRun: false, Force: false, Threshold: 30}
		cleanupJSON, _ := json.Marshal(cleanupReq)
		resp, err := agentHandler.HandleCleanup(ctx, cleanupJSON)
		if err != nil {
			t.Fatalf("HandleCleanup: %v", err)
		}
		result, ok := resp.(*CleanupAgentResponse)
		if !ok {
			t.Fatalf("expected *CleanupAgentResponse, got %T", resp)
		}
		if result.DryRun {
			t.Error("Expected dry_run=false")
		}
		if result.Message != "Use --force to delete all orphans without prompting" {
			t.Errorf("Unexpected message: %s", result.Message)
		}
	})

	t.Run("force_mode_attempts_deletion", func(t *testing.T) {
		cleanupReq := CleanupAgentRequest{DryRun: false, Force: true, Threshold: 30}
		cleanupJSON, _ := json.Marshal(cleanupReq)
		resp, err := agentHandler.HandleCleanup(ctx, cleanupJSON)
		if err != nil {
			t.Fatalf("HandleCleanup: %v", err)
		}
		result, ok := resp.(*CleanupAgentResponse)
		if !ok {
			t.Fatalf("expected *CleanupAgentResponse, got %T", resp)
		}
		if result.DryRun {
			t.Error("Expected dry_run=false in force mode")
		}
		// Force mode runs HandleDelete for each orphan; some may fail validation
		// but the response should still be returned
		if result.Message == "" {
			t.Error("Expected non-empty message")
		}
	})
}

func TestGetMessageCount(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	agentHandler := NewAgentHandler(s)
	agentID := "agent:tester:ABC123"

	// No messages → count should be 0
	count := agentHandler.getMessageCount(context.Background(), agentID)
	if count != 0 {
		t.Errorf("Expected 0 messages, got %d", count)
	}

	// Insert a message directly into SQLite
	_, err = s.RawDB().Exec(`INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES (?, ?, ?, datetime('now'), 'markdown', 'test')`,
		"msg_test001", agentID, "ses_test001")
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}

	count = agentHandler.getMessageCount(context.Background(), agentID)
	if count != 1 {
		t.Errorf("Expected 1 message, got %d", count)
	}

	// Non-existent agent → 0
	count = agentHandler.getMessageCount(context.Background(), "agent:ghost:XYZ999")
	if count != 0 {
		t.Errorf("Expected 0 for non-existent agent, got %d", count)
	}
}

func TestNewMessageHandlerWithDispatcher(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewMessageHandlerWithDispatcher(s, nil)
	if handler == nil {
		t.Fatal("Expected non-nil handler")
	}
	if handler.state != s {
		t.Error("Handler state mismatch")
	}
}

// TestBuildForAgentValues_NameOnly verifies that buildForAgentValues returns only
// the agent name/ID, not the role, after the name-only routing change.
func TestBuildForAgentValues_NameOnly(t *testing.T) {
	values := buildForAgentValues("impl_api", "implementer")
	if len(values) != 1 || values[0] != "impl_api" {
		t.Errorf("expected [impl_api], got %v", values)
	}
}

func TestBuildForAgentValues_EmptyAgent(t *testing.T) {
	// When forAgent is empty, even with a role, should return nil
	values := buildForAgentValues("", "implementer")
	if values != nil {
		t.Errorf("expected nil when forAgent is empty, got %v", values)
	}
}

func TestBuildForAgentValues_BothEmpty(t *testing.T) {
	values := buildForAgentValues("", "")
	if values != nil {
		t.Errorf("expected nil when both are empty, got %v", values)
	}
}

// TestRegisterCreatesRoleGroup verifies that registering an agent with a role
// auto-creates a group for that role in the groups and group_members tables.
func TestRegisterCreatesRoleGroup(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_rolegroup")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)
	ctx := context.Background()

	req := RegisterRequest{
		Role:   "implementer",
		Module: "auth",
	}
	reqJSON, _ := json.Marshal(req)
	_, err = handler.HandleRegister(ctx, reqJSON)
	if err != nil {
		t.Fatalf("HandleRegister() error = %v", err)
	}

	// Verify the group "implementer" was created
	var groupCount int
	err = s.RawDB().QueryRow(`SELECT COUNT(*) FROM groups WHERE name = ?`, "implementer").Scan(&groupCount)
	if err != nil {
		t.Fatalf("query groups: %v", err)
	}
	if groupCount != 1 {
		t.Errorf("expected 1 group named 'implementer', got %d", groupCount)
	}

	// Verify a group_member of type 'role' with value 'implementer' exists
	var memberCount int
	err = s.RawDB().QueryRow(`
		SELECT COUNT(*) FROM group_members gm
		JOIN groups g ON g.group_id = gm.group_id
		WHERE g.name = ? AND gm.member_type = 'role' AND gm.member_value = ?
	`, "implementer", "implementer").Scan(&memberCount)
	if err != nil {
		t.Fatalf("query group_members: %v", err)
	}
	if memberCount != 1 {
		t.Errorf("expected 1 group member of type 'role' for 'implementer', got %d", memberCount)
	}
}

// TestRegisterRoleGroupIdempotent verifies that re-registering an agent does not
// create duplicate groups.
func TestRegisterRoleGroupIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_idempotent")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)
	ctx := context.Background()

	// Register same role twice (re-register)
	req := RegisterRequest{Role: "planner", Module: "arch"}
	reqJSON, _ := json.Marshal(req)
	_, err = handler.HandleRegister(ctx, reqJSON)
	if err != nil {
		t.Fatalf("first HandleRegister() error = %v", err)
	}

	req2 := RegisterRequest{Role: "planner", Module: "arch", ReRegister: true}
	req2JSON, _ := json.Marshal(req2)
	_, err = handler.HandleRegister(ctx, req2JSON)
	if err != nil {
		t.Fatalf("second HandleRegister() error = %v", err)
	}

	// Verify only 1 group named 'planner' exists
	var groupCount int
	err = s.RawDB().QueryRow(`SELECT COUNT(*) FROM groups WHERE name = ?`, "planner").Scan(&groupCount)
	if err != nil {
		t.Fatalf("query groups: %v", err)
	}
	if groupCount != 1 {
		t.Errorf("expected 1 group named 'planner' after two registrations, got %d", groupCount)
	}
}

// TestRegisterNameRoleValidation verifies the name≠role collision checks.
func TestRegisterNameRoleValidation(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_namecheck")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)
	ctx := context.Background()

	t.Run("name_equals_own_role", func(t *testing.T) {
		req := RegisterRequest{
			Name:   "implementer",
			Role:   "implementer",
			Module: "test",
		}
		reqJSON, _ := json.Marshal(req)
		_, err := handler.HandleRegister(ctx, reqJSON)
		if err == nil {
			t.Fatal("expected error when name == role, got nil")
		}
		if !containsString(err.Error(), "cannot be the same as its role") {
			t.Errorf("expected 'cannot be the same as its role' in error, got: %v", err)
		}
	})

	t.Run("name_conflicts_with_existing_role", func(t *testing.T) {
		// Register agent1 with name="coordinator", role="worker"
		req1 := RegisterRequest{Name: "coordinator", Role: "worker", Module: "test"}
		req1JSON, _ := json.Marshal(req1)
		_, err := handler.HandleRegister(ctx, req1JSON)
		if err != nil {
			t.Fatalf("register agent1: %v", err)
		}

		// Try to register with name="worker" (which is an existing role)
		req2 := RegisterRequest{Name: "worker", Role: "tester", Module: "test"}
		req2JSON, _ := json.Marshal(req2)
		_, err = handler.HandleRegister(ctx, req2JSON)
		if err == nil {
			t.Fatal("expected error when name conflicts with existing role, got nil")
		}
		if !containsString(err.Error(), "conflicts with existing role") {
			t.Errorf("expected 'conflicts with existing role' in error, got: %v", err)
		}
	})

	t.Run("role_conflicts_with_existing_agent_name", func(t *testing.T) {
		// Register agent with name="alice", role="planner"
		req1 := RegisterRequest{Name: "alice", Role: "planner", Module: "test"}
		req1JSON, _ := json.Marshal(req1)
		_, err := handler.HandleRegister(ctx, req1JSON)
		if err != nil {
			t.Fatalf("register alice: %v", err)
		}

		// Try to register with role="alice" (which is an existing agent name/ID)
		req2 := RegisterRequest{Name: "bob", Role: "alice", Module: "test"}
		req2JSON, _ := json.Marshal(req2)
		_, err = handler.HandleRegister(ctx, req2JSON)
		if err == nil {
			t.Fatal("expected error when role conflicts with existing agent name, got nil")
		}
		if !containsString(err.Error(), "conflicts with existing agent name") {
			t.Errorf("expected 'conflicts with existing agent name' in error, got: %v", err)
		}
	})

	t.Run("reregister_skips_validation", func(t *testing.T) {
		// Re-registering an agent should skip name≠role validation
		req := RegisterRequest{Name: "coordinator", Role: "worker", Module: "test", ReRegister: true}
		reqJSON, _ := json.Marshal(req)
		_, err := handler.HandleRegister(ctx, reqJSON)
		if err != nil {
			t.Errorf("re-registration should not fail due to name≠role validation, got: %v", err)
		}
	})
}

// containsString is a helper that checks if s contains substr.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
