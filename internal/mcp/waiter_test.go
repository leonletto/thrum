package mcp

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestWaiterTimeoutWithNoDaemon verifies the polling waiter returns a
// timeout result (not an error) when the daemon is unreachable for the
// entire wait window.
func TestWaiterTimeoutWithNoDaemon(t *testing.T) {
	// Use a socket that doesn't exist; polling connect attempts will fail
	// on every tick. With a 1-second overall timeout the reconnect budget
	// (60s) is irrelevant — timeoutTimer fires first.
	socket := filepath.Join(t.TempDir(), "missing.sock")
	w := NewWaiter(context.Background(), socket, "waiter_x", "test-role")
	defer w.Close()

	start := time.Now()
	result, err := w.WaitForMessage(context.Background(), 1)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("WaitForMessage returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if result.Status != "timeout" {
		t.Errorf("expected status 'timeout', got %q", result.Status)
	}
	if elapsed < 900*time.Millisecond || elapsed > 2*time.Second {
		t.Errorf("expected ~1s elapsed, got %v", elapsed)
	}
}

// TestWaiterContextCancellation verifies the waiter returns the context
// error when its parent context is canceled mid-wait.
func TestWaiterContextCancellation(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "missing.sock")
	w := NewWaiter(context.Background(), socket, "waiter_x", "test-role")
	defer w.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := w.WaitForMessage(ctx, 30)
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

// TestWaiterSingleActive verifies the single-waiter constraint: a second
// concurrent WaitForMessage must fail fast with a specific error.
func TestWaiterSingleActive(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "missing.sock")
	w := NewWaiter(context.Background(), socket, "waiter_x", "test-role")
	defer w.Close()

	// Start first wait in the background.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = w.WaitForMessage(context.Background(), 2)
	}()

	// Give the goroutine a moment to reach the active gate.
	time.Sleep(50 * time.Millisecond)

	// Second wait should fail immediately.
	_, err := w.WaitForMessage(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error for concurrent wait, got nil")
	}
	want := "another wait_for_message is already active; only one waiter per agent"
	if err.Error() != want {
		t.Errorf("unexpected error: %v", err)
	}

	wg.Wait()
}

// TestWaiterReconnectBudgetExhaustionReturnsTimeout verifies that when the
// daemon is unreachable for the full reconnect budget, WaitForMessage
// returns a structured timeout result (not an error). This is the
// documented behavior for MCP clients, so they get a uniform "no message"
// shape whether the outage was brief or prolonged.
func TestWaiterReconnectBudgetExhaustionReturnsTimeout(t *testing.T) {
	// Shrink the reconnect budget so the test doesn't wait 60s.
	prev := reconnectTimeout
	reconnectTimeout = 300 * time.Millisecond
	t.Cleanup(func() { reconnectTimeout = prev })

	socket := filepath.Join(t.TempDir(), "missing.sock")
	w := NewWaiter(context.Background(), socket, "waiter_x", "test-role")
	defer w.Close()

	start := time.Now()
	// Overall timeout is 10s, well above the shrunk reconnect budget; we
	// expect the budget to fire first and convert to a timeout result.
	result, err := w.WaitForMessage(context.Background(), 10)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected timeout result, got error: %v", err)
	}
	if result == nil || result.Status != "timeout" {
		t.Fatalf("expected status 'timeout', got %+v", result)
	}
	// Should return in budget + a couple of poll ticks; generous upper
	// bound to tolerate CI scheduler noise.
	if elapsed > 2*time.Second {
		t.Errorf("expected reconnect-budget-exhaustion to return within ~1s, took %v", elapsed)
	}
}

// TestWaiterCloseStopsInFlight verifies Close() unblocks an in-flight wait.
func TestWaiterCloseStopsInFlight(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "missing.sock")
	w := NewWaiter(context.Background(), socket, "waiter_x", "test-role")

	errCh := make(chan error, 1)
	go func() {
		_, err := w.WaitForMessage(context.Background(), 30)
		errCh <- err
	}()

	// Allow the waiter to enter its polling loop.
	time.Sleep(100 * time.Millisecond)
	_ = w.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error after Close, got nil")
		}
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForMessage did not return after Close")
	}
}

func TestDeriveAgentStatus(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name       string
		lastSeenAt string
		expected   string
	}{
		{"empty", "", "offline"},
		{"recent", now.Add(-1 * time.Minute).Format(time.RFC3339Nano), "active"},
		{"3 min ago", now.Add(-3 * time.Minute).Format(time.RFC3339Nano), "offline"},
		{"15 min ago", now.Add(-15 * time.Minute).Format(time.RFC3339Nano), "offline"},
		{"invalid", "not-a-timestamp", "offline"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveAgentStatus(tt.lastSeenAt, now)
			if got != tt.expected {
				t.Errorf("deriveAgentStatus(%q) = %q, want %q", tt.lastSeenAt, got, tt.expected)
			}
		})
	}
}
