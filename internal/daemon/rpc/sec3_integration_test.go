package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/stretchr/testify/require"
)

// fakeResolver is a peercred.Resolver that returns a pre-programmed result
// on every call. Used for testing the sec.3 dispatch-level enforcement
// without depending on the real kernel peercred path (already tested in
// internal/daemon/identity/peercred/resolver_test.go).
type fakeResolver struct {
	id  *peercred.ResolvedIdentity // if non-nil, Resolve returns (id, nil)
	err error                      // if non-nil, Resolve returns (nil, err)
}

func (f *fakeResolver) Resolve(_ net.Conn) (*peercred.ResolvedIdentity, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.id, nil
}

// sec3TestHarness spins up a real daemon.Server over a temp unix socket,
// wires a fake peercred resolver, inserts a synthetic agent row so
// resolveAgentAndSession can find an active session, and registers the
// message.send handler. Returns the socket path, the state (for DB
// inspection), and the caller agent ID.
type sec3TestHarness struct {
	socketPath string
	state      *state.State
	agentID    string
	sessionID  string
	server     *daemon.Server
	msgHandler *MessageHandler
	stopFn     func()
}

// registerDelete registers the message.delete handler on the harness server.
// Separated from the constructor so sec.3 tests (which only need send/list)
// don't have to care about delete wiring.
func (h *sec3TestHarness) registerDelete(t *testing.T) {
	t.Helper()
	h.server.RegisterHandler("message.delete", h.msgHandler.HandleDelete)
}

// setResolver rotates the server's identity resolver. Used by tests that
// need to simulate two different callers against the same state DB (e.g.
// "agent B attempts to delete agent A's message").
func (h *sec3TestHarness) setResolver(r peercred.Resolver) {
	h.server.SetIdentityResolver(r)
}

func newSec3Harness(t *testing.T, resolver peercred.Resolver, registerAgent bool) *sec3TestHarness {
	t.Helper()

	// macOS 104-char socket path limit; use /tmp prefix to stay short.
	tmpDir, err := os.MkdirTemp("", "sec3")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	socketPath := filepath.Join(tmpDir, "t.sock")

	st, err := state.NewState(tmpDir, tmpDir, "test-repo", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	const agentID = "impl_sec3_test"
	const sessionID = "ses_sec3_test_001"

	// Pre-register target agent so message.send recipient validation (if any)
	// and resolveAgentAndSession session lookup both succeed.
	if registerAgent {
		now := time.Now().UTC().Format(time.RFC3339)
		_, err = st.DB().ExecContext(context.Background(), `
			INSERT INTO agents (agent_id, kind, role, module, display, hostname, agent_pid, registered_at, last_seen_at)
			VALUES (?, 'implementer', 'implementer', 'sec3', ?, '', 0, ?, ?)
		`, agentID, "Sec3 Test Agent", now, now)
		require.NoError(t, err, "insert synthetic sec3 agent")

		_, err = st.DB().ExecContext(context.Background(), `
			INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
			VALUES (?, ?, ?, ?)
		`, sessionID, agentID, now, now)
		require.NoError(t, err, "insert synthetic sec3 session")
	}

	msgHandler := NewMessageHandler(st)

	server := daemon.NewServer(socketPath)
	server.SetIdentityResolver(resolver)
	server.RegisterHandler("message.send", msgHandler.HandleSend)
	server.RegisterHandler("message.list", msgHandler.HandleList)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, server.Start(ctx))
	waitForSocketReady(t, socketPath)

	stopFn := func() {
		cancel()
		_ = server.Stop()
	}
	t.Cleanup(stopFn)

	return &sec3TestHarness{
		socketPath: socketPath,
		state:      st,
		agentID:    agentID,
		sessionID:  sessionID,
		server:     server,
		msgHandler: msgHandler,
		stopFn:     stopFn,
	}
}

// sendRPC dials the harness socket and sends a single JSON-RPC request,
// returning the raw response envelope.
func (h *sec3TestHarness) sendRPC(t *testing.T, method string, params map[string]any) (*rpcResponse, error) {
	t.Helper()
	conn, err := net.Dial("unix", h.socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.Close() }()

	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      1,
	}
	reqJSON, _ := json.Marshal(req)
	reqJSON = append(reqJSON, '\n')
	if _, err := conn.Write(reqJSON); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	buf := make([]byte, 8192)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var resp rpcResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w (raw: %q)", err, string(buf[:n]))
	}
	return &resp, nil
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	ID json.RawMessage `json:"id"`
}

// TestSec3_ForgedCallerAgentID_Rejected proves that when peercred resolves
// the connecting process to identity X, a message.send request that claims
// caller_agent_id=Y is rejected with a clear "identity mismatch" error.
// This is the central forgery-defense test.
func TestSec3_ForgedCallerAgentID_Rejected(t *testing.T) {
	resolver := &fakeResolver{
		id: &peercred.ResolvedIdentity{
			AgentID:  "impl_sec3_test", // kernel says this is the real identity
			Worktree: "/tmp/fake-worktree",
			PID:      os.Getpid(),
		},
	}
	h := newSec3Harness(t, resolver, true /* registerAgent */)

	// Attempt to forge identity: claim to be a different agent.
	resp, err := h.sendRPC(t, "message.send", map[string]any{
		"caller_agent_id": "impersonator_attacker", // forged
		"to":              "impl_sec3_test",
		"content":    "forged message",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Error, "forged caller_agent_id must be rejected")
	require.Contains(t, resp.Error.Message, "identity mismatch",
		"error message should say identity mismatch")

	// Prove nothing got written — the DB should have zero messages.
	var count int
	row := h.state.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM messages`)
	require.NoError(t, row.Scan(&count))
	require.Equal(t, 0, count, "no message should have been persisted on forgery")
}

// TestSec3_OmittedCallerAgentID_ResolvedFromPeercred proves that when
// caller_agent_id is NOT supplied by the client, the daemon still resolves
// identity via peercred and the request proceeds successfully. This is the
// happy path for the new model: the CLI doesn't need to send caller_agent_id
// anymore.
func TestSec3_OmittedCallerAgentID_ResolvedFromPeercred(t *testing.T) {
	resolver := &fakeResolver{
		id: &peercred.ResolvedIdentity{
			AgentID:  "impl_sec3_test",
			Worktree: "/tmp/fake-worktree",
			PID:      os.Getpid(),
		},
	}
	h := newSec3Harness(t, resolver, true /* registerAgent */)

	resp, err := h.sendRPC(t, "message.send", map[string]any{
		// caller_agent_id deliberately omitted
		"to":           "impl_sec3_test",
		"content": "legit message via peercred",
	})
	require.NoError(t, err)
	require.Nil(t, resp.Error, "request with omitted caller_agent_id should succeed; got: %+v", resp.Error)

	// Verify the message was persisted under the peercred-resolved agent ID.
	var authorID, content string
	row := h.state.DB().QueryRowContext(context.Background(),
		`SELECT agent_id, body_content FROM messages LIMIT 1`)
	require.NoError(t, row.Scan(&authorID, &content))
	require.Equal(t, "impl_sec3_test", authorID,
		"persisted message should be authored by the peercred-resolved agent")
	require.Contains(t, content, "legit message via peercred")
}

// TestSec3_AnonymousCaller_MutatingRPC_Rejected proves that a caller whose
// peercred resolution returned ErrAnonymous (CWD outside any registered
// worktree) cannot invoke mutating RPCs — the dispatcher rejects at the
// allowlist layer before the handler even runs.
func TestSec3_AnonymousCaller_MutatingRPC_Rejected(t *testing.T) {
	resolver := &fakeResolver{
		err: peercred.ErrAnonymous, // peercred ran, no match
	}
	h := newSec3Harness(t, resolver, false /* registerAgent: N/A */)

	resp, err := h.sendRPC(t, "message.send", map[string]any{
		"caller_agent_id": "somebody",
		"to":              "anyone",
		"content":    "should be rejected",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Error, "anonymous mutating RPC must be rejected")
	require.Equal(t, -32002, resp.Error.Code,
		"should use the anonymous-rejection error code")
	require.True(t,
		strings.Contains(resp.Error.Message, "anonymous caller") ||
			strings.Contains(resp.Error.Message, "registered agent worktree"),
		"error should explain anonymous rejection, got: %q", resp.Error.Message)
}

// TestSec3_AnonymousCaller_ReadOnlyRPC_Allowed proves the flip side of the
// above — a caller with no resolved identity CAN invoke read-only RPCs on
// the allowlist. This preserves the `cd ~ && thrum team` workflow.
func TestSec3_AnonymousCaller_ReadOnlyRPC_Allowed(t *testing.T) {
	resolver := &fakeResolver{
		err: peercred.ErrAnonymous,
	}
	h := newSec3Harness(t, resolver, false)

	// message.list is on the allowlist. It should succeed even for anonymous.
	resp, err := h.sendRPC(t, "message.list", map[string]any{
		// No caller_agent_id. No filters that require identity.
	})
	require.NoError(t, err)
	require.Nil(t, resp.Error,
		"message.list should be allowed for anonymous caller; got: %+v", resp.Error)
}
