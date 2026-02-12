package daemon

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

func TestSyncRegistry_RegisterAllowed(t *testing.T) {
	r := NewSyncRegistry()

	handler := func(ctx context.Context, params json.RawMessage) (any, error) {
		return "ok", nil
	}

	for _, method := range []string{"sync.pull", "sync.peer_info", "sync.notify"} {
		if err := r.Register(method, handler); err != nil {
			t.Errorf("Register(%q) should succeed: %v", method, err)
		}
	}
}

func TestSyncRegistry_RegisterRejected(t *testing.T) {
	r := NewSyncRegistry()

	handler := func(ctx context.Context, params json.RawMessage) (any, error) {
		return "ok", nil
	}

	// Application RPCs must be rejected
	for _, method := range []string{"message.send", "agent.list", "health", "message.read"} {
		if err := r.Register(method, handler); err == nil {
			t.Errorf("Register(%q) should fail for non-sync method", method)
		}
	}
}

func TestSyncRegistry_ServeSyncRPC_SyncMethod(t *testing.T) {
	r := NewSyncRegistry()
	_ = r.Register("sync.peer_info", func(ctx context.Context, params json.RawMessage) (any, error) {
		return map[string]string{"daemon_id": "test-daemon"}, nil
	})

	// Create a pipe to simulate a connection
	client, server := net.Pipe()
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go r.ServeSyncRPC(ctx, server, "test-peer")

	// Send sync.peer_info request
	req := `{"jsonrpc":"2.0","method":"sync.peer_info","id":1}` + "\n"
	if _, err := client.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read response
	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp jsonRPCResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Error != nil {
		t.Errorf("expected success, got error: %s", resp.Error.Message)
	}
}

func TestSyncRegistry_ServeSyncRPC_RejectsAppRPC(t *testing.T) {
	r := NewSyncRegistry()

	client, server := net.Pipe()
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go r.ServeSyncRPC(ctx, server, "test-peer")

	// Try calling message.send — must be rejected
	req := `{"jsonrpc":"2.0","method":"message.send","params":{},"id":1}` + "\n"
	if _, err := client.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp jsonRPCResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Error == nil {
		t.Error("expected error for message.send, got success")
	}
	if resp.Error != nil && resp.Error.Code != -32601 {
		t.Errorf("expected code -32601, got %d", resp.Error.Code)
	}
}

func TestSyncRegistry_ServeSyncRPC_RejectsNonWhitelisted(t *testing.T) {
	r := NewSyncRegistry()

	client, server := net.Pipe()
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go r.ServeSyncRPC(ctx, server, "test-peer")

	// Try calling sync.unknown — not in whitelist
	req := `{"jsonrpc":"2.0","method":"sync.unknown","id":1}` + "\n"
	if _, err := client.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp jsonRPCResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Error == nil {
		t.Error("expected error for sync.unknown")
	}
}

// newTestPeerRegistry creates a PeerRegistry with a test peer for token auth tests.
func newTestPeerRegistry(t *testing.T) *PeerRegistry {
	t.Helper()
	reg, err := NewPeerRegistry(t.TempDir() + "/peers.json")
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	_ = reg.AddPeer(&PeerInfo{
		DaemonID: "d_test_peer",
		Name:     "test-peer",
		Address:  "127.0.0.1:9999",
		Token:    "valid-token-abc123",
	})
	return reg
}

// sendSyncRPC sends a JSON-RPC request and reads the response on a pipe.
func sendSyncRPC(t *testing.T, conn net.Conn, method string, params any) jsonRPCResponse {
	t.Helper()

	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"id":      1,
	}
	if params != nil {
		req["params"] = params
	}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp jsonRPCResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

func TestSyncRegistry_TokenAuth_ValidToken(t *testing.T) {
	peers := newTestPeerRegistry(t)

	r := NewSyncRegistry()
	r.SetPeerRegistry(peers)
	_ = r.Register("sync.peer_info", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{"status": "ok"}, nil
	})

	client, server := net.Pipe()
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.ServeSyncRPC(ctx, server, "test-peer")

	resp := sendSyncRPC(t, client, "sync.peer_info", map[string]string{"token": "valid-token-abc123"})

	if resp.Error != nil {
		t.Errorf("expected success with valid token, got error: %s", resp.Error.Message)
	}
}

func TestSyncRegistry_TokenAuth_InvalidToken(t *testing.T) {
	peers := newTestPeerRegistry(t)

	r := NewSyncRegistry()
	r.SetPeerRegistry(peers)
	_ = r.Register("sync.peer_info", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{"status": "ok"}, nil
	})

	client, server := net.Pipe()
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.ServeSyncRPC(ctx, server, "test-peer")

	resp := sendSyncRPC(t, client, "sync.peer_info", map[string]string{"token": "wrong-token"})

	if resp.Error == nil {
		t.Fatal("expected error for invalid token, got success")
	}
	if !strings.Contains(resp.Error.Message, "unauthorized") {
		t.Errorf("error message = %q, want something containing 'unauthorized'", resp.Error.Message)
	}
}

func TestSyncRegistry_TokenAuth_MissingToken(t *testing.T) {
	peers := newTestPeerRegistry(t)

	r := NewSyncRegistry()
	r.SetPeerRegistry(peers)
	_ = r.Register("sync.pull", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{"status": "ok"}, nil
	})

	client, server := net.Pipe()
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.ServeSyncRPC(ctx, server, "test-peer")

	// No token in params
	resp := sendSyncRPC(t, client, "sync.pull", map[string]any{"after_sequence": 0})

	if resp.Error == nil {
		t.Fatal("expected error for missing token, got success")
	}
	if !strings.Contains(resp.Error.Message, "unauthorized") {
		t.Errorf("error message = %q, want something containing 'unauthorized'", resp.Error.Message)
	}
}

func TestSyncRegistry_TokenAuth_PairRequestExempt(t *testing.T) {
	peers := newTestPeerRegistry(t)

	r := NewSyncRegistry()
	r.SetPeerRegistry(peers)
	_ = r.Register("pair.request", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{"status": "ok"}, nil
	})

	client, server := net.Pipe()
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.ServeSyncRPC(ctx, server, "test-peer")

	// No token — pair.request should still succeed
	resp := sendSyncRPC(t, client, "pair.request", map[string]string{"code": "1234"})

	if resp.Error != nil {
		t.Errorf("pair.request should not require token auth, got error: %s", resp.Error.Message)
	}
}

func TestSyncRegistry_TokenAuth_NoRegistryMeansNoAuth(t *testing.T) {
	// Without SetPeerRegistry, auth is disabled (backward compat)
	r := NewSyncRegistry()
	_ = r.Register("sync.pull", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]string{"status": "ok"}, nil
	})

	client, server := net.Pipe()
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.ServeSyncRPC(ctx, server, "test-peer")

	// No token, no peer registry — should succeed
	resp := sendSyncRPC(t, client, "sync.pull", map[string]any{"after_sequence": 0})

	if resp.Error != nil {
		t.Errorf("expected success without peer registry, got error: %s", resp.Error.Message)
	}
}
