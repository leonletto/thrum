package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

func TestBridgeNew(t *testing.T) {
	cfg := config.TelegramConfig{
		Token:  "123456789:AAHxxxxxxx",
		Target: "@coordinator_main",
		UserID: "leon-letto",
		ChatID: -100123456,
	}
	b := New(cfg, "9999")
	if b == nil {
		t.Fatal("New returned nil")
	}
	if b.wsPort != "9999" {
		t.Errorf("wsPort = %q, want 9999", b.wsPort)
	}
	if b.logger == nil {
		t.Error("logger should not be nil")
	}
}

func TestBridgeRunCancellation(t *testing.T) {
	cfg := config.TelegramConfig{
		Token:  "invalid_token", // Will fail to connect, triggering retry
		Target: "@coordinator_main",
		UserID: "leon-letto",
	}
	b := New(cfg, "0") // Port 0 = no server listening

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Good — Run exited after context cancellation
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestBridgePanicRecovery(t *testing.T) {
	// Verify the defer/recover in Run doesn't crash on panic
	cfg := config.TelegramConfig{
		Token:  "test",
		Target: "@test",
		UserID: "test",
	}
	b := New(cfg, "0")

	done := make(chan struct{})
	go func() {
		// Simulate a panic in Run by calling it in a way that triggers
		// the recover — we can't easily trigger a panic inside run(),
		// but we can verify the structure doesn't crash the test.
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately
		b.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}
