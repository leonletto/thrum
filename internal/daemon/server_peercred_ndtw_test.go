package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
)

// introspectionFailResolver reproduces the post-thrum-ndtw contract: when
// the resolver cannot introspect the connecting process (gopsutil.Cwd
// fails, tspeer.Get fails, …), it returns a plain error — NOT wrapped
// with ErrAnonymous. server.go must then leave ctxWithIdentity un-injected
// so the request takes the legacy client-asserted identity path instead of
// being rejected as anonymous.
type introspectionFailResolver struct{}

func (i *introspectionFailResolver) Resolve(_ net.Conn) (*peercred.ResolvedIdentity, error) {
	return nil, errors.New("peercred: cannot read CWD for PID 999: permission denied")
}

// TestHandleConnection_ResolverIntrospectFail_LegacyFallthrough verifies that
// a non-ErrAnonymous resolver error routes the request through legacy
// client-asserted identity handling (pre-v0.9.0 behavior), letting a
// non-allowlisted mutating RPC succeed.
//
// Regression guard for thrum-ndtw: an interactive shell where gopsutil.Cwd
// fails on its PID must NOT be rejected as anonymous — it must fall through
// to legacy instead. Paired with peercred.TestResolve_CWDFails_NotAnon which
// enforces the resolver-side half of the contract.
func TestHandleConnection_ResolverIntrospectFail_LegacyFallthrough(t *testing.T) {
	// Short tmp prefix to stay under macOS 104-char unix socket path limit.
	tmpDir, err := os.MkdirTemp("", "ndtw")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	socketPath := filepath.Join(tmpDir, "t.sock")

	server := NewServer(socketPath)
	server.SetIdentityResolver(&introspectionFailResolver{})

	// Non-allowlisted method. The anonymous allowlist would reject this if
	// the resolver's error were classified as "provably anonymous"; under
	// the legacy fallthrough the handler runs and returns success.
	server.RegisterHandler("test.mutating", func(_ context.Context, _ json.RawMessage) (any, error) {
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

	raw := sendRPC(t, conn, "test.mutating", 1)
	_, rpcErr := parseRPCResponse(t, raw)
	if rpcErr != nil {
		t.Fatalf("non-allowlisted method should succeed via legacy fallthrough, got error %d: %s", rpcErr.Code, rpcErr.Message)
	}
}
