package daemon

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
)

// anonResolver is a peercred.Resolver that always returns ErrAnonymous.
// Combined with the server's direct PIDFromConn extraction, this proves
// the PID is plumbed into handler context even when identity resolution
// misses — the exact contract guard checks rely on (Rule #4‴ works off
// the connecting PID, never a client-asserted agent_id).
type anonResolver struct{}

func (a *anonResolver) Resolve(_ net.Conn) (*peercred.ResolvedIdentity, error) {
	return nil, peercred.ErrAnonymous
}

func TestServer_ConnectingPIDPlumbedToHandlerContext(t *testing.T) {
	// Use short socket path (macOS 104-char limit rejects t.TempDir's nested path).
	tmpDir, err := os.MkdirTemp("", "srvpid")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	socketPath := filepath.Join(tmpDir, "t.sock")

	server := NewServer(socketPath)
	server.SetIdentityResolver(&anonResolver{})

	// Register a method to the anonymous allowlist via a read-only RPC
	// already present (daemon.status). That method isn't ours, so use a
	// custom handler that echoes the connecting PID back to the caller.
	// Anonymous callers can only invoke allowlisted methods, so we piggyback
	// on a method in anonymousAllowedMethods.
	var captured atomic.Int64
	server.RegisterHandler("daemon.status", func(ctx context.Context, _ json.RawMessage) (any, error) {
		pid, ok := peercred.ConnectingPIDFromContext(ctx)
		if ok {
			captured.Store(int64(pid))
		}
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

	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  "daemon.status",
		"params":  map[string]any{},
		"id":      1,
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
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("unmarshal: %v (raw: %q)", err, string(buf[:n]))
	}
	if resp.Error != nil {
		t.Fatalf("RPC error: %+v", resp.Error)
	}

	got := int(captured.Load())
	if got != os.Getpid() {
		t.Errorf("connecting PID in handler ctx = %d, want %d (test process PID)", got, os.Getpid())
	}
}
