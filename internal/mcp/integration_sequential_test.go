//go:build integration

package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/identity"
)

// testEnv represents a test environment with a running daemon.
// Agents can be activated and deactivated while daemon stays running.
type testEnv struct {
	t          *testing.T
	repoPath   string
	socketPath string
	server     *daemon.Server
	state      *state.State
}

// newTestEnv creates a test environment with a running daemon.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	// Use short temp path to avoid socket length limits
	shortTmp := filepath.Join(os.TempDir(), "mcp-"+filepath.Base(t.TempDir()))
	if err := os.MkdirAll(shortTmp, 0o755); err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(shortTmp) })

	// Create .thrum structure
	thrumDir := filepath.Join(shortTmp, ".thrum")
	varDir := filepath.Join(thrumDir, "var")
	syncDir := filepath.Join(thrumDir, "sync")
	identitiesDir := filepath.Join(thrumDir, "identities")

	for _, dir := range []string{varDir, syncDir, identitiesDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create dir %s: %v", dir, err)
		}
	}

	socketPath := filepath.Join(varDir, "thrum.sock")
	repoID := "test-repo-123"

	// Create state
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("create state: %v", err)
	}

	// Create and configure daemon server
	server := daemon.NewServer(socketPath)

	// Register all RPC handlers
	msgHandler := rpc.NewMessageHandler(st)
	server.RegisterHandler("message.send", msgHandler.HandleSend)
	server.RegisterHandler("message.list", msgHandler.HandleList)
	server.RegisterHandler("message.markRead", msgHandler.HandleMarkRead)

	agentHandler := rpc.NewAgentHandler(st)
	server.RegisterHandler("agent.list", agentHandler.HandleList)
	server.RegisterHandler("agent.register", agentHandler.HandleRegister)

	sessionHandler := rpc.NewSessionHandler(st)
	server.RegisterHandler("session.start", sessionHandler.HandleStart)

	healthHandler := rpc.NewHealthHandler(time.Now(), "test-1.0.0", repoID)
	server.RegisterHandler("health", healthHandler.Handle)

	// Start daemon
	ctx := context.Background()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() { server.Stop() })

	// Give server time to start
	time.Sleep(20 * time.Millisecond)

	return &testEnv{
		t:          t,
		repoPath:   shortTmp,
		socketPath: socketPath,
		server:     server,
		state:      st,
	}
}

// activateAgent sets up an agent for testing (identity, registration, session).
// Returns the agent ID and session ID.
func (e *testEnv) activateAgent(role, module string) (agentID, sessionID string) {
	e.t.Helper()

	// Create identity file named after the agent (e.g., "sender_agent.json")
	// Use a simple name based on role for uniqueness (underscores only, no hyphens)
	agentName := role + "_agent"

	identDir := filepath.Join(e.repoPath, ".thrum", "identities")
	identityFile := filepath.Join(identDir, agentName+".json")

	// Generate agent ID with name
	repoID := "test-repo-123"
	agentID = identity.GenerateAgentID(repoID, role, module, agentName)

	// Create identity with name
	identity := map[string]any{
		"version": 1,
		"repo_id": repoID,
		"agent": map[string]any{
			"kind":    "agent",
			"name":    agentName,
			"role":    role,
			"module":  module,
			"display": role,
		},
		"worktree":     "test",
		"confirmed_by": "test",
		"updated_at":   time.Now().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		e.t.Fatalf("marshal identity: %v", err)
	}

	if err := os.WriteFile(identityFile, data, 0o644); err != nil {
		e.t.Fatalf("write identity file: %v", err)
	}

	// Set environment variables (including name for NewServer to find identity)
	e.t.Setenv("THRUM_ROLE", role)
	e.t.Setenv("THRUM_MODULE", module)
	e.t.Setenv("THRUM_NAME", agentName)

	// Register agent via RPC (with name to match identity)
	client, err := cli.NewClient(e.socketPath)
	if err != nil {
		e.t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	var regResp rpc.RegisterResponse
	if err := client.Call("agent.register", rpc.RegisterRequest{
		Role:   role,
		Module: module,
		Name:   agentName,
	}, &regResp); err != nil {
		e.t.Fatalf("register agent: %v", err)
	}

	// Start session
	var sessionResp rpc.SessionStartResponse
	if err := client.Call("session.start", rpc.SessionStartRequest{
		AgentID: regResp.AgentID,
	}, &sessionResp); err != nil {
		e.t.Fatalf("start session: %v", err)
	}

	return regResp.AgentID, sessionResp.SessionID
}

// deactivateAgent removes all identity files (allows switching to another agent).
// We remove all files to ensure clean state when activating the next agent.
func (e *testEnv) deactivateAgent() {
	identDir := filepath.Join(e.repoPath, ".thrum", "identities")
	files, _ := os.ReadDir(identDir)
	for _, f := range files {
		os.Remove(filepath.Join(identDir, f.Name()))
	}
}

// TestSequentialSendAndReceive tests message sending and receiving with sequential agent activation.
func TestSequentialSendAndReceive(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Activate sender and send message
	senderAgentID, _ := env.activateAgent("sender", "mcp")
	senderMCP, err := NewServer(env.repoPath)
	if err != nil {
		t.Fatalf("create sender MCP: %v", err)
	}

	_, output, err := senderMCP.handleSendMessage(ctx, nil, SendMessageInput{
		To:       "@receiver",
		Content:  "Hello from sender",
		Priority: "normal",
	})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if output.Status != "delivered" {
		t.Errorf("expected status delivered, got %s", output.Status)
	}
	env.deactivateAgent()

	// Activate receiver and check messages
	receiverAgentID, _ := env.activateAgent("receiver", "mcp")
	receiverMCP, err := NewServer(env.repoPath)
	if err != nil {
		t.Fatalf("create receiver MCP: %v", err)
	}

	_, checkOutput, err := receiverMCP.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("check messages: %v", err)
	}

	if len(checkOutput.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(checkOutput.Messages))
	}

	msg := checkOutput.Messages[0]
	if msg.Content != "Hello from sender" {
		t.Errorf("expected 'Hello from sender', got %q", msg.Content)
	}
	if msg.From != senderAgentID {
		t.Errorf("expected from %s, got %s", senderAgentID, msg.From)
	}

	// Verify message was marked as read
	_, checkOutput2, err := receiverMCP.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if len(checkOutput2.Messages) != 0 {
		t.Errorf("expected 0 messages after read, got %d", len(checkOutput2.Messages))
	}

	_ = receiverAgentID // used for validation
}

// TestSequentialCheckMessagesMarksRead tests that check_messages marks messages as read.
func TestSequentialCheckMessagesMarksRead(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Activate sender and send message
	env.activateAgent("sender", "mcp")
	senderMCP, err := NewServer(env.repoPath)
	if err != nil {
		t.Fatalf("create sender MCP: %v", err)
	}
	_, _, err = senderMCP.handleSendMessage(ctx, nil, SendMessageInput{
		To:       "@receiver",
		Content:  "test message",
		Priority: "normal",
	})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	env.deactivateAgent()

	// Activate receiver
	env.activateAgent("receiver", "mcp")
	receiverMCP, err := NewServer(env.repoPath)
	if err != nil {
		t.Fatalf("create receiver MCP: %v", err)
	}

	// First check - should return message
	_, output1, err := receiverMCP.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("first check: %v", err)
	}
	if len(output1.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(output1.Messages))
	}

	// Second check - should be empty (marked as read)
	_, output2, err := receiverMCP.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if len(output2.Messages) != 0 {
		t.Errorf("expected 0 messages after read, got %d", len(output2.Messages))
	}
}

// TestSequentialListAgents tests list_agents with multiple registered agents.
func TestSequentialListAgents(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Register multiple agents (keep identity files)
	env.activateAgent("implementer", "backend")
	// Don't deactivate - leave identity file

	env.activateAgent("reviewer", "frontend")
	// Don't deactivate - leave identity file

	env.activateAgent("query", "test")

	queryMCP, err := NewServer(env.repoPath)
	if err != nil {
		t.Fatalf("create query MCP: %v", err)
	}

	// List agents - should see all registered agents (including offline)
	_, output, err := queryMCP.handleListAgents(ctx, nil, ListAgentsInput{
		IncludeOffline: true,
	})
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}

	if len(output.Agents) < 3 {
		t.Errorf("expected at least 3 agents, got %d", len(output.Agents))
	}

	// Verify specific agents exist
	roles := make(map[string]bool)
	for _, agent := range output.Agents {
		roles[agent.Role] = true
	}

	if !roles["implementer"] {
		t.Error("expected to find implementer agent")
	}
	if !roles["reviewer"] {
		t.Error("expected to find reviewer agent")
	}
	if !roles["query"] {
		t.Error("expected to find query agent")
	}
}

// TestSequentialBroadcast tests broadcast_message reaching multiple agents.
func TestSequentialBroadcast(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Register 3 agents
	env.activateAgent("agent1", "module1")
	env.deactivateAgent()
	env.activateAgent("agent2", "module2")
	env.deactivateAgent()
	env.activateAgent("agent3", "module3")
	env.deactivateAgent()

	// Agent1 sends broadcast
	env.activateAgent("agent1", "module1")
	agent1MCP, err := NewServer(env.repoPath)
	if err != nil {
		t.Fatalf("create agent1 MCP: %v", err)
	}
	_, _, err = agent1MCP.handleBroadcast(ctx, nil, BroadcastInput{
		Content:  "broadcast from agent1",
		Priority: "normal",
	})
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	env.deactivateAgent()

	// Agent2 should receive broadcast
	env.activateAgent("agent2", "module2")
	agent2MCP, err := NewServer(env.repoPath)
	if err != nil {
		t.Fatalf("create agent2 MCP: %v", err)
	}
	_, output2, err := agent2MCP.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("check agent2 messages: %v", err)
	}
	if len(output2.Messages) == 0 {
		t.Error("agent2 should have received broadcast")
	} else if output2.Messages[0].Content != "broadcast from agent1" {
		t.Errorf("expected 'broadcast from agent1', got %q", output2.Messages[0].Content)
	}
	env.deactivateAgent()

	// Agent3 should also receive broadcast
	env.activateAgent("agent3", "module3")
	agent3MCP, err := NewServer(env.repoPath)
	if err != nil {
		t.Fatalf("create agent3 MCP: %v", err)
	}
	_, output3, err := agent3MCP.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("check agent3 messages: %v", err)
	}
	if len(output3.Messages) == 0 {
		t.Error("agent3 should have received broadcast")
	}
	env.deactivateAgent()

	// Agent1 should NOT receive own broadcast
	env.activateAgent("agent1", "module1")
	agent1MCP2, err := NewServer(env.repoPath)
	if err != nil {
		t.Fatalf("create agent1 MCP (second time): %v", err)
	}
	_, output1, err := agent1MCP2.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("check agent1 messages: %v", err)
	}
	if len(output1.Messages) > 0 {
		t.Error("agent1 should not receive own broadcast")
	}
}

// TestMCPServerFailsWithoutDaemon tests error handling when daemon is not running.
func TestMCPServerFailsWithoutDaemon(t *testing.T) {
	repoPath := t.TempDir()

	// Create identity
	identDir := filepath.Join(repoPath, ".thrum", "identities")
	os.MkdirAll(identDir, 0o755)

	identity := map[string]any{
		"version": 1,
		"repo_id": "test-repo-123",
		"agent": map[string]any{
			"kind":    "agent",
			"name":    "test_agent",
			"role":    "test",
			"module":  "test",
			"display": "test",
		},
		"worktree":     "test",
		"confirmed_by": "test",
		"updated_at":   time.Now().Format(time.RFC3339),
	}

	data, _ := json.MarshalIndent(identity, "", "  ")
	os.WriteFile(filepath.Join(identDir, "test_agent.json"), data, 0o644)

	t.Setenv("THRUM_ROLE", "test")
	t.Setenv("THRUM_MODULE", "test")
	t.Setenv("THRUM_NAME", "test_agent")

	// Create MCP server (should succeed)
	mcpServer, err := NewServer(repoPath)
	if err != nil {
		t.Fatalf("NewServer should succeed without daemon: %v", err)
	}

	// Try to send message (should fail - no daemon)
	ctx := context.Background()
	_, _, err = mcpServer.handleSendMessage(ctx, nil, SendMessageInput{
		To:       "@someone",
		Content:  "test",
		Priority: "normal",
	})

	if err == nil {
		t.Fatal("expected error when daemon not running")
	}

	// Error should mention connection/socket
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}
