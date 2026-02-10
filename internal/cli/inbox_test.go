package cli

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func TestInbox(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	// Mock handler for message.list
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
		if request["method"] != "message.list" {
			t.Errorf("Expected method 'message.list', got %v", request["method"])
		}

		// Send response
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{
				"messages": []map[string]any{
					{
						"message_id": "msg_01HXE8Z7",
						"agent_id":   "agent:implementer:ABC123",
						"body": map[string]any{
							"format":  "markdown",
							"content": "Test message 1",
						},
						"created_at": "2026-02-03T10:00:00Z",
					},
					{
						"message_id": "msg_01HXE8A2",
						"agent_id":   "agent:reviewer:XYZ789",
						"body": map[string]any{
							"format":  "markdown",
							"content": "Test message 2",
						},
						"created_at": "2026-02-03T09:00:00Z",
					},
				},
				"total":       2,
				"unread":      1,
				"page":        1,
				"page_size":   10,
				"total_pages": 1,
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

	opts := InboxOptions{
		PageSize: 10,
		Page:     1,
	}

	result, err := Inbox(client, opts)
	if err != nil {
		t.Fatalf("Inbox failed: %v", err)
	}

	if len(result.Messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(result.Messages))
	}

	if result.Total != 2 {
		t.Errorf("Expected total 2, got %d", result.Total)
	}

	if result.Unread != 1 {
		t.Errorf("Expected unread 1, got %d", result.Unread)
	}
}

func TestInbox_WithFilters(t *testing.T) {
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
				"messages":    []map[string]any{},
				"total":       0,
				"unread":      0,
				"page":        1,
				"page_size":   10,
				"total_pages": 0,
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

	opts := InboxOptions{
		Scope:    "module:auth",
		Mentions: true,
		Unread:   true,
		PageSize: 20,
		Page:     2,
	}

	_, err = Inbox(client, opts)
	if err != nil {
		t.Fatalf("Inbox failed: %v", err)
	}

	// Verify scope was sent
	scope, ok := receivedParams["scope"].(map[string]any)
	if !ok {
		t.Fatal("Expected scope in params")
	}
	if scope["type"] != "module" || scope["value"] != "auth" {
		t.Errorf("Expected scope module:auth, got %v:%v", scope["type"], scope["value"])
	}

	// Verify mentions flag
	if mentions, ok := receivedParams["mentions"].(bool); !ok || !mentions {
		t.Error("Expected mentions=true")
	}

	// Verify unread flag
	if unread, ok := receivedParams["unread"].(bool); !ok || !unread {
		t.Error("Expected unread=true")
	}

	// Verify pagination
	if pageSize, ok := receivedParams["page_size"].(float64); !ok || int(pageSize) != 20 {
		t.Errorf("Expected page_size=20, got %v", receivedParams["page_size"])
	}

	if page, ok := receivedParams["page"].(float64); !ok || int(page) != 2 {
		t.Errorf("Expected page=2, got %v", receivedParams["page"])
	}
}

func TestFormatInbox(t *testing.T) {
	result := &InboxResult{
		Messages: []Message{
			{
				MessageID: "msg_01HXE8Z7",
				AgentID:   "agent:planner:ABC123",
				Body: struct {
					Format     string `json:"format"`
					Content    string `json:"content"`
					Structured string `json:"structured,omitempty"`
				}{
					Format:  "markdown",
					Content: "We should refactor the sync daemon before adding embeddings.",
				},
				CreatedAt: time.Now().Add(-2 * time.Minute).Format(time.RFC3339),
			},
			{
				MessageID: "msg_01HXE8A2",
				AgentID:   "agent:reviewer:XYZ789",
				Body: struct {
					Format     string `json:"format"`
					Content    string `json:"content"`
					Structured string `json:"structured,omitempty"`
				}{
					Format:  "markdown",
					Content: "LGTM on the auth changes. Ready to merge.",
				},
				CreatedAt: time.Now().Add(-15 * time.Minute).Format(time.RFC3339),
			},
		},
		Total:      47,
		Unread:     12,
		Page:       1,
		PageSize:   10,
		TotalPages: 5,
	}

	output := FormatInbox(result)

	// Verify output contains expected elements
	if !strings.Contains(output, "msg_01HXE8Z7") {
		t.Error("Output should contain message ID")
	}

	if !strings.Contains(output, "@planner") {
		t.Error("Output should contain agent name")
	}

	if !strings.Contains(output, "We should refactor") {
		t.Error("Output should contain message content")
	}

	if !strings.Contains(output, "Showing 1-2 of 47 messages") {
		t.Error("Output should contain pagination info")
	}

	if !strings.Contains(output, "(12 unread)") {
		t.Error("Output should contain unread count")
	}

	// Verify box drawing
	if !strings.Contains(output, "┌") || !strings.Contains(output, "┐") {
		t.Error("Output should contain top border")
	}

	if !strings.Contains(output, "└") || !strings.Contains(output, "┘") {
		t.Error("Output should contain bottom border")
	}
}

func TestFormatInbox_Empty(t *testing.T) {
	result := &InboxResult{
		Messages:   []Message{},
		Total:      0,
		Unread:     0,
		Page:       1,
		PageSize:   10,
		TotalPages: 0,
	}

	output := FormatInbox(result)
	if !strings.Contains(output, "No messages in inbox.") {
		t.Errorf("Expected empty state message, got %q", output)
	}
}

func TestFormatInbox_EmptyWithFilter(t *testing.T) {
	result := &InboxResult{
		Messages:   []Message{},
		Total:      12,
		Unread:     0,
		Page:       1,
		PageSize:   10,
		TotalPages: 0,
	}

	output := FormatInboxWithOptions(result, InboxFormatOptions{ActiveScope: "module:auth"})
	if !strings.Contains(output, "No messages matching filter") {
		t.Errorf("Expected filter feedback, got %q", output)
	}
	if !strings.Contains(output, "module:auth") {
		t.Errorf("Expected filter value in output, got %q", output)
	}
	if !strings.Contains(output, "12 total") {
		t.Errorf("Expected total count in output, got %q", output)
	}
}

func TestFormatInbox_ThreadAndReadStatus(t *testing.T) {
	result := &InboxResult{
		Messages: []Message{
			{
				MessageID: "msg_01HXE8Z7",
				ThreadID:  "thr_01ABC",
				AgentID:   "agent:planner:ABC123",
				Body: struct {
					Format     string `json:"format"`
					Content    string `json:"content"`
					Structured string `json:"structured,omitempty"`
				}{
					Format:  "markdown",
					Content: "Threaded message",
				},
				CreatedAt: time.Now().Add(-5 * time.Minute).Format(time.RFC3339),
				IsRead:    true,
			},
			{
				MessageID: "msg_01HXE8A2",
				AgentID:   "agent:reviewer:XYZ789",
				Body: struct {
					Format     string `json:"format"`
					Content    string `json:"content"`
					Structured string `json:"structured,omitempty"`
				}{
					Format:  "markdown",
					Content: "Unread message",
				},
				CreatedAt: time.Now().Add(-2 * time.Minute).Format(time.RFC3339),
				IsRead:    false,
			},
		},
		Total:      2,
		Unread:     1,
		Page:       1,
		PageSize:   10,
		TotalPages: 1,
	}

	output := FormatInbox(result)

	// Check thread reference
	if !strings.Contains(output, "thread:thr_01ABC") {
		t.Error("Output should contain thread reference")
	}

	// Check read indicators (○ for read, ● for unread)
	if !strings.Contains(output, "○") {
		t.Error("Output should contain read indicator (○)")
	}
	if !strings.Contains(output, "●") {
		t.Error("Output should contain unread indicator (●)")
	}
}

func TestFormatInbox_EditedMessage(t *testing.T) {
	result := &InboxResult{
		Messages: []Message{
			{
				MessageID: "msg_01HXE8Z7",
				AgentID:   "agent:planner:ABC123",
				Body: struct {
					Format     string `json:"format"`
					Content    string `json:"content"`
					Structured string `json:"structured,omitempty"`
				}{
					Format:  "markdown",
					Content: "Updated content after edit",
				},
				CreatedAt: time.Now().Add(-10 * time.Minute).Format(time.RFC3339),
				UpdatedAt: time.Now().Add(-2 * time.Minute).Format(time.RFC3339),
			},
		},
		Total:      1,
		Unread:     0,
		Page:       1,
		PageSize:   10,
		TotalPages: 1,
	}

	output := FormatInbox(result)

	// Verify "(edited)" indicator is present
	if !strings.Contains(output, "(edited)") {
		t.Error("Output should contain '(edited)' indicator for edited messages")
	}

	// Verify message ID is still present
	if !strings.Contains(output, "msg_01HXE8Z7") {
		t.Error("Output should contain message ID")
	}

	// Verify content is present
	if !strings.Contains(output, "Updated content after edit") {
		t.Error("Output should contain updated message content")
	}
}

func TestExtractAgentName(t *testing.T) {
	tests := []struct {
		agentID string
		want    string
	}{
		// Legacy format: agent:role:hash (3 parts)
		{"agent:implementer:ABC123DEF4", "@implementer"},
		{"agent:reviewer:XYZ7891234", "@reviewer"},
		{"agent:coordinator:1B9K33T6RK", "@coordinator"},
		// Current unnamed format: role_hash (10 char base32 hash)
		{"implementer_ABC123DEF4", "@implementer"},
		{"reviewer_XYZ7891234", "@reviewer"},
		{"coordinator_1B9K33T6RK", "@coordinator"},
		// Named format: just the name
		{"furiosa", "@furiosa"},
		{"nux", "@nux"},
		{"planner", "@planner"},
		// Named with underscores (not confused with role_hash due to hash length/format check)
		{"max_rockatansky", "@max_rockatansky"},
		{"immortan_joe", "@immortan_joe"},
	}

	for _, tt := range tests {
		t.Run(tt.agentID, func(t *testing.T) {
			got := extractAgentName(tt.agentID)
			if got != tt.want {
				t.Errorf("extractAgentName(%q) = %q, want %q", tt.agentID, got, tt.want)
			}
		})
	}
}

func TestFormatRelativeTime(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		timestamp string
		want      string
	}{
		{
			name:      "just now",
			timestamp: now.Add(-30 * time.Second).Format(time.RFC3339),
			want:      "just now",
		},
		{
			name:      "minutes ago",
			timestamp: now.Add(-5 * time.Minute).Format(time.RFC3339),
			want:      "5m ago",
		},
		{
			name:      "hours ago",
			timestamp: now.Add(-3 * time.Hour).Format(time.RFC3339),
			want:      "3h ago",
		},
		{
			name:      "days ago",
			timestamp: now.Add(-2 * 24 * time.Hour).Format(time.RFC3339),
			want:      "2d ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatRelativeTime(tt.timestamp)
			if got != tt.want {
				t.Errorf("formatRelativeTime() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWordWrap(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		want  string
	}{
		{
			name:  "no wrap needed",
			text:  "Short text",
			width: 20,
			want:  "Short text",
		},
		{
			name:  "wrap at word boundary",
			text:  "This is a longer text that needs wrapping",
			width: 20,
			want:  "This is a longer\ntext that needs\nwrapping",
		},
		{
			name:  "single word longer than width",
			text:  "Supercalifragilisticexpialidocious",
			width: 20,
			want:  "Supercalifragilisticexpialidocious",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wordWrap(tt.text, tt.width)
			if got != tt.want {
				t.Errorf("wordWrap() = %q, want %q", got, tt.want)
			}
		})
	}
}
