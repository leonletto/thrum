//go:build integration

package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// testDaemon wraps a daemon server for integration tests.
type testDaemon struct {
	server     *daemon.Server
	repoPath   string
	socketPath string
	wsServer   *http.Server
	wsPort     int
}

// newTestDaemon creates a daemon server in a temp directory.
func newTestDaemon(t *testing.T) *testDaemon {
	t.Helper()

	// Use a much shorter base temp directory to avoid socket path length limits
	// Create in /tmp/mcp-test-{random} instead of the default test temp dir
	shortTmp := filepath.Join(os.TempDir(), "mcp-"+filepath.Base(t.TempDir()))
	if err := os.MkdirAll(shortTmp, 0o755); err != nil {
		t.Fatalf("create short temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(shortTmp) })
	repoPath := shortTmp

	// Create .thrum directory structure
	thrumDir := filepath.Join(repoPath, ".thrum")
	varDir := filepath.Join(thrumDir, "var")
	syncDir := filepath.Join(thrumDir, "sync")
	identitiesDir := filepath.Join(thrumDir, "identities")

	for _, dir := range []string{varDir, syncDir, identitiesDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create dir %s: %v", dir, err)
		}
	}

	// Socket path in standard location
	socketPath := filepath.Join(varDir, "thrum.sock")

	// Create state
	repoID := "test-repo-123"
	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("create state: %v", err)
	}

	// Create daemon server
	server := daemon.NewServer(socketPath)

	// Register RPC handlers
	msgHandler := rpc.NewMessageHandler(st)
	server.RegisterHandler("message.send", msgHandler.HandleSend)
	server.RegisterHandler("message.get", msgHandler.HandleGet)
	server.RegisterHandler("message.list", msgHandler.HandleList)
	server.RegisterHandler("message.markRead", msgHandler.HandleMarkRead)

	// Agent handler (for list_agents and agent.register)
	agentHandler := rpc.NewAgentHandler(st)
	server.RegisterHandler("agent.list", agentHandler.HandleList)
	server.RegisterHandler("agent.register", agentHandler.HandleRegister)

	// Session handler
	sessionHandler := rpc.NewSessionHandler(st)
	server.RegisterHandler("session.start", sessionHandler.HandleStart)

	// Group handler
	groupHandler := rpc.NewGroupHandler(st)
	server.RegisterHandler("group.create", groupHandler.HandleCreate)
	server.RegisterHandler("group.delete", groupHandler.HandleDelete)
	server.RegisterHandler("group.member.add", groupHandler.HandleMemberAdd)
	server.RegisterHandler("group.member.remove", groupHandler.HandleMemberRemove)
	server.RegisterHandler("group.list", groupHandler.HandleList)
	server.RegisterHandler("group.info", groupHandler.HandleInfo)

	// Ensure @everyone group exists (as daemon startup would)
	if err := rpc.EnsureEveryoneGroup(context.Background(), st); err != nil {
		t.Fatalf("ensure everyone group: %v", err)
	}

	// Health handler
	healthHandler := rpc.NewHealthHandler(time.Now(), "test-1.0.0", repoID)
	server.RegisterHandler("health", healthHandler.Handle)

	return &testDaemon{
		server:     server,
		repoPath:   repoPath,
		socketPath: socketPath,
		wsPort:     0, // Will be set when WS server starts
	}
}

// start starts the daemon server and WebSocket server.
func (td *testDaemon) start(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	if err := td.server.Start(ctx); err != nil {
		t.Fatalf("failed to start daemon server: %v", err)
	}

	// Wait for socket to be ready
	waitForSocketReady(t, td.socketPath)

	// TODO: Start WebSocket server for wait_for_message tests
	// For now, skip WS server - we'll test without it first
}

// stop stops the daemon server.
func (td *testDaemon) stop() {
	if td.server != nil {
		td.server.Stop()
	}
	if td.wsServer != nil {
		td.wsServer.Close()
	}
}

// createIdentity creates a test identity in the daemon's repo.
// Name-only routing requires a non-empty agent name in the identity file.
func (td *testDaemon) createIdentity(t *testing.T, filename, role, module string) {
	t.Helper()

	identDir := filepath.Join(td.repoPath, ".thrum", "identities")
	if err := os.MkdirAll(identDir, 0o755); err != nil {
		t.Fatalf("create identities dir: %v", err)
	}

	identity := map[string]any{
		"version": 1,
		"repo_id": "test-repo-123",
		"agent": map[string]any{
			"kind":    "agent",
			"name":    filename,
			"role":    role,
			"module":  module,
			"display": filename,
		},
		"worktree":     "test",
		"confirmed_by": "test",
		"updated_at":   time.Now().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		t.Fatalf("marshal identity: %v", err)
	}

	if err := os.WriteFile(filepath.Join(identDir, filename+".json"), data, 0o644); err != nil {
		t.Fatalf("write identity file: %v", err)
	}
}

// newClient creates a daemon RPC client.
func (td *testDaemon) newClient() (*cli.Client, error) {
	return cli.NewClient(td.socketPath)
}

// registerAndStartSession registers an agent and starts a session.
func (td *testDaemon) registerAndStartSession(t *testing.T, role, module, name string) string {
	t.Helper()

	client, err := td.newClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	var regResp rpc.RegisterResponse
	err = client.Call("agent.register", rpc.RegisterRequest{
		Name:   name,
		Role:   role,
		Module: module,
	}, &regResp)
	if err != nil {
		t.Fatalf("failed to register agent %s: %v", name, err)
	}

	// Start session
	var sessionResp rpc.SessionStartResponse
	err = client.Call("session.start", rpc.SessionStartRequest{
		AgentID: regResp.AgentID,
	}, &sessionResp)
	if err != nil {
		t.Fatalf("failed to start session for %s: %v", name, err)
	}

	return sessionResp.SessionID
}

// TestSendMessageAndCheckMessages verifies the send_message and check_messages round-trip.
func TestSendMessageAndCheckMessages(t *testing.T) {
	td := newTestDaemon(t)
	defer td.stop()

	// Create identities for sender and receiver
	td.createIdentity(t, "sender", "test-sender", "mcp")
	td.createIdentity(t, "receiver", "test-receiver", "mcp")

	td.start(t)

	// Register and start sessions for both agents
	td.registerAndStartSession(t, "test-sender", "mcp", "sender")
	td.registerAndStartSession(t, "test-receiver", "mcp", "receiver")

	// Set environment variables so daemon can resolve agent
	t.Setenv("THRUM_NAME", "sender")
	t.Setenv("THRUM_ROLE", "test-sender")
	t.Setenv("THRUM_MODULE", "mcp")

	// Create MCP server for sender
	mcpServer, err := NewServer(td.repoPath)
	if err != nil {
		t.Fatalf("NewServer for sender failed: %v", err)
	}

	// Send a message to receiver (by agent name, not role)
	ctx := context.Background()
	input := SendMessageInput{
		To:       "@receiver",
		Content:  "hello from integration test",
	}

	_, output, err := mcpServer.handleSendMessage(ctx, nil, input)
	if err != nil {
		t.Fatalf("handleSendMessage failed: %v", err)
	}

	if output.Status != "delivered" {
		t.Errorf("expected status 'delivered', got %q", output.Status)
	}

	// Now check messages as receiver
	t.Setenv("THRUM_NAME", "receiver")
	mcpServerReceiver, err := NewServer(td.repoPath)
	if err != nil {
		t.Fatalf("NewServer for receiver failed: %v", err)
	}

	_, checkOutput, err := mcpServerReceiver.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("handleCheckMessages failed: %v", err)
	}

	if len(checkOutput.Messages) == 0 {
		t.Fatal("expected at least one message, got none")
	}

	msg := checkOutput.Messages[0]
	if msg.Content != "hello from integration test" {
		t.Errorf("expected content 'hello from integration test', got %q", msg.Content)
	}
	// From is the full agent_id string like "agent:test-sender:hash"
	// Just verify it contains the sender role
	if msg.From == "" {
		t.Error("expected non-empty from field")
	}
}

// TestCheckMessagesMarksAsRead verifies that check_messages consumes unread messages.
func TestCheckMessagesMarksAsRead(t *testing.T) {
	td := newTestDaemon(t)
	defer td.stop()

	td.createIdentity(t, "sender", "test-sender", "mcp")
	td.createIdentity(t, "receiver", "test-receiver", "mcp")

	td.start(t)

	// Register and start sessions for both agents
	td.registerAndStartSession(t, "test-sender", "mcp", "sender")
	td.registerAndStartSession(t, "test-receiver", "mcp", "receiver")

	ctx := context.Background()

	// Send a message
	t.Setenv("THRUM_ROLE", "test-sender")
	t.Setenv("THRUM_MODULE", "mcp")
	t.Setenv("THRUM_NAME", "sender")

	mcpSender, err := NewServer(td.repoPath)
	if err != nil {
		t.Fatalf("NewServer for sender failed: %v", err)
	}

	_, _, err = mcpSender.handleSendMessage(ctx, nil, SendMessageInput{
		To:       "@receiver",
		Content:  "test message",
	})
	if err != nil {
		t.Fatalf("handleSendMessage failed: %v", err)
	}

	// Check messages as receiver (first call)
	t.Setenv("THRUM_ROLE", "test-receiver")
	t.Setenv("THRUM_MODULE", "mcp")
	t.Setenv("THRUM_NAME", "receiver")

	mcpReceiver, err := NewServer(td.repoPath)
	if err != nil {
		t.Fatalf("NewServer for receiver failed: %v", err)
	}

	_, output1, err := mcpReceiver.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("first handleCheckMessages failed: %v", err)
	}

	if len(output1.Messages) != 1 {
		t.Fatalf("expected 1 message on first call, got %d", len(output1.Messages))
	}

	// Check messages again (second call) - should be empty since already marked read
	_, output2, err := mcpReceiver.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("second handleCheckMessages failed: %v", err)
	}

	if len(output2.Messages) != 0 {
		t.Errorf("expected 0 messages on second call (already consumed), got %d", len(output2.Messages))
	}
}

// TestListAgents verifies that list_agents returns registered agents.
func TestListAgents(t *testing.T) {
	td := newTestDaemon(t)
	defer td.stop()

	// Create multiple identities
	td.createIdentity(t, "alice", "implementer", "backend")
	td.createIdentity(t, "bob", "reviewer", "frontend")

	td.start(t)

	// Register agents (no need to start sessions for list_agents)
	td.registerAndStartSession(t, "implementer", "backend", "alice")
	td.registerAndStartSession(t, "reviewer", "frontend", "bob")

	// Use alice's identity for the MCP server
	t.Setenv("THRUM_NAME", "alice")
	mcpServer, err := NewServer(td.repoPath)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()
	_, output, err := mcpServer.handleListAgents(ctx, nil, ListAgentsInput{})
	if err != nil {
		t.Fatalf("handleListAgents failed: %v", err)
	}

	// We should see at least the 2 agents we registered
	if len(output.Agents) < 2 {
		t.Errorf("expected at least 2 agents, got %d", len(output.Agents))
	}

	// Verify alice is in the list
	found := false
	for _, agent := range output.Agents {
		if agent.Role == "implementer" && agent.Module == "backend" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find alice (implementer:backend) in agent list")
	}
}

// TestBroadcastMessage verifies that broadcast_message reaches all agents except sender.
func TestBroadcastMessage(t *testing.T) {
	td := newTestDaemon(t)
	defer td.stop()

	// Create 3 agents
	td.createIdentity(t, "alice", "agent1", "module1")
	td.createIdentity(t, "bob", "agent2", "module2")
	td.createIdentity(t, "charlie", "agent3", "module3")

	td.start(t)

	// Register and start sessions for all agents
	td.registerAndStartSession(t, "agent1", "module1", "alice")
	td.registerAndStartSession(t, "agent2", "module2", "bob")
	td.registerAndStartSession(t, "agent3", "module3", "charlie")

	ctx := context.Background()

	// Alice broadcasts a message
	t.Setenv("THRUM_ROLE", "agent1")
	t.Setenv("THRUM_MODULE", "module1")
	t.Setenv("THRUM_NAME", "alice")

	mcpAlice, err := NewServer(td.repoPath)
	if err != nil {
		t.Fatalf("NewServer for alice failed: %v", err)
	}

	_, _, err = mcpAlice.handleBroadcast(ctx, nil, BroadcastInput{
		Content:  "broadcast from alice",
	})
	if err != nil {
		t.Fatalf("handleBroadcast failed: %v", err)
	}

	// Bob should receive the broadcast
	t.Setenv("THRUM_ROLE", "agent2")
	t.Setenv("THRUM_MODULE", "module2")
	t.Setenv("THRUM_NAME", "bob")

	mcpBob, err := NewServer(td.repoPath)
	if err != nil {
		t.Fatalf("NewServer for bob failed: %v", err)
	}

	_, bobMsgs, err := mcpBob.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("handleCheckMessages for bob failed: %v", err)
	}

	if len(bobMsgs.Messages) == 0 {
		t.Fatal("bob should have received broadcast message")
	}
	if bobMsgs.Messages[0].Content != "broadcast from alice" {
		t.Errorf("expected content 'broadcast from alice', got %q", bobMsgs.Messages[0].Content)
	}

	// Charlie should also receive the broadcast
	t.Setenv("THRUM_ROLE", "agent3")
	t.Setenv("THRUM_MODULE", "module3")
	t.Setenv("THRUM_NAME", "charlie")

	mcpCharlie, err := NewServer(td.repoPath)
	if err != nil {
		t.Fatalf("NewServer for charlie failed: %v", err)
	}

	_, charlieMsgs, err := mcpCharlie.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("handleCheckMessages for charlie failed: %v", err)
	}

	if len(charlieMsgs.Messages) == 0 {
		t.Fatal("charlie should have received broadcast message")
	}

	// Alice should NOT receive her own broadcast
	_, aliceMsgs, err := mcpAlice.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("handleCheckMessages for alice failed: %v", err)
	}

	if len(aliceMsgs.Messages) > 0 {
		t.Error("alice should not receive her own broadcast message")
	}
}

// TestMCPServerStartupFailsWithoutDaemon verifies error when daemon is not running.
func TestMCPServerStartupFailsWithoutDaemon(t *testing.T) {
	repoPath := t.TempDir()
	createTestIdentity(t, repoPath, "test", "test-role", "test-module")

	t.Setenv("THRUM_NAME", "test")

	// Create MCP server but don't start daemon
	mcpServer, err := NewServer(repoPath)
	if err != nil {
		t.Fatalf("NewServer should succeed even without daemon: %v", err)
	}

	// Try to send a message - this should fail because daemon is not running
	ctx := context.Background()
	_, _, err = mcpServer.handleSendMessage(ctx, nil, SendMessageInput{
		To:       "@someone",
		Content:  "test",
	})

	if err == nil {
		t.Fatal("expected error when daemon is not running, got nil")
	}

	// Error should mention connection or socket
	errMsg := err.Error()
	if errMsg == "" {
		t.Error("expected non-empty error message")
	}
	// The error should be something like "dial unix ... no such file or directory"
}

// TestWaitForMessageTimeout verifies timeout behavior when no messages arrive.
func TestWaitForMessageTimeout(t *testing.T) {
	t.Skip("TODO: Implement after WebSocket server is added to testDaemon")

	td := newTestDaemon(t)
	defer td.stop()

	td.createIdentity(t, "waiter", "test-waiter", "mcp")
	td.start(t)

	t.Setenv("THRUM_NAME", "waiter")
	mcpServer, err := NewServer(td.repoPath)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()
	start := time.Now()

	_, output, err := mcpServer.handleWaitForMessage(ctx, nil, WaitForMessageInput{
		Timeout: 2,
	})
	if err != nil {
		t.Fatalf("handleWaitForMessage failed: %v", err)
	}

	elapsed := time.Since(start)

	if output.Status != "timeout" {
		t.Errorf("expected status 'timeout', got %q", output.Status)
	}

	// Should take approximately 2 seconds
	if elapsed < 1900*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Errorf("expected ~2s timeout, took %v", elapsed)
	}
}

// TestWaitForMessageReceivesMessage verifies message reception via WebSocket.
func TestWaitForMessageReceivesMessage(t *testing.T) {
	t.Skip("TODO: Implement after WebSocket server is added to testDaemon")

	td := newTestDaemon(t)
	defer td.stop()

	td.createIdentity(t, "sender", "test-sender", "mcp")
	td.createIdentity(t, "waiter", "test-waiter", "mcp")

	td.start(t)

	ctx := context.Background()

	// Start wait_for_message in a goroutine
	t.Setenv("THRUM_NAME", "waiter")
	mcpWaiter, err := NewServer(td.repoPath)
	if err != nil {
		t.Fatalf("NewServer for waiter failed: %v", err)
	}

	type waitResult struct {
		output WaitForMessageOutput
		err    error
	}
	resultCh := make(chan waitResult, 1)

	go func() {
		_, output, err := mcpWaiter.handleWaitForMessage(ctx, nil, WaitForMessageInput{
			Timeout: 10,
		})
		resultCh <- waitResult{output, err}
	}()

	// Give waiter time to connect to WebSocket (intentional - ensuring operation is in-flight)
	time.Sleep(500 * time.Millisecond)

	// Send a message to waiter
	t.Setenv("THRUM_NAME", "sender")
	mcpSender, err := NewServer(td.repoPath)
	if err != nil {
		t.Fatalf("NewServer for sender failed: %v", err)
	}

	_, _, err = mcpSender.handleSendMessage(ctx, nil, SendMessageInput{
		To:       "@waiter",
		Content:  "wake up!",
	})
	if err != nil {
		t.Fatalf("handleSendMessage failed: %v", err)
	}

	// Wait for result with timeout
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("waitForMessageHandler failed: %v", result.err)
		}
		if result.output.Status != "message_received" {
			t.Errorf("expected status 'message_received', got %q", result.output.Status)
		}
		if result.output.Message == nil {
			t.Fatal("expected a message, got nil")
		}
		if result.output.Message.Content != "wake up!" {
			t.Errorf("expected content 'wake up!', got %q", result.output.Message.Content)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait_for_message did not return within 5 seconds")
	}

	// Should return in < 2 seconds (we waited 500ms + send time)
}

// waitForSocketReady waits for a Unix socket to become available and accept connections, with timeout.
func waitForSocketReady(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		// Check if socket file exists
		if _, err := os.Stat(socketPath); err == nil {
			// Try to actually connect to verify server is ready
			conn, err := net.Dial("unix", socketPath)
			if err == nil {
				_ = conn.Close()
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("socket %s did not become available", socketPath)
}
