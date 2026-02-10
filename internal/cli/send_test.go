package cli

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestSend(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	// Mock handler for message.send
	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			t.Logf("Failed to decode request: %v", err)
			return
		}

		// Verify method
		if request["method"] != "message.send" {
			t.Errorf("Expected method 'message.send', got %v", request["method"])
		}

		// Verify params
		params, ok := request["params"].(map[string]any)
		if !ok {
			t.Error("params is not a map")
			return
		}

		if params["content"] != "Test message" {
			t.Errorf("Expected content 'Test message', got %v", params["content"])
		}

		// Send success response
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{
				"message_id": "msg_01HXE8Z7",
				"created_at": "2026-02-03T10:00:00Z",
			},
		}

		if err := encoder.Encode(response); err != nil {
			t.Logf("Failed to encode response: %v", err)
		}
	})

	time.Sleep(50 * time.Millisecond)

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	opts := SendOptions{
		Content: "Test message",
	}

	result, err := Send(client, opts)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if result.MessageID != "msg_01HXE8Z7" {
		t.Errorf("Expected message_id 'msg_01HXE8Z7', got %s", result.MessageID)
	}

	if result.CreatedAt != "2026-02-03T10:00:00Z" {
		t.Errorf("Expected created_at '2026-02-03T10:00:00Z', got %s", result.CreatedAt)
	}
}

func TestSend_WithScopes(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	var receivedParams map[string]any

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			return
		}

		var ok bool
		receivedParams, ok = request["params"].(map[string]any)
		if !ok {
			t.Error("params should be map[string]any")
			return
		}

		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{
				"message_id": "msg_01HXE8Z7",
				"created_at": "2026-02-03T10:00:00Z",
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

	opts := SendOptions{
		Content: "Test message",
		Scopes:  []string{"module:auth", "file:src/auth.go"},
	}

	_, err = Send(client, opts)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify scopes were sent correctly
	scopes, ok := receivedParams["scopes"].([]any)
	if !ok || len(scopes) != 2 {
		t.Fatalf("Expected 2 scopes, got %v", receivedParams["scopes"])
	}

	scope1, ok := scopes[0].(map[string]any)
	if !ok {
		t.Fatal("scope1 should be map[string]any")
	}
	if scope1["type"] != "module" || scope1["value"] != "auth" {
		t.Errorf("Expected scope module:auth, got %v:%v", scope1["type"], scope1["value"])
	}

	scope2, ok := scopes[1].(map[string]any)
	if !ok {
		t.Fatal("scope2 should be map[string]any")
	}
	if scope2["type"] != "file" || scope2["value"] != "src/auth.go" {
		t.Errorf("Expected scope file:src/auth.go, got %v:%v", scope2["type"], scope2["value"])
	}
}

func TestSend_WithRefs(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	var receivedParams map[string]any

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			return
		}

		var ok bool
		receivedParams, ok = request["params"].(map[string]any)
		if !ok {
			t.Error("params should be map[string]any")
			return
		}

		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{
				"message_id": "msg_01HXE8Z7",
				"created_at": "2026-02-03T10:00:00Z",
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

	opts := SendOptions{
		Content: "Test message",
		Refs:    []string{"issue:beads-123", "commit:abc123"},
	}

	_, err = Send(client, opts)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify refs were sent correctly
	refs, ok := receivedParams["refs"].([]any)
	if !ok || len(refs) != 2 {
		t.Fatalf("Expected 2 refs, got %v", receivedParams["refs"])
	}

	ref1, ok := refs[0].(map[string]any)
	if !ok {
		t.Fatal("ref1 should be map[string]any")
	}
	if ref1["type"] != "issue" || ref1["value"] != "beads-123" {
		t.Errorf("Expected ref issue:beads-123, got %v:%v", ref1["type"], ref1["value"])
	}

	ref2, ok := refs[1].(map[string]any)
	if !ok {
		t.Fatal("ref2 should be map[string]any")
	}
	if ref2["type"] != "commit" || ref2["value"] != "abc123" {
		t.Errorf("Expected ref commit:abc123, got %v:%v", ref2["type"], ref2["value"])
	}
}

func TestSend_WithStructured(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	var receivedParams map[string]any

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			return
		}

		var ok bool
		receivedParams, ok = request["params"].(map[string]any)
		if !ok {
			t.Error("params should be map[string]any")
			return
		}

		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{
				"message_id": "msg_01HXE8Z7",
				"created_at": "2026-02-03T10:00:00Z",
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

	opts := SendOptions{
		Content:    "Test message",
		Structured: `{"status":"complete","progress":100}`,
	}

	_, err = Send(client, opts)
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// Verify structured data was sent correctly
	structured, ok := receivedParams["structured"].(map[string]any)
	if !ok {
		t.Fatalf("Expected structured to be a map, got %T", receivedParams["structured"])
	}

	if structured["status"] != "complete" {
		t.Errorf("Expected status 'complete', got %v", structured["status"])
	}

	progress, ok := structured["progress"].(float64)
	if !ok {
		t.Fatal("progress should be float64")
	}
	if progress != 100 {
		t.Errorf("Expected progress 100, got %v", progress)
	}
}

func TestParseScopes(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		want    []map[string]string
		wantErr bool
	}{
		{
			name:  "single scope",
			input: []string{"module:auth"},
			want: []map[string]string{
				{"type": "module", "value": "auth"},
			},
		},
		{
			name:  "multiple scopes",
			input: []string{"module:auth", "file:src/main.go"},
			want: []map[string]string{
				{"type": "module", "value": "auth"},
				{"type": "file", "value": "src/main.go"},
			},
		},
		{
			name:  "value with colon",
			input: []string{"url:https://example.com"},
			want: []map[string]string{
				{"type": "url", "value": "https://example.com"},
			},
		},
		{
			name:    "invalid format",
			input:   []string{"invalid"},
			wantErr: true,
		},
		{
			name:  "empty input",
			input: []string{},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseScopes(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseScopes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			if len(got) != len(tt.want) {
				t.Errorf("parseScopes() length = %d, want %d", len(got), len(tt.want))
				return
			}

			for i := range got {
				if got[i]["type"] != tt.want[i]["type"] || got[i]["value"] != tt.want[i]["value"] {
					t.Errorf("parseScopes()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseRefs(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		want    []map[string]string
		wantErr bool
	}{
		{
			name:  "single ref",
			input: []string{"issue:beads-123"},
			want: []map[string]string{
				{"type": "issue", "value": "beads-123"},
			},
		},
		{
			name:  "multiple refs",
			input: []string{"issue:beads-123", "commit:abc123"},
			want: []map[string]string{
				{"type": "issue", "value": "beads-123"},
				{"type": "commit", "value": "abc123"},
			},
		},
		{
			name:    "invalid format",
			input:   []string{"invalid"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRefs(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseRefs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			if len(got) != len(tt.want) {
				t.Errorf("parseRefs() length = %d, want %d", len(got), len(tt.want))
				return
			}

			for i := range got {
				if got[i]["type"] != tt.want[i]["type"] || got[i]["value"] != tt.want[i]["value"] {
					t.Errorf("parseRefs()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
