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

// routingTestEnv is a test environment that includes group handlers and the @everyone group.
type routingTestEnv struct {
	t          *testing.T
	repoPath   string
	socketPath string
	server     *daemon.Server
	state      *state.State
}

// newRoutingTestEnv creates a test environment with full message routing support.
// Unlike newTestEnv, this registers group handlers and creates the @everyone group.
func newRoutingTestEnv(t *testing.T) *routingTestEnv {
	t.Helper()

	// Use short temp path to avoid socket length limits.
	shortTmp := filepath.Join(os.TempDir(), "mcp-rt-"+filepath.Base(t.TempDir()))
	if err := os.MkdirAll(shortTmp, 0o755); err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(shortTmp) })

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

	st, err := state.NewState(thrumDir, thrumDir, repoID)
	if err != nil {
		t.Fatalf("create state: %v", err)
	}

	// Register all RPC handlers including group handlers.
	srv := daemon.NewServer(socketPath)

	msgHandler := rpc.NewMessageHandler(st)
	srv.RegisterHandler("message.send", msgHandler.HandleSend)
	srv.RegisterHandler("message.get", msgHandler.HandleGet)
	srv.RegisterHandler("message.list", msgHandler.HandleList)
	srv.RegisterHandler("message.markRead", msgHandler.HandleMarkRead)

	agentHandler := rpc.NewAgentHandler(st)
	srv.RegisterHandler("agent.list", agentHandler.HandleList)
	srv.RegisterHandler("agent.register", agentHandler.HandleRegister)

	sessionHandler := rpc.NewSessionHandler(st)
	srv.RegisterHandler("session.start", sessionHandler.HandleStart)

	groupHandler := rpc.NewGroupHandler(st)
	srv.RegisterHandler("group.create", groupHandler.HandleCreate)
	srv.RegisterHandler("group.delete", groupHandler.HandleDelete)
	srv.RegisterHandler("group.member.add", groupHandler.HandleMemberAdd)
	srv.RegisterHandler("group.member.remove", groupHandler.HandleMemberRemove)
	srv.RegisterHandler("group.list", groupHandler.HandleList)
	srv.RegisterHandler("group.info", groupHandler.HandleInfo)
	srv.RegisterHandler("group.members", groupHandler.HandleMembers)

	healthHandler := rpc.NewHealthHandler(time.Now(), "test-1.0.0", repoID)
	srv.RegisterHandler("health", healthHandler.Handle)

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() { srv.Stop() })

	waitForSocketReady(t, socketPath)

	// Create the built-in @everyone group so broadcasts work.
	if err := rpc.EnsureEveryoneGroup(ctx, st); err != nil {
		t.Fatalf("ensure everyone group: %v", err)
	}

	return &routingTestEnv{
		t:          t,
		repoPath:   shortTmp,
		socketPath: socketPath,
		server:     srv,
		state:      st,
	}
}

// activateRoutingAgent creates an identity file and registers the agent with the daemon.
// Returns the agent ID after registration.
func (e *routingTestEnv) activateRoutingAgent(name, role, module string) string {
	e.t.Helper()

	identDir := filepath.Join(e.repoPath, ".thrum", "identities")
	identityFile := filepath.Join(identDir, name+".json")

	repoID := "test-repo-123"
	agentID := identity.GenerateAgentID(repoID, role, module, name)

	ident := map[string]any{
		"version": 1,
		"repo_id": repoID,
		"agent": map[string]any{
			"kind":    "agent",
			"name":    name,
			"role":    role,
			"module":  module,
			"display": name,
		},
		"worktree":     "test",
		"confirmed_by": "test",
		"updated_at":   time.Now().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(ident, "", "  ")
	if err != nil {
		e.t.Fatalf("marshal identity: %v", err)
	}
	if err := os.WriteFile(identityFile, data, 0o644); err != nil {
		e.t.Fatalf("write identity file: %v", err)
	}

	// Register agent with name via RPC.
	client, err := cli.NewClient(e.socketPath)
	if err != nil {
		e.t.Fatalf("create client: %v", err)
	}
	defer client.Close()

	var regResp rpc.RegisterResponse
	if err := client.Call("agent.register", rpc.RegisterRequest{
		Role:   role,
		Module: module,
		Name:   name,
	}, &regResp); err != nil {
		e.t.Fatalf("register agent %s: %v", name, err)
	}

	// Start session so the agent can send/receive messages.
	var sessionResp rpc.SessionStartResponse
	if err := client.Call("session.start", rpc.SessionStartRequest{
		AgentID: regResp.AgentID,
	}, &sessionResp); err != nil {
		e.t.Fatalf("start session for %s: %v", name, err)
	}

	if regResp.AgentID != agentID {
		e.t.Logf("note: registered agent ID %q differs from expected %q", regResp.AgentID, agentID)
	}

	return regResp.AgentID
}

// newRoutingMCPServer creates an MCP server for the given agent (by setting env vars).
func (e *routingTestEnv) newRoutingMCPServer(name, role, module string) *Server {
	e.t.Helper()

	e.t.Setenv("THRUM_NAME", name)
	e.t.Setenv("THRUM_ROLE", role)
	e.t.Setenv("THRUM_MODULE", module)

	srv, err := NewServer(e.repoPath)
	if err != nil {
		e.t.Fatalf("NewServer for %s: %v", name, err)
	}
	return srv
}

// TestMCPRoutingParity verifies that check_messages correctly receives name-directed,
// role-directed, and broadcast messages — matching CLI inbox behavior.
//
//   - impl_api (role=implementer) should receive:
//     1. @implementer (role-based mention)
//     2. @impl_api (name-based mention)
//     3. @everyone (broadcast via group scope)
//
//   - coordinator (role=coordinator) should receive:
//     1. @everyone (broadcast via group scope) only
func TestMCPRoutingParity(t *testing.T) {
	env := newRoutingTestEnv(t)
	ctx := context.Background()

	// Register impl_api and coordinator agents.
	env.activateRoutingAgent("impl_api", "implementer", "backend")
	env.activateRoutingAgent("coordinator", "coordinator", "orchestration")

	// Register a sender agent to send the test messages.
	env.activateRoutingAgent("sender_agent", "sender", "test")

	// Activate sender and create its MCP server.
	senderMCP := env.newRoutingMCPServer("sender_agent", "sender", "test")

	// Message 1: role-based mention — to @implementer.
	// This should reach impl_api (ForAgentRole="implementer") but NOT coordinator.
	_, out1, err := senderMCP.handleSendMessage(ctx, nil, SendMessageInput{
		To:       "@implementer",
		Content:  "role-directed: hello implementer role",
		Priority: "normal",
	})
	if err != nil {
		t.Fatalf("send @implementer message: %v", err)
	}
	if out1.Status != "delivered" {
		t.Errorf("expected status 'delivered' for role-directed msg, got %q", out1.Status)
	}

	// Message 2: name-based mention — to @impl_api.
	// This should reach impl_api (ForAgent="impl_api") but NOT coordinator.
	_, out2, err := senderMCP.handleSendMessage(ctx, nil, SendMessageInput{
		To:       "@impl_api",
		Content:  "name-directed: hello impl_api",
		Priority: "normal",
	})
	if err != nil {
		t.Fatalf("send @impl_api message: %v", err)
	}
	if out2.Status != "delivered" {
		t.Errorf("expected status 'delivered' for name-directed msg, got %q", out2.Status)
	}

	// Message 3: broadcast via @everyone group.
	// This should reach BOTH impl_api and coordinator.
	_, out3, err := senderMCP.handleSendMessage(ctx, nil, SendMessageInput{
		To:       "@everyone",
		Content:  "broadcast: hello everyone",
		Priority: "normal",
	})
	if err != nil {
		t.Fatalf("send @everyone message: %v", err)
	}
	if out3.Status != "delivered" {
		t.Errorf("expected status 'delivered' for broadcast msg, got %q", out3.Status)
	}

	// --- Verify impl_api receives all 3 messages ---
	implMCP := env.newRoutingMCPServer("impl_api", "implementer", "backend")

	_, implOut, err := implMCP.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("check_messages for impl_api: %v", err)
	}

	if len(implOut.Messages) != 3 {
		t.Errorf("impl_api: expected 3 messages, got %d", len(implOut.Messages))
		for i, m := range implOut.Messages {
			t.Logf("  msg[%d]: %q", i, m.Content)
		}
	} else {
		// Verify the message contents.
		contents := make(map[string]bool, 3)
		for _, m := range implOut.Messages {
			contents[m.Content] = true
		}
		if !contents["role-directed: hello implementer role"] {
			t.Error("impl_api: missing role-directed message (@implementer)")
		}
		if !contents["name-directed: hello impl_api"] {
			t.Error("impl_api: missing name-directed message (@impl_api)")
		}
		if !contents["broadcast: hello everyone"] {
			t.Error("impl_api: missing broadcast message (@everyone)")
		}
	}

	// --- Verify coordinator receives only the broadcast (message 3) ---
	coordMCP := env.newRoutingMCPServer("coordinator", "coordinator", "orchestration")

	_, coordOut, err := coordMCP.handleCheckMessages(ctx, nil, CheckMessagesInput{})
	if err != nil {
		t.Fatalf("check_messages for coordinator: %v", err)
	}

	if len(coordOut.Messages) != 1 {
		t.Errorf("coordinator: expected 1 message (broadcast only), got %d", len(coordOut.Messages))
		for i, m := range coordOut.Messages {
			t.Logf("  msg[%d]: %q", i, m.Content)
		}
	} else {
		if coordOut.Messages[0].Content != "broadcast: hello everyone" {
			t.Errorf("coordinator: expected broadcast message, got %q", coordOut.Messages[0].Content)
		}
	}
}
