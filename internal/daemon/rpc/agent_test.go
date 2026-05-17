package rpc

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
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
			// thrum-iw42: role+module uniqueness was dropped. Two distinct
			// agent_ids sharing (role, module) must both register — the
			// "one agent per worktree" invariant is enforced at the
			// worktree/identity layer, not the DB.
			name: "different_agent_same_role_module_succeeds_iw42",
			request: RegisterRequest{
				Role:    "implementer",
				Module:  "auth",
				Display: "Different Agent",
			},
			wantStatus:   "registered",
			wantConflict: false,
			wantErr:      false,
			setupFunc: func(s *state.State) error {
				// Pre-seed a DB row with a different agent_id but same role+module.
				// Before iw42 this triggered a conflict; now both coexist.
				_, err := s.RawDB().Exec(`
					INSERT INTO agents (agent_id, kind, role, module, display, registered_at)
					VALUES (?, ?, ?, ?, ?, ?)
				`, "agent:implementer:CONFLICT00", "agent", "implementer", "auth", "", "2026-01-01T00:00:00Z")
				return err
			},
		},
		{
			// thrum-iw42: the Force field was removed alongside the
			// role+module conflict branch. This case used to exercise
			// Force=true overriding a DB conflict; with Option C there
			// is no DB conflict to override — the pre-seeded row has a
			// different agent_id, so registration proceeds as a fresh
			// (different-name) agent. Kept to pin the post-removal
			// behavior: different agent_id with same (role, module)
			// coexists.
			name: "different_agent_id_same_role_module_coexists",
			request: RegisterRequest{
				Role:    "implementer",
				Module:  "auth",
				Display: "New Agent",
			},
			wantStatus:   "registered",
			wantConflict: false,
			wantErr:      false,
			setupFunc: func(s *state.State) error {
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
		{
			name: "invalid_agent_name_uppercase",
			request: RegisterRequest{
				Name:   "MyAgent",
				Role:   "implementer",
				Module: "auth",
			},
			wantErr: true,
		},
		{
			name: "invalid_agent_name_special_chars",
			request: RegisterRequest{
				Name:   "my/agent.name",
				Role:   "implementer",
				Module: "auth",
			},
			wantErr: true,
		},
		{
			name: "valid_agent_name_with_hyphens",
			request: RegisterRequest{
				Name:    "my-agent",
				Role:    "implementer",
				Module:  "auth",
				Display: "Hyphenated Agent",
			},
			wantStatus:   "registered",
			wantConflict: false,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory and state
			tmpDir := t.TempDir()
			thrumDir := filepath.Join(tmpDir, ".thrum")

			s, err := state.NewState(thrumDir, thrumDir, "test_repo_123", "")
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

// TestAgentRegister_SameAgentPIDChange covers Fix A from thrum-pxz.14:
// when a same-agent-returning register call reports a different PID than
// the stored one, the handler must update the PID even without
// ReRegister=true. This unblocks the self-heal bootstrap path.
func TestAgentRegister_SameAgentPIDChange(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)

	// Step 1: register initial agent with PID 1000.
	firstReq := RegisterRequest{
		Role:     "implementer",
		Module:   "auth",
		Display:  "Auth Implementer",
		AgentPID: 1000,
	}
	firstJSON, _ := json.Marshal(firstReq)
	firstResp, err := handler.HandleRegister(context.Background(), firstJSON)
	if err != nil {
		t.Fatalf("initial register: %v", err)
	}
	firstReg := firstResp.(*RegisterResponse)
	if firstReg.Status != "registered" {
		t.Fatalf("initial status = %s, want registered", firstReg.Status)
	}

	var storedPID int
	if err := s.RawDB().QueryRow("SELECT agent_pid FROM agents WHERE agent_id = ?", firstReg.AgentID).Scan(&storedPID); err != nil {
		t.Fatalf("query stored pid: %v", err)
	}
	if storedPID != 1000 {
		t.Fatalf("stored pid after first register = %d, want 1000", storedPID)
	}

	// Step 2: same agent re-registers with a DIFFERENT PID, no ReRegister flag.
	// Fix A must detect the PID change and persist the update.
	secondReq := RegisterRequest{
		Role:       "implementer",
		Module:     "auth",
		Display:    "Auth Implementer",
		AgentPID:   2000,
		ReRegister: false,
	}
	secondJSON, _ := json.Marshal(secondReq)
	secondResp, err := handler.HandleRegister(context.Background(), secondJSON)
	if err != nil {
		t.Fatalf("pid-change register: %v", err)
	}
	secondReg := secondResp.(*RegisterResponse)
	if secondReg.Status != "updated" {
		t.Errorf("pid-change status = %s, want updated", secondReg.Status)
	}

	if err := s.RawDB().QueryRow("SELECT agent_pid FROM agents WHERE agent_id = ?", firstReg.AgentID).Scan(&storedPID); err != nil {
		t.Fatalf("query updated pid: %v", err)
	}
	if storedPID != 2000 {
		t.Errorf("stored pid after pid-change register = %d, want 2000", storedPID)
	}
}

// TestRegister_ForceChangesRole — thrum-ufv5.2 regression. Pins the two
// adjacent branches of HandleRegister's re-register switch with a single
// table-driven test so a future refactor can't silently break either:
//
//   - Force=true  → triggers re-registration → agents projection refreshes
//     (role/module in the projected row reflect the new request).
//   - Force=false + ReRegister=false + PID matches existing → no-op branch
//     fires → agents projection stays at the originally-registered values.
//
// Without the first branch, `agent.list` returns stale role/module while
// whoami + the identity file show the new values (SC-04 e2e failure).
// Without the second branch, every CLI command that fires AgentRegister for
// liveness-touch would silently clobber role/module (identity-refresh hot
// path).
func TestRegister_ForceChangesRole(t *testing.T) {
	cases := []struct {
		name       string
		force      bool
		wantStatus string
		wantRole   string
		wantModule string
	}{
		{
			name:       "Force_true_refreshes_projection",
			force:      true,
			wantStatus: "updated",
			wantRole:   "owner",
			wantModule: "all",
		},
		{
			name:       "Force_false_same_pid_is_noop",
			force:      false,
			wantStatus: "registered",
			wantRole:   "coordinator", // unchanged — no-op branch fires
			wantModule: "e2e",         // unchanged — no-op branch fires
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			thrumDir := filepath.Join(tmpDir, ".thrum")
			s, err := state.NewState(thrumDir, thrumDir, "test_repo_123", "")
			if err != nil {
				t.Fatalf("create state: %v", err)
			}
			defer func() { _ = s.Close() }()

			handler := NewAgentHandler(s)

			// Step 1: register named agent with role=coordinator module=e2e.
			// AgentPID defaults to 0 — matches the second call below so the
			// PID-update branch of the switch does NOT fire and the only
			// remaining trigger for re-registration is Force (or ReRegister).
			firstReq := RegisterRequest{
				Name:   "e2e_coordinator",
				Role:   "coordinator",
				Module: "e2e",
			}
			firstJSON, _ := json.Marshal(firstReq)
			firstResp, err := handler.HandleRegister(context.Background(), firstJSON)
			if err != nil {
				t.Fatalf("initial register: %v", err)
			}
			firstReg := firstResp.(*RegisterResponse)
			if firstReg.AgentID != "e2e_coordinator" {
				t.Fatalf("initial agent_id = %s, want e2e_coordinator", firstReg.AgentID)
			}

			// Step 2: re-register with different role/module. Force governs
			// whether the projection refreshes or the no-op branch fires.
			secondReq := RegisterRequest{
				Name:    "e2e_coordinator",
				Role:    "owner",
				Module:  "all",
				Display: "Leon",
				Force:   tc.force,
			}
			secondJSON, _ := json.Marshal(secondReq)
			secondResp, err := handler.HandleRegister(context.Background(), secondJSON)
			if err != nil {
				t.Fatalf("second register: %v", err)
			}
			secondReg := secondResp.(*RegisterResponse)
			if secondReg.Status != tc.wantStatus {
				t.Errorf("status = %s, want %s", secondReg.Status, tc.wantStatus)
			}

			// Verify the agents projection reflects the expected role AND
			// module. Force-true case pins the SC-04 fix; Force-false case
			// pins the no-op branch that keeps identity refresh cheap.
			var storedRole, storedModule string
			err = s.RawDB().QueryRow(
				"SELECT role, module FROM agents WHERE agent_id = ?",
				"e2e_coordinator",
			).Scan(&storedRole, &storedModule)
			if err != nil {
				t.Fatalf("query agents projection: %v", err)
			}
			if storedRole != tc.wantRole {
				t.Errorf("stored role = %q, want %q", storedRole, tc.wantRole)
			}
			if storedModule != tc.wantModule {
				t.Errorf("stored module = %q, want %q", storedModule, tc.wantModule)
			}
		})
	}
}

// TestRegister_ForcePreservesRegisteredAt — review finding #1. The agents
// projection's ON CONFLICT clause must leave registered_at untouched when
// a force re-register writes the same row. The original first-registration
// time is what `agent.list ORDER BY registered_at DESC` relies on, and
// INSERT OR REPLACE (the pre-fix form) would reset it on every force
// quickstart — harmless pre-ufv5.6 because only PID-drift hit this path,
// now exposed on every --force after ufv5.2+.6 landed.
func TestRegister_ForcePreservesRegisteredAt(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_reg_at", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)

	// Register once and capture registered_at.
	firstReq := RegisterRequest{
		Name:   "preserve_me",
		Role:   "coordinator",
		Module: "e2e",
	}
	firstJSON, _ := json.Marshal(firstReq)
	if _, err := handler.HandleRegister(context.Background(), firstJSON); err != nil {
		t.Fatalf("initial register: %v", err)
	}

	var firstRegisteredAt string
	err = s.RawDB().QueryRow(
		"SELECT registered_at FROM agents WHERE agent_id = ?",
		"preserve_me",
	).Scan(&firstRegisteredAt)
	if err != nil {
		t.Fatalf("query initial registered_at: %v", err)
	}
	if firstRegisteredAt == "" {
		t.Fatal("initial registered_at unexpectedly empty")
	}

	// Sleep briefly so a reset would produce a DISTINCT timestamp.
	// Nanosecond precision in the event timestamp would not always
	// register as different without a wall-clock gap.
	time.Sleep(10 * time.Millisecond)

	// Re-register via Force. INSERT OR REPLACE would have written a new
	// registered_at; ON CONFLICT DO UPDATE must leave the original.
	secondReq := RegisterRequest{
		Name:    "preserve_me",
		Role:    "owner",
		Module:  "all",
		Display: "Leon",
		Force:   true,
	}
	secondJSON, _ := json.Marshal(secondReq)
	if _, err := handler.HandleRegister(context.Background(), secondJSON); err != nil {
		t.Fatalf("force register: %v", err)
	}

	var secondRegisteredAt string
	err = s.RawDB().QueryRow(
		"SELECT registered_at FROM agents WHERE agent_id = ?",
		"preserve_me",
	).Scan(&secondRegisteredAt)
	if err != nil {
		t.Fatalf("query post-force registered_at: %v", err)
	}

	if secondRegisteredAt != firstRegisteredAt {
		t.Errorf("registered_at changed on force re-register: %q → %q (should stay %q)",
			firstRegisteredAt, secondRegisteredAt, firstRegisteredAt)
	}

	// Sanity: role still updated (so ON CONFLICT DO UPDATE is actually
	// applying the other columns; a no-op projection would also match the
	// registered_at expectation for the wrong reason).
	var storedRole string
	err = s.RawDB().QueryRow(
		"SELECT role FROM agents WHERE agent_id = ?",
		"preserve_me",
	).Scan(&storedRole)
	if err != nil {
		t.Fatalf("query post-force role: %v", err)
	}
	if storedRole != "owner" {
		t.Errorf("stored role = %q, want %q — ON CONFLICT may not be updating columns",
			storedRole, "owner")
	}
}

// TestAgentRegister_SameAgentSamePID covers the idempotent no-op path
// from thrum-pxz.14 Fix A: when the caller's PID matches the stored
// PID and ReRegister is false, the handler must NOT emit a new event.
func TestAgentRegister_SameAgentSamePID(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)

	// Step 1: register initial agent with PID 1000.
	req := RegisterRequest{
		Role:     "implementer",
		Module:   "auth",
		Display:  "Auth Implementer",
		AgentPID: 1000,
	}
	reqJSON, _ := json.Marshal(req)
	firstResp, err := handler.HandleRegister(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("initial register: %v", err)
	}
	firstReg := firstResp.(*RegisterResponse)

	// Snapshot the event count so we can assert no new event was written.
	var eventsBefore int
	if err := s.RawDB().QueryRow("SELECT COUNT(*) FROM events WHERE type = 'agent.register'").Scan(&eventsBefore); err != nil {
		t.Fatalf("query events before: %v", err)
	}

	// Step 2: same agent re-registers with the SAME PID and no ReRegister flag.
	// This must be an idempotent no-op — status "registered" with no event.
	secondResp, err := handler.HandleRegister(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("idempotent register: %v", err)
	}
	secondReg := secondResp.(*RegisterResponse)
	if secondReg.Status != "registered" {
		t.Errorf("idempotent status = %s, want registered", secondReg.Status)
	}

	var eventsAfter int
	if err := s.RawDB().QueryRow("SELECT COUNT(*) FROM events WHERE type = 'agent.register'").Scan(&eventsAfter); err != nil {
		t.Fatalf("query events after: %v", err)
	}
	if eventsAfter != eventsBefore {
		t.Errorf("agent.register event count grew: before=%d after=%d (expected no new event)", eventsBefore, eventsAfter)
	}

	// Verify the stored PID is unchanged.
	var storedPID int
	if err := s.RawDB().QueryRow("SELECT agent_pid FROM agents WHERE agent_id = ?", firstReg.AgentID).Scan(&storedPID); err != nil {
		t.Fatalf("query stored pid: %v", err)
	}
	if storedPID != 1000 {
		t.Errorf("stored pid after idempotent register = %d, want 1000", storedPID)
	}
}

func TestAgentList(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123", "")
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
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Set environment variables for identity
	t.Setenv("THRUM_ROLE", "implementer")
	t.Setenv("THRUM_MODULE", "test")

	handler := NewAgentHandler(s)

	// Register an agent up front so whoami has a real CallerAgentID to
	// forward. The pre-guard whoami fallback ("load from daemon config
	// and derive agent_id") was deleted in Epic 5 Task 4.3 — whoami
	// callers now always supply CallerAgentID.
	agentHandler := NewAgentHandler(s)
	registerReq := RegisterRequest{Role: "implementer", Module: "test"}
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

	whoamiParams, _ := json.Marshal(struct {
		CallerAgentID string `json:"caller_agent_id"`
	}{CallerAgentID: agentID})

	t.Run("whoami_without_session", func(t *testing.T) {
		resp, err := handler.HandleWhoami(context.Background(), whoamiParams)
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
		if whoamiResp.Source != "caller" {
			t.Errorf("Source = %s, want caller", whoamiResp.Source)
		}
		if whoamiResp.SessionID != "" {
			t.Error("SessionID should be empty when no active session")
		}
	})

	t.Run("whoami_with_active_session", func(t *testing.T) {
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
		resp, err := handler.HandleWhoami(context.Background(), whoamiParams)
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

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_listctx", "")
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
	st, err := state.NewState(thrumDir, syncDir, "test-repo", "")
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

		// Verify sessions were removed
		err = st.RawDB().QueryRow("SELECT COUNT(*) FROM sessions WHERE agent_id = ?", "test_agent").Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query sessions: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 sessions in database, got %d", count)
		}

		// Verify old events were removed (only the agent.cleanup event from deletion itself should remain)
		err = st.RawDB().QueryRow(
			"SELECT COUNT(*) FROM events WHERE event_json LIKE ? AND event_json NOT LIKE ?",
			"%test_agent%", "%agent.cleanup%").Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query events: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 old events referencing agent, got %d", count)
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

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123", "")
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

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123", "")
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

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewMessageHandlerWithDispatcher(s, nil, "", "", "")
	if handler == nil {
		t.Fatal("Expected non-nil handler")
	}
	if handler.state != s {
		t.Error("Handler state mismatch")
	}
}

// TestBuildForAgentValues_NameOnly verifies that buildForAgentValues returns the
// agent name/ID, user:-prefixed form, and role for inbox mention matching.
func TestBuildForAgentValues_NameOnly(t *testing.T) {
	values := buildForAgentValues("impl_api", "implementer")
	if len(values) != 3 || values[0] != "impl_api" || values[1] != "user:impl_api" || values[2] != "implementer" {
		t.Errorf("expected [impl_api user:impl_api implementer], got %v", values)
	}
}

// TestBuildForAgentValues_RoleSameAsName verifies that when role equals name,
// it is not duplicated in the values.
func TestBuildForAgentValues_RoleSameAsName(t *testing.T) {
	values := buildForAgentValues("coordinator", "coordinator")
	if len(values) != 2 || values[0] != "coordinator" || values[1] != "user:coordinator" {
		t.Errorf("expected [coordinator user:coordinator], got %v", values)
	}
}

// TestBuildForAgentValues_UserPrefixed verifies that a user:-prefixed forAgent
// does not get double-prefixed.
func TestBuildForAgentValues_UserPrefixed(t *testing.T) {
	values := buildForAgentValues("user:leon-letto", "")
	if len(values) != 1 || values[0] != "user:leon-letto" {
		t.Errorf("expected [user:leon-letto], got %v", values)
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

// TestRegisterCreatesRoleGroup and TestRegisterRoleGroupIdempotent removed —
// auto role group creation no longer exists.

// TestRegisterNameRoleValidation verifies the name≠role collision checks.
func TestRegisterNameRoleValidation(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_namecheck", "")
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

func TestHandleRegister_StoresAgentPID(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)

	// Register with agent_pid=12345
	req := RegisterRequest{
		Role:     "implementer",
		Module:   "auth",
		Display:  "Auth Implementer",
		AgentPID: 12345,
	}
	reqJSON, _ := json.Marshal(req)
	resp, err := handler.HandleRegister(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("HandleRegister() error = %v", err)
	}

	regResp, ok := resp.(*RegisterResponse)
	if !ok {
		t.Fatalf("response is not *RegisterResponse, got %T", resp)
	}
	if regResp.Status != "registered" {
		t.Errorf("Status = %s, want registered", regResp.Status)
	}

	// List agents and verify agent_pid is returned
	listReq := ListAgentsRequest{}
	listJSON, _ := json.Marshal(listReq)
	listResp, err := handler.HandleList(context.Background(), listJSON)
	if err != nil {
		t.Fatalf("HandleList() error = %v", err)
	}

	agents := listResp.(*ListAgentsResponse).Agents
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].AgentPID != 12345 {
		t.Errorf("AgentPID = %d, want 12345", agents[0].AgentPID)
	}
}

func TestHandleRegister_SamePID_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)

	req := RegisterRequest{
		Name:     "my-agent",
		Role:     "implementer",
		Module:   "auth",
		AgentPID: 99999,
	}

	// First registration
	reqJSON, _ := json.Marshal(req)
	resp1, err := handler.HandleRegister(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("first HandleRegister() error = %v", err)
	}
	reg1 := resp1.(*RegisterResponse)
	if reg1.Status != "registered" {
		t.Errorf("first Status = %s, want registered", reg1.Status)
	}

	// Second registration with same name and same PID (re-register)
	req.ReRegister = true
	reqJSON, _ = json.Marshal(req)
	resp2, err := handler.HandleRegister(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("second HandleRegister() error = %v", err)
	}
	reg2 := resp2.(*RegisterResponse)
	if reg2.Status != "updated" {
		t.Errorf("second Status = %s, want updated", reg2.Status)
	}

	// List and verify agent_pid is still present
	listReq := ListAgentsRequest{}
	listJSON, _ := json.Marshal(listReq)
	listResp, err := handler.HandleList(context.Background(), listJSON)
	if err != nil {
		t.Fatalf("HandleList() error = %v", err)
	}

	agents := listResp.(*ListAgentsResponse).Agents
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].AgentPID != 99999 {
		t.Errorf("AgentPID = %d, want 99999", agents[0].AgentPID)
	}
}

// --- thrum-xir.18.2: ensureActiveSession helper tests --------------------

// seedAgentRow inserts a minimal agent row directly so the test can drive
// ensureActiveSession without going through HandleRegister (which would
// itself emit events and confound event-count assertions).
func seedAgentRow(t *testing.T, s *state.State, agentID string, pid int) {
	t.Helper()
	_, err := s.RawDB().Exec(`
		INSERT INTO agents (agent_id, kind, role, module, display, hostname, agent_pid, registered_at)
		VALUES (?, 'agent', 'implementer', 'test', '', '', ?, ?)
	`, agentID, pid, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("seed agent row: %v", err)
	}
}

// seedSessionRow inserts a sessions row with the supplied ended_at value.
// Pass empty string for an active session, or a timestamp string for an
// ended one.
func seedSessionRow(t *testing.T, s *state.State, sessionID, agentID, endedAt string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if endedAt == "" {
		_, err := s.RawDB().Exec(`
			INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
			VALUES (?, ?, ?, ?)
		`, sessionID, agentID, now, now)
		if err != nil {
			t.Fatalf("seed active session: %v", err)
		}
		return
	}
	_, err := s.RawDB().Exec(`
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at, ended_at, end_reason)
		VALUES (?, ?, ?, ?, ?, 'test_seeded')
	`, sessionID, agentID, now, now, endedAt)
	if err != nil {
		t.Fatalf("seed ended session: %v", err)
	}
}

// countSessionStartEvents returns the total number of agent.session.start
// events recorded in the events table. Tests use isolated tmpDir-backed
// state, so a global count is unambiguous per-test.
func countSessionStartEvents(t *testing.T, s *state.State) int {
	t.Helper()
	var n int
	err := s.RawDB().QueryRow(`
		SELECT COUNT(*) FROM events WHERE type = 'agent.session.start'
	`).Scan(&n)
	if err != nil {
		t.Fatalf("count session.start events: %v", err)
	}
	return n
}

// countActiveSessionRows returns the number of sessions rows with ended_at
// IS NULL for the given agent_id.
func countActiveSessionRows(t *testing.T, s *state.State, agentID string) int {
	t.Helper()
	var n int
	err := s.RawDB().QueryRow(`
		SELECT COUNT(*) FROM sessions
		WHERE agent_id = ? AND ended_at IS NULL
	`, agentID).Scan(&n)
	if err != nil {
		t.Fatalf("count active sessions: %v", err)
	}
	return n
}

// TestEnsureActiveSession_AlreadyActive — happy path: an active session
// already exists, the helper must no-op and write zero events.
func TestEnsureActiveSession_AlreadyActive(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_eas_active", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	const agentID = "agt_already_active"
	seedAgentRow(t, s, agentID, os.Getpid())
	seedSessionRow(t, s, "ses_existing_active", agentID, "")

	handler := NewAgentHandler(s)
	s.Lock()
	defer s.Unlock()

	eventsBefore := countSessionStartEvents(t, s)
	rowsBefore := countActiveSessionRows(t, s, agentID)

	got, err := handler.ensureActiveSession(context.Background(), agentID, os.Getpid())
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	if got != "" {
		t.Errorf("returned session_id = %q, want empty (already-active no-op)", got)
	}

	if n := countSessionStartEvents(t, s); n != eventsBefore {
		t.Errorf("session.start events grew: before=%d after=%d", eventsBefore, n)
	}
	if n := countActiveSessionRows(t, s, agentID); n != rowsBefore {
		t.Errorf("active session row count grew: before=%d after=%d", rowsBefore, n)
	}
}

// TestEnsureActiveSession_OfflineAgentLivePID — resurrect path: ended
// session and a live PID; helper must emit one session.start event.
func TestEnsureActiveSession_OfflineAgentLivePID(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_eas_offline", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	const agentID = "agt_offline_live"
	seedAgentRow(t, s, agentID, os.Getpid())
	endedAt := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339Nano)
	seedSessionRow(t, s, "ses_old_ended", agentID, endedAt)

	handler := NewAgentHandler(s)
	s.Lock()
	defer s.Unlock()

	eventsBefore := countSessionStartEvents(t, s)

	got, err := handler.ensureActiveSession(context.Background(), agentID, os.Getpid())
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	if got == "" {
		t.Fatalf("returned empty session_id, want non-empty resurrected ID")
	}
	if !strings.HasPrefix(got, "ses_") {
		t.Errorf("session_id = %q, want ses_ prefix", got)
	}
	if got == "ses_old_ended" {
		t.Errorf("returned the ended session_id %q, want a fresh ULID", got)
	}

	if n := countSessionStartEvents(t, s); n != eventsBefore+1 {
		t.Errorf("session.start events: before=%d after=%d, want +1", eventsBefore, n)
	}
	if n := countActiveSessionRows(t, s, agentID); n != 1 {
		t.Errorf("active session rows = %d, want 1", n)
	}
}

// TestEnsureActiveSession_DeadPIDNoResurrect — dead PID short-circuits the
// resurrect path; helper must return "" with no side effects.
func TestEnsureActiveSession_DeadPIDNoResurrect(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_eas_dead", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	const agentID = "agt_dead_pid"
	seedAgentRow(t, s, agentID, 999999)
	endedAt := time.Now().UTC().Format(time.RFC3339Nano)
	seedSessionRow(t, s, "ses_old_dead", agentID, endedAt)

	handler := NewAgentHandler(s)
	s.Lock()
	defer s.Unlock()

	eventsBefore := countSessionStartEvents(t, s)
	rowsBefore := countActiveSessionRows(t, s, agentID)

	got, err := handler.ensureActiveSession(context.Background(), agentID, 999999)
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	if got != "" {
		t.Errorf("returned session_id = %q, want empty (dead-PID skip)", got)
	}
	if n := countSessionStartEvents(t, s); n != eventsBefore {
		t.Errorf("session.start events grew on dead-PID skip: before=%d after=%d", eventsBefore, n)
	}
	if n := countActiveSessionRows(t, s, agentID); n != rowsBefore {
		t.Errorf("active session rows grew on dead-PID skip: before=%d after=%d", rowsBefore, n)
	}
}

// TestEnsureActiveSession_ZeroPIDNoResurrect — pid=0 short-circuits before
// the IsRunning check; helper must return "" with no side effects. This
// guards Rule 6 (test 5 must pass a real PID, not zero) by giving the
// zero-PID case its own dedicated test.
func TestEnsureActiveSession_ZeroPIDNoResurrect(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_eas_zero", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	const agentID = "agt_zero_pid"
	seedAgentRow(t, s, agentID, 0)
	endedAt := time.Now().UTC().Format(time.RFC3339Nano)
	seedSessionRow(t, s, "ses_old_zero", agentID, endedAt)

	handler := NewAgentHandler(s)
	s.Lock()
	defer s.Unlock()

	eventsBefore := countSessionStartEvents(t, s)
	rowsBefore := countActiveSessionRows(t, s, agentID)

	got, err := handler.ensureActiveSession(context.Background(), agentID, 0)
	if err != nil {
		t.Fatalf("ensureActiveSession: %v", err)
	}
	if got != "" {
		t.Errorf("returned session_id = %q, want empty (zero-PID skip)", got)
	}
	if n := countSessionStartEvents(t, s); n != eventsBefore {
		t.Errorf("session.start events grew on zero-PID skip: before=%d after=%d", eventsBefore, n)
	}
	if n := countActiveSessionRows(t, s, agentID); n != rowsBefore {
		t.Errorf("active session rows grew on zero-PID skip: before=%d after=%d", rowsBefore, n)
	}
}

// --- thrum-xir.18.3: HandleRegister wiring tests ------------------------

// TestHandleRegister_ResurrectOfflineSession — Test 5 (Rule 6 critical):
// MUST use os.Getpid() as the request PID. Zero or a hardcoded constant
// would cause ensureActiveSession to short-circuit before exercising the
// resurrect path, and the test would pass for the wrong reason.
//
// Arrange: existing agent with stored PID == os.Getpid() (so the
// same-agent same-PID no-op branch is taken — no agent.register event
// emitted) plus an ended session row. Act: call HandleRegister with
// AgentPID = os.Getpid(). Assert: response surfaces SessionResumed=true
// with a fresh session_id, exactly one new agent.session.start event,
// zero new agent.register events, one active session row.
func TestHandleRegister_ResurrectOfflineSession(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_resurrect", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	livePID := os.Getpid()

	// Step 1: register the agent normally so the daemon-generated agent_id
	// matches what HandleRegister will look up on the second call.
	handler := NewAgentHandler(s)
	regReq := RegisterRequest{
		Role:     "implementer",
		Module:   "resurrect",
		Display:  "Resurrect Test",
		AgentPID: livePID,
	}
	regJSON, _ := json.Marshal(regReq)
	firstResp, err := handler.HandleRegister(context.Background(), regJSON)
	if err != nil {
		t.Fatalf("initial register: %v", err)
	}
	agentID := firstResp.(*RegisterResponse).AgentID
	if agentID == "" {
		t.Fatalf("initial register returned empty agent_id")
	}

	// Step 2: end any active session for this agent (simulating the
	// scenario where pre-pxz.14 self-heal killed the session row).
	endedAt := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339Nano)
	if _, err := s.RawDB().Exec(`
		UPDATE sessions SET ended_at = ?, end_reason = 'test_seed_ended'
		WHERE agent_id = ? AND ended_at IS NULL
	`, endedAt, agentID); err != nil {
		t.Fatalf("seed ended session: %v", err)
	}
	if n := countActiveSessionRows(t, s, agentID); n != 0 {
		t.Fatalf("active sessions after seeding ended_at = %d, want 0", n)
	}

	// Snapshot event counts.
	var registerEventsBefore int
	if err := s.RawDB().QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'agent.register'`).Scan(&registerEventsBefore); err != nil {
		t.Fatalf("count register events: %v", err)
	}
	startEventsBefore := countSessionStartEvents(t, s)

	// Step 3: re-register with the SAME live PID. No PID drift, no
	// ReRegister — this hits the no-op same-agent branch, then the new
	// resurrect call kicks in.
	secondResp, err := handler.HandleRegister(context.Background(), regJSON)
	if err != nil {
		t.Fatalf("second register: %v", err)
	}
	regResp := secondResp.(*RegisterResponse)

	if !regResp.SessionResumed {
		t.Errorf("SessionResumed = false, want true")
	}
	if regResp.SessionID == "" {
		t.Errorf("SessionID = empty, want fresh session id")
	}
	if !strings.HasPrefix(regResp.SessionID, "ses_") {
		t.Errorf("SessionID = %q, want ses_ prefix", regResp.SessionID)
	}

	// Zero new agent.register events (PID matched).
	var registerEventsAfter int
	if err := s.RawDB().QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'agent.register'`).Scan(&registerEventsAfter); err != nil {
		t.Fatalf("count register events after: %v", err)
	}
	if registerEventsAfter != registerEventsBefore {
		t.Errorf("agent.register event count grew: before=%d after=%d (PID matched, expected no event)",
			registerEventsBefore, registerEventsAfter)
	}

	// Exactly one new agent.session.start event.
	if n := countSessionStartEvents(t, s); n != startEventsBefore+1 {
		t.Errorf("session.start events: before=%d after=%d, want +1", startEventsBefore, n)
	}

	// One active session row.
	if n := countActiveSessionRows(t, s, agentID); n != 1 {
		t.Errorf("active session rows = %d, want 1", n)
	}
}

// TestHandleRegister_ResurrectWithPIDDrift — both Fix A (PID self-heal)
// and the new resurrect must fire when the stored PID is dead and the
// caller passes a new live PID. Asserts both events are written.
func TestHandleRegister_ResurrectWithPIDDrift(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_drift_resurrect", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	livePID := os.Getpid()
	deadPID := 999999

	// Step 1: register normally so the daemon picks the agent_id and
	// emits the initial agent.register + session.start events.
	handler := NewAgentHandler(s)
	regReq := RegisterRequest{
		Role:     "implementer",
		Module:   "drift",
		AgentPID: deadPID,
	}
	regJSON, _ := json.Marshal(regReq)
	firstResp, err := handler.HandleRegister(context.Background(), regJSON)
	if err != nil {
		t.Fatalf("initial register: %v", err)
	}
	agentID := firstResp.(*RegisterResponse).AgentID
	if agentID == "" {
		t.Fatalf("initial register returned empty agent_id")
	}

	// Step 2: end any active session — simulates the recovery scenario
	// the resurrect path is built for.
	endedAt := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339Nano)
	if _, err := s.RawDB().Exec(`
		UPDATE sessions SET ended_at = ?, end_reason = 'test_seed_ended'
		WHERE agent_id = ? AND ended_at IS NULL
	`, endedAt, agentID); err != nil {
		t.Fatalf("seed ended session: %v", err)
	}
	if n := countActiveSessionRows(t, s, agentID); n != 0 {
		t.Fatalf("active sessions after seeding ended_at = %d, want 0", n)
	}

	// Snapshot event counts after the initial register but before the
	// drift act, so the assertions count only the second register's
	// emissions.
	var registerBefore int
	if err := s.RawDB().QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'agent.register'`).Scan(&registerBefore); err != nil {
		t.Fatalf("count register events: %v", err)
	}
	startBefore := countSessionStartEvents(t, s)

	// Step 3: re-register with a DIFFERENT live PID. Hits the PID-drift
	// branch (writes an agent.register "updated" event via Fix A) AND
	// the resurrect (writes a fresh session.start event).
	driftReq := regReq
	driftReq.AgentPID = livePID
	driftJSON, _ := json.Marshal(driftReq)
	driftResp, err := handler.HandleRegister(context.Background(), driftJSON)
	if err != nil {
		t.Fatalf("drift register: %v", err)
	}
	regResp := driftResp.(*RegisterResponse)

	if !regResp.SessionResumed {
		t.Errorf("SessionResumed = false, want true")
	}
	if regResp.SessionID == "" {
		t.Errorf("SessionID = empty, want fresh session id")
	}
	if regResp.Status != "updated" {
		t.Errorf("Status = %q, want updated (PID drift branch)", regResp.Status)
	}

	var registerAfter int
	if err := s.RawDB().QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'agent.register'`).Scan(&registerAfter); err != nil {
		t.Fatalf("count register events after: %v", err)
	}
	if registerAfter != registerBefore+1 {
		t.Errorf("agent.register events: before=%d after=%d, want +1", registerBefore, registerAfter)
	}
	if n := countSessionStartEvents(t, s); n != startBefore+1 {
		t.Errorf("session.start events: before=%d after=%d, want +1", startBefore, n)
	}
	if n := countActiveSessionRows(t, s, agentID); n != 1 {
		t.Errorf("active session rows = %d, want 1", n)
	}
}

// TestHandleRegister_NoResurrectAlreadyActive — agent with active session
// must not trigger resurrect; SessionResumed=false and no new events.
func TestHandleRegister_NoResurrectAlreadyActive(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_already_active", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	livePID := os.Getpid()

	handler := NewAgentHandler(s)
	regReq := RegisterRequest{
		Role:     "implementer",
		Module:   "active",
		AgentPID: livePID,
	}
	regJSON, _ := json.Marshal(regReq)
	firstResp, err := handler.HandleRegister(context.Background(), regJSON)
	if err != nil {
		t.Fatalf("initial register: %v", err)
	}
	agentID := firstResp.(*RegisterResponse).AgentID

	// Seed an active session manually so the helper sees it on the
	// second call.
	seedSessionRow(t, s, "ses_already_active", agentID, "")
	if n := countActiveSessionRows(t, s, agentID); n < 1 {
		t.Fatalf("expected at least 1 active session, got %d", n)
	}

	startBefore := countSessionStartEvents(t, s)
	var registerBefore int
	_ = s.RawDB().QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'agent.register'`).Scan(&registerBefore)

	// Re-register with same PID. Both branches must no-op.
	secondResp, err := handler.HandleRegister(context.Background(), regJSON)
	if err != nil {
		t.Fatalf("second register: %v", err)
	}
	regResp := secondResp.(*RegisterResponse)
	if regResp.SessionResumed {
		t.Errorf("SessionResumed = true, want false (active session present)")
	}
	if regResp.SessionID != "" {
		t.Errorf("SessionID = %q, want empty (no resurrect)", regResp.SessionID)
	}

	var registerAfter int
	_ = s.RawDB().QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'agent.register'`).Scan(&registerAfter)
	if registerAfter != registerBefore {
		t.Errorf("agent.register events grew: before=%d after=%d", registerBefore, registerAfter)
	}
	if n := countSessionStartEvents(t, s); n != startBefore {
		t.Errorf("session.start events grew on already-active path: before=%d after=%d", startBefore, n)
	}
}

// TestRegisterResponse_SessionResumedJSON verifies the new SessionResumed +
// SessionID fields round-trip correctly and stay omitted when unset
// (thrum-xir.18.1).
func TestRegisterResponse_SessionResumedJSON(t *testing.T) {
	t.Run("omits when unset", func(t *testing.T) {
		resp := RegisterResponse{
			AgentID: "agt_test",
			Status:  "registered",
		}
		data, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		s := string(data)
		if strings.Contains(s, "session_id") {
			t.Errorf("expected session_id omitted, got %s", s)
		}
		if strings.Contains(s, "session_resumed") {
			t.Errorf("expected session_resumed omitted, got %s", s)
		}
	})
	t.Run("round-trips when set", func(t *testing.T) {
		original := RegisterResponse{
			AgentID:        "agt_test",
			Status:         "registered",
			SessionID:      "ses_01ABC",
			SessionResumed: true,
		}
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded RegisterResponse
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if decoded.SessionID != "ses_01ABC" {
			t.Errorf("SessionID = %q, want ses_01ABC", decoded.SessionID)
		}
		if !decoded.SessionResumed {
			t.Errorf("SessionResumed = false, want true")
		}
	})
}

// TestAgentRegister_CrossDaemonCoexistence — thrum-mm3l regression.
//
// Two daemons on different machines may have agents with overlapping
// (role, module) — that's the whole point of multi-machine coordination.
// HandleRegister's conflict check must scope to the LOCAL daemon only;
// seeing a synced-from-remote agent with the same role+module must not be
// treated as a local conflict, and the force-override DELETE must never
// touch a row with a non-local origin_daemon.
//
// Pre-fix behavior: registering a local agent with Force/ReRegister would
// delete the synced cross-daemon agent silently from the local agents
// table, leaving the remote agent invisible until the original daemon
// re-registered it. The symptom was `thrum team --all` missing remote
// agents and `message.send` failing with `unknown recipient` on replies.
func TestAgentRegister_CrossDaemonCoexistence(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_123", "local_daemon_id")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Seed a synced-from-remote agent with role=coordinator, module=main.
	// Using a distinct origin_daemon simulates a peer-synced agent row.
	remoteEvent := types.AgentRegisterEvent{
		Type:         "agent.register",
		Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
		EventID:      "evt_remote_coord",
		OriginDaemon: "remote_daemon_mac",
		AgentID:      "coordinator_main",
		Kind:         "agent",
		Role:         "coordinator",
		Module:       "main",
		Hostname:     "leonsmacm1pro",
		AgentPID:     55765,
	}
	if err := s.WriteEvent(context.Background(), remoteEvent); err != nil {
		t.Fatalf("seed remote agent: %v", err)
	}

	handler := NewAgentHandler(s)

	// Register a LOCAL agent with the same role+module but a different
	// agent_id. Use ReRegister=true to exercise the force-override path.
	req := RegisterRequest{
		Name:       "coordinator_main_remote",
		Role:       "coordinator",
		Module:     "main",
		ReRegister: true,
		AgentPID:   99999,
	}
	reqJSON, _ := json.Marshal(req)
	resp, err := handler.HandleRegister(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("HandleRegister: %v", err)
	}
	regResp, ok := resp.(*RegisterResponse)
	if !ok {
		t.Fatalf("response is not *RegisterResponse, got %T", resp)
	}
	if regResp.Status != "registered" {
		t.Errorf("Status = %s, want registered (no cross-daemon conflict expected)", regResp.Status)
	}

	// BOTH agents must still be in the agents table after local registration.
	var remoteCount int
	err = s.RawDB().QueryRow(
		`SELECT COUNT(*) FROM agents WHERE agent_id = ?`, "coordinator_main",
	).Scan(&remoteCount)
	if err != nil {
		t.Fatalf("query remote agent: %v", err)
	}
	if remoteCount != 1 {
		t.Errorf("remote agent coordinator_main count = %d after local register, want 1 (force-override wiped a cross-daemon row)", remoteCount)
	}

	var localCount int
	err = s.RawDB().QueryRow(
		`SELECT COUNT(*) FROM agents WHERE agent_id = ?`, "coordinator_main_remote",
	).Scan(&localCount)
	if err != nil {
		t.Fatalf("query local agent: %v", err)
	}
	if localCount != 1 {
		t.Errorf("local agent coordinator_main_remote count = %d, want 1", localCount)
	}

	// Verify origin_daemon is correctly populated for both rows.
	var remoteOrigin, localOrigin string
	_ = s.RawDB().QueryRow(
		`SELECT origin_daemon FROM agents WHERE agent_id = ?`, "coordinator_main",
	).Scan(&remoteOrigin)
	_ = s.RawDB().QueryRow(
		`SELECT origin_daemon FROM agents WHERE agent_id = ?`, "coordinator_main_remote",
	).Scan(&localOrigin)
	if remoteOrigin != "remote_daemon_mac" {
		t.Errorf("remote agent origin_daemon = %q, want remote_daemon_mac", remoteOrigin)
	}
	if localOrigin != "local_daemon_id" {
		t.Errorf("local agent origin_daemon = %q, want local_daemon_id", localOrigin)
	}
}

// TestRegister_TwoProxiesSameRoleModule covers thrum-iw42: after dropping
// the role+module uniqueness check (Option C), two distinct proxy names
// with the same (role, module) must BOTH succeed. Mirrors the real
// scenario where Telegram registers @thrum:coordinator_main and the peer
// bridge registers @thrum:impl_mocksf_s2 — both use role=remote,
// module=thrum but carry different names.
func TestRegister_TwoProxiesSameRoleModule(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_iw42", "local_daemon_id")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)

	// First proxy: Telegram-bridge-style thrum:coordinator_main.
	first := RegisterRequest{
		Name:   "thrum:coordinator_main",
		Role:   "remote",
		Module: "thrum",
	}
	reqJSON, _ := json.Marshal(first)
	resp, err := handler.HandleRegister(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("first HandleRegister: %v", err)
	}
	if got := resp.(*RegisterResponse).Status; got != "registered" {
		t.Fatalf("first Status = %q, want registered", got)
	}

	// Second proxy: peer-bridge-style thrum:impl_mocksf_s2, SAME role + module.
	second := RegisterRequest{
		Name:   "thrum:impl_mocksf_s2",
		Role:   "remote",
		Module: "thrum",
	}
	reqJSON, _ = json.Marshal(second)
	resp, err = handler.HandleRegister(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("second HandleRegister: %v", err)
	}
	regResp := resp.(*RegisterResponse)
	if regResp.Status != "registered" {
		t.Errorf("second Status = %q, want registered (role+module uniqueness removed per iw42)", regResp.Status)
	}
	if regResp.Conflict != nil {
		t.Errorf("second Conflict = %+v, want nil (no role+module collision any more)", regResp.Conflict)
	}

	// Verify both rows coexist in the agents table.
	var count int
	if err := s.RawDB().QueryRow("SELECT COUNT(*) FROM agents WHERE role='remote' AND module='thrum'").Scan(&count); err != nil {
		t.Fatalf("count agents: %v", err)
	}
	if count != 2 {
		t.Errorf("agents with role=remote/module=thrum = %d, want 2", count)
	}
}

// TestRegister_SameAgentIDStillRejected preserves the existing
// same-agent-returning semantics after the role+module check removal.
// A second registration for the SAME agent_id with a DIFFERENT PID must
// PID-self-heal (status "updated"), not spawn a duplicate.
func TestRegister_SameAgentIDStillRejected(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_same_id", "local_daemon_id")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)

	first := RegisterRequest{
		Name:     "alice",
		Role:     "implementer",
		Module:   "auth",
		AgentPID: 100,
	}
	reqJSON, _ := json.Marshal(first)
	if _, err := handler.HandleRegister(context.Background(), reqJSON); err != nil {
		t.Fatalf("first HandleRegister: %v", err)
	}

	// Same agent_id (name), different PID → PID self-heal, status "updated".
	second := RegisterRequest{
		Name:     "alice",
		Role:     "implementer",
		Module:   "auth",
		AgentPID: 200,
	}
	reqJSON, _ = json.Marshal(second)
	resp, err := handler.HandleRegister(context.Background(), reqJSON)
	if err != nil {
		t.Fatalf("second HandleRegister: %v", err)
	}
	regResp := resp.(*RegisterResponse)
	if regResp.Status != "updated" {
		t.Errorf("Status = %q, want updated (PID self-heal path)", regResp.Status)
	}

	// Exactly one row, updated PID.
	var count, pid int
	if err := s.RawDB().QueryRow("SELECT COUNT(*), MAX(agent_pid) FROM agents WHERE agent_id='alice'").Scan(&count, &pid); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("agents count = %d, want 1 (same-id should dedupe)", count)
	}
	if pid != 200 {
		t.Errorf("agent_pid = %d, want 200 (self-heal replaces stale)", pid)
	}
}

func TestAgentWhoami_TmuxAliveReflectsSessionState(t *testing.T) {
	// Mirror the TestAgentWhoami setup exactly.
	t.Setenv("THRUM_HOME", "")
	t.Setenv("THRUM_NAME", "")
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	s, err := state.NewState(thrumDir, thrumDir, "test_repo_tmux_alive", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	t.Setenv("THRUM_ROLE", "implementer")
	t.Setenv("THRUM_MODULE", "tmuxtest")

	agentHandler := NewAgentHandler(s)
	registerReq := RegisterRequest{Role: "implementer", Module: "tmuxtest"}
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

	// Write a synthetic identity file with a TmuxSession that definitely
	// does not exist. The daemon must compute TmuxAlive=false without error.
	idsDir := filepath.Join(tmpDir, ".thrum", "identities")
	if err := os.MkdirAll(idsDir, 0o750); err != nil {
		t.Fatalf("mkdir identities: %v", err)
	}
	idFile := config.IdentityFile{
		Version:     1,
		AgentPID:    12345,
		TmuxSession: "thrum-nonexistent-session-xyzzy:0.0",
	}
	idFileJSON, _ := json.Marshal(idFile)
	idFilePath := filepath.Join(idsDir, agentID+".json")
	if err := os.WriteFile(idFilePath, idFileJSON, 0o600); err != nil {
		t.Fatalf("write identity file: %v", err)
	}

	whoamiParams, _ := json.Marshal(struct {
		CallerAgentID string `json:"caller_agent_id"`
	}{CallerAgentID: agentID})

	resp, err := agentHandler.HandleWhoami(context.Background(), whoamiParams)
	if err != nil {
		t.Fatalf("HandleWhoami() error = %v", err)
	}

	whoamiResp, ok := resp.(*WhoamiResponse)
	if !ok {
		t.Fatalf("response is not *WhoamiResponse, got %T", resp)
	}

	// AgentPID and TmuxSession should come straight from the identity file.
	if whoamiResp.AgentPID != 12345 {
		t.Errorf("AgentPID = %d, want 12345", whoamiResp.AgentPID)
	}
	if whoamiResp.TmuxSession != "thrum-nonexistent-session-xyzzy:0.0" {
		t.Errorf("TmuxSession = %q, want %q", whoamiResp.TmuxSession, "thrum-nonexistent-session-xyzzy:0.0")
	}

	// The session does not exist, so TmuxAlive must be false regardless of
	// whether tmux is installed (binary not found also returns false).
	if whoamiResp.TmuxAlive {
		t.Errorf("TmuxAlive = true for a non-existent session, want false")
	}

	// Host should be populated (non-empty; exact value is machine-dependent).
	if whoamiResp.Host == "" {
		t.Error("Host should be non-empty")
	}
}

// TestHandleRegister_EnforceOneIdentityOnSelfRename — thrum-33dt
// narrowed by thrum-dw06. Daemon-side enforcement fires only when the
// peercred-resolved caller is registering themselves, i.e.
// resolved.AgentID == keepName. In that self-rename case, stale
// siblings in the caller's worktree are quarantined.
//
// Note: under thrum-dw06 the previously-tested "caller X registers
// different name Y wipes stale Z" scenario no longer quarantines Z,
// because wiping siblings in a worktree the caller isn't
// self-registering into is destructive to co-located agents. That
// coverage moved to TestHandleRegister_PreservesCallerIdentity +
// TestHandleRegister_PreservesCoLocatedAgents below.
func TestHandleRegister_EnforceOneIdentityOnSelfRename(t *testing.T) {
	tmpDir := t.TempDir()
	// thrum-182j CWD-match gate inside EnforceOneIdentityWith resolves
	// both the CallerCwd and the target via `git rev-parse
	// --show-toplevel`. The daemon's self-rename path passes
	// resolved.Worktree as both, so they must be a real git worktree
	// root or the gate refuses enforcement. TempDir is not a git repo
	// by default; initialize one here so the test exercises the
	// quarantine path end-to-end rather than short-circuiting at the
	// CWD gate.
	if out, err := exec.Command("git", "-C", tmpDir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init tmpDir: %v (%s)", err, out)
	}
	// EvalSymlinks-canonicalize tmpDir so the path used in
	// peercred.WithIdentity matches gitToplevel's output after the
	// daemon's CWD-match normalization. Without this, macOS /tmp →
	// /private/tmp divergence fails the comparison even though both
	// sides point at the same worktree.
	if resolved, err := filepath.EvalSymlinks(tmpDir); err == nil {
		tmpDir = resolved
	}
	thrumDir := filepath.Join(tmpDir, ".thrum")
	idsDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(idsDir, 0o750); err != nil {
		t.Fatalf("mkdir identities: %v", err)
	}

	// Seed a stale identity file with a reliably-dead PID. The
	// self-registering caller ("new_agent") will trigger enforcement
	// because peercred resolves the caller to "new_agent" (matching
	// keepName below).
	//
	// AgentPID must be outside any plausible kernel PID range so the
	// thrum-182j defense-in-depth liveness check inside
	// EnforceOneIdentityWith correctly classifies it as dead. PID 1 is
	// init (always alive) and would be preserved, masking the
	// legitimate quarantine under test. 99999999 is well above the
	// macOS default kern.maxproc ceiling (99999) and comfortably
	// above the common Linux pid_max default (4194304).
	stale := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "old_agent", Role: "implementer", Module: "legacy",
		},
		AgentPID: 99999999,
	}
	if err := config.SaveIdentityFile(thrumDir, stale); err != nil {
		t.Fatalf("seed stale identity: %v", err)
	}
	stalePath := filepath.Join(idsDir, "old_agent.json")
	if _, err := os.Stat(stalePath); err != nil {
		t.Fatalf("stale identity file missing before register: %v", err)
	}

	s, err := state.NewState(thrumDir, thrumDir, "repo_33dt", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)

	// Inject peercred with AgentID=new_agent → register new_agent =
	// self-rename path. Enforcement fires; stale old_agent gets
	// quarantined.
	ctx := peercred.WithIdentity(context.Background(), &peercred.ResolvedIdentity{
		AgentID:  "new_agent",
		Worktree: tmpDir,
		PID:      os.Getpid(),
	})

	req := RegisterRequest{
		Name:     "new_agent",
		Role:     "implementer",
		Module:   "active",
		AgentPID: 77777,
	}
	reqJSON, _ := json.Marshal(req)
	resp, err := handler.HandleRegister(ctx, reqJSON)
	if err != nil {
		t.Fatalf("HandleRegister: %v", err)
	}
	regResp, ok := resp.(*RegisterResponse)
	if !ok || regResp.Status != "registered" {
		t.Fatalf("Register Status = %v (resp=%+v), want registered", regResp, resp)
	}

	// Stale sibling must be out of the top-level directory — ajmd's
	// quarantine semantics move it to .quarantine/.
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Errorf("stale identity old_agent.json should be quarantined after self-rename register (err=%v)", err)
	}
	// And it must land in .quarantine/ (not deleted outright).
	qDir := filepath.Join(idsDir, ".quarantine")
	qEntries, _ := os.ReadDir(qDir)
	foundInQuarantine := false
	for _, e := range qEntries {
		if strings.HasPrefix(e.Name(), "old_agent.json.") {
			foundInQuarantine = true
			break
		}
	}
	if !foundInQuarantine {
		t.Errorf("expected old_agent.json quarantined (ajmd semantics); .quarantine entries: %v", qEntries)
	}
}

// TestHandleRegister_PreservesCallerIdentity — thrum-dw06 regression.
// When a caller (peercred-resolved to an existing agent) registers a
// DIFFERENTLY named agent from the same worktree — e.g. an E2E test
// harness that registers short-lived test agents from the coordinator
// dir — the daemon-side enforceWorktreeIdentity hook must NOT
// quarantine the caller's own identity file. Thrum-ajmd softened the
// blast radius from delete → quarantine; this test pins the stricter
// invariant that the caller's file is preserved entirely.
func TestHandleRegister_PreservesCallerIdentity(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	idsDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(idsDir, 0o750); err != nil {
		t.Fatalf("mkdir identities: %v", err)
	}

	// Seed the caller's own identity file. The injected peercred
	// identity below names this caller as the worktree's legitimate
	// owner; the newly registered name will differ.
	caller := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "caller_agent", Role: "coordinator", Module: "main",
		},
		AgentPID: os.Getpid(),
	}
	if err := config.SaveIdentityFile(thrumDir, caller); err != nil {
		t.Fatalf("seed caller identity: %v", err)
	}
	callerPath := filepath.Join(idsDir, "caller_agent.json")
	if _, err := os.Stat(callerPath); err != nil {
		t.Fatalf("caller identity missing before register: %v", err)
	}

	s, err := state.NewState(thrumDir, thrumDir, "repo_dw06", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)
	// Explicit peercred.WithIdentity so the test's caller AgentID is
	// colocated with the seeded identity file name (not the helper's
	// hardcoded "test_caller") and a future helper refactor cannot
	// silently change test semantics. (review finding #2)
	ctx := peercred.WithIdentity(context.Background(), &peercred.ResolvedIdentity{
		AgentID:  "caller_agent",
		Worktree: tmpDir,
		PID:      os.Getpid(),
	})

	// Register a DIFFERENT agent name from the caller's context — the
	// E2E-harness pattern that regressed under thrum-33dt.
	req := RegisterRequest{
		Name:     "short_lived_target",
		Role:     "tester",
		Module:   "cleanup-test",
		AgentPID: 77778,
	}
	reqJSON, _ := json.Marshal(req)
	resp, err := handler.HandleRegister(ctx, reqJSON)
	if err != nil {
		t.Fatalf("HandleRegister: %v", err)
	}
	regResp, ok := resp.(*RegisterResponse)
	if !ok || regResp.Status != "registered" {
		t.Fatalf("Register Status = %v (resp=%+v), want registered", regResp, resp)
	}

	// The caller's own identity file must still be at the top level —
	// NOT moved into .quarantine/. Without the fix, it would be
	// quarantined because keepName ("short_lived_target") did not match
	// the caller's filename.
	if _, err := os.Stat(callerPath); err != nil {
		t.Fatalf("caller identity caller_agent.json must survive register of differently named agent: %v", err)
	}

	// And the quarantine dir must not contain the caller's identity.
	qDir := filepath.Join(idsDir, ".quarantine")
	if entries, err := os.ReadDir(qDir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "caller_agent.json.") {
				t.Errorf("caller identity should not be in quarantine, found: %s", e.Name())
			}
		}
	}
}

// TestHandleRegister_PreservesCoLocatedAgents — thrum-dw06. When one
// agent registers a differently named agent from a shared worktree
// (e.g. an E2E harness dir hosting multiple short-lived test agents),
// OTHER co-located registered agents' identity files must also be
// preserved, not just the caller's. Under thrum-33dt's single-keeper
// enforcement, peercred-resolving the caller to "agent_b" while
// registering "cleanup_target" would have quarantined "agent_a.json"
// (a sibling not in the keep set). The narrowed semantic skips
// enforcement entirely for bootstrap/harness registrations, so all
// co-located agents survive.
func TestHandleRegister_PreservesCoLocatedAgents(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	idsDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(idsDir, 0o750); err != nil {
		t.Fatalf("mkdir identities: %v", err)
	}

	// Two pre-existing agents co-located in the same worktree.
	agentA := &config.IdentityFile{
		Version: 5,
		Agent:   config.AgentConfig{Kind: "agent", Name: "agent_a", Role: "coordinator", Module: "main"},
	}
	agentB := &config.IdentityFile{
		Version: 5,
		Agent:   config.AgentConfig{Kind: "agent", Name: "agent_b", Role: "tester", Module: "e2e"},
	}
	if err := config.SaveIdentityFile(thrumDir, agentA); err != nil {
		t.Fatalf("seed agent_a: %v", err)
	}
	if err := config.SaveIdentityFile(thrumDir, agentB); err != nil {
		t.Fatalf("seed agent_b: %v", err)
	}

	s, err := state.NewState(thrumDir, thrumDir, "repo_dw06_colocated", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)

	// Caller = agent_b. Registration = cleanup_target (bootstrap case).
	ctx := peercred.WithIdentity(context.Background(), &peercred.ResolvedIdentity{
		AgentID:  "agent_b",
		Worktree: tmpDir,
		PID:      os.Getpid(),
	})

	req := RegisterRequest{
		Name:     "cleanup_target",
		Role:     "tester",
		Module:   "ephemeral",
		AgentPID: 99999,
	}
	reqJSON, _ := json.Marshal(req)
	if _, err := handler.HandleRegister(ctx, reqJSON); err != nil {
		t.Fatalf("HandleRegister: %v", err)
	}

	// Both pre-existing co-located agents must remain at top level.
	for _, name := range []string{"agent_a", "agent_b"} {
		if _, err := os.Stat(filepath.Join(idsDir, name+".json")); err != nil {
			t.Errorf("%s.json must survive bootstrap register of different name: %v", name, err)
		}
	}
}

// TestAgentRegister_ModeIdentityValidator pins the B-B1 Task 5 contract:
// agent.register rejects the (persistent, ephemeral) reserved grid cell
// and the (ephemeral, auto_respawn=true) cross-axis violation, while
// accepting every other canonical combination per substrate-canonical-
// reference.md §3.3 + spec §4.3.
func TestAgentRegister_ModeIdentityValidator(t *testing.T) {
	cases := []struct {
		name        string
		mode        string
		identity    string
		autoRespawn bool
		wantErr     bool
		wantSubstr  string
	}{
		// Three permitted combinations from canonical §3.3 grid.
		{name: "persistent_long_lived_personal_agent", mode: "persistent", identity: "long_lived", wantErr: false},
		{name: "ephemeral_long_lived_scheduled_agent", mode: "ephemeral", identity: "long_lived", wantErr: false},
		{name: "ephemeral_ephemeral_implementer_class", mode: "ephemeral", identity: "ephemeral", wantErr: false},

		// (mode, identity) grid rejection.
		{
			name: "rejects_persistent_ephemeral_reserved", mode: "persistent", identity: "ephemeral",
			wantErr: true, wantSubstr: "(persistent, ephemeral) is reserved",
		},
		// Cross-axis rejection.
		{
			name: "rejects_ephemeral_with_auto_respawn", mode: "ephemeral", identity: "long_lived", autoRespawn: true,
			wantErr: true, wantSubstr: "auto_respawn = true is only meaningful when mode = persistent",
		},
		// Invalid mode / identity strings.
		{
			name: "rejects_invalid_mode", mode: "weird", identity: "long_lived",
			wantErr: true, wantSubstr: "mode must be",
		},
		{
			name: "rejects_invalid_identity", mode: "persistent", identity: "weird",
			wantErr: true, wantSubstr: "identity must be",
		},
		// Empty fields default to (persistent, long_lived) — the v0.10.x
		// back-compat case where old clients don't send the new fields.
		{name: "empty_defaults_to_persistent_long_lived", mode: "", identity: "", wantErr: false},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			thrumDir := filepath.Join(tmpDir, ".thrum")
			s, err := state.NewState(thrumDir, thrumDir, "test_repo_modeident", "")
			if err != nil {
				t.Fatalf("create state: %v", err)
			}
			defer func() { _ = s.Close() }()

			handler := NewAgentHandler(s)
			req := RegisterRequest{
				Name:        tt.name,
				Role:        "implementer",
				Module:      "mode_ident_test",
				Mode:        tt.mode,
				Identity:    tt.identity,
				AutoRespawn: tt.autoRespawn,
			}
			reqJSON, _ := json.Marshal(req)
			_, err = handler.HandleRegister(context.Background(), reqJSON)

			if (err != nil) != tt.wantErr {
				t.Fatalf("HandleRegister err=%v; wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr && tt.wantSubstr != "" {
				if !strings.Contains(err.Error(), tt.wantSubstr) {
					t.Errorf("err = %v; want substring %q", err, tt.wantSubstr)
				}
			}
		})
	}
}

// TestAgentRegister_ModeIdentityValidator_WholeValidation pins the
// whole-validation contract: a request with BOTH invalid mode AND
// invalid identity surfaces BOTH error messages, not just the first.
// First-fail validators force callers to round-trip on each problem;
// whole-validation lets operators fix all problems in one pass.
func TestAgentRegister_ModeIdentityValidator_WholeValidation(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	s, err := state.NewState(thrumDir, thrumDir, "test_repo_whole", "")
	if err != nil {
		t.Fatalf("create state: %v", err)
	}
	defer func() { _ = s.Close() }()

	handler := NewAgentHandler(s)
	req := RegisterRequest{
		Name:     "whole_test",
		Role:     "implementer",
		Module:   "whole_validation",
		Mode:     "nonsense_mode",
		Identity: "nonsense_identity",
	}
	reqJSON, _ := json.Marshal(req)
	_, err = handler.HandleRegister(context.Background(), reqJSON)
	if err == nil {
		t.Fatal("expected error from invalid mode+identity, got nil")
	}
	if !strings.Contains(err.Error(), "mode must be") {
		t.Errorf("err missing mode-validation substring: %v", err)
	}
	if !strings.Contains(err.Error(), "identity must be") {
		t.Errorf("err missing identity-validation substring: %v", err)
	}
}
