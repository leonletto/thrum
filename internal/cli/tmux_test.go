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

func TestTmuxQueue_RPC(t *testing.T) {
	mockResp := TmuxQueueResponse{CommandID: "cmd_abc123", Position: 1}
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		var request map[string]any
		json.NewDecoder(conn).Decode(&request)
		if request["method"] != "tmux.queue" {
			t.Errorf("expected method tmux.queue, got %v", request["method"])
		}
		params, _ := request["params"].(map[string]any)
		if params["session"] != "test-session" {
			t.Errorf("expected session test-session, got %v", params["session"])
		}
		if params["text"] != "echo hello" {
			t.Errorf("expected text 'echo hello', got %v", params["text"])
		}
		json.NewEncoder(conn).Encode(map[string]any{
			"jsonrpc": "2.0", "id": request["id"], "result": mockResp,
		})
	})
	<-daemon.Ready()

	client, _ := NewClient(socketPath)
	defer client.Close()

	notifyFalse := false
	result, err := TmuxQueue(client, TmuxQueueOptions{
		Session:          "test-session",
		Text:             "echo hello",
		TimeoutMs:        30000,
		Requester:        "test-agent",
		SilenceMs:        2500,
		NotifyOnComplete: &notifyFalse,
	})
	if err != nil {
		t.Fatalf("TmuxQueue: %v", err)
	}
	if result.CommandID != "cmd_abc123" {
		t.Errorf("CommandID = %q, want %q", result.CommandID, "cmd_abc123")
	}
	if result.Position != 1 {
		t.Errorf("Position = %d, want 1", result.Position)
	}
}

func TestTmuxQueueStatus_RPC(t *testing.T) {
	active := &TmuxQueuedView{ID: "cmd_abc123", Text: "echo hello", State: "running"}
	mockResp := TmuxQueueStatusResponse{
		Session: "test-session",
		Active:  active,
		Queued:  []TmuxQueuedView{{ID: "cmd_def456", Text: "echo world", State: "queued"}},
	}
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		var request map[string]any
		json.NewDecoder(conn).Decode(&request)
		if request["method"] != "tmux.queue-status" {
			t.Errorf("expected method tmux.queue-status, got %v", request["method"])
		}
		json.NewEncoder(conn).Encode(map[string]any{
			"jsonrpc": "2.0", "id": request["id"], "result": mockResp,
		})
	})
	<-daemon.Ready()

	client, _ := NewClient(socketPath)
	defer client.Close()

	result, err := TmuxQueueStatus(client, "test-session")
	if err != nil {
		t.Fatalf("TmuxQueueStatus: %v", err)
	}
	if result.Session != "test-session" {
		t.Errorf("Session = %q, want %q", result.Session, "test-session")
	}
	if result.Active == nil {
		t.Fatal("expected active command, got nil")
	}
	if result.Active.ID != "cmd_abc123" {
		t.Errorf("Active.ID = %q, want %q", result.Active.ID, "cmd_abc123")
	}
	if len(result.Queued) != 1 {
		t.Fatalf("expected 1 queued command, got %d", len(result.Queued))
	}
}

func TestTmuxCancel_RPC(t *testing.T) {
	mockResp := TmuxCancelResponse{CommandID: "cmd_abc123", State: "cancelled", Output: ""}
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		var request map[string]any
		json.NewDecoder(conn).Decode(&request)
		if request["method"] != "tmux.cancel" {
			t.Errorf("expected method tmux.cancel, got %v", request["method"])
		}
		json.NewEncoder(conn).Encode(map[string]any{
			"jsonrpc": "2.0", "id": request["id"], "result": mockResp,
		})
	})
	<-daemon.Ready()

	client, _ := NewClient(socketPath)
	defer client.Close()

	result, err := TmuxCancel(client, "cmd_abc123")
	if err != nil {
		t.Fatalf("TmuxCancel: %v", err)
	}
	if result.CommandID != "cmd_abc123" {
		t.Errorf("CommandID = %q, want %q", result.CommandID, "cmd_abc123")
	}
	if result.State != "cancelled" {
		t.Errorf("State = %q, want %q", result.State, "cancelled")
	}
}
