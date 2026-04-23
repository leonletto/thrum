package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
)

// gatedResolver is a peercred.Resolver whose ListAgentWorktrees output is
// controlled by a boolean flag. When the gate is closed (false), it returns
// an empty list, making every caller anonymous. When open (true), it returns
// a single worktree matching the current test process's CWD, so Resolve
// succeeds for any connection from this PID.
type gatedResolver struct {
	mu       sync.Mutex
	open     bool
	worktree string
	agentID  string
}

func newGatedResolver(agentID, worktree string) *gatedResolver {
	return &gatedResolver{agentID: agentID, worktree: worktree}
}

func (g *gatedResolver) setOpen(v bool) {
	g.mu.Lock()
	g.open = v
	g.mu.Unlock()
}

// Resolve re-implements the Resolver interface using the gated lister. We call
// the real resolver indirectly by building one on-the-fly so that the actual
// peercred PID→CWD lookup is exercised (not mocked). When the gate is closed
// the lister returns an empty slice, so Resolve returns ErrAnonymous.
func (g *gatedResolver) Resolve(conn net.Conn) (*peercred.ResolvedIdentity, error) {
	g.mu.Lock()
	open := g.open
	wt := g.worktree
	id := g.agentID
	g.mu.Unlock()

	if !open {
		return nil, peercred.ErrAnonymous
	}

	// Build a minimal lister and let the real resolver walk PID→CWD.
	lister := &staticLister{entries: []peercred.AgentWorktree{{AgentID: id, Worktree: wt}}}
	r := peercred.NewResolver(lister)
	return r.Resolve(conn)
}

type staticLister struct {
	entries []peercred.AgentWorktree
}

func (s *staticLister) ListAgentWorktrees() ([]peercred.AgentWorktree, error) {
	return s.entries, nil
}

// sendRPC marshals and sends a JSON-RPC 2.0 request over conn, then reads one
// newline-delimited response. Returns the raw response bytes.
func sendRPC(t *testing.T, conn net.Conn, method string, id int) json.RawMessage {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  map[string]any{},
		"id":      id,
	}
	body, _ := json.Marshal(req)
	body = append(body, '\n')
	if _, err := conn.Write(body); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return buf[:n]
}

// parseRPCResponse decodes a raw JSON-RPC response into result + error.
func parseRPCResponse(t *testing.T, raw json.RawMessage) (result json.RawMessage, rpcErr *struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}) {
	t.Helper()
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v (raw: %q)", err, string(raw))
	}
	return resp.Result, resp.Error
}

// TestHandleConnection_ResolvesPeercredPerRPC verifies that a connection which
// was initially anonymous (lister empty at connect time) succeeds on a
// subsequent RPC after the lister is populated — proving that identity
// resolution happens per-RPC, not once per connection.
func TestHandleConnection_ResolvesPeercredPerRPC(t *testing.T) {
	// Determine the worktree path that will match this test process.
	// peercred resolves PID→CWD→git-root. For the test to pass the gate-open
	// case, we need a worktree that matches our CWD's git root.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// Walk upward to find the git root (same logic as resolver.go).
	gitRoot := findTestGitRoot(cwd)
	if gitRoot == "" {
		// If we can't find a git root, register the CWD itself.
		gitRoot = cwd
	}

	tmpDir, err := os.MkdirTemp("", "perrpc")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	socketPath := filepath.Join(tmpDir, "t.sock")

	resolver := newGatedResolver("test-agent", gitRoot)

	server := NewServer(socketPath)
	server.SetIdentityResolver(resolver)

	// Register a non-allowlisted method; only an identified caller may invoke it.
	server.RegisterHandler("test.echo", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})

	if err := server.Start(t.Context()); err != nil {
		t.Fatalf("server start: %v", err)
	}
	defer func() { _ = server.Stop() }()

	waitForSocketReady(t, socketPath)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// --- Step 1: gate closed — caller is anonymous, non-allowlisted method must be rejected.
	raw := sendRPC(t, conn, "test.echo", 1)
	_, rpcErr := parseRPCResponse(t, raw)
	if rpcErr == nil {
		t.Fatal("expected RPC error for anonymous caller on non-allowlisted method, got success")
	}
	if rpcErr.Code != -32002 {
		t.Errorf("expected error code -32002, got %d (%s)", rpcErr.Code, rpcErr.Message)
	}

	// --- Step 2: open the gate (simulate agent registration completing).
	resolver.setOpen(true)

	// --- Step 3: same connection, second call — must now succeed.
	raw = sendRPC(t, conn, "test.echo", 2)
	result, rpcErr2 := parseRPCResponse(t, raw)
	if rpcErr2 != nil {
		t.Fatalf("expected success after gate opened, got error %d: %s", rpcErr2.Code, rpcErr2.Message)
	}
	if string(result) == "" || string(result) == "null" {
		t.Errorf("expected non-empty result, got %q", string(result))
	}
}

// TestHandleConnection_AnonymousAllowlistStillWorks confirms that anonymous
// callers (lister always empty) can still invoke allowlisted methods on the
// same connection after peercred is enabled.
func TestHandleConnection_AnonymousAllowlistStillWorks(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "anonallow")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	socketPath := filepath.Join(tmpDir, "t.sock")

	// Always-anonymous resolver (no registered agents).
	server := NewServer(socketPath)
	server.SetIdentityResolver(&anonResolver{})

	// team.list is in anonymousAllowedMethods.
	server.RegisterHandler("team.list", func(_ context.Context, _ json.RawMessage) (any, error) {
		return []string{}, nil
	})

	if err := server.Start(t.Context()); err != nil {
		t.Fatalf("server start: %v", err)
	}
	defer func() { _ = server.Stop() }()

	waitForSocketReady(t, socketPath)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// First call on the connection.
	raw := sendRPC(t, conn, "team.list", 1)
	_, rpcErr := parseRPCResponse(t, raw)
	if rpcErr != nil {
		t.Fatalf("allowlisted method should succeed for anonymous caller, got error %d: %s", rpcErr.Code, rpcErr.Message)
	}

	// Second call on the SAME connection — must still succeed.
	raw = sendRPC(t, conn, "team.list", 2)
	_, rpcErr2 := parseRPCResponse(t, raw)
	if rpcErr2 != nil {
		t.Fatalf("second call: allowlisted method should succeed for anonymous caller, got error %d: %s", rpcErr2.Code, rpcErr2.Message)
	}
}

// findTestGitRoot walks dir upward to find the nearest directory containing a
// ".git" entry. Mirrors the private findGitRoot in the peercred package.
func findTestGitRoot(dir string) string {
	prev := ""
	for dir != "" && dir != prev {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		prev = dir
		dir = filepath.Dir(dir)
	}
	return ""
}
