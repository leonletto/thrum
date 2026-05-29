package cli

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// TestAgentListContextWithTimeout_HonorsDeadline is the thrum-5988 mitigation
// guard. `thrum prime` computes a cosmetic active-agent count via
// agent.listContext, whose daemon handler takes the global state.Lock; under
// fleet load the snapshot walker can hold that lock for seconds, so prime used
// to block the full 10s default RPC deadline and degrade silently. The fix
// runs that probe with a short caller-supplied deadline. This test drives the
// helper against a mock daemon that never answers listContext and asserts the
// call fails FAST (within a small multiple of the deadline) rather than hanging
// to the 10s default.
func TestAgentListContextWithTimeout_HonorsDeadline(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	// Handler reads the request but deliberately never writes a response —
	// modelling a daemon stalled on state.Lock. It blocks on the stop channel
	// so the connection stays open (forcing the client to hit its deadline
	// rather than getting an EOF).
	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		var request map[string]any
		_ = json.NewDecoder(conn).Decode(&request)
		<-daemon.stopChan // hold the connection open; never respond
	})

	<-daemon.Ready()

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	const timeout = 150 * time.Millisecond
	start := time.Now()
	_, callErr := AgentListContextWithTimeout(client, "", "", "", timeout)
	elapsed := time.Since(start)

	if callErr == nil {
		t.Fatal("expected a timeout error from a non-responsive daemon, got nil")
	}
	// Must return promptly after the deadline — well under the 10s default.
	// Generous ceiling absorbs scheduler jitter without admitting a 10s hang.
	if elapsed > 2*time.Second {
		t.Errorf("call took %v, want it to fail near the %v deadline (not the 10s default)", elapsed, timeout)
	}
}

// TestClient_SocketPath verifies the accessor that lets prime dial a dedicated
// short-lived connection for the best-effort active-count probe (so a slow or
// late listContext response cannot desync the primary connection's later RPCs).
func TestClient_SocketPath(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()
	daemon.start(t, func(conn net.Conn) { _ = conn.Close() })
	<-daemon.Ready()

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	if got := client.SocketPath(); got != socketPath {
		t.Errorf("SocketPath() = %q, want %q", got, socketPath)
	}

	// The accessor must support dialing a second independent connection.
	probe, err := NewClient(client.SocketPath())
	if err != nil {
		t.Fatalf("dial second connection via SocketPath(): %v", err)
	}
	_ = probe.Close()
}
