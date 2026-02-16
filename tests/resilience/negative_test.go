//go:build resilience

package resilience

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRPC_SendToNonexistentAgent verifies that sending a message scoped to a
// non-existent agent returns a graceful error, not a panic or crash.
func TestRPC_SendToNonexistentAgent(t *testing.T) {
	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	// Ensure sender has an active session
	ensureSession(t, socketPath, "coordinator_0000")

	_, err := rpcCallRaw(socketPath, "message.send", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"content":         "Hello ghost agent",
		"format":          "markdown",
		"scopes": []map[string]any{
			{"type": "agent", "value": "nonexistent_agent_99999"},
		},
	})

	// The messaging protocol accepts messages to unknown agents (deferred delivery /
	// future agent). The key invariant is no crash or panic — either outcome is valid.
	if err != nil {
		t.Logf("Send to non-existent agent returned error (expected): %v", err)
	} else {
		t.Log("Send to non-existent agent succeeded (message accepted)")
	}

	// Verify daemon is still healthy after the request
	_, err = rpcCallRaw(socketPath, "health", nil)
	if err != nil {
		t.Fatalf("daemon unhealthy after send to non-existent agent: %v", err)
	}
}

// TestRPC_MalformedRequest sends invalid JSON-RPC requests and verifies the
// daemon responds with errors rather than panicking.
func TestRPC_MalformedRequest(t *testing.T) {
	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	// Table-driven: add new malformation cases here.
	tests := []struct {
		name    string
		payload string
	}{
		{"empty object", `{}`},
		{"missing method", `{"jsonrpc":"2.0","id":1}`},
		{"wrong jsonrpc version", `{"jsonrpc":"1.0","id":1,"method":"health"}`},
		{"invalid json", `{not valid json`},
		{"null method", `{"jsonrpc":"2.0","id":1,"method":null}`},
		{"numeric method", `{"jsonrpc":"2.0","id":1,"method":42}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer conn.Close()

			conn.SetDeadline(time.Now().Add(5 * time.Second))

			// Send the malformed request
			_, err = conn.Write([]byte(tt.payload + "\n"))
			if err != nil {
				t.Fatalf("write: %v", err)
			}

			// Read response — we expect either an error response or connection close
			buf := make([]byte, 4096)
			n, err := conn.Read(buf)
			if err != nil {
				// Connection closed by server — acceptable for truly invalid JSON
				t.Logf("Server closed connection for %q (acceptable)", tt.name)
				return
			}

			// Parse response — should be a JSON-RPC error
			var response struct {
				Error *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(buf[:n], &response); err != nil {
				t.Logf("Non-JSON response for %q: %s", tt.name, string(buf[:n]))
				return
			}

			if response.Error != nil {
				t.Logf("Got expected error for %q: code=%d msg=%s", tt.name, response.Error.Code, response.Error.Message)
			}
		})
	}

	// Verify daemon is still healthy after all malformed requests
	_, err := rpcCallRaw(socketPath, "health", nil)
	if err != nil {
		t.Fatalf("daemon unhealthy after malformed requests: %v", err)
	}
}

// TestRPC_ConnectionDropMidRequest verifies that disconnecting mid-request
// doesn't crash the daemon or leak resources.
func TestRPC_ConnectionDropMidRequest(t *testing.T) {
	done := checkGoroutineLeaks(t, 5)
	defer done()

	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	// Open connections, send partial data, then disconnect abruptly
	for i := range 10 {
		conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
		if err != nil {
			t.Fatalf("connect %d: %v", i, err)
		}

		// Send partial JSON-RPC request (no newline terminator, incomplete JSON)
		partial := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"message.list","params":{"caller_agent`, i+1)
		_, err = conn.Write([]byte(partial))
		if err != nil {
			conn.Close()
			t.Logf("write %d failed (connection reset): %v", i, err)
			continue
		}

		// Abruptly close without reading response
		conn.Close()
	}

	// Give the daemon a moment to clean up
	time.Sleep(100 * time.Millisecond)

	// Verify daemon is still healthy and accepting connections
	_, err := rpcCallRaw(socketPath, "health", nil)
	if err != nil {
		t.Fatalf("daemon unhealthy after connection drops: %v", err)
	}

	// Send a proper request to confirm full functionality
	var result struct {
		Agents []map[string]any `json:"agents"`
	}
	rpcCall(t, socketPath, "agent.list", map[string]any{}, &result)
	if len(result.Agents) != 50 {
		t.Errorf("expected 50 agents after connection drops, got %d", len(result.Agents))
	}
}

// TestConcurrent_ErrorsUnderLoad mixes valid and invalid requests concurrently
// and verifies that valid requests still succeed.
func TestConcurrent_ErrorsUnderLoad(t *testing.T) {
	done := checkGoroutineLeaks(t, 5)
	defer done()

	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	// Ensure agents have active sessions
	for i := range 5 {
		ensureSession(t, socketPath, fixtureAgentName(i))
	}

	var wg sync.WaitGroup
	var validSuccess, validFail, invalidCount atomic.Int64

	// 5 goroutines sending valid requests
	for i := range 5 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentID := fixtureAgentName(idx)
			for j := range 20 {
				_, err := rpcCallRaw(socketPath, "message.send", map[string]any{
					"caller_agent_id": agentID,
					"content":         fmt.Sprintf("Valid msg %d from %d", j, idx),
					"format":          "markdown",
				})
				if err != nil {
					validFail.Add(1)
				} else {
					validSuccess.Add(1)
				}
			}
		}(i)
	}

	// 5 goroutines sending invalid requests (unknown methods)
	for i := range 5 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := range 20 {
				_, err := rpcCallRaw(socketPath, "nonexistent.method."+fmt.Sprint(j), map[string]any{
					"garbage": idx,
				})
				if err != nil {
					invalidCount.Add(1)
				}
			}
		}(i)
	}

	start := time.Now()
	wg.Wait()
	elapsed := time.Since(start)

	t.Logf("Valid: %d success, %d fail | Invalid: %d errors (elapsed %v)",
		validSuccess.Load(), validFail.Load(), invalidCount.Load(), elapsed)

	if elapsed > 30*time.Second {
		t.Errorf("mixed load took %v (expected <30s; may indicate serialization)", elapsed)
	}

	// All valid requests should succeed
	if validFail.Load() > 0 {
		t.Errorf("expected 0 valid request failures, got %d (invalid requests may have disrupted valid ones)",
			validFail.Load())
	}

	// Invalid requests should all return errors
	if invalidCount.Load() != 100 {
		t.Errorf("expected 100 invalid request errors, got %d", invalidCount.Load())
	}

	// Daemon should still be healthy
	_, err := rpcCallRaw(socketPath, "health", nil)
	if err != nil {
		t.Fatalf("daemon unhealthy after mixed load: %v", err)
	}
}
