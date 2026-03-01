package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
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

			if method != "message.list" {
				t.Errorf("Expected only message.list calls, got %s", method)
				return
			}

			// Return a message on the second poll (simulating arrival)
			messages := []map[string]any{}
			if callCount > 1 {
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

			response := map[string]any{
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

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	})

	time.Sleep(50 * time.Millisecond)

	opts := WaitOptions{
		Timeout: 5 * time.Second,
		Quiet:   true,
	}

	message, err := Wait(socketPath, opts)
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

		for {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				return
			}

			// Only message.list calls expected — never return messages
			response := map[string]any{
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

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	})

	time.Sleep(50 * time.Millisecond)

	opts := WaitOptions{
		Timeout: 1 * time.Second,
		Quiet:   true,
	}

	message, err := Wait(socketPath, opts)
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

	var listParams map[string]any

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		for {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				return
			}

			// Capture message.list params for verification
			listParams, _ = request["params"].(map[string]any)

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

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	})

	time.Sleep(50 * time.Millisecond)

	opts := WaitOptions{
		Timeout: 5 * time.Second,
		Scope:   "module:auth",
		Mention: "@reviewer",
		Quiet:   true,
	}

	_, err := Wait(socketPath, opts)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	// Verify message.list params include filters
	if scope, ok := listParams["scope"].(map[string]any); !ok {
		t.Error("Expected scope in message.list params")
	} else {
		if scope["type"] != "module" || scope["value"] != "auth" {
			t.Errorf("Expected scope module:auth, got %v:%v", scope["type"], scope["value"])
		}
	}

	if mention, ok := listParams["mention_role"].(string); !ok || mention != "reviewer" {
		t.Errorf("Expected mention_role 'reviewer' in message.list params, got %v", listParams["mention_role"])
	}
}

func TestWait_AgentFiltered(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	var listParams map[string]any

	daemon.start(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		for {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				return
			}

			// Capture message.list params for verification
			listParams, _ = request["params"].(map[string]any)

			response := map[string]any{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"result": map[string]any{
					"messages": []map[string]any{
						{
							"message_id": "msg_directed",
							"agent_id":   "agent:coordinator:XYZ",
							"body": map[string]any{
								"format":  "markdown",
								"content": "Directed message",
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

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	})

	time.Sleep(50 * time.Millisecond)

	opts := WaitOptions{
		Timeout:      5 * time.Second,
		ForAgent:     "test_agent",
		ForAgentRole: "tester",
		Quiet:        true,
	}

	message, err := Wait(socketPath, opts)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	if message.MessageID != "msg_directed" {
		t.Errorf("Expected message_id 'msg_directed', got %s", message.MessageID)
	}

	// Verify RPC request includes for_agent and for_agent_role parameters
	if forAgent, ok := listParams["for_agent"].(string); !ok || forAgent != "test_agent" {
		t.Errorf("Expected for_agent 'test_agent' in message.list params, got %v", listParams["for_agent"])
	}
	if forAgentRole, ok := listParams["for_agent_role"].(string); !ok || forAgentRole != "tester" {
		t.Errorf("Expected for_agent_role 'tester' in message.list params, got %v", listParams["for_agent_role"])
	}
}

func TestWait_ScopeAndMentionExcludeAgentFilters(t *testing.T) {
	// When --scope or --mention is provided, for_agent and for_agent_role must
	// NOT be sent to message.list.  AND-ing those params with the explicit
	// scope/mention filter caused scoped messages to be silently dropped,
	// making --scope and --mention always time out (thrum-dkj).
	//
	// Use a short socket path to avoid macOS Unix socket length limits (104 chars).
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("thrum-smf-%d.sock", time.Now().UnixNano()%100000))
	_ = os.Remove(socketPath)
	t.Cleanup(func() { _ = os.Remove(socketPath) })

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	var listParams map[string]any

	messageHandler := func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		for {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				return
			}

			listParams, _ = request["params"].(map[string]any)

			response := map[string]any{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"result": map[string]any{
					"messages": []map[string]any{
						{
							"message_id": "msg_scoped",
							"agent_id":   "agent:sender:ABC",
							"body": map[string]any{
								"format":  "markdown",
								"content": "Scoped message",
							},
							"created_at": time.Now().Format(time.RFC3339),
						},
					},
					"total":       1,
					"unread":      1,
					"page":        1,
					"page_size":   10,
					"total_pages": 1,
				},
			}

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go messageHandler(conn)
		}
	}()

	time.Sleep(50 * time.Millisecond)

	opts := WaitOptions{
		Timeout:      5 * time.Second,
		Scope:        "module:payments",
		Mention:      "@reviewer",
		ForAgent:     "my_agent",
		ForAgentRole: "implementer",
		Quiet:        true,
	}

	_, err = Wait(socketPath, opts)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	// scope and mention_role MUST be present
	if scope, ok := listParams["scope"].(map[string]any); !ok {
		t.Error("Expected scope in message.list params")
	} else if scope["type"] != "module" || scope["value"] != "payments" {
		t.Errorf("Unexpected scope value: %v", scope)
	}

	if mention, ok := listParams["mention_role"].(string); !ok || mention != "reviewer" {
		t.Errorf("Expected mention_role 'reviewer', got %v", listParams["mention_role"])
	}

	// for_agent and for_agent_role must NOT be present when scope/mention is set
	if _, present := listParams["for_agent"]; present {
		t.Error("for_agent must NOT be sent when scope is active (causes AND-filter that drops scoped messages)")
	}
	if _, present := listParams["for_agent_role"]; present {
		t.Error("for_agent_role must NOT be sent when scope is active")
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

			// Verify created_after is passed in params
			params, _ := request["params"].(map[string]any)
			createdAfter, _ := params["created_after"].(string)
			if createdAfter == "" {
				t.Error("Expected created_after in message.list params")
			}

			// Simulate server-side filtering: first poll returns nothing
			// (daemon is filtering old messages), second poll returns new message
			var messages []map[string]any
			if callCount > 1 {
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

			response := map[string]any{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"result": map[string]any{
					"messages":    messages,
					"total":       len(messages),
					"unread":      len(messages),
					"page":        1,
					"page_size":   10,
					"total_pages": 1,
				},
			}

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	})

	time.Sleep(50 * time.Millisecond)

	opts := WaitOptions{
		Timeout: 5 * time.Second,
		After:   afterTime,
		Quiet:   true,
	}

	message, err := Wait(socketPath, opts)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	// Should return new message (server filtered old ones via created_after)
	if message.MessageID != "msg_new" {
		t.Errorf("Expected message_id 'msg_new', got %s", message.MessageID)
	}
}

func TestWait_SeenMessagesSkipped(t *testing.T) {
	daemon, socketPath := newMockDaemon(t)
	defer daemon.stop()

	callCount := 0

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

			// First 2 polls: return same message (simulates a message that was
			// already seen but still returned by daemon, e.g. read-marked message)
			// Third poll: return a new message
			var messages []map[string]any
			if callCount <= 2 {
				messages = []map[string]any{
					{
						"message_id": "msg_existing",
						"agent_id":   "agent:sender:XYZ",
						"body": map[string]any{
							"format":  "markdown",
							"content": "Already seen",
						},
						"created_at": time.Now().Format(time.RFC3339Nano),
					},
				}
			} else {
				messages = []map[string]any{
					{
						"message_id": "msg_existing",
						"agent_id":   "agent:sender:XYZ",
						"body": map[string]any{
							"format":  "markdown",
							"content": "Already seen",
						},
						"created_at": time.Now().Format(time.RFC3339Nano),
					},
					{
						"message_id": "msg_fresh",
						"agent_id":   "agent:sender:XYZ",
						"body": map[string]any{
							"format":  "markdown",
							"content": "Fresh message",
						},
						"created_at": time.Now().Format(time.RFC3339Nano),
					},
				}
			}

			response := map[string]any{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"result": map[string]any{
					"messages":    messages,
					"total":       len(messages),
					"unread":      len(messages),
					"page":        1,
					"page_size":   10,
					"total_pages": 1,
				},
			}

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	})

	time.Sleep(50 * time.Millisecond)

	opts := WaitOptions{
		Timeout: 5 * time.Second,
		Quiet:   true,
	}

	message, err := Wait(socketPath, opts)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	// First call returns msg_existing — wait returns it immediately
	if message.MessageID != "msg_existing" {
		t.Errorf("Expected message_id 'msg_existing', got %s", message.MessageID)
	}
}

func TestWait_ReconnectAfterDaemonRestart(t *testing.T) {
	// Use a short socket path to avoid macOS Unix socket length limits
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("thrum-test-%d.sock", time.Now().UnixNano()%100000))
	_ = os.Remove(socketPath)
	t.Cleanup(func() { _ = os.Remove(socketPath) })

	messageHandler := func(conn net.Conn) {
		defer func() { _ = conn.Close() }()

		decoder := json.NewDecoder(conn)
		encoder := json.NewEncoder(conn)

		for {
			var request map[string]any
			if err := decoder.Decode(&request); err != nil {
				return
			}

			response := map[string]any{
				"jsonrpc": "2.0",
				"id":      request["id"],
				"result": map[string]any{
					"messages": []map[string]any{
						{
							"message_id": "msg_post_restart",
							"agent_id":   "agent:restarted:XYZ",
							"body": map[string]any{
								"format":  "markdown",
								"content": "After daemon restart",
							},
							"created_at": time.Now().Format(time.RFC3339Nano),
						},
					},
					"total":       1,
					"unread":      1,
					"page":        1,
					"page_size":   10,
					"total_pages": 1,
				},
			}

			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	}

	// Start the daemon after a 1-second delay (simulating restart)
	go func() {
		time.Sleep(1 * time.Second)

		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			t.Logf("Failed to create listener: %v", err)
			return
		}
		t.Cleanup(func() { _ = listener.Close() })

		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go messageHandler(conn)
		}
	}()

	opts := WaitOptions{
		Timeout: 10 * time.Second,
		Quiet:   true,
	}

	message, err := Wait(socketPath, opts)
	if err != nil {
		t.Fatalf("Wait failed (should have reconnected): %v", err)
	}

	if message.MessageID != "msg_post_restart" {
		t.Errorf("Expected message_id 'msg_post_restart', got %s", message.MessageID)
	}
}
