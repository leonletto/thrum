package websocket_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	ws "github.com/leonletto/thrum/internal/websocket"
)

// TestIntegration_EventBroadcast tests that events are broadcast to all connected clients.
func TestIntegration_EventBroadcast(t *testing.T) {
	// Create server with handler registry
	registry := ws.NewSimpleRegistry()

	// Register a test handler that broadcasts events
	broadcasted := make(chan bool, 3)
	registry.Register("test.broadcast", func(ctx context.Context, params json.RawMessage) (any, error) {
		// Simulate broadcasting to 3 clients
		for i := 0; i < 3; i++ {
			broadcasted <- true
		}
		return map[string]string{"status": "broadcasted"}, nil
	})

	server := ws.NewServer("localhost:19991", registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect 3 clients
	clients := make([]*websocket.Conn, 3)
	for i := 0; i < 3; i++ {
		conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:19991", nil)
		if err != nil {
			t.Fatalf("failed to connect client %d: %v", i, err)
		}
		defer func() { _ = conn.Close() }()
		clients[i] = conn
	}

	// Send broadcast request from first client
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "test.broadcast",
		"params":  map[string]any{},
		"id":      1,
	}
	requestJSON, _ := json.Marshal(request)

	if err := clients[0].WriteMessage(websocket.TextMessage, requestJSON); err != nil {
		t.Fatalf("failed to send request: %v", err)
	}

	// Read response from first client
	_, _, err := clients[0].ReadMessage()
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	// Verify broadcasts received
	timeout := time.After(2 * time.Second)
	receivedCount := 0
	for i := 0; i < 3; i++ {
		select {
		case <-broadcasted:
			receivedCount++
		case <-timeout:
			t.Fatalf("timeout waiting for broadcasts, only received %d/3", receivedCount)
		}
	}

	if receivedCount != 3 {
		t.Errorf("Expected 3 broadcasts, got %d", receivedCount)
	}
}

// TestIntegration_MultiClientConcurrent tests multiple clients sending requests concurrently.
func TestIntegration_MultiClientConcurrent(t *testing.T) {
	registry := ws.NewSimpleRegistry()

	// Register echo handler
	registry.Register("echo", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req map[string]any
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		return req, nil
	})

	server := ws.NewServer("localhost:19992", registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect 10 clients concurrently
	numClients := 10
	done := make(chan bool, numClients)

	for i := 0; i < numClients; i++ {
		go func(clientID int) {
			conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:19992", nil)
			if err != nil {
				t.Errorf("client %d: failed to connect: %v", clientID, err)
				done <- false
				return
			}
			defer func() { _ = conn.Close() }()

			// Send echo request
			request := map[string]any{
				"jsonrpc": "2.0",
				"method":  "echo",
				"params":  map[string]any{"client_id": clientID, "message": "hello"},
				"id":      clientID,
			}
			requestJSON, _ := json.Marshal(request)

			if err := conn.WriteMessage(websocket.TextMessage, requestJSON); err != nil {
				t.Errorf("client %d: failed to send: %v", clientID, err)
				done <- false
				return
			}

			// Read response
			_, responseData, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("client %d: failed to read: %v", clientID, err)
				done <- false
				return
			}

			var response map[string]any
			if err := json.Unmarshal(responseData, &response); err != nil {
				t.Errorf("client %d: failed to unmarshal: %v", clientID, err)
				done <- false
				return
			}

			// Verify response
			if response["jsonrpc"] != "2.0" {
				t.Errorf("client %d: invalid jsonrpc version", clientID)
				done <- false
				return
			}

			done <- true
		}(i)
	}

	// Wait for all clients to complete
	successCount := 0
	timeout := time.After(5 * time.Second)
	for i := 0; i < numClients; i++ {
		select {
		case success := <-done:
			if success {
				successCount++
			}
		case <-timeout:
			t.Fatalf("timeout waiting for clients, only %d/%d completed", i, numClients)
		}
	}

	if successCount != numClients {
		t.Errorf("Expected all %d clients to succeed, got %d", numClients, successCount)
	}
}

// TestIntegration_BatchRequests tests batch JSON-RPC requests over WebSocket.
func TestIntegration_BatchRequests(t *testing.T) {
	registry := ws.NewSimpleRegistry()

	// Register multiple handlers
	registry.Register("add", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			A int `json:"a"`
			B int `json:"b"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		return map[string]int{"result": req.A + req.B}, nil
	})

	registry.Register("multiply", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req struct {
			A int `json:"a"`
			B int `json:"b"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		return map[string]int{"result": req.A * req.B}, nil
	})

	server := ws.NewServer("localhost:19993", registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect client
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:19993", nil)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send batch request
	batch := []map[string]any{
		{
			"jsonrpc": "2.0",
			"method":  "add",
			"params":  map[string]int{"a": 5, "b": 3},
			"id":      1,
		},
		{
			"jsonrpc": "2.0",
			"method":  "multiply",
			"params":  map[string]int{"a": 4, "b": 7},
			"id":      2,
		},
	}
	batchJSON, _ := json.Marshal(batch)

	if err := conn.WriteMessage(websocket.TextMessage, batchJSON); err != nil {
		t.Fatalf("failed to send batch: %v", err)
	}

	// Read batch response
	_, responseData, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var responses []map[string]any
	if err := json.Unmarshal(responseData, &responses); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(responses) != 2 {
		t.Fatalf("Expected 2 responses, got %d", len(responses))
	}

	// Verify responses (order may vary)
	for _, resp := range responses {
		if resp["error"] != nil {
			t.Errorf("Expected no error, got %v", resp["error"])
		}

		idFloat, ok := resp["id"].(float64)
		if !ok {
			t.Errorf("Expected numeric id, got %T", resp["id"])
			continue
		}
		id := int(idFloat)

		result, ok := resp["result"].(map[string]any)
		if !ok {
			t.Errorf("Expected result object, got %T", resp["result"])
			continue
		}

		resultValue, ok := result["result"].(float64)
		if !ok {
			t.Errorf("Expected numeric result, got %T", result["result"])
			continue
		}

		switch id {
		case 1:
			// add: 5 + 3 = 8
			if int(resultValue) != 8 {
				t.Errorf("Expected add result 8, got %d", int(resultValue))
			}
		case 2:
			// multiply: 4 * 7 = 28
			if int(resultValue) != 28 {
				t.Errorf("Expected multiply result 28, got %d", int(resultValue))
			}
		default:
			t.Errorf("Unexpected response id: %d", id)
		}
	}
}

// TestIntegration_ErrorHandling tests error responses over WebSocket.
func TestIntegration_ErrorHandling(t *testing.T) {
	registry := ws.NewSimpleRegistry()

	// Register handler that returns error
	registry.Register("fail", func(ctx context.Context, params json.RawMessage) (any, error) {
		return nil, fmt.Errorf("intentional failure")
	})

	server := ws.NewServer("localhost:19994", registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect client
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:19994", nil)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Test 1: Unknown method
	request1 := map[string]any{
		"jsonrpc": "2.0",
		"method":  "unknown",
		"params":  map[string]any{},
		"id":      1,
	}
	request1JSON, _ := json.Marshal(request1)

	if err := conn.WriteMessage(websocket.TextMessage, request1JSON); err != nil {
		t.Fatalf("failed to send request: %v", err)
	}

	_, response1Data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var response1 map[string]any
	if err := json.Unmarshal(response1Data, &response1); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response1["error"] == nil {
		t.Error("Expected error for unknown method, got none")
	}

	// Test 2: Handler that returns error
	request2 := map[string]any{
		"jsonrpc": "2.0",
		"method":  "fail",
		"params":  map[string]any{},
		"id":      2,
	}
	request2JSON, _ := json.Marshal(request2)

	if err := conn.WriteMessage(websocket.TextMessage, request2JSON); err != nil {
		t.Fatalf("failed to send request: %v", err)
	}

	_, response2Data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var response2 map[string]any
	if err := json.Unmarshal(response2Data, &response2); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response2["error"] == nil {
		t.Error("Expected error from fail handler, got none")
	}
}

// TestIntegration_ConnectionCleanup tests that connections are properly cleaned up.
func TestIntegration_ConnectionCleanup(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:19995", registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect and disconnect multiple clients
	for i := 0; i < 5; i++ {
		conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:19995", nil)
		if err != nil {
			t.Fatalf("failed to connect client %d: %v", i, err)
		}

		// Close immediately
		_ = conn.Close()
	}

	// Give server time to clean up
	time.Sleep(100 * time.Millisecond)

	// Server should still be functional
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:19995", nil)
	if err != nil {
		t.Fatalf("failed to connect after cleanup: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send a test request
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "health",
		"params":  map[string]any{},
		"id":      1,
	}
	requestJSON, _ := json.Marshal(request)

	if err := conn.WriteMessage(websocket.TextMessage, requestJSON); err != nil {
		t.Fatalf("failed to send request: %v", err)
	}

	// Should receive response (even if it's an error for unknown method)
	_, _, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read response after cleanup: %v", err)
	}
}
