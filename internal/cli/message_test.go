package cli

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/types"
)

func TestMessageGet(t *testing.T) {
	mockResponse := map[string]any{
		"message": map[string]any{
			"message_id": "msg_01HXE8Z7",
			"thread_id":  "thr_01HXE8Y2",
			"author": map[string]string{
				"agent_id":   "agent:implementer:ABC123",
				"session_id": "ses_01HXE8Y2",
			},
			"body": map[string]any{
				"format":  "markdown",
				"content": "Hello from implementer",
			},
			"scopes":     []map[string]string{{"type": "module", "value": "auth"}},
			"refs":       []map[string]string{},
			"metadata":   map[string]string{},
			"created_at": "2026-02-03T10:00:00Z",
		},
	}

	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			return
		}

		if request["method"] != "message.get" {
			t.Errorf("Expected method 'message.get', got %v", request["method"])
		}

		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result":  mockResponse,
		}
		_ = encoder.Encode(response)
	})

	time.Sleep(50 * time.Millisecond)

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	result, err := MessageGet(client, "msg_01HXE8Z7")
	if err != nil {
		t.Fatalf("MessageGet() error = %v", err)
	}

	if result.Message.MessageID != "msg_01HXE8Z7" {
		t.Errorf("MessageID = %s, want msg_01HXE8Z7", result.Message.MessageID)
	}

	if result.Message.Author.AgentID != "agent:implementer:ABC123" {
		t.Errorf("Author.AgentID = %s, want agent:implementer:ABC123", result.Message.Author.AgentID)
	}

	if result.Message.Body.Content != "Hello from implementer" {
		t.Errorf("Body.Content = %s, want 'Hello from implementer'", result.Message.Body.Content)
	}
}

func TestFormatMessageGet(t *testing.T) {
	resp := &MessageGetResponse{
		Message: MessageDetail{
			MessageID: "msg_01HXE8Z7",
			ThreadID:  "thr_01HXE8Y2",
			Author: AuthorInfo{
				AgentID:   "agent:implementer:ABC123",
				SessionID: "ses_01HXE8Y2",
			},
			Body: types.MessageBody{
				Format:  "markdown",
				Content: "Hello from implementer",
			},
			Scopes:    []types.Scope{{Type: "module", Value: "auth"}},
			Refs:      []types.Ref{{Type: "mention", Value: "reviewer"}},
			CreatedAt: time.Now().Add(-5 * time.Minute).Format(time.RFC3339),
		},
	}

	output := FormatMessageGet(resp)

	for _, expected := range []string{
		"msg_01HXE8Z7",
		"@implementer",
		"thr_01HXE8Y2",
		"module:auth",
		"mention:reviewer",
		"Hello from implementer",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("Output should contain %q, got:\n%s", expected, output)
		}
	}
}

func TestFormatMessageGet_Deleted(t *testing.T) {
	resp := &MessageGetResponse{
		Message: MessageDetail{
			MessageID: "msg_deleted",
			Author:    AuthorInfo{AgentID: "agent:test:123"},
			Body:      types.MessageBody{Content: "deleted content"},
			CreatedAt: time.Now().Format(time.RFC3339),
			Deleted:   true,
		},
	}

	output := FormatMessageGet(resp)
	if !strings.Contains(output, "DELETED") {
		t.Error("Output should contain DELETED for deleted messages")
	}
}

func TestFormatMessageGet_Edited(t *testing.T) {
	resp := &MessageGetResponse{
		Message: MessageDetail{
			MessageID: "msg_edited",
			Author:    AuthorInfo{AgentID: "agent:test:123"},
			Body:      types.MessageBody{Content: "edited content"},
			CreatedAt: time.Now().Add(-10 * time.Minute).Format(time.RFC3339),
			UpdatedAt: time.Now().Add(-2 * time.Minute).Format(time.RFC3339),
		},
	}

	output := FormatMessageGet(resp)
	if !strings.Contains(output, "Edited:") {
		t.Error("Output should contain 'Edited:' for edited messages")
	}
}

func TestFormatMessageEdit(t *testing.T) {
	resp := &MessageEditResponse{
		MessageID: "msg_01HXE8Z7",
		UpdatedAt: "2026-02-03T10:00:00Z",
		Version:   3,
	}

	output := FormatMessageEdit(resp)
	if !strings.Contains(output, "msg_01HXE8Z7") {
		t.Error("Output should contain message ID")
	}
	if !strings.Contains(output, "version 3") {
		t.Error("Output should contain version number")
	}
	if !strings.Contains(output, "✓") {
		t.Error("Output should contain success indicator")
	}
}

func TestFormatMessageDelete(t *testing.T) {
	resp := &MessageDeleteResponse{
		MessageID: "msg_01HXE8Z7",
		DeletedAt: "2026-02-03T10:00:00Z",
	}

	output := FormatMessageDelete(resp)
	if !strings.Contains(output, "msg_01HXE8Z7") {
		t.Error("Output should contain message ID")
	}
	if !strings.Contains(output, "deleted") {
		t.Error("Output should contain 'deleted'")
	}
}

func TestFormatMarkRead(t *testing.T) {
	tests := []struct {
		name     string
		resp     *MarkReadResponse
		contains string
	}{
		{
			name:     "single message",
			resp:     &MarkReadResponse{MarkedCount: 1},
			contains: "1 message",
		},
		{
			name:     "multiple messages",
			resp:     &MarkReadResponse{MarkedCount: 5},
			contains: "5 messages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatMarkRead(tt.resp)
			if !strings.Contains(output, tt.contains) {
				t.Errorf("Output should contain %q, got %q", tt.contains, output)
			}
		})
	}
}

func TestMessageEdit(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			return
		}

		if request["method"] != "message.edit" {
			t.Errorf("Expected method 'message.edit', got %v", request["method"])
		}

		params, ok := request["params"].(map[string]any)
		if !ok {
			t.Error("params should be map[string]any")
			return
		}
		if params["message_id"] != "msg_01HXE8Z7" {
			t.Errorf("Expected message_id 'msg_01HXE8Z7', got %v", params["message_id"])
		}

		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{
				"message_id": "msg_01HXE8Z7",
				"updated_at": "2026-02-03T10:05:00Z",
				"version":    2,
			},
		}
		_ = encoder.Encode(response)
	})

	time.Sleep(50 * time.Millisecond)

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	result, err := MessageEdit(client, "msg_01HXE8Z7", "Updated content")
	if err != nil {
		t.Fatalf("MessageEdit() error = %v", err)
	}

	if result.MessageID != "msg_01HXE8Z7" {
		t.Errorf("MessageID = %s, want msg_01HXE8Z7", result.MessageID)
	}
	if result.Version != 2 {
		t.Errorf("Version = %d, want 2", result.Version)
	}
}

func TestMessageDelete(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			return
		}

		if request["method"] != "message.delete" {
			t.Errorf("Expected method 'message.delete', got %v", request["method"])
		}

		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{
				"message_id": "msg_01HXE8Z7",
				"deleted_at": "2026-02-03T10:05:00Z",
			},
		}
		_ = encoder.Encode(response)
	})

	time.Sleep(50 * time.Millisecond)

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	result, err := MessageDelete(client, "msg_01HXE8Z7")
	if err != nil {
		t.Fatalf("MessageDelete() error = %v", err)
	}

	if result.MessageID != "msg_01HXE8Z7" {
		t.Errorf("MessageID = %s, want msg_01HXE8Z7", result.MessageID)
	}
}

func TestMessageMarkRead(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			return
		}

		if request["method"] != "message.markRead" {
			t.Errorf("Expected method 'message.markRead', got %v", request["method"])
		}

		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{
				"marked_count": 3,
				"also_read_by": map[string]any{
					"msg_01": []string{"agent:reviewer:XYZ789"},
				},
			},
		}
		_ = encoder.Encode(response)
	})

	time.Sleep(50 * time.Millisecond)

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	result, err := MessageMarkRead(client, []string{"msg_01", "msg_02", "msg_03"}, "")
	if err != nil {
		t.Fatalf("MessageMarkRead() error = %v", err)
	}

	if result.MarkedCount != 3 {
		t.Errorf("MarkedCount = %d, want 3", result.MarkedCount)
	}

	if result.AlsoReadBy == nil {
		t.Fatal("AlsoReadBy should not be nil")
	}

	if agents, ok := result.AlsoReadBy["msg_01"]; !ok || len(agents) != 1 {
		t.Errorf("AlsoReadBy[msg_01] = %v, want 1 agent", agents)
	}
}
