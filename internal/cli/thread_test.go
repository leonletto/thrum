package cli

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func TestThreadCreate(t *testing.T) {
	mockResponse := CreateThreadResponse{
		ThreadID:  "thr_01HXE8Y2...",
		Title:     "Authentication discussion",
		CreatedBy: "agent:implementer:ABC123",
		CreatedAt: "2026-02-03T10:00:00Z",
	}

	// Create mock daemon
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	// Start mock daemon with handler
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
		if request["method"] != "thread.create" {
			t.Errorf("Expected method 'thread.create', got %v", request["method"])
		}

		// Send response
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result":  mockResponse,
		}

		if err := encoder.Encode(response); err != nil {
			t.Logf("Failed to encode response: %v", err)
		}
	})

	// Wait for daemon to be ready
	time.Sleep(50 * time.Millisecond)

	// Create client
	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Call ThreadCreate
	opts := ThreadCreateOptions{
		Title: "Authentication discussion",
	}

	result, err := ThreadCreate(client, opts)
	if err != nil {
		t.Fatalf("ThreadCreate() error = %v", err)
	}

	if result.ThreadID != mockResponse.ThreadID {
		t.Errorf("ThreadID = %s, want %s", result.ThreadID, mockResponse.ThreadID)
	}

	if result.Title != mockResponse.Title {
		t.Errorf("Title = %s, want %s", result.Title, mockResponse.Title)
	}
}

func TestFormatThreadCreate(t *testing.T) {
	result := CreateThreadResponse{
		ThreadID:  "thr_01HXE8Y2...",
		Title:     "Authentication discussion",
		CreatedBy: "agent:implementer:ABC123",
		CreatedAt: "2026-02-03T10:00:00Z",
	}

	output := FormatThreadCreate(&result)

	expectedFields := []string{
		"thr_01HXE8Y2...",
		"Authentication discussion",
		"agent:implementer:ABC123",
	}

	for _, field := range expectedFields {
		if !contains(output, field) {
			t.Errorf("Output should contain '%s'", field)
		}
	}
}

func TestFormatThreadCreate_WithMessage(t *testing.T) {
	msgID := "msg_01HXE8Z7"
	result := CreateThreadResponse{
		ThreadID:  "thr_01HXE8Y2",
		Title:     "Auth discussion",
		CreatedBy: "agent:planner:ABC",
		CreatedAt: "2026-02-03T10:00:00Z",
		MessageID: &msgID,
	}

	output := FormatThreadCreate(&result)
	if !strings.Contains(output, "msg_01HXE8Z7") {
		t.Error("Output should contain initial message ID")
	}
}

func TestThreadList(t *testing.T) {
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

		if request["method"] != "thread.list" {
			t.Errorf("Expected method 'thread.list', got %v", request["method"])
		}

		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{
				"threads": []map[string]any{
					{
						"thread_id":     "thr_01",
						"title":         "Auth discussion",
						"message_count": 5,
						"unread_count":  2,
						"last_activity": "2026-02-03T10:00:00Z",
						"last_sender":   "agent:implementer:ABC",
						"created_by":    "agent:planner:XYZ",
						"created_at":    "2026-02-03T09:00:00Z",
					},
				},
				"total":       1,
				"page":        1,
				"page_size":   10,
				"total_pages": 1,
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

	result, err := ThreadList(client, ThreadListOptions{})
	if err != nil {
		t.Fatalf("ThreadList() error = %v", err)
	}

	if len(result.Threads) != 1 {
		t.Fatalf("Expected 1 thread, got %d", len(result.Threads))
	}
	if result.Threads[0].ThreadID != "thr_01" {
		t.Errorf("ThreadID = %s, want thr_01", result.Threads[0].ThreadID)
	}
	if result.Threads[0].UnreadCount != 2 {
		t.Errorf("UnreadCount = %d, want 2", result.Threads[0].UnreadCount)
	}
}

func TestFormatThreadList(t *testing.T) {
	resp := &ThreadListResponse{
		Threads: []ThreadSummary{
			{
				ThreadID:     "thr_01HXE8Y2",
				Title:        "Auth discussion",
				MessageCount: 5,
				UnreadCount:  2,
				LastActivity: time.Now().Add(-5 * time.Minute).Format(time.RFC3339),
				LastSender:   "agent:implementer:ABC",
				CreatedBy:    "agent:planner:XYZ",
				CreatedAt:    time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
			},
			{
				ThreadID:     "thr_02",
				Title:        "Code review",
				MessageCount: 3,
				UnreadCount:  0,
				LastActivity: time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
				LastSender:   "agent:reviewer:DEF",
				CreatedBy:    "agent:planner:XYZ",
				CreatedAt:    time.Now().Add(-3 * time.Hour).Format(time.RFC3339),
			},
		},
		Total:      2,
		Page:       1,
		PageSize:   10,
		TotalPages: 1,
	}

	output := FormatThreadList(resp)

	for _, expected := range []string{
		"thr_01HXE8Y2",
		"Auth discussion",
		"thr_02",
		"Code review",
		"THREAD",
		"TITLE",
		"Showing 1-2 of 2",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("Output should contain %q", expected)
		}
	}

	// Unread count of 0 should show "·"
	if !strings.Contains(output, "·") {
		t.Error("Zero unread count should show '·'")
	}
}

func TestFormatThreadList_Empty(t *testing.T) {
	resp := &ThreadListResponse{
		Threads:    []ThreadSummary{},
		Total:      0,
		Page:       1,
		PageSize:   10,
		TotalPages: 0,
	}

	output := FormatThreadList(resp)
	if !strings.Contains(output, "No threads found") {
		t.Errorf("Expected 'No threads found', got %q", output)
	}
}

func TestThreadShow(t *testing.T) {
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

		if request["method"] != "thread.get" {
			t.Errorf("Expected method 'thread.get', got %v", request["method"])
		}

		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{
				"thread": map[string]any{
					"thread_id":  "thr_01",
					"title":      "Auth discussion",
					"created_by": "agent:planner:ABC",
					"created_at": "2026-02-03T09:00:00Z",
				},
				"messages": []map[string]any{
					{
						"message_id": "msg_01",
						"agent_id":   "agent:planner:ABC",
						"body":       map[string]string{"format": "markdown", "content": "Let's discuss auth"},
						"created_at": "2026-02-03T09:05:00Z",
					},
					{
						"message_id": "msg_02",
						"agent_id":   "agent:implementer:DEF",
						"body":       map[string]string{"format": "markdown", "content": "I'll handle OAuth"},
						"created_at": "2026-02-03T09:10:00Z",
					},
				},
				"total":       2,
				"page":        1,
				"page_size":   10,
				"total_pages": 1,
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

	result, err := ThreadShow(client, ThreadShowOptions{ThreadID: "thr_01"})
	if err != nil {
		t.Fatalf("ThreadShow() error = %v", err)
	}

	if result.Thread.ThreadID != "thr_01" {
		t.Errorf("ThreadID = %s, want thr_01", result.Thread.ThreadID)
	}
	if len(result.Messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(result.Messages))
	}
}

func TestFormatThreadShow(t *testing.T) {
	resp := &ThreadShowResponse{
		Thread: ThreadDetailInfo{
			ThreadID:  "thr_01HXE8Y2",
			Title:     "Auth discussion",
			CreatedBy: "agent:planner:ABC",
			CreatedAt: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
		},
		Messages: []Message{
			{
				MessageID: "msg_01",
				AgentID:   "agent:planner:ABC",
				Body: struct {
					Format     string `json:"format"`
					Content    string `json:"content"`
					Structured string `json:"structured,omitempty"`
				}{Format: "markdown", Content: "Let's discuss auth"},
				CreatedAt: time.Now().Add(-55 * time.Minute).Format(time.RFC3339),
			},
		},
		Total:      1,
		Page:       1,
		PageSize:   10,
		TotalPages: 1,
	}

	output := FormatThreadShow(resp)

	for _, expected := range []string{
		"thr_01HXE8Y2",
		"Auth discussion",
		"@planner",
		"msg_01",
		"Let's discuss auth",
		"Showing 1-1 of 1",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("Output should contain %q", expected)
		}
	}

	// Verify box drawing
	if !strings.Contains(output, "┌") || !strings.Contains(output, "┘") {
		t.Error("Output should contain box borders")
	}
}

func TestFormatThreadShow_Empty(t *testing.T) {
	resp := &ThreadShowResponse{
		Thread: ThreadDetailInfo{
			ThreadID:  "thr_empty",
			Title:     "Empty thread",
			CreatedBy: "agent:test:ABC",
			CreatedAt: time.Now().Format(time.RFC3339),
		},
		Messages:   []Message{},
		Total:      0,
		Page:       1,
		PageSize:   10,
		TotalPages: 0,
	}

	output := FormatThreadShow(resp)
	if !strings.Contains(output, "No messages in this thread") {
		t.Error("Output should indicate empty thread")
	}
}
