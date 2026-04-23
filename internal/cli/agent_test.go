package cli

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

func TestAgentRegister(t *testing.T) {
	mockResponse := RegisterResponse{
		AgentID: "agent:implementer:ABC123",
		Status:  "registered",
	}

	// Create mock daemon
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	// Start mock daemon with handler
	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			t.Logf("Failed to decode request: %v", err)
			return
		}

		// Verify method
		if request["method"] != "agent.register" {
			t.Errorf("Expected method 'agent.register', got %v", request["method"])
		}

		// Send response
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result":  mockResponse,
		}

		if err := encoder.Encode(response); err != nil {
			t.Logf("Failed to encode response: %v", err)
		}
	})

	// Wait for daemon to be ready
	<-daemon.Ready()

	// Create client
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Call AgentRegister
	opts := AgentRegisterOptions{
		Role:   "implementer",
		Module: "auth",
	}

	result, err := AgentRegister(client, opts)
	if err != nil {
		t.Fatalf("AgentRegister() error = %v", err)
	}

	if result.Status != mockResponse.Status {
		t.Errorf("Status = %s, want %s", result.Status, mockResponse.Status)
	}

	if result.AgentID != mockResponse.AgentID {
		t.Errorf("AgentID = %s, want %s", result.AgentID, mockResponse.AgentID)
	}
}

// TestAgentRegister_ForceFwd — thrum-ufv5.6 regression.
// Verifies the CLI-side AgentRegister serializes Force=true into the
// "force" JSON field on the wire. Paired with the daemon-side Force
// trigger (thrum-ufv5.2), this guarantees a `thrum quickstart --force`
// actually re-registers the agent in the daemon projection even when
// the caller's detected PID matches the stored one (post-purge scenario
// where --force is the only remaining trigger for re-registration).
//
// Test name kept short to stay under macOS's 104-char unix-socket path
// limit when combined with $TMPDIR.
func TestAgentRegister_ForceFwd(t *testing.T) {
	mockResponse := RegisterResponse{
		AgentID: "e2e_coordinator",
		Status:  "updated",
	}

	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	var captured map[string]any
	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			t.Logf("decode request: %v", err)
			return
		}
		if params, ok := request["params"].(map[string]any); ok {
			captured = params
		}

		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result":  mockResponse,
		}
		_ = encoder.Encode(response)
	})
	<-daemon.Ready()

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Call with Force=true — the CLI register command and (after
	// thrum-ufv5.6) cli.Quickstart both forward --force into this field.
	opts := AgentRegisterOptions{
		Name:   "e2e_coordinator",
		Role:   "owner",
		Module: "all",
		Force:  true,
	}
	if _, err := AgentRegister(client, opts); err != nil {
		t.Fatalf("AgentRegister: %v", err)
	}

	if captured == nil {
		t.Fatal("daemon did not receive a request")
	}
	// Key assertion: wire payload carries "force":true.
	// Without this, the daemon's Force branch never fires and the
	// agents projection stays stale on --force.
	force, _ := captured["force"].(bool)
	if !force {
		t.Errorf(`params["force"] = %v, want true; full params: %+v`, captured["force"], captured)
	}
}

func TestAgentList(t *testing.T) {
	mockResponse := ListAgentsResponse{
		Agents: []AgentInfo{
			{
				AgentID:      "agent:implementer:ABC123",
				Kind:         "agent",
				Role:         "implementer",
				Module:       "auth",
				Display:      "Auth Implementer",
				RegisteredAt: "2026-02-03T10:00:00Z",
			},
			{
				AgentID:      "agent:reviewer:XYZ789",
				Kind:         "agent",
				Role:         "reviewer",
				Module:       "auth",
				Display:      "Auth Reviewer",
				RegisteredAt: "2026-02-03T11:00:00Z",
			},
		},
	}

	// Create mock daemon
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	// Start mock daemon with handler
	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			t.Logf("Failed to decode request: %v", err)
			return
		}

		// Verify method
		if request["method"] != "agent.list" {
			t.Errorf("Expected method 'agent.list', got %v", request["method"])
		}

		// Send response
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result":  mockResponse,
		}

		if err := encoder.Encode(response); err != nil {
			t.Logf("Failed to encode response: %v", err)
		}
	})

	// Wait for daemon to be ready
	<-daemon.Ready()

	// Create client
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Call AgentList
	result, err := AgentList(client, AgentListOptions{})
	if err != nil {
		t.Fatalf("AgentList() error = %v", err)
	}

	if len(result.Agents) != len(mockResponse.Agents) {
		t.Errorf("Agent count = %d, want %d", len(result.Agents), len(mockResponse.Agents))
	}
}

func TestAgentWhoami(t *testing.T) {
	mockResponse := WhoamiResult{
		AgentID:      "agent:implementer:ABC123",
		Role:         "implementer",
		Module:       "auth",
		Display:      "Auth Implementer",
		Source:       "environment",
		SessionID:    "ses_01HXE...",
		SessionStart: "2026-02-03T10:00:00Z",
	}

	// Create mock daemon
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	// Start mock daemon with handler
	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			t.Logf("Failed to decode request: %v", err)
			return
		}

		// Verify method
		if request["method"] != "agent.whoami" {
			t.Errorf("Expected method 'agent.whoami', got %v", request["method"])
		}

		// Send response
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result":  mockResponse,
		}

		if err := encoder.Encode(response); err != nil {
			t.Logf("Failed to encode response: %v", err)
		}
	})

	// Wait for daemon to be ready
	<-daemon.Ready()

	// Create client
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Call AgentWhoami
	result, err := AgentWhoami(client)
	if err != nil {
		t.Fatalf("AgentWhoami() error = %v", err)
	}

	if result.AgentID != mockResponse.AgentID {
		t.Errorf("AgentID = %s, want %s", result.AgentID, mockResponse.AgentID)
	}

	if result.Role != mockResponse.Role {
		t.Errorf("Role = %s, want %s", result.Role, mockResponse.Role)
	}
}

func TestFormatRegisterResponse(t *testing.T) {
	tests := []struct {
		name     string
		response RegisterResponse
		contains []string
	}{
		{
			name: "registered",
			response: RegisterResponse{
				AgentID: "agent:implementer:ABC123",
				Status:  "registered",
			},
			contains: []string{"registered", "agent:implementer:ABC123"},
		},
		{
			name: "conflict",
			response: RegisterResponse{
				Status: "conflict",
				Conflict: &ConflictInfo{
					ExistingAgentID: "agent:implementer:XYZ789",
					RegisteredAt:    "2026-02-03T10:00:00Z",
				},
			},
			contains: []string{"conflict", "agent:implementer:XYZ789"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatRegisterResponse(&tt.response)
			for _, substr := range tt.contains {
				if !contains(output, substr) {
					t.Errorf("Output should contain '%s'", substr)
				}
			}
		})
	}
}

func TestFormatAgentList(t *testing.T) {
	tests := []struct {
		name     string
		response ListAgentsResponse
		contains []string
	}{
		{
			name:     "empty_list",
			response: ListAgentsResponse{Agents: []AgentInfo{}},
			contains: []string{"No agents"},
		},
		{
			name: "with_agents",
			response: ListAgentsResponse{
				Agents: []AgentInfo{
					{
						AgentID:      "agent:implementer:ABC123",
						Role:         "implementer",
						Module:       "auth",
						RegisteredAt: "2026-02-03T10:00:00Z",
					},
				},
			},
			contains: []string{"implementer", "auth"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatAgentList(&tt.response)
			for _, substr := range tt.contains {
				if !contains(output, substr) {
					t.Errorf("Output should contain '%s'", substr)
				}
			}
		})
	}
}

func TestFormatWhoHas(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		response ListContextResponse
		contains []string
	}{
		{
			name:     "no agents editing file",
			file:     "auth.go",
			response: ListContextResponse{Contexts: []AgentWorkContext{}},
			contains: []string{"No agents", "auth.go"},
		},
		{
			name: "agent editing file",
			file: "auth.go",
			response: ListContextResponse{
				Contexts: []AgentWorkContext{
					{
						AgentID:          "agent:planner:auth",
						Branch:           "feature/auth",
						UncommittedFiles: []string{"auth.go", "auth_test.go", "handler.go"},
					},
				},
			},
			contains: []string{"@planner", "auth.go", "3 uncommitted", "feature/auth"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatWhoHas(tt.file, &tt.response)
			for _, substr := range tt.contains {
				if !contains(output, substr) {
					t.Errorf("Output should contain '%s', got: %s", substr, output)
				}
			}
		})
	}
}

func TestFormatPing(t *testing.T) {
	tests := []struct {
		name     string
		role     string
		agents   ListAgentsResponse
		contexts *ListContextResponse
		contains []string
	}{
		{
			name:     "agent not found",
			role:     "unknown",
			agents:   ListAgentsResponse{Agents: []AgentInfo{}},
			contexts: nil,
			contains: []string{"@unknown", "not found"},
		},
		{
			name: "active agent matched by name",
			role: "agent_b",
			agents: ListAgentsResponse{
				Agents: []AgentInfo{
					{AgentID: "agent:reviewer:auth", Role: "reviewer", Display: "agent_b"},
				},
			},
			contexts: &ListContextResponse{
				Contexts: []AgentWorkContext{
					{
						AgentID:   "agent:reviewer:auth",
						SessionID: "ses_abc",
						Intent:    "Reviewing PR #42",
						Branch:    "feature/auth",
					},
				},
			},
			contains: []string{"@agent_b", "active", "Reviewing PR #42", "feature/auth"},
		},
		{
			name: "fallback to role match",
			role: "reviewer",
			agents: ListAgentsResponse{
				Agents: []AgentInfo{
					{AgentID: "agent:reviewer:auth", Role: "reviewer", Display: "agent_b"},
				},
			},
			contexts: &ListContextResponse{
				Contexts: []AgentWorkContext{
					{
						AgentID:   "agent:reviewer:auth",
						SessionID: "ses_abc",
						Intent:    "Reviewing PR #42",
					},
				},
			},
			contains: []string{"@reviewer", "active"},
		},
		{
			name: "offline agent",
			role: "builder",
			agents: ListAgentsResponse{
				Agents: []AgentInfo{
					{AgentID: "agent:builder:core", Role: "builder", Display: "builder", LastSeenAt: "2026-02-03T10:00:00Z"},
				},
			},
			contexts: &ListContextResponse{Contexts: []AgentWorkContext{}},
			contains: []string{"@builder", "offline"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatPing(tt.role, &tt.agents, tt.contexts)
			for _, substr := range tt.contains {
				if !contains(output, substr) {
					t.Errorf("Output should contain '%s', got: %s", substr, output)
				}
			}
		})
	}
}

func TestAgentSummary_IncludesHookDeliveryFields(t *testing.T) {
	idFile := &config.IdentityFile{
		Agent:       config.AgentConfig{Name: "bob", Role: "impl", Module: "mod"},
		AgentPID:    12345,
		TmuxSession: "bob:0.0",
	}
	daemonInfo := &WhoamiResult{
		TmuxAlive: true,
		Host:      "laptop.local",
	}
	s := BuildAgentSummary(idFile, "/p/bob.json", daemonInfo)
	if s.Host != "laptop.local" || s.PID != 12345 || s.TmuxSession != "bob:0.0" || !s.TmuxAlive {
		t.Fatalf("missing hook-delivery fields: %+v", s)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"host", "pid", "tmux_session", "tmux_alive"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing JSON key %q in %s", key, string(data))
		}
	}
}
