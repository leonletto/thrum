//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/state"
)

var rpcRequestID atomic.Int64

// startTestDaemon creates a temp .thrum directory, initializes state, and starts a daemon.
func startTestDaemon(t *testing.T) (string, string) {
	t.Helper()

	// Create temp repo with .thrum structure
	repoDir := t.TempDir()
	thrumDir := filepath.Join(repoDir, ".thrum")
	os.MkdirAll(filepath.Join(thrumDir, "events"), 0750)
	os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750)

	// Short socket path (macOS 108-char limit)
	sockDir, err := os.MkdirTemp("", "ts-pid-*")
	if err != nil {
		t.Fatalf("create sock dir: %v", err)
	}
	socketPath := filepath.Join(sockDir, "t.sock")
	t.Cleanup(func() { os.RemoveAll(sockDir) })

	// Initialize state and daemon
	st, err := state.NewState(thrumDir, thrumDir, "test-pid-identity", "")
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	server := daemon.NewServer(socketPath)

	// Register agent handler
	agentHandler := rpc.NewAgentHandler(st)
	server.RegisterHandler("agent.register", agentHandler.HandleRegister)
	server.RegisterHandler("agent.list", agentHandler.HandleList)
	server.RegisterHandler("agent.whoami", agentHandler.HandleWhoami)

	if err := server.Start(context.Background()); err != nil {
		st.Close()
		t.Fatalf("server start: %v", err)
	}

	// Wait for socket
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Cleanup(func() {
		server.Stop()
		st.Close()
	})

	return thrumDir, socketPath
}

// rpcCall makes a JSON-RPC call to the daemon.
func rpcCall(t *testing.T, socketPath, method string, params any, result any) {
	t.Helper()

	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	reqID := rpcRequestID.Add(1)
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  method,
		"params":  params,
	}

	if err := json.NewEncoder(conn).Encode(request); err != nil {
		t.Fatalf("send: %v", err)
	}

	var response struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatalf("recv: %v", err)
	}

	if response.Error != nil {
		t.Fatalf("RPC error %d: %s", response.Error.Code, response.Error.Message)
	}

	if result != nil {
		if err := json.Unmarshal(response.Result, result); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
	}
}

// rpcCallRaw makes a JSON-RPC call and returns raw result (doesn't fail on RPC error).
func rpcCallRaw(socketPath, method string, params any) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	reqID := rpcRequestID.Add(1)
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  method,
		"params":  params,
	}

	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	var response struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return nil, fmt.Errorf("recv: %w", err)
	}

	if response.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", response.Error.Code, response.Error.Message)
	}

	return response.Result, nil
}

func TestPIDIdentity_RegisterWithPID(t *testing.T) {
	_, socketPath := startTestDaemon(t)

	// Register agent with a specific claude_pid
	var regResult rpc.RegisterResponse
	rpcCall(t, socketPath, "agent.register", map[string]any{
		"name":       "test_agent",
		"role":       "implementer",
		"module":     "test",
		"claude_pid": os.Getpid(),
	}, &regResult)

	if regResult.Status != "registered" {
		t.Fatalf("expected status=registered, got %s", regResult.Status)
	}
	if regResult.AgentID == "" {
		t.Fatal("expected non-empty agent_id")
	}

	// List agents and verify claude_pid is stored
	var listResult rpc.ListAgentsResponse
	rpcCall(t, socketPath, "agent.list", map[string]any{}, &listResult)

	if len(listResult.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(listResult.Agents))
	}
	if listResult.Agents[0].AgentPID != os.Getpid() {
		t.Errorf("expected claude_pid=%d, got %d", os.Getpid(), listResult.Agents[0].AgentPID)
	}
}

func TestPIDIdentity_SamePIDIdempotent(t *testing.T) {
	_, socketPath := startTestDaemon(t)

	params := map[string]any{
		"name":       "test_agent",
		"role":       "implementer",
		"module":     "test",
		"claude_pid": os.Getpid(),
	}

	// Register twice with same PID
	var result1 rpc.RegisterResponse
	rpcCall(t, socketPath, "agent.register", params, &result1)
	if result1.Status != "registered" {
		t.Fatalf("first register: expected registered, got %s", result1.Status)
	}

	var result2 rpc.RegisterResponse
	rpcCall(t, socketPath, "agent.register", params, &result2)
	if result2.Status != "registered" {
		t.Fatalf("second register: expected registered, got %s", result2.Status)
	}
}

func TestPIDIdentity_ReRegisterUpdatesPID(t *testing.T) {
	_, socketPath := startTestDaemon(t)

	// Register with PID 111
	var result1 rpc.RegisterResponse
	rpcCall(t, socketPath, "agent.register", map[string]any{
		"name":       "test_agent",
		"role":       "implementer",
		"module":     "test",
		"claude_pid": 111,
	}, &result1)

	if result1.Status != "registered" {
		t.Fatalf("first register: expected registered, got %s", result1.Status)
	}

	// Re-register same name with different PID (simulating new session taking over)
	var result2 rpc.RegisterResponse
	rpcCall(t, socketPath, "agent.register", map[string]any{
		"name":        "test_agent",
		"role":        "implementer",
		"module":      "test",
		"claude_pid":  os.Getpid(),
		"re_register": true,
	}, &result2)

	if result2.Status != "updated" {
		t.Fatalf("re-register: expected updated, got %s", result2.Status)
	}

	// Verify PID was updated in the agent list
	var listResult rpc.ListAgentsResponse
	rpcCall(t, socketPath, "agent.list", map[string]any{}, &listResult)

	if len(listResult.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(listResult.Agents))
	}
	if listResult.Agents[0].AgentPID != os.Getpid() {
		t.Errorf("expected updated claude_pid=%d, got %d", os.Getpid(), listResult.Agents[0].AgentPID)
	}
}

func TestPIDIdentity_DeadPIDReclaim(t *testing.T) {
	_, socketPath := startTestDaemon(t)

	// Register with dead PID
	var result1 rpc.RegisterResponse
	rpcCall(t, socketPath, "agent.register", map[string]any{
		"name":       "dead_agent",
		"role":       "implementer",
		"module":     "test",
		"claude_pid": 999999, // dead PID
	}, &result1)

	if result1.Status != "registered" {
		t.Fatalf("first register: expected registered, got %s", result1.Status)
	}

	// Re-register same name with new PID and re_register flag — should succeed
	var result2 rpc.RegisterResponse
	rpcCall(t, socketPath, "agent.register", map[string]any{
		"name":        "dead_agent",
		"role":        "implementer",
		"module":      "test",
		"claude_pid":  os.Getpid(),
		"re_register": true,
	}, &result2)

	if result2.Status != "updated" {
		t.Fatalf("re-register: expected updated, got %s", result2.Status)
	}

	// Verify PID was updated
	var listResult rpc.ListAgentsResponse
	rpcCall(t, socketPath, "agent.list", map[string]any{}, &listResult)

	found := false
	for _, agent := range listResult.Agents {
		if agent.AgentID == result2.AgentID {
			if agent.AgentPID != os.Getpid() {
				t.Errorf("expected updated claude_pid=%d, got %d", os.Getpid(), agent.AgentPID)
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("re-registered agent not found in list")
	}
}

func TestPIDIdentity_WhoamiResolvesByCallerID(t *testing.T) {
	_, socketPath := startTestDaemon(t)

	// Register agent with PID
	var regResult rpc.RegisterResponse
	rpcCall(t, socketPath, "agent.register", map[string]any{
		"name":       "pid_agent",
		"role":       "implementer",
		"module":     "test",
		"claude_pid": os.Getpid(),
	}, &regResult)

	if regResult.Status != "registered" {
		t.Fatalf("register: expected registered, got %s", regResult.Status)
	}

	// Call whoami with the registered agent's ID
	var whoamiResult rpc.WhoamiResponse
	rpcCall(t, socketPath, "agent.whoami", map[string]any{
		"caller_agent_id": regResult.AgentID,
	}, &whoamiResult)

	if whoamiResult.AgentID != regResult.AgentID {
		t.Errorf("whoami agent_id = %s, want %s", whoamiResult.AgentID, regResult.AgentID)
	}
	if whoamiResult.Role != "implementer" {
		t.Errorf("whoami role = %s, want implementer", whoamiResult.Role)
	}
}

func TestPIDIdentity_RawConflictWithoutReRegister(t *testing.T) {
	_, socketPath := startTestDaemon(t)

	// Register first agent
	var result1 rpc.RegisterResponse
	rpcCall(t, socketPath, "agent.register", map[string]any{
		"name":       "agent_alpha",
		"role":       "implementer",
		"module":     "test",
		"claude_pid": os.Getpid(),
	}, &result1)

	if result1.Status != "registered" {
		t.Fatalf("first register: expected registered, got %s", result1.Status)
	}

	// Try to register different name with same role+module (no re_register, no force)
	// This should return a conflict since role+module is already taken
	raw, err := rpcCallRaw(socketPath, "agent.register", map[string]any{
		"name":       "agent_beta",
		"role":       "implementer",
		"module":     "test",
		"claude_pid": 999998,
	})

	if err != nil {
		// Conflict may come as RPC error
		t.Logf("Got conflict error (expected): %v", err)
		return
	}

	var result2 rpc.RegisterResponse
	if err := json.Unmarshal(raw, &result2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result2.Status != "conflict" {
		t.Fatalf("expected conflict, got %s", result2.Status)
	}
	if result2.Conflict == nil {
		t.Fatal("expected conflict info")
	}
	if result2.Conflict.ConflictPID != os.Getpid() {
		t.Errorf("ConflictPID = %d, want %d", result2.Conflict.ConflictPID, os.Getpid())
	}
}
