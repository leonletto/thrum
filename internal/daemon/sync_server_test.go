package daemon

import (
	"context"
	"encoding/json"
	"net"
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

	go r.ServeSyncRPC(ctx, server)

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

	go r.ServeSyncRPC(ctx, server)

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

	go r.ServeSyncRPC(ctx, server)

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
