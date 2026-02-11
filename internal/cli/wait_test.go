package cli

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func TestWait_MessageReceived(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	callCount := 0

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		// Handle multiple requests on same connection
		for {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				return
			}

			callCount++
			method, ok := request["method"].(string)
			if !ok {
				t.Error("method should be string")
				return
			}

			var response map[string]any

			switch method {
			case "subscribe":
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"subscription_id": "sub_123",
					},
				}

			case "message.list":
				// Return a message on the second call (simulating arrival)
				messages := []map[string]any{}
				if callCount > 2 {
					messages = append(messages, map[string]any{
						"message_id": "msg_01HXE8Z7",
						"agent_id":   "agent:implementer:ABC123",
						"body": map[string]any{
							"format":  "markdown",
							"content": "Test message",
						},
						"created_at": time.Now().Format(time.RFC3339),
					})
				}

				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"messages":    messages,
						"total":       len(messages),
						"unread":      len(messages),
						"page":        1,
						"page_size":   1,
						"total_pages": 1,
					},
				}

			case "unsubscribe":
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result":  map[string]any{},
				}
			}

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	})

	time.Sleep(50 * time.Millisecond)

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	opts := WaitOptions{
		Timeout: 5 * time.Second,
	}

	message, err := Wait(client, opts)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	if message == nil {
		t.Fatal("Expected message, got nil")
	}

	if message.MessageID != "msg_01HXE8Z7" {
		t.Errorf("Expected message_id 'msg_01HXE8Z7', got %s", message.MessageID)
	}
}

func TestWait_Timeout(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		// Handle multiple requests on same connection
		for {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				return
			}

			method, ok := request["method"].(string)
			if !ok {
				t.Error("method should be string")
				return
			}

			var response map[string]any

			switch method {
			case "subscribe":
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"subscription_id": "sub_123",
					},
				}

			case "message.list":
				// Never return messages
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"messages":    []map[string]any{},
						"total":       0,
						"unread":      0,
						"page":        1,
						"page_size":   1,
						"total_pages": 0,
					},
				}

			case "unsubscribe":
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result":  map[string]any{},
				}
			}

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	})

	time.Sleep(50 * time.Millisecond)

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	opts := WaitOptions{
		Timeout: 1 * time.Second,
	}

	message, err := Wait(client, opts)
	if err == nil {
		t.Fatal("Expected timeout error")
	}

	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("Expected timeout error, got: %v", err)
	}

	if message != nil {
		t.Error("Expected nil message on timeout")
	}
}

func TestWait_WithFilters(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	var subscribeParams map[string]any

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		// Handle multiple requests on same connection
		for {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				return
			}

			method, ok := request["method"].(string)
			if !ok {
				t.Error("method should be string")
				return
			}

			var response map[string]any

			switch method {
			case "subscribe":
				var ok bool
				subscribeParams, ok = request["params"].(map[string]any)
				if !ok {
					t.Error("params should be map[string]any")
					return
				}
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"subscription_id": "sub_123",
					},
				}

			case "message.list":
				// Return a message immediately
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"messages": []map[string]any{
							{
								"message_id": "msg_01HXE8Z7",
								"agent_id":   "agent:implementer:ABC123",
								"body": map[string]any{
									"format":  "markdown",
									"content": "Test message",
								},
								"created_at": time.Now().Format(time.RFC3339),
							},
						},
						"total":       1,
						"unread":      1,
						"page":        1,
						"page_size":   1,
						"total_pages": 1,
					},
				}

			case "unsubscribe":
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result":  map[string]any{},
				}
			}

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	})

	time.Sleep(50 * time.Millisecond)

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	opts := WaitOptions{
		Timeout: 5 * time.Second,
		Scope:   "module:auth",
		Mention: "@reviewer",
	}

	_, err = Wait(client, opts)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	// Verify subscribe params included filters
	if scope, ok := subscribeParams["scope"].(map[string]any); !ok {
		t.Error("Expected scope in subscribe params")
	} else {
		if scope["type"] != "module" || scope["value"] != "auth" {
			t.Errorf("Expected scope module:auth, got %v:%v", scope["type"], scope["value"])
		}
	}

	if mention, ok := subscribeParams["mention_role"].(string); !ok || mention != "reviewer" {
		t.Errorf("Expected mention_role 'reviewer', got %v", subscribeParams["mention_role"])
	}
}

func TestWait_WithAllFlag(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	var subscribeParams map[string]any

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		for {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				return
			}

			method, _ := request["method"].(string)

			var response map[string]any

			switch method {
			case "subscribe":
				subscribeParams, _ = request["params"].(map[string]any)
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"subscription_id": "sub_123",
					},
				}

			case "message.list":
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"messages": []map[string]any{
							{
								"message_id": "msg_broadcast",
								"agent_id":   "agent:coordinator:XYZ",
								"body": map[string]any{
									"format":  "markdown",
									"content": "Broadcast message",
								},
								"created_at": time.Now().Format(time.RFC3339Nano),
							},
						},
						"total":       1,
						"unread":      1,
						"page":        1,
						"page_size":   1,
						"total_pages": 1,
					},
				}

			case "unsubscribe":
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result":  map[string]any{},
				}
			}

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	})

	time.Sleep(50 * time.Millisecond)

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	opts := WaitOptions{
		Timeout: 5 * time.Second,
		All:     true,
	}

	message, err := Wait(client, opts)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	if message.MessageID != "msg_broadcast" {
		t.Errorf("Expected message_id 'msg_broadcast', got %s", message.MessageID)
	}

	// Verify "all" was passed in subscribe params
	if allVal, ok := subscribeParams["all"]; !ok || allVal != true {
		t.Errorf("Expected all=true in subscribe params, got %v", subscribeParams["all"])
	}
}

func TestWait_WithAfterFilter(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	callCount := 0
	afterTime := time.Now().Add(-10 * time.Second) // 10s ago

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		for {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				return
			}

			callCount++
			method, _ := request["method"].(string)

			var response map[string]any

			switch method {
			case "subscribe":
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"subscription_id": "sub_123",
					},
				}

			case "message.list":
				// First poll: return old message (before threshold)
				// Second poll: return new message (after threshold)
				var messages []map[string]any
				if callCount <= 3 {
					messages = []map[string]any{
						{
							"message_id": "msg_old",
							"agent_id":   "agent:old:XYZ",
							"body": map[string]any{
								"format":  "markdown",
								"content": "Old message",
							},
							"created_at": afterTime.Add(-1 * time.Minute).Format(time.RFC3339Nano),
						},
					}
				} else {
					messages = []map[string]any{
						{
							"message_id": "msg_new",
							"agent_id":   "agent:new:XYZ",
							"body": map[string]any{
								"format":  "markdown",
								"content": "New message",
							},
							"created_at": time.Now().Format(time.RFC3339Nano),
						},
					}
				}

				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result": map[string]any{
						"messages":    messages,
						"total":       len(messages),
						"unread":      len(messages),
						"page":        1,
						"page_size":   1,
						"total_pages": 1,
					},
				}

			case "unsubscribe":
				response = map[string]any{
					"jsonrpc": "2.0",
					"id":      request["id"],
					"result":  map[string]any{},
				}
			}

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	})

	time.Sleep(50 * time.Millisecond)

	client, err := NewClient(socketPath)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer func() { _ = client.Close() }()

	opts := WaitOptions{
		Timeout: 5 * time.Second,
		After:   afterTime,
	}

	message, err := Wait(client, opts)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	// Should have skipped old message and returned new one
	if message.MessageID != "msg_new" {
		t.Errorf("Expected message_id 'msg_new', got %s", message.MessageID)
	}
}
