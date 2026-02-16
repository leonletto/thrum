//go:build resilience

package resilience

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/subscriptions"
)

const fixturePath = "testdata/thrum-fixture.tar.gz"

var rpcRequestID atomic.Int64
var sharedFixtureDir string

func TestMain(m *testing.M) {
	// Extract fixture once
	tmpDir, err := os.MkdirTemp("", "thrum-resilience-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp dir: %v\n", err)
		os.Exit(1)
	}
	if err := extractTarGz(fixturePath, tmpDir); err != nil {
		fmt.Fprintf(os.Stderr, "extract fixture: %v\n", err)
		os.Exit(1)
	}
	sharedFixtureDir = filepath.Join(tmpDir, ".thrum")

	code := m.Run()
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

// setupSharedFixture returns the shared fixture path. The fixture is read-only —
// tests MUST NOT modify files in the returned directory.
func setupSharedFixture(t testing.TB) string {
	t.Helper()
	if sharedFixtureDir == "" {
		t.Fatal("shared fixture not initialized (TestMain not run?)")
	}
	return sharedFixtureDir
}

// setupMutableFixture copies the shared fixture to a test-specific temp dir.
// Use this for tests that modify the DB, JSONL files, or other fixture data.
func setupMutableFixture(t *testing.T) string {
	t.Helper()

	if sharedFixtureDir == "" {
		t.Fatal("shared fixture not initialized (TestMain not run?)")
	}

	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")

	// Copy shared fixture to test-specific directory using cp -a (preserves permissions, fast)
	cpCmd := exec.Command("cp", "-a", sharedFixtureDir, thrumDir)
	if out, err := cpCmd.CombinedOutput(); err != nil {
		t.Fatalf("cp shared fixture: %v\n%s", err, out)
	}

	return thrumDir
}

// setupFixture is kept for compatibility — it creates a mutable copy.
// All tests that start a daemon modify the DB/JSONL files and need a mutable copy.
func setupFixture(t *testing.T) string {
	return setupMutableFixture(t)
}

// extractTarGz extracts a .tar.gz file to the destination directory.
func extractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dst, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
				return err
			}
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
		}
	}
	return nil
}

// TB is the common interface between *testing.T and *testing.B.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(f func())
	TempDir() string
}

// registerAllHandlers registers all RPC handlers on the server.
func registerAllHandlers(server *daemon.Server, st *state.State) {
	startTime := time.Now()
	healthHandler := rpc.NewHealthHandler(startTime, "test-resilience", "test-repo")
	server.RegisterHandler("health", healthHandler.Handle)

	agentHandler := rpc.NewAgentHandler(st)
	server.RegisterHandler("agent.register", agentHandler.HandleRegister)
	server.RegisterHandler("agent.list", agentHandler.HandleList)
	server.RegisterHandler("agent.whoami", agentHandler.HandleWhoami)
	server.RegisterHandler("agent.listContext", agentHandler.HandleListContext)
	server.RegisterHandler("agent.delete", agentHandler.HandleDelete)

	sessionHandler := rpc.NewSessionHandler(st)
	server.RegisterHandler("session.start", sessionHandler.HandleStart)
	server.RegisterHandler("session.end", sessionHandler.HandleEnd)
	server.RegisterHandler("session.list", sessionHandler.HandleList)
	server.RegisterHandler("session.heartbeat", sessionHandler.HandleHeartbeat)
	server.RegisterHandler("session.setIntent", sessionHandler.HandleSetIntent)
	server.RegisterHandler("session.setTask", sessionHandler.HandleSetTask)

	dispatcher := subscriptions.NewDispatcher(st.DB())
	messageHandler := rpc.NewMessageHandlerWithDispatcher(st, dispatcher)
	server.RegisterHandler("message.send", messageHandler.HandleSend)
	server.RegisterHandler("message.get", messageHandler.HandleGet)
	server.RegisterHandler("message.list", messageHandler.HandleList)
	server.RegisterHandler("message.delete", messageHandler.HandleDelete)
	server.RegisterHandler("message.edit", messageHandler.HandleEdit)
	server.RegisterHandler("message.markRead", messageHandler.HandleMarkRead)

	groupHandler := rpc.NewGroupHandler(st)
	server.RegisterHandler("group.create", groupHandler.HandleCreate)
	server.RegisterHandler("group.list", groupHandler.HandleList)
	server.RegisterHandler("group.info", groupHandler.HandleInfo)
	server.RegisterHandler("group.members", groupHandler.HandleMembers)
	server.RegisterHandler("group.member.add", groupHandler.HandleMemberAdd)

	subscriptionHandler := rpc.NewSubscriptionHandler(st)
	server.RegisterHandler("subscribe", subscriptionHandler.HandleSubscribe)
	server.RegisterHandler("unsubscribe", subscriptionHandler.HandleUnsubscribe)
	server.RegisterHandler("subscriptions.list", subscriptionHandler.HandleList)

	teamHandler := rpc.NewTeamHandler(st)
	server.RegisterHandler("team.list", teamHandler.HandleList)

	contextHandler := rpc.NewContextHandler(st)
	server.RegisterHandler("context.save", contextHandler.HandleSave)
	server.RegisterHandler("context.show", contextHandler.HandleShow)
	server.RegisterHandler("context.clear", contextHandler.HandleClear)
}

// shortSocketPath returns a short socket path under /tmp, avoiding the
// 104-char Unix socket path limit on macOS. Registers cleanup to remove the dir.
func shortSocketPath(t testing.TB) string {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "ts-*")
	if err != nil {
		t.Fatalf("create sock dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	return filepath.Join(sockDir, "t.sock")
}

// startDaemonAt starts a daemon on the given socket path. Returns state, server.
func startDaemonAt(t TB, thrumDir, socketPath string) (*state.State, *daemon.Server) {
	t.Helper()

	os.Remove(socketPath)

	st, err := state.NewState(thrumDir, thrumDir, "test-resilience")
	if err != nil {
		t.Fatalf("NewState failed: %v", err)
	}

	server := daemon.NewServer(socketPath)
	registerAllHandlers(server, st)

	if err := server.Start(context.Background()); err != nil {
		st.Close()
		t.Fatalf("Server start failed: %v", err)
	}

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

	return st, server
}

// startDaemonManual starts a daemon WITHOUT registering cleanup.
// Callers control the full lifecycle (must call server.Stop() and st.Close() themselves).
// A safety-net t.Cleanup for st.Close() IS registered since it's idempotent.
func startDaemonManual(t *testing.T, thrumDir, agentName string) (*state.State, *daemon.Server, string) {
	t.Helper()

	socketPath := shortSocketPath(t)

	st, err := state.NewState(thrumDir, thrumDir, agentName)
	if err != nil {
		t.Fatalf("NewState failed: %v", err)
	}

	// Register safety-net cleanup for state only (idempotent)
	t.Cleanup(func() { st.Close() })

	server := daemon.NewServer(socketPath)
	registerAllHandlers(server, st)

	if err := server.Start(context.Background()); err != nil {
		st.Close()
		t.Fatalf("Server start failed: %v", err)
	}

	// Wait for socket to be ready
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	return st, server, socketPath
}

// startTestDaemon starts a daemon against the fixture's thrumDir.
// Returns the state, server, and socket path. Registers cleanup.
func startTestDaemon(t TB, thrumDir string) (*state.State, *daemon.Server, string) {
	t.Helper()

	// Unix sockets have a 108-char path limit on macOS.
	// TempDir paths with test names can exceed this, so use a short /tmp path.
	sockDir, err := os.MkdirTemp("", "ts-*")
	if err != nil {
		t.Fatalf("create sock dir: %v", err)
	}
	socketPath := filepath.Join(sockDir, "t.sock")
	t.Cleanup(func() { os.RemoveAll(sockDir) })

	st, server := startDaemonAt(t, thrumDir, socketPath)
	return st, server, socketPath
}

// cliSocketPath stores the socket path for CLI tests, set by setupCLIFixture.
// runThrum passes this via THRUM_SOCKET env var.
var cliSocketPath string

// setupCLIFixture extracts the fixture, starts a daemon with a short socket path,
// and stores it for runThrum to pass via THRUM_SOCKET env var.
// Returns the repoDir (--repo value for CLI commands).
func setupCLIFixture(t *testing.T) string {
	t.Helper()

	thrumDir := setupFixture(t)
	repoDir := filepath.Dir(thrumDir)

	socketPath := shortSocketPath(t)
	startDaemonAt(t, thrumDir, socketPath)
	cliSocketPath = socketPath

	return repoDir
}

// rpcCall makes a JSON-RPC call to the daemon and returns the result.
func rpcCall(t *testing.T, socketPath, method string, params any, result any) {
	t.Helper()

	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("Failed to connect to daemon: %v", err)
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

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(request); err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	var response struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&response); err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	if response.Error != nil {
		t.Fatalf("RPC error %d: %s", response.Error.Code, response.Error.Message)
	}

	if result != nil {
		if err := json.Unmarshal(response.Result, result); err != nil {
			t.Fatalf("Failed to unmarshal result: %v", err)
		}
	}
}

// rpcCallRaw makes a JSON-RPC call and returns raw result and error (doesn't fail test).
func rpcCallRaw(socketPath, method string, params any) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(10 * time.Second))

	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      rpcRequestID.Add(1),
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
		return nil, fmt.Errorf("rpc error %d: %s", response.Error.Code, response.Error.Message)
	}

	return response.Result, nil
}

// fixtureRoles mirrors the role assignment in the generator.
var fixtureRoles = []string{"coordinator", "implementer", "reviewer", "planner", "tester"}

// fixtureAgentName returns the agent name for a given fixture index (0-49).
func fixtureAgentName(idx int) string {
	return fmt.Sprintf("%s_%04d", fixtureRoles[idx%len(fixtureRoles)], idx)
}

// jsonUnmarshal is a helper to unmarshal json.RawMessage.
func jsonUnmarshal(data json.RawMessage, v any) error {
	return json.Unmarshal(data, v)
}

// ensureSession starts a session for the given agent via RPC, returning the session ID.
// This ensures the agent has an active session for message.send calls.
func ensureSession(t *testing.T, socketPath, agentID string) string {
	t.Helper()
	var result struct {
		SessionID string `json:"session_id"`
	}
	rpcCall(t, socketPath, "session.start", map[string]any{
		"agent_id": agentID,
	}, &result)
	if result.SessionID == "" {
		t.Fatalf("ensureSession: empty session_id for agent %s", agentID)
	}
	return result.SessionID
}

// ensureSessionRaw starts a session via RPC without failing the test on error.
func ensureSessionRaw(socketPath, agentID string) error {
	_, err := rpcCallRaw(socketPath, "session.start", map[string]any{
		"agent_id": agentID,
	})
	return err
}
