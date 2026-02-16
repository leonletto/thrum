//go:build resilience

package resilience

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// checkGoroutineLeaks snapshots goroutine count before the test body runs
// and verifies no goroutines leaked after completion (with a small tolerance
// for runtime-internal goroutines that may start/stop asynchronously).
func checkGoroutineLeaks(t *testing.T, tolerance int) func() {
	t.Helper()
	// Let any previous test goroutines settle
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()
	return func() {
		// Give goroutines time to wind down
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			runtime.GC()
			after := runtime.NumGoroutine()
			if after <= before+tolerance {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		after := runtime.NumGoroutine()
		if after > before+tolerance {
			t.Errorf("goroutine leak detected: before=%d after=%d (tolerance=%d)", before, after, tolerance)
		}
	}
}

func TestConcurrent_10Senders(t *testing.T) {
	done := checkGoroutineLeaks(t, 5)
	defer done()

	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	// Ensure all sender agents have active sessions
	for i := range 10 {
		agentID := fixtureAgentName(i % 50)
		ensureSession(t, socketPath, agentID)
	}

	var wg sync.WaitGroup
	var errors atomic.Int64
	var sent atomic.Int64

	for i := range 10 {
		wg.Add(1)
		go func(senderIdx int) {
			defer wg.Done()
			agentID := fixtureAgentName(senderIdx % 50)

			for j := range 100 {
				_, err := rpcCallRaw(socketPath, "message.send", map[string]any{
					"caller_agent_id": agentID,
					"content":         fmt.Sprintf("Concurrent msg %d from sender %d", j, senderIdx),
					"format":          "markdown",
				})
				if err != nil {
					errors.Add(1)
					t.Errorf("sender %d msg %d: %v", senderIdx, j, err)
					return
				}
				sent.Add(1)
			}
		}(i)
	}

	wg.Wait()
	t.Logf("Sent %d messages with %d errors", sent.Load(), errors.Load())

	if errors.Load() > 0 {
		t.Errorf("expected 0 errors, got %d", errors.Load())
	}
	if sent.Load() != 1000 {
		t.Errorf("expected 1000 messages sent, got %d", sent.Load())
	}
}

func TestConcurrent_ReadWriteMix(t *testing.T) {
	done := checkGoroutineLeaks(t, 5)
	defer done()

	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	// Ensure writer agents have active sessions (use agents at indices 10-14)
	for i := range 5 {
		agentID := fixtureAgentName(10 + i)
		ensureSession(t, socketPath, agentID)
	}

	var wg sync.WaitGroup
	var writeErrors, readErrors atomic.Int64

	// 5 writers
	for i := range 5 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentID := fixtureAgentName(10 + idx)
			for j := range 50 {
				_, err := rpcCallRaw(socketPath, "message.send", map[string]any{
					"caller_agent_id": agentID,
					"content":         fmt.Sprintf("Write %d from %d", j, idx),
					"format":          "markdown",
				})
				if err != nil {
					writeErrors.Add(1)
					t.Errorf("writer %d msg %d: %v", idx, j, err)
				}
			}
		}(i)
	}

	// 5 readers
	for i := range 5 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentID := fixtureAgentName(20 + idx) // readers use agents 20-24
			for range 50 {
				_, err := rpcCallRaw(socketPath, "message.list", map[string]any{
					"caller_agent_id": agentID,
					"page_size":       20,
				})
				if err != nil {
					readErrors.Add(1)
					t.Errorf("reader %d: %v", idx, err)
				}
			}
		}(i)
	}

	wg.Wait()
	t.Logf("Write errors: %d, Read errors: %d", writeErrors.Load(), readErrors.Load())
}

func TestConcurrent_InboxUnderLoad(t *testing.T) {
	done := checkGoroutineLeaks(t, 5)
	defer done()

	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	var wg sync.WaitGroup
	var errors atomic.Int64

	// Writer goroutine sending messages
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 200 {
			_, err := rpcCallRaw(socketPath, "message.send", map[string]any{
				"caller_agent_id": "coordinator_0000",
				"content":         fmt.Sprintf("Load message %d", i),
				"format":          "markdown",
			})
			if err != nil {
				errors.Add(1)
			}
		}
	}()

	// 5 inbox query goroutines running concurrently
	for i := range 5 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentID := fixtureAgentName(idx + 1) // readers use agents 1-5
			for range 100 {
				_, err := rpcCallRaw(socketPath, "message.list", map[string]any{
					"caller_agent_id": agentID,
					"page_size":       100,
				})
				if err != nil {
					errors.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()
	if errors.Load() > 0 {
		t.Errorf("got %d errors during concurrent inbox+send", errors.Load())
	}
}

func TestConcurrent_SessionLifecycle(t *testing.T) {
	done := checkGoroutineLeaks(t, 5)
	defer done()

	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	var wg sync.WaitGroup
	var errors atomic.Int64

	// 10 goroutines each starting and ending sessions
	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentID := fixtureAgentName((idx * 5) + 4) // agents at indices 4,9,14...

			// Start a new session
			result, err := rpcCallRaw(socketPath, "session.start", map[string]any{
				"agent_id": agentID,
			})
			if err != nil {
				errors.Add(1)
				t.Errorf("session start %d: %v", idx, err)
				return
			}

			// Extract session ID from result
			var startResult struct {
				SessionID string `json:"session_id"`
			}
			if err := jsonUnmarshal(result, &startResult); err != nil {
				errors.Add(1)
				t.Errorf("parse session start result %d: %v", idx, err)
				return
			}

			if startResult.SessionID == "" {
				errors.Add(1)
				t.Errorf("empty session_id for agent %s", agentID)
				return
			}

			// End the session
			_, err = rpcCallRaw(socketPath, "session.end", map[string]any{
				"session_id": startResult.SessionID,
				"agent_id":   agentID,
			})
			if err != nil {
				errors.Add(1)
				t.Errorf("session end %d: %v", idx, err)
			}
		}(i)
	}

	wg.Wait()
	if errors.Load() > 0 {
		t.Errorf("got %d errors during concurrent session lifecycle", errors.Load())
	}
}
