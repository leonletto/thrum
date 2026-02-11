package mcp

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestWaiterQueueDrain(t *testing.T) {
	// Test that WaitForMessage returns immediately when queue has messages
	w := &Waiter{
		queue: []MessageNotification{
			{MessageID: "msg-1", Preview: "Hello", Timestamp: "2026-01-01T00:00:00Z"},
		},
		socketPath: "/nonexistent/socket", // fetchAndMark will fail, falling back to preview
		ctx:        context.Background(),
	}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	defer w.cancel()

	result, err := w.WaitForMessage(context.Background(), 5, "")
	if err != nil {
		t.Fatalf("WaitForMessage: %v", err)
	}

	if result.Status != "message_received" {
		t.Errorf("expected status 'message_received', got %q", result.Status)
	}
	if result.Message == nil {
		t.Fatal("expected message, got nil")
	}
	if result.Message.MessageID != "msg-1" {
		t.Errorf("expected message_id 'msg-1', got %q", result.Message.MessageID)
	}
	if result.WaitedSeconds != 0 {
		t.Errorf("expected 0 waited_seconds for queued message, got %d", result.WaitedSeconds)
	}
}

func TestWaiterTimeout(t *testing.T) {
	w := &Waiter{
		queue:      []MessageNotification{},
		socketPath: "/nonexistent/socket",
	}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	defer w.cancel()

	start := time.Now()
	result, err := w.WaitForMessage(context.Background(), 1, "")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("WaitForMessage: %v", err)
	}
	if result.Status != "timeout" {
		t.Errorf("expected status 'timeout', got %q", result.Status)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("expected ~1 second wait, got %v", elapsed)
	}
}

func TestWaiterContextCancellation(t *testing.T) {
	w := &Waiter{
		queue:      []MessageNotification{},
		socketPath: "/nonexistent/socket",
	}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	defer w.cancel()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := w.WaitForMessage(ctx, 30, "")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestWaiterSingleActive(t *testing.T) {
	w := &Waiter{
		queue:      []MessageNotification{},
		socketPath: "/nonexistent/socket",
	}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	defer w.cancel()

	// Start first wait in background
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = w.WaitForMessage(context.Background(), 2, "")
	}()

	// Give goroutine time to start waiting (intentional - ensuring operation is in-flight)
	time.Sleep(50 * time.Millisecond)

	// Second wait should fail
	_, err := w.WaitForMessage(context.Background(), 1, "")
	if err == nil {
		t.Fatal("expected error for concurrent wait")
	}
	if err.Error() != "another wait_for_message is already active; only one waiter per agent" {
		t.Errorf("unexpected error: %v", err)
	}

	wg.Wait()
}

func TestWaiterChannelSignal(t *testing.T) {
	w := &Waiter{
		queue:      []MessageNotification{},
		socketPath: "/nonexistent/socket",
	}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	defer w.cancel()

	// Start wait in background
	resultCh := make(chan *WaitForMessageOutput, 1)
	go func() {
		result, err := w.WaitForMessage(context.Background(), 10, "")
		if err != nil {
			return
		}
		resultCh <- result
	}()

	// Give goroutine time to start waiting (intentional - ensuring operation is in-flight)
	time.Sleep(50 * time.Millisecond)

	// Simulate notification arrival
	w.mu.Lock()
	w.queue = append(w.queue, MessageNotification{
		MessageID: "msg-signal",
		Preview:   "Signal test",
		Timestamp: "2026-01-01T00:00:00Z",
	})
	if w.waiterCh != nil {
		close(w.waiterCh)
		w.waiterCh = nil
	}
	w.mu.Unlock()

	select {
	case result := <-resultCh:
		if result.Status != "message_received" {
			t.Errorf("expected status 'message_received', got %q", result.Status)
		}
		if result.Message == nil || result.Message.MessageID != "msg-signal" {
			t.Error("expected message with ID 'msg-signal'")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitForMessage did not return after signal")
	}
}

func TestWaiterQueueOverflow(t *testing.T) {
	w := &Waiter{
		queue: make([]MessageNotification, maxQueueSize),
	}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	defer w.cancel()

	// Fill to capacity
	for i := range w.queue {
		w.queue[i] = MessageNotification{MessageID: "old"}
	}

	// readLoop would add beyond capacity — simulate that
	w.mu.Lock()
	if len(w.queue) >= maxQueueSize {
		w.queue = w.queue[1:]
	}
	w.queue = append(w.queue, MessageNotification{MessageID: "new"})
	w.mu.Unlock()

	if len(w.queue) != maxQueueSize {
		t.Errorf("expected queue size %d, got %d", maxQueueSize, len(w.queue))
	}
	if w.queue[len(w.queue)-1].MessageID != "new" {
		t.Error("expected newest message at end of queue")
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
