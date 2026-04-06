package bridge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leonletto/thrum/internal/bridge"
)

func newTestWSServer(t *testing.T, handler func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		handler(conn)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWSClient_DialCallClose(t *testing.T) {
	t.Parallel()
	srv := newTestWSServer(t, func(conn *websocket.Conn) {
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
	})

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	client := bridge.NewWSClient(url)
	ctx := context.Background()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() = %v", err)
	}
	defer client.Close()

	if !client.Connected() {
		t.Fatal("should be connected after Connect()")
	}

	result, err := client.Call(ctx, "test.echo", map[string]any{"key": "val"})
	if err != nil {
		t.Fatalf("Call() = %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(result, &parsed)
	if parsed["echo"] != "test.echo" {
		t.Fatalf("echo = %v, want test.echo", parsed["echo"])
	}
}

func TestWSClient_Notifications(t *testing.T) {
	t.Parallel()
	srv := newTestWSServer(t, func(conn *websocket.Conn) {
		time.Sleep(20 * time.Millisecond)
		notif := map[string]any{
			"jsonrpc": "2.0",
			"method":  "notification.message",
			"params":  map[string]any{"body": "hello"},
		}
		data, _ := json.Marshal(notif)
		conn.WriteMessage(websocket.TextMessage, data)
		time.Sleep(time.Second)
	})

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	client := bridge.NewWSClient(url)

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() = %v", err)
	}
	defer client.Close()

	select {
	case n := <-client.Notifications():
		if n.Method != "notification.message" {
			t.Fatalf("Method = %q, want notification.message", n.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}

func TestWSClient_ImplementsTransportBridge(t *testing.T) {
	var _ bridge.TransportBridge = (*bridge.WSClient)(nil)
}

func TestWSClient_AddressValidator(t *testing.T) {
	t.Parallel()
	rejectAll := func(addr string) error {
		return bridge.ErrAddressRejected
	}
	client := bridge.NewWSClient("ws://example.com/ws", bridge.WithAddressValidator(rejectAll))
	err := client.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error from address validator")
	}
}

func TestWSClient_LoopbackValidator(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{"loopback IPv4", "ws://127.0.0.1:9999/ws", false},
		{"loopback IPv6", "ws://[::1]:9999/ws", false},
		{"localhost", "ws://localhost:9999/ws", false},
		{"external IP", "ws://8.8.8.8:9999/ws", true},
		{"external hostname", "ws://example.com:9999/ws", true},
		{"private network", "ws://192.168.1.1:9999/ws", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := bridge.LoopbackValidator(tc.addr)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for %q, got nil", tc.addr)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error for %q, got %v", tc.addr, err)
			}
		})
	}
}

func TestWSClient_PeerName(t *testing.T) {
	t.Parallel()
	// Default peer name from URL host
	c := bridge.NewWSClient("ws://localhost:8080/ws")
	if c.PeerName() != "localhost:8080" {
		t.Fatalf("PeerName() = %q, want localhost:8080", c.PeerName())
	}

	// Custom peer name
	c2 := bridge.NewWSClient("ws://localhost:8080/ws", bridge.WithPeerName("my-peer"))
	if c2.PeerName() != "my-peer" {
		t.Fatalf("PeerName() = %q, want my-peer", c2.PeerName())
	}
}

func TestWSClient_RPCError(t *testing.T) {
	t.Parallel()
	srv := newTestWSServer(t, func(conn *websocket.Conn) {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var req map[string]any
		json.Unmarshal(msg, &req)
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"error": map[string]any{
				"code":    -32600,
				"message": "Invalid request",
			},
		}
		data, _ := json.Marshal(resp)
		conn.WriteMessage(websocket.TextMessage, data)
	})

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	client := bridge.NewWSClient(url)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() = %v", err)
	}
	defer client.Close()

	_, err := client.Call(ctx, "bad.method", nil)
	if err == nil {
		t.Fatal("expected error from RPC error response")
	}
	if !strings.Contains(err.Error(), "Invalid request") {
		t.Errorf("expected 'Invalid request' in error, got %q", err.Error())
	}
}

func TestWSClient_CloseIdempotent(t *testing.T) {
	t.Parallel()
	srv := newTestWSServer(t, func(conn *websocket.Conn) {
		time.Sleep(2 * time.Second)
	})

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	client := bridge.NewWSClient(url)
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() = %v", err)
	}

	// Close multiple times — must not panic.
	for range 5 {
		client.Close()
	}

	if client.Connected() {
		t.Fatal("should not be connected after Close()")
	}
}
