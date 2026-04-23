package websocket_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
	ws "github.com/leonletto/thrum/internal/websocket"
)

// checkOriginCases exercises the pure checkOrigin logic via the exported
// test helper. Tests are run without a live server.
func TestCheckOrigin(t *testing.T) {
	allowedOrigins := ws.AllowedOriginsForPort(9999)

	cases := []struct {
		name   string
		origin string
		want   bool
	}{
		{"empty origin allowed (loopback-friendly)", "", true},
		{"http localhost allowed", "http://localhost:9999", true},
		{"http 127.0.0.1 allowed", "http://127.0.0.1:9999", true},
		{"ws localhost allowed", "ws://localhost:9999", true},
		{"ws 127.0.0.1 allowed", "ws://127.0.0.1:9999", true},
		{"https localhost rejected", "https://localhost:9999", false},
		{"foreign origin rejected", "https://evil.example", false},
		{"foreign http origin rejected", "http://evil.example:9999", false},
		{"wrong port rejected", "http://localhost:8080", false},
		{"non-loopback http rejected even if port matches", "http://192.168.1.1:9999", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ws.CheckOrigin(allowedOrigins, tc.origin)
			if got != tc.want {
				t.Errorf("CheckOrigin(%q) = %v, want %v", tc.origin, got, tc.want)
			}
		})
	}
}

func TestServerLifecycle(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9998", registry, nil)
	ctx := context.Background()

	// Start server
	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Verify server is listening
	if server.Addr() != "localhost:9998" {
		t.Fatalf("expected addr localhost:9998, got %s", server.Addr())
	}

	// Stop server
	if err := server.Stop(); err != nil {
		t.Fatalf("failed to stop server: %v", err)
	}
}

func TestServerPort(t *testing.T) {
	testCases := []struct {
		name     string
		addr     string
		expected int
	}{
		{"standard port", "localhost:9999", 9999},
		{"different port", "localhost:8080", 8080},
		{"ip address", "127.0.0.1:3000", 3000},
		{"all interfaces", "0.0.0.0:5555", 5555},
		{"ipv6 localhost", "[::1]:7777", 7777},
		{"invalid no port", "localhost", 0},
		{"invalid empty", "", 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			registry := ws.NewSimpleRegistry()
			server := ws.NewServer(tc.addr, registry, nil)
			if got := server.Port(); got != tc.expected {
				t.Errorf("Port() = %d, expected %d", got, tc.expected)
			}
		})
	}
}

func TestWebSocketConnection(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9997", registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9997/", nil)
	if err != nil {
		t.Fatalf("failed to connect to WebSocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Connection successful
}

func TestHandlerRegistration(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9996", registry, nil)
	ctx := context.Background()

	called := false
	registry.Register("test_method", func(ctx context.Context, params json.RawMessage) (any, error) {
		called = true
		return map[string]string{"status": "ok"}, nil
	})

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9996/", nil)
	if err != nil {
		t.Fatalf("failed to connect to WebSocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send JSON-RPC request
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "test_method",
		"params":  map[string]any{},
		"id":      1,
	}
	if err := conn.WriteJSON(request); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// Read response
	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	// Verify response
	if resp["jsonrpc"] != "2.0" {
		t.Fatalf("expected jsonrpc 2.0, got %v", resp["jsonrpc"])
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected error in response: %v", resp["error"])
	}

	if !called {
		t.Fatal("handler was not called")
	}
}

func TestUnknownMethod(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9995", registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9995/", nil)
	if err != nil {
		t.Fatalf("failed to connect to WebSocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send request with unknown method
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "unknown_method",
		"id":      1,
	}
	if err := conn.WriteJSON(request); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// Read response
	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	// Verify error response
	if resp["error"] == nil {
		t.Fatal("expected error in response")
	}

	errorMap, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field is not a map: %T", resp["error"])
	}
	code, ok := errorMap["code"].(float64)
	if !ok {
		t.Fatalf("code field is not a number: %T", errorMap["code"])
	}
	if code != -32601 {
		t.Fatalf("expected error code -32601, got %v", code)
	}
}

func TestInvalidJSONRPC(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9994", registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9994/", nil)
	if err != nil {
		t.Fatalf("failed to connect to WebSocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send request with wrong jsonrpc version
	request := map[string]any{
		"jsonrpc": "1.0",
		"method":  "test",
		"id":      1,
	}
	if err := conn.WriteJSON(request); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// Read response
	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	// Verify error response
	if resp["error"] == nil {
		t.Fatal("expected error in response")
	}

	errorMap, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field is not a map: %T", resp["error"])
	}
	code, ok := errorMap["code"].(float64)
	if !ok {
		t.Fatalf("code field is not a number: %T", errorMap["code"])
	}
	if code != -32600 {
		t.Fatalf("expected error code -32600, got %v", code)
	}
}

func TestHandlerError(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9993", registry, nil)
	ctx := context.Background()

	// Register handler that returns an error
	registry.Register("error_method", func(ctx context.Context, params json.RawMessage) (any, error) {
		return nil, fmt.Errorf("intentional error")
	})

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9993/", nil)
	if err != nil {
		t.Fatalf("failed to connect to WebSocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send JSON-RPC request
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "error_method",
		"id":      1,
	}
	if err := conn.WriteJSON(request); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// Read response
	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	// Verify error response
	if resp["error"] == nil {
		t.Fatal("expected error in response")
	}

	errorMap, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field is not a map: %T", resp["error"])
	}
	code, ok := errorMap["code"].(float64)
	if !ok {
		t.Fatalf("code field is not a number: %T", errorMap["code"])
	}
	if code != -32000 {
		t.Fatalf("expected error code -32000, got %v", code)
	}

	if errorMap["message"] != "intentional error" {
		t.Fatalf("expected error message 'intentional error', got %v", errorMap["message"])
	}
}

func TestMultipleConcurrentConnections(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9992", registry, nil)
	ctx := context.Background()

	var callCount atomic.Int32
	registry.Register("ping", func(ctx context.Context, params json.RawMessage) (any, error) {
		callCount.Add(1)
		return map[string]string{"status": "pong"}, nil
	})

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Create 5 concurrent connections
	numConns := 5
	done := make(chan bool, numConns)

	for i := range numConns {
		go func(clientID int) {
			defer func() { done <- true }()

			// Connect to WebSocket
			conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9992/", nil)
			if err != nil {
				t.Errorf("client %d: failed to connect: %v", clientID, err)
				return
			}
			defer func() { _ = conn.Close() }()

			// Send ping request
			request := map[string]any{
				"jsonrpc": "2.0",
				"method":  "ping",
				"id":      clientID,
			}
			if err := conn.WriteJSON(request); err != nil {
				t.Errorf("client %d: failed to write request: %v", clientID, err)
				return
			}

			// Read response
			var resp map[string]any
			if err := conn.ReadJSON(&resp); err != nil {
				t.Errorf("client %d: failed to read response: %v", clientID, err)
				return
			}

			// Verify response
			if resp["error"] != nil {
				t.Errorf("client %d: unexpected error: %v", clientID, resp["error"])
			}
		}(i)
	}

	// Wait for all connections to finish
	for range numConns {
		<-done
	}

	// Give time for all handlers to complete
	time.Sleep(100 * time.Millisecond)

	if got := callCount.Load(); got != int32(numConns) {
		t.Fatalf("expected %d handler calls, got %d", numConns, got)
	}
}

func TestParseError(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9991", registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9991/", nil)
	if err != nil {
		t.Fatalf("failed to connect to WebSocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send invalid JSON
	if err := conn.WriteMessage(websocket.TextMessage, []byte("{invalid json}")); err != nil {
		t.Fatalf("failed to write message: %v", err)
	}

	// Read response
	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	// Verify parse error
	if resp["error"] == nil {
		t.Fatal("expected error in response")
	}

	errorMap, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field is not a map: %T", resp["error"])
	}
	code, ok := errorMap["code"].(float64)
	if !ok {
		t.Fatalf("code field is not a number: %T", errorMap["code"])
	}
	if code != -32700 {
		t.Fatalf("expected error code -32700 (parse error), got %v", code)
	}
}

func TestClientRegistry(t *testing.T) {
	clientRegistry := ws.NewClientRegistry()

	if clientRegistry.Count() != 0 {
		t.Fatalf("expected 0 clients, got %d", clientRegistry.Count())
	}

	// Create mock connection (we won't use it, just need the type)
	handlerRegistry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9990", handlerRegistry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect to get a real connection object
	wsConn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9990/", nil)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = wsConn.Close() }()

	// We can't directly create a ws.Connection from outside the package,
	// so this test just verifies the registry interface
	if count := clientRegistry.Count(); count != 0 {
		t.Fatalf("expected 0 clients after setup, got %d", count)
	}
}

func TestRequestWithParams(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9989", registry, nil)
	ctx := context.Background()

	var receivedParams map[string]any
	registry.Register("echo", func(ctx context.Context, params json.RawMessage) (any, error) {
		if err := json.Unmarshal(params, &receivedParams); err != nil {
			return nil, err
		}
		return receivedParams, nil
	})

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9989/", nil)
	if err != nil {
		t.Fatalf("failed to connect to WebSocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send request with params
	testParams := map[string]any{
		"message": "hello",
		"count":   42,
	}
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "echo",
		"params":  testParams,
		"id":      1,
	}
	if err := conn.WriteJSON(request); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// Read response
	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	// Verify response
	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}

	// Verify params were received and echoed back
	if receivedParams["message"] != "hello" {
		t.Fatalf("expected message 'hello', got %v", receivedParams["message"])
	}
	count, ok := receivedParams["count"].(float64)
	if !ok {
		t.Fatalf("count field is not a number: %T", receivedParams["count"])
	}
	if count != 42 {
		t.Fatalf("expected count 42, got %v", count)
	}
}

func TestRequestWithNilParams(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9986", registry, nil)
	ctx := context.Background()

	// Handler that unmarshals params into a struct (like agent.list does).
	// This previously failed when the client omitted the "params" field,
	// because json.Unmarshal(nil, &v) returns an error.
	type listReq struct {
		Role string `json:"role"`
	}
	registry.Register("agent.list", func(ctx context.Context, params json.RawMessage) (any, error) {
		var req listReq
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		return map[string]any{"agents": []string{}, "filter": req.Role}, nil
	})

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9986/", nil)
	if err != nil {
		t.Fatalf("failed to connect to WebSocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send request WITHOUT params field (like JS client does when params is undefined)
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "agent.list",
		"id":      1,
	}
	if err := conn.WriteJSON(request); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	if resp["error"] != nil {
		t.Fatalf("expected success but got error: %v", resp["error"])
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result to be an object, got %T", resp["result"])
	}
	if result["filter"] != "" {
		t.Fatalf("expected empty filter, got %v", result["filter"])
	}
}

func TestBatchRequest(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9988", registry, nil)
	ctx := context.Background()

	// Register test handlers
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

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9988/", nil)
	if err != nil {
		t.Fatalf("failed to connect to WebSocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send batch request
	batchRequest := []map[string]any{
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
		{
			"jsonrpc": "2.0",
			"method":  "unknown_method",
			"id":      3,
		},
	}

	if err := conn.WriteJSON(batchRequest); err != nil {
		t.Fatalf("failed to write batch request: %v", err)
	}

	// Read batch response
	var batchResp []map[string]any
	if err := conn.ReadJSON(&batchResp); err != nil {
		t.Fatalf("failed to read batch response: %v", err)
	}

	// Verify we got 3 responses
	if len(batchResp) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(batchResp))
	}

	// Verify first response (add)
	if batchResp[0]["error"] != nil {
		t.Fatalf("expected no error for add, got %v", batchResp[0]["error"])
	}
	addResult, ok := batchResp[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is not a map: %T", batchResp[0]["result"])
	}
	addValue, ok := addResult["result"].(float64)
	if !ok {
		t.Fatalf("result value is not a number: %T", addResult["result"])
	}
	if addValue != 8 {
		t.Fatalf("expected add result 8, got %v", addValue)
	}

	// Verify second response (multiply)
	if batchResp[1]["error"] != nil {
		t.Fatalf("expected no error for multiply, got %v", batchResp[1]["error"])
	}
	multiplyResult, ok := batchResp[1]["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is not a map: %T", batchResp[1]["result"])
	}
	multiplyValue, ok := multiplyResult["result"].(float64)
	if !ok {
		t.Fatalf("result value is not a number: %T", multiplyResult["result"])
	}
	if multiplyValue != 28 {
		t.Fatalf("expected multiply result 28, got %v", multiplyValue)
	}

	// Verify third response (error)
	if batchResp[2]["error"] == nil {
		t.Fatal("expected error for unknown method")
	}
	errorMap, ok := batchResp[2]["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field is not a map: %T", batchResp[2]["error"])
	}
	code, ok := errorMap["code"].(float64)
	if !ok {
		t.Fatalf("code field is not a number: %T", errorMap["code"])
	}
	if code != -32601 {
		t.Fatalf("expected error code -32601, got %v", code)
	}
}

func TestEmptyBatchRequest(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9987", registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9987/", nil)
	if err != nil {
		t.Fatalf("failed to connect to WebSocket: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send empty batch request
	emptyBatch := []map[string]any{}
	if err := conn.WriteJSON(emptyBatch); err != nil {
		t.Fatalf("failed to write empty batch: %v", err)
	}

	// Read error response
	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	// Verify error
	if resp["error"] == nil {
		t.Fatal("expected error for empty batch")
	}

	errorMap, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field is not a map: %T", resp["error"])
	}
	code, ok := errorMap["code"].(float64)
	if !ok {
		t.Fatalf("code field is not a number: %T", errorMap["code"])
	}
	if code != -32600 {
		t.Fatalf("expected error code -32600 (invalid request), got %v", code)
	}
}

func TestWebSocketAtWsPathWithUI(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	registry.Register("ping", func(ctx context.Context, params json.RawMessage) (any, error) {
		return map[string]string{"status": "pong"}, nil
	})

	// Create a mock filesystem with index.html
	uiFS := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<!DOCTYPE html><html><body>Test UI</body></html>"),
		},
	}

	server := ws.NewServer("localhost:9986", registry, uiFS)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect to WebSocket at /ws
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9986/ws", nil)
	if err != nil {
		t.Fatalf("failed to connect to WebSocket at /ws: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send JSON-RPC request
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "ping",
		"params":  map[string]any{},
		"id":      1,
	}
	if err := conn.WriteJSON(request); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// Read response
	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is not a map: %T", resp["result"])
	}
	if result["status"] != "pong" {
		t.Fatalf("expected status pong, got %v", result["status"])
	}
}

func TestServer_TokenAuth_ValidToken(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	validator := func(token string) bool { return token == "valid-token" }
	server := ws.NewServer("localhost:9983", registry, nil, ws.WithTokenValidator(validator))
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect with valid token via Authorization header — should succeed.
	headers := http.Header{"Authorization": []string{"Bearer valid-token"}}
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9983/", headers)
	if err != nil {
		t.Fatalf("expected successful connection with valid token, got: %v", err)
	}
	_ = conn.Close()
}

func TestServer_TokenAuth_InvalidToken(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	validator := func(token string) bool { return token == "valid-token" }
	server := ws.NewServer("localhost:9982", registry, nil, ws.WithTokenValidator(validator))
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect with wrong token via Authorization header — should get 401.
	headers := http.Header{"Authorization": []string{"Bearer wrong"}}
	_, resp, err := websocket.DefaultDialer.Dial("ws://localhost:9982/", headers)
	if err == nil {
		t.Fatal("expected connection to be rejected, but it succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("expected HTTP 401, got %d (err: %v)", status, err)
	}
}

func TestServer_TokenAuth_NoToken_Localhost_Allowed(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	validator := func(token string) bool { return token == "valid-token" }
	server := ws.NewServer("localhost:9981", registry, nil, ws.WithTokenValidator(validator))
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Connect from localhost without token — loopback should be allowed
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9981/", nil)
	if err != nil {
		t.Fatalf("expected localhost connection without token to be allowed, got: %v", err)
	}
	_ = conn.Close()
}

func TestServer_TokenAuth_NoValidator_AllAllowed(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	// No WithTokenValidator — all connections should be accepted
	server := ws.NewServer("localhost:9980", registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9980/", nil)
	if err != nil {
		t.Fatalf("expected connection to be accepted when no validator configured, got: %v", err)
	}
	_ = conn.Close()
}

func TestSPAFallbackServesIndexHTML(t *testing.T) {
	registry := ws.NewSimpleRegistry()

	indexContent := "<!DOCTYPE html><html><body>SPA Root</body></html>"
	uiFS := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(indexContent),
		},
		"assets/app.js": &fstest.MapFile{
			Data: []byte("console.log('app')"),
		},
	}

	server := ws.NewServer("localhost:9985", registry, uiFS)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Test 1: Root path serves index.html
	resp, err := http.Get("http://localhost:9985/")
	if err != nil {
		t.Fatalf("failed to GET /: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if string(body) != indexContent {
		t.Fatalf("expected index.html content at /, got %q", string(body))
	}
	if resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("expected text/html content-type, got %s", resp.Header.Get("Content-Type"))
	}
	if resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("expected no-cache for index.html, got %s", resp.Header.Get("Cache-Control"))
	}

	// Test 2: Unknown path serves index.html (SPA fallback)
	resp, err = http.Get("http://localhost:9985/some/deep/route")
	if err != nil {
		t.Fatalf("failed to GET /some/deep/route: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if string(body) != indexContent {
		t.Fatalf("expected index.html content at /some/deep/route, got %q", string(body))
	}

	// Test 3: Assets path serves the actual file with cache headers
	resp, err = http.Get("http://localhost:9985/assets/app.js")
	if err != nil {
		t.Fatalf("failed to GET /assets/app.js: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if string(body) != "console.log('app')" {
		t.Fatalf("expected app.js content, got %q", string(body))
	}
	if resp.Header.Get("Cache-Control") != "max-age=31536000, immutable" {
		t.Fatalf("expected immutable cache for assets, got %s", resp.Header.Get("Cache-Control"))
	}
}

// TestCheckOriginAllowlist_ForeignOriginRejected verifies that a foreign
// Origin header causes the WebSocket handshake to fail with HTTP 403.
func TestCheckOriginAllowlist_ForeignOriginRejected(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9979", registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	headers := http.Header{"Origin": []string{"https://evil.example"}}
	_, resp, err := websocket.DefaultDialer.Dial("ws://localhost:9979/", headers)
	if err == nil {
		t.Fatal("expected connection with foreign origin to be rejected, but it succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("expected HTTP 403 for foreign origin, got %d (err: %v)", status, err)
	}
}

// TestCheckOriginAllowlist_LocalhostHttpOriginAccepted verifies that a
// browser-style http://localhost:{port} Origin is accepted.
func TestCheckOriginAllowlist_LocalhostHttpOriginAccepted(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	const port = "9978"
	server := ws.NewServer("localhost:"+port, registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	headers := http.Header{"Origin": []string{"http://localhost:" + port}}
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:"+port+"/", headers)
	if err != nil {
		t.Fatalf("expected http://localhost:%s origin to be accepted, got: %v", port, err)
	}
	_ = conn.Close()
}

// TestCheckOriginAllowlist_Loopback127HttpOriginAccepted verifies that a
// browser-style http://127.0.0.1:{port} Origin is accepted.
func TestCheckOriginAllowlist_Loopback127HttpOriginAccepted(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	const port = "9977"
	server := ws.NewServer("localhost:"+port, registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	headers := http.Header{"Origin": []string{"http://127.0.0.1:" + port}}
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:"+port+"/", headers)
	if err != nil {
		t.Fatalf("expected http://127.0.0.1:%s origin to be accepted, got: %v", port, err)
	}
	_ = conn.Close()
}

// TestCheckOriginAllowlist_WsOriginAccepted verifies that a ws:// loopback
// Origin header is accepted.
func TestCheckOriginAllowlist_WsOriginAccepted(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	const port = "9976"
	server := ws.NewServer("localhost:"+port, registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	headers := http.Header{"Origin": []string{"ws://localhost:" + port}}
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:"+port+"/", headers)
	if err != nil {
		t.Fatalf("expected ws://localhost:%s origin to be accepted, got: %v", port, err)
	}
	_ = conn.Close()
}

// TestCheckOriginAllowlist_EmptyOriginAccepted verifies that a missing Origin
// header (loopback CLI tool) is not rejected.
func TestCheckOriginAllowlist_EmptyOriginAccepted(t *testing.T) {
	registry := ws.NewSimpleRegistry()
	server := ws.NewServer("localhost:9975", registry, nil)
	ctx := context.Background()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// No Origin header — gorilla DefaultDialer doesn't set one.
	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:9975/", nil)
	if err != nil {
		t.Fatalf("expected connection without Origin header to be accepted, got: %v", err)
	}
	_ = conn.Close()
}
