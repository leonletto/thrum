package peer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leonletto/thrum/internal/bridge/peer"
)

// wsUpgrader is a shared upgrader for test servers.
var wsUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

// recordingServer is a test WebSocket server that records all called methods
// and returns pre-configured responses.
type recordingServer struct {
	mu      sync.Mutex
	methods []string
}

func (rs *recordingServer) record(method string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.methods = append(rs.methods, method)
}

func (rs *recordingServer) calledMethods() []string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]string, len(rs.methods))
	copy(out, rs.methods)
	return out
}

func (rs *recordingServer) wasCalled(method string) bool {
	for _, m := range rs.calledMethods() {
		if m == method {
			return true
		}
	}
	return false
}

// buildLocalServer creates a test WS server that mimics the local Thrum daemon.
// It records RPC calls and returns appropriate stub responses.
func buildLocalServer(t *testing.T, rs *recordingServer) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("local server upgrade: %v", err)
			return
		}
		defer conn.Close()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var req struct {
				JSONRPC string         `json:"jsonrpc"`
				ID      *int64         `json:"id"`
				Method  string         `json:"method"`
				Params  map[string]any `json:"params"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}

			rs.record(req.Method)

			var result json.RawMessage
			switch req.Method {
			case "user.register":
				result = json.RawMessage(`{"ok":true}`)
			case "session.start":
				result = json.RawMessage(`{"session_id":"ses-test"}`)
			case "group.list":
				result = json.RawMessage(`{"groups":[]}`)
			case "group.create":
				result = json.RawMessage(`{"ok":true}`)
			case "group.member.add":
				result = json.RawMessage(`{"ok":true}`)
			case "agent.register":
				result = json.RawMessage(`{"ok":true}`)
			case "message.send":
				result = json.RawMessage(`{"message_id":"msg-test"}`)
			case "message.get":
				result = json.RawMessage(`{"content":"hello","recipients":[],"author":{"agent_id":"remote-agent"}}`)
			case "session.heartbeat":
				result = json.RawMessage(`{"ok":true}`)
			case "session.end":
				result = json.RawMessage(`{"ok":true}`)
			default:
				result = json.RawMessage(`{"ok":true}`)
			}

			if req.ID == nil {
				continue
			}

			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      *req.ID,
				"result":  json.RawMessage(result),
			}
			data, _ := json.Marshal(resp)
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// buildRemoteServer creates a simple test WS server that keeps connections
// alive (acts as the remote peer daemon).
func buildRemoteServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("remote server upgrade: %v", err)
			return
		}
		defer conn.Close()

		// Echo RPC calls back with a generic ok response so the client doesn't hang.
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID *int64 `json:"id"`
			}
			if json.Unmarshal(msg, &req) != nil || req.ID == nil {
				continue
			}
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      *req.ID,
				"result":  map[string]any{"ok": true},
			}
			data, _ := json.Marshal(resp)
			if conn.WriteMessage(websocket.TextMessage, data) != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// wsPortFrom extracts just the port string from an httptest.Server URL.
func wsPortFrom(srv *httptest.Server) string {
	u, _ := url.Parse(srv.URL)
	return u.Port()
}

// wsHostFrom extracts "host:port" from an httptest.Server URL.
func wsHostFrom(srv *httptest.Server) string {
	u, _ := url.Parse(srv.URL)
	return u.Host
}

// TestPeerBridge_Lifecycle verifies the full registration sequence:
// user.register → session.start → agent.register are all called on the local daemon.
func TestPeerBridge_Lifecycle(t *testing.T) {
	t.Parallel()

	rs := &recordingServer{}
	localSrv := buildLocalServer(t, rs)
	remoteSrv := buildRemoteServer(t)

	cfg := peer.BridgeConfig{
		LocalWSPort:  wsPortFrom(localSrv),
		PeerName:     "mock-sf",
		PeerAddress:  wsHostFrom(remoteSrv),
		PeerToken:    "test-token",
		BridgeUserID: "user:peer-mock-sf",
		ProxyPrefix:  "mock-sf",
		RemoteAgents: []string{"coordinator_main"},
		Target:       "@coordinator_main",
	}

	b := peer.NewBridge(cfg, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Run bridge in background; it should run until the context times out.
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- b.Run(ctx)
	}()

	// Wait for context to expire (ensures at least the setup calls were made).
	<-ctx.Done()

	// Allow goroutine to finish.
	select {
	case <-runErrCh:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge did not stop after context cancellation")
	}

	// Verify the registration sequence.
	for _, required := range []string{"user.register", "session.start", "agent.register"} {
		if !rs.wasCalled(required) {
			t.Errorf("expected %s to be called; called methods: %v", required, rs.calledMethods())
		}
	}
}

// TestPeerBridge_ConnectsRemote verifies that both local and remote connections
// are established during bridge startup.
func TestPeerBridge_ConnectsRemote(t *testing.T) {
	t.Parallel()

	localConnected := make(chan struct{}, 1)
	remoteConnected := make(chan struct{}, 1)

	// Local server: signal on first connection, then handle normally.
	localSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		select {
		case localConnected <- struct{}{}:
		default:
		}
		// Handle RPC calls.
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID     *int64 `json:"id"`
				Method string `json:"method"`
			}
			if json.Unmarshal(msg, &req) != nil || req.ID == nil {
				continue
			}
			var result json.RawMessage
			switch req.Method {
			case "session.start":
				result = json.RawMessage(`{"session_id":"ses-connect-test"}`)
			case "group.list":
				result = json.RawMessage(`{"groups":[]}`)
			default:
				result = json.RawMessage(`{"ok":true}`)
			}
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      *req.ID,
				"result":  json.RawMessage(result),
			}
			data, _ := json.Marshal(resp)
			if conn.WriteMessage(websocket.TextMessage, data) != nil {
				return
			}
		}
	}))
	t.Cleanup(localSrv.Close)

	// Remote server: signal on first connection, then keep alive.
	remoteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		select {
		case remoteConnected <- struct{}{}:
		default:
		}
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID *int64 `json:"id"`
			}
			if json.Unmarshal(msg, &req) != nil || req.ID == nil {
				continue
			}
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      *req.ID,
				"result":  map[string]any{"ok": true},
			}
			data, _ := json.Marshal(resp)
			if conn.WriteMessage(websocket.TextMessage, data) != nil {
				return
			}
		}
	}))
	t.Cleanup(remoteSrv.Close)

	localU, _ := url.Parse(localSrv.URL)
	remoteU, _ := url.Parse(remoteSrv.URL)

	cfg := peer.BridgeConfig{
		LocalWSPort:  localU.Port(),
		PeerName:     "remote-peer",
		PeerAddress:  remoteU.Host,
		PeerToken:    "tok",
		BridgeUserID: "user:peer-remote",
		ProxyPrefix:  "remote",
		RemoteAgents: []string{"agent1"},
	}

	b := peer.NewBridge(cfg, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go b.Run(ctx) //nolint:errcheck

	// Both servers should receive a connection within the timeout.
	timeout := time.After(4 * time.Second)

	for i := 0; i < 2; i++ {
		select {
		case <-localConnected:
			t.Log("local daemon connected")
		case <-remoteConnected:
			t.Log("remote peer connected")
		case <-timeout:
			t.Fatal("timed out waiting for both connections to be established")
		}
	}
}

// TestPeerBridge_LocalWS_RejectsNonLoopback verifies that bridge startup fails
// when LocalWSPort resolves to a non-loopback address — but since we always use
// 127.0.0.1 in Run, we instead verify the bridge accepts loopback addresses.
func TestPeerBridge_SessionEndOnShutdown(t *testing.T) {
	t.Parallel()

	rs := &recordingServer{}
	localSrv := buildLocalServer(t, rs)
	remoteSrv := buildRemoteServer(t)

	cfg := peer.BridgeConfig{
		LocalWSPort:  wsPortFrom(localSrv),
		PeerName:     "shutdown-peer",
		PeerAddress:  wsHostFrom(remoteSrv),
		PeerToken:    "tok",
		BridgeUserID: "user:peer-shutdown",
		ProxyPrefix:  "shutdown",
		RemoteAgents: []string{},
	}

	b := peer.NewBridge(cfg, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() { runErrCh <- b.Run(ctx) }()

	<-ctx.Done()
	select {
	case <-runErrCh:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge did not stop after context cancellation")
	}

	if !rs.wasCalled("session.end") {
		t.Errorf("expected session.end to be called on shutdown; got: %v", rs.calledMethods())
	}
}

// TestPeerBridge_NoRemoteAgents verifies that the bridge works correctly when
// no RemoteAgents are configured (no proxy agents to register).
func TestPeerBridge_NoRemoteAgents(t *testing.T) {
	t.Parallel()

	rs := &recordingServer{}
	localSrv := buildLocalServer(t, rs)
	remoteSrv := buildRemoteServer(t)

	cfg := peer.BridgeConfig{
		LocalWSPort:  wsPortFrom(localSrv),
		PeerName:     "empty-peer",
		PeerAddress:  wsHostFrom(remoteSrv),
		PeerToken:    "tok",
		BridgeUserID: "user:peer-empty",
		ProxyPrefix:  "empty",
		RemoteAgents: []string{}, // No remote agents
	}

	b := peer.NewBridge(cfg, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() { runErrCh <- b.Run(ctx) }()

	<-ctx.Done()
	select {
	case <-runErrCh:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge did not stop")
	}

	// Should still have registered and started a session, but no agent.register.
	if !rs.wasCalled("user.register") {
		t.Error("expected user.register to be called")
	}
	if !rs.wasCalled("session.start") {
		t.Error("expected session.start to be called")
	}
	// With no remote agents, agent.register should NOT have been called.
	if rs.wasCalled("agent.register") {
		t.Error("agent.register should not be called when RemoteAgents is empty")
	}
}

// TestPeerBridge_RemoteConnectFails verifies that Run returns an error if the
// remote peer is unreachable.
func TestPeerBridge_RemoteConnectFails(t *testing.T) {
	t.Parallel()

	rs := &recordingServer{}
	localSrv := buildLocalServer(t, rs)

	cfg := peer.BridgeConfig{
		LocalWSPort:  wsPortFrom(localSrv),
		PeerName:     "unreachable-peer",
		PeerAddress:  "127.0.0.1:1", // Nothing listening here.
		PeerToken:    "tok",
		BridgeUserID: "user:peer-fail",
		ProxyPrefix:  "fail",
		RemoteAgents: []string{},
	}

	b := peer.NewBridge(cfg, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := b.Run(ctx)
	if err == nil {
		t.Fatal("expected error connecting to unreachable remote")
	}
	if !strings.Contains(err.Error(), "remote connect") {
		t.Errorf("error = %q, want 'remote connect' in message", err.Error())
	}
}
