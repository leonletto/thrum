package cli

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
)

func TestFormatTmuxStatus(t *testing.T) {
	resp := &TmuxStatusResponse{
		Sessions: []TmuxSessionInfo{
			{Name: "coordinator-main", Agent: "coordinator_main", Role: "coordinator",
				Module: "main", State: "alive", Runtime: "claude", Branch: "thrum-dev"},
			{Name: "implementer-api", Agent: "impl_api", Role: "implementer",
				Module: "api", State: "stale", Runtime: "opencode", Branch: "feature/api"},
		},
	}
	output := FormatTmuxStatus(resp)

	if !strings.Contains(output, "coordinator-main") {
		t.Error("output should contain session name")
	}
	if !strings.Contains(output, "alive") {
		t.Error("output should contain state")
	}
	if !strings.Contains(output, "stale") {
		t.Error("output should contain stale state")
	}
	if !strings.Contains(output, "SESSION") {
		t.Error("output should contain header")
	}
}

func TestFormatTmuxStatus_Empty(t *testing.T) {
	resp := &TmuxStatusResponse{Sessions: []TmuxSessionInfo{}}
	output := FormatTmuxStatus(resp)
	if !strings.Contains(output, "No tmux-managed sessions") {
		t.Error("empty status should show no sessions message")
	}
}

func TestFormatTmuxCreate(t *testing.T) {
	resp := &TmuxCreateResponse{Session: "implementer-api"}
	output := FormatTmuxCreate(resp)
	if !strings.Contains(output, "implementer-api") {
		t.Error("output should contain session name")
	}
}

func TestTmuxCreate_RPC(t *testing.T) {
	mockResp := TmuxCreateResponse{Session: "implementer-api"}
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		var request map[string]any
		json.NewDecoder(conn).Decode(&request)
		if request["method"] != "tmux.create" {
			t.Errorf("expected method tmux.create, got %v", request["method"])
		}
		json.NewEncoder(conn).Encode(map[string]any{
			"jsonrpc": "2.0", "id": request["id"], "result": mockResp,
		})
	})
	<-daemon.Ready()

	client, _ := NewClient(socketPath)
	defer client.Close()

	result, err := TmuxCreate(client, TmuxCreateOptions{Name: "implementer-api", Cwd: "/tmp/test"})
	if err != nil {
		t.Fatalf("TmuxCreate: %v", err)
	}
	if result.Session != "implementer-api" {
		t.Errorf("Session = %q, want %q", result.Session, "implementer-api")
	}
}

func TestTmuxStatus_RPC(t *testing.T) {
	mockResp := TmuxStatusResponse{
		Sessions: []TmuxSessionInfo{
			{Name: "test-session", Agent: "test", State: "alive"},
		},
	}
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		var request map[string]any
		json.NewDecoder(conn).Decode(&request)
		json.NewEncoder(conn).Encode(map[string]any{
			"jsonrpc": "2.0", "id": request["id"], "result": mockResp,
		})
	})
	<-daemon.Ready()

	client, _ := NewClient(socketPath)
	defer client.Close()

	result, err := TmuxStatus(client)
	if err != nil {
		t.Fatalf("TmuxStatus: %v", err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(result.Sessions))
	}
	if result.Sessions[0].Name != "test-session" {
		t.Errorf("Session name = %q, want %q", result.Sessions[0].Name, "test-session")
	}
}
