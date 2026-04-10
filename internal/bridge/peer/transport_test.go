package peer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leonletto/thrum/internal/bridge"
	"github.com/leonletto/thrum/internal/bridge/peer"
)

func newTestWSServer(t *testing.T, handler func(*websocket.Conn, *http.Request)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		handler(conn, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func echoHandler(conn *websocket.Conn, _ *http.Request) {
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var req map[string]any
		json.Unmarshal(msg, &req)
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result":  map[string]any{"echo": req["method"]},
		}
		data, _ := json.Marshal(resp)
		conn.WriteMessage(websocket.TextMessage, data)
	}
}

func TestPeerTransport_ConnectAndCall(t *testing.T) {
	t.Parallel()
	srv := newTestWSServer(t, echoHandler)

	u, _ := url.Parse(srv.URL)
	transport := peer.NewPeerTransport("test-peer", u.Host, "secret-token")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect() = %v", err)
	}
	defer transport.Close()

	if !transport.Connected() {
		t.Fatal("should be connected after Connect()")
	}

	result, err := transport.Call(ctx, "test.echo", map[string]any{"key": "val"})
	if err != nil {
		t.Fatalf("Call() = %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if parsed["echo"] != "test.echo" {
		t.Fatalf("echo = %v, want test.echo", parsed["echo"])
	}
}

func TestPeerTransport_TokenInAuthHeader(t *testing.T) {
	t.Parallel()

	authCh := make(chan string, 1)
	queryCh := make(chan string, 1)
	srv := newTestWSServer(t, func(conn *websocket.Conn, r *http.Request) {
		authCh <- r.Header.Get("Authorization")
		queryCh <- r.URL.Query().Get("token")
		echoHandler(conn, r)
	})

	u, _ := url.Parse(srv.URL)
	transport := peer.NewPeerTransport("token-peer", u.Host, "my-secret-token")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect() = %v", err)
	}
	defer transport.Close()

	select {
	case auth := <-authCh:
		want := "Bearer my-secret-token"
		if auth != want {
			t.Fatalf("Authorization = %q, want %q", auth, want)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for auth header capture")
	}

	// Token MUST NOT appear in the URL — that is the whole point of this
	// refactor. URL query strings leak into access logs, proxies, and history.
	if q := <-queryCh; q != "" {
		t.Fatalf("token leaked into URL query: %q", q)
	}
}

func TestPeerTransport_LocalPortDiscovery(t *testing.T) {
	t.Parallel()
	srv := newTestWSServer(t, echoHandler)

	// Extract port from test server URL.
	u, _ := url.Parse(srv.URL)
	port := u.Port()

	// Create temp dir with .thrum/var/ws.port file.
	tmpDir := t.TempDir()
	varDir := filepath.Join(tmpDir, ".thrum", "var")
	if err := os.MkdirAll(varDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	portFile := filepath.Join(varDir, "ws.port")
	if err := os.WriteFile(portFile, []byte(fmt.Sprintf("%s\n", port)), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	transport := peer.NewLocalPeerTransport("local-peer", tmpDir, "local-token")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := transport.Connect(ctx); err != nil {
		t.Fatalf("Connect() = %v", err)
	}
	defer transport.Close()

	result, err := transport.Call(ctx, "local.test", nil)
	if err != nil {
		t.Fatalf("Call() = %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if parsed["echo"] != "local.test" {
		t.Fatalf("echo = %v, want local.test", parsed["echo"])
	}
}

func TestPeerTransport_LocalPortDiscovery_MissingFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	transport := peer.NewLocalPeerTransport("local-peer", tmpDir, "token")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := transport.Connect(ctx)
	if err == nil {
		t.Fatal("expected error when port file missing")
	}
	if !strings.Contains(err.Error(), "read port file") {
		t.Errorf("error = %q, want 'read port file' in message", err.Error())
	}
}

func TestPeerTransport_ImplementsTransportBridge(t *testing.T) {
	var _ bridge.TransportBridge = (*peer.PeerTransport)(nil)
}

func TestPeerTransport_NotConnected(t *testing.T) {
	t.Parallel()
	transport := peer.NewPeerTransport("not-connected", "127.0.0.1:9999", "token")

	_, err := transport.Call(context.Background(), "any.method", nil)
	if err == nil {
		t.Fatal("expected error when calling without connecting")
	}
	if !strings.Contains(err.Error(), "not connected") {
		t.Errorf("error = %q, want 'not connected' in message", err.Error())
	}
}

func TestPeerTransport_NotConnected_Notifications(t *testing.T) {
	t.Parallel()
	transport := peer.NewPeerTransport("not-connected", "127.0.0.1:9999", "token")

	ch := transport.Notifications()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel from Notifications() when not connected")
		}
		// closed channel — correct
	default:
		t.Fatal("expected closed channel to be immediately readable")
	}
}

func TestPeerTransport_PeerName(t *testing.T) {
	t.Parallel()
	transport := peer.NewPeerTransport("my-peer", "127.0.0.1:9999", "token")
	if transport.PeerName() != "my-peer" {
		t.Fatalf("PeerName() = %q, want my-peer", transport.PeerName())
	}

	local := peer.NewLocalPeerTransport("local-name", "/some/path", "token")
	if local.PeerName() != "local-name" {
		t.Fatalf("PeerName() = %q, want local-name", local.PeerName())
	}
}
