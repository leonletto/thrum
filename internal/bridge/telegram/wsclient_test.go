package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// upgrader is a permissive WebSocket upgrader for tests.
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// makeTestServer creates a test HTTP server that upgrades to WebSocket and
// delegates each accepted connection to handler. The caller is responsible for
// cleaning up the server.
func makeTestServer(t *testing.T, handler func(conn *websocket.Conn)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		defer conn.Close()
		handler(conn)
	}))
	return srv
}

// httpToWS converts an http:// URL to ws://.
func httpToWS(u string) string {
	return strings.Replace(u, "http://", "ws://", 1)
}

// TestWSClientCall verifies that Call sends a valid JSON-RPC 2.0 request and
// returns the server's result.
func TestWSClientCall(t *testing.T) {
	t.Parallel()

	srv := makeTestServer(t, func(conn *websocket.Conn) {
		// Read one request and echo a response.
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Logf("server read: %v", err)
			return
		}

		var req map[string]any
		if err := json.Unmarshal(msg, &req); err != nil {
			t.Errorf("server unmarshal: %v", err)
			return
		}

		// Validate JSON-RPC 2.0 structure.
		if req["jsonrpc"] != "2.0" {
			t.Errorf("expected jsonrpc=2.0, got %v", req["jsonrpc"])
		}
		if req["method"] != "user.register" {
			t.Errorf("expected method=user.register, got %v", req["method"])
		}
		id := req["id"]
		if id == nil {
			t.Error("expected non-nil id")
			return
		}

		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  map[string]any{"ok": true},
		}
		if err := conn.WriteJSON(resp); err != nil {
			t.Logf("server write: %v", err)
		}
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, httpToWS(srv.URL))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	result, err := client.Call(ctx, "user.register", map[string]any{"name": "test"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got["ok"] != true {
		t.Errorf("expected ok=true, got %v", got["ok"])
	}
}

// TestWSClientNotifications verifies that server-push notifications (no id)
// are routed to the Notifications() channel.
func TestWSClientNotifications(t *testing.T) {
	t.Parallel()

	srv := makeTestServer(t, func(conn *websocket.Conn) {
		// Wait briefly then push a notification.
		time.Sleep(20 * time.Millisecond)
		notif := map[string]any{
			"jsonrpc": "2.0",
			"method":  "notification.message",
			"params": map[string]any{
				"message_id": "msg-42",
				"preview":    "hello",
			},
		}
		if err := conn.WriteJSON(notif); err != nil {
			t.Logf("server write notification: %v", err)
		}
		// Keep connection open until test finishes.
		time.Sleep(2 * time.Second)
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, httpToWS(srv.URL))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	select {
	case n := <-client.Notifications():
		if n.Method != "notification.message" {
			t.Errorf("expected method=notification.message, got %q", n.Method)
		}
		var params map[string]any
		if err := json.Unmarshal(n.Params, &params); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if params["message_id"] != "msg-42" {
			t.Errorf("expected message_id=msg-42, got %v", params["message_id"])
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for notification")
	}
}

// TestWSClientPingPong verifies that the pong handler is configured: the
// server sends a ping and the connection remains alive (no timeout).
func TestWSClientPingPong(t *testing.T) {
	t.Parallel()

	pingReceived := make(chan struct{})

	srv := makeTestServer(t, func(conn *websocket.Conn) {
		// Register a ping handler on the server side to detect the pong response.
		conn.SetPongHandler(func(string) error {
			select {
			case pingReceived <- struct{}{}:
			default:
			}
			return nil
		})

		// Send a ping from the server.
		if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
			t.Logf("server ping: %v", err)
			return
		}

		// Drain messages until pong arrives or timeout.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, httpToWS(srv.URL))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	// The gorilla library handles pings automatically by replying with pong.
	// We just verify the client doesn't crash and the pong was sent.
	select {
	case <-pingReceived:
		// Pong was received by server — pong handler is set up correctly.
	case <-time.After(3 * time.Second):
		t.Error("server did not receive pong within 3s")
	}
}

// TestWSClientClose verifies that Close() is idempotent (safe to call many times).
func TestWSClientClose(t *testing.T) {
	t.Parallel()

	srv := makeTestServer(t, func(conn *websocket.Conn) {
		// Keep the server connection open.
		time.Sleep(2 * time.Second)
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, httpToWS(srv.URL))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Close multiple times — must not panic or block.
	for i := 0; i < 5; i++ {
		client.Close()
	}
}

// TestWSClientLoopbackValidation verifies that non-loopback URLs are rejected.
func TestWSClientLoopbackValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "loopback 127.0.0.1",
			url:     "ws://127.0.0.1:9999/ws",
			wantErr: false,
		},
		{
			name:    "loopback IPv6",
			url:     "ws://[::1]:9999/ws",
			wantErr: false,
		},
		{
			name:    "localhost",
			url:     "ws://localhost:9999/ws",
			wantErr: false,
		},
		{
			name:    "external IP",
			url:     "ws://8.8.8.8:9999/ws",
			wantErr: true,
		},
		{
			name:    "external hostname",
			url:     "ws://example.com:9999/ws",
			wantErr: true,
		},
		{
			name:    "private network IP",
			url:     "ws://192.168.1.1:9999/ws",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateLoopback(tc.url)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for URL %q, got nil", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error for URL %q, got %v", tc.url, err)
			}
		})
	}
}

// TestWSClientCallRPCError verifies that a JSON-RPC error response is returned
// as a Go error from Call().
func TestWSClientCallRPCError(t *testing.T) {
	t.Parallel()

	srv := makeTestServer(t, func(conn *websocket.Conn) {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var req map[string]any
		if err := json.Unmarshal(msg, &req); err != nil {
			return
		}
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"error": map[string]any{
				"code":    -32600,
				"message": "Invalid request",
			},
		}
		_ = conn.WriteJSON(resp)
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, httpToWS(srv.URL))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	_, err = client.Call(ctx, "bad.method", nil)
	if err == nil {
		t.Fatal("expected error from RPC error response, got nil")
	}
	if !strings.Contains(err.Error(), "Invalid request") {
		t.Errorf("expected 'Invalid request' in error, got %q", err.Error())
	}
}

// TestWSClientCallContextCancellation verifies that Call returns when context
// is cancelled while waiting for a response.
func TestWSClientCallContextCancellation(t *testing.T) {
	t.Parallel()

	srv := makeTestServer(t, func(conn *websocket.Conn) {
		// Server receives the request but never responds.
		_, _, _ = conn.ReadMessage()
		time.Sleep(5 * time.Second)
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, httpToWS(srv.URL))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	callCtx, callCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer callCancel()

	_, err = client.Call(callCtx, "session.start", nil)
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
}
