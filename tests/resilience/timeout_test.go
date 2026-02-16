//go:build resilience

package resilience

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestTimeout_HandlerDeadlineEnforced verifies that the server's 10s per-request
// timeout fires when a handler blocks. We register a deliberately slow handler
// and verify the client gets an error within a reasonable window.
// Also verifies that context cancellation propagates through to the handler.
func TestTimeout_HandlerDeadlineEnforced(t *testing.T) {
	thrumDir := setupFixture(t)
	_, server, socketPath := startDaemonManual(t, thrumDir, "test-timeout")
	defer server.Stop()

	// Track whether the handler's context was cancelled
	var ctxCancelled atomic.Bool

	// Register a deliberately slow handler that blocks until context expires
	server.RegisterHandler("test.slow", func(ctx context.Context, params json.RawMessage) (any, error) {
		<-ctx.Done()
		ctxCancelled.Store(true)
		return nil, ctx.Err()
	})

	// Call the slow handler and measure response time
	start := time.Now()
	_, err := rpcCallRaw(socketPath, "test.slow", nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from timed-out handler, got nil")
	}
	t.Logf("Slow handler returned error after %v: %v", elapsed, err)

	// The server's per-request timeout is 10s. We expect the error within ~11s
	// (10s timeout + some overhead). It should NOT take 30s+ (our rpcCallRaw deadline).
	if elapsed > 15*time.Second {
		t.Errorf("timeout took %v (expected ~10s from server's per-request timeout)", elapsed)
	}
	if elapsed < 5*time.Second {
		t.Errorf("handler returned too quickly (%v) — timeout may not be enforced", elapsed)
	}

	// Verify context cancellation propagated to the handler
	if !ctxCancelled.Load() {
		t.Error("context cancellation did not propagate to handler")
	}
}

// TestTimeout_ConcurrentRequestsIndependent verifies that one timed-out request
// doesn't block other concurrent requests on different connections.
func TestTimeout_ConcurrentRequestsIndependent(t *testing.T) {
	thrumDir := setupFixture(t)
	_, server, socketPath := startDaemonManual(t, thrumDir, "test-timeout-concurrent")
	defer server.Stop()

	// Slow handler that blocks for the full timeout
	server.RegisterHandler("test.slow", func(ctx context.Context, params json.RawMessage) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	// Start a slow request in the background
	var slowDone sync.WaitGroup
	slowDone.Add(1)
	go func() {
		defer slowDone.Done()
		rpcCallRaw(socketPath, "test.slow", nil)
	}()

	// Wait a moment for the slow request to be in-flight
	time.Sleep(200 * time.Millisecond)

	// Send fast requests concurrently — they should complete quickly
	var fastErrors atomic.Int64
	var fastWg sync.WaitGroup
	for i := range 5 {
		fastWg.Add(1)
		go func(idx int) {
			defer fastWg.Done()
			start := time.Now()
			_, err := rpcCallRaw(socketPath, "health", nil)
			elapsed := time.Since(start)
			if err != nil {
				fastErrors.Add(1)
				t.Errorf("fast request %d failed: %v", idx, err)
			}
			if elapsed > 5*time.Second {
				fastErrors.Add(1)
				t.Errorf("fast request %d took %v (should be <5s even with slow request in-flight)", idx, elapsed)
			}
		}(i)
	}

	fastWg.Wait()
	t.Logf("Fast requests completed with %d errors while slow request in-flight", fastErrors.Load())

	if fastErrors.Load() > 0 {
		t.Error("fast requests were blocked by the slow request — timeout isolation broken")
	}

	slowDone.Wait()
}
