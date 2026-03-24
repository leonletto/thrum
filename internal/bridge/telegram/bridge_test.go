package telegram

import (
	"context"
	"errors"
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

// newTestBridgeWithBot constructs a Bridge with a pre-populated bot pointer
// and running=true so Pair() can be exercised without a real Telegram connection.
func newTestBridgeWithBot(t *testing.T) (*Bridge, *Bot) {
	t.Helper()
	cfg := config.TelegramConfig{
		Token:  "test-token",
		Target: "@test",
		UserID: "test",
	}
	b := New(cfg, "0")
	bot := &Bot{
		messages: make(chan InboundMessage, 32),
	}
	b.bot.Store(bot)
	b.running.Store(true)
	return b, bot
}

// TestBridge_Pair_Success verifies that Pair() returns the result sent by a
// simulated Poll()-like goroutine as soon as pairMode is active.
func TestBridge_Pair_Success(t *testing.T) {
	t.Parallel()

	b, bot := newTestBridgeWithBot(t)

	want := PairResult{
		UserID:    123,
		Username:  "testuser",
		FirstName: "Test",
		LastName:  "User",
		ChatID:    456,
		Text:      "pair me",
	}

	// Goroutine that waits until pairMode is active then delivers the result.
	go func() {
		// Poll until pairMode is set (mirrors Poll() behavior).
		for i := 0; i < 1000; i++ {
			if bot.pairMode.Load() {
				ch := bot.pairCh.Load()
				if ch != nil {
					select {
					case *ch <- want:
					default:
					}
					return
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()

	ctx := context.Background()
	got, err := b.Pair(ctx, 5*time.Second)
	if err != nil {
		t.Fatalf("Pair() returned unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("Pair() = %+v, want %+v", got, want)
	}

	// After Pair() returns, pairMode must be cleared.
	if bot.pairMode.Load() {
		t.Error("pairMode should be false after Pair() returns")
	}
	if bot.pairCh.Load() != nil {
		t.Error("pairCh should be nil after Pair() returns")
	}
}

// TestBridge_Pair_Timeout verifies that Pair() returns a timeout error when no
// message arrives within the deadline, and that pairMode is reverted.
func TestBridge_Pair_Timeout(t *testing.T) {
	t.Parallel()

	b, bot := newTestBridgeWithBot(t)

	ctx := context.Background()
	_, err := b.Pair(ctx, 50*time.Millisecond)
	if err == nil {
		t.Fatal("Pair() should have returned a timeout error")
	}
	if errors.Is(err, ErrBridgeNotRunning) || errors.Is(err, ErrPairingInProgress) {
		t.Errorf("Pair() returned wrong error type: %v", err)
	}

	// pairMode must be reverted after timeout.
	if bot.pairMode.Load() {
		t.Error("pairMode should be false after timeout")
	}
	if bot.pairCh.Load() != nil {
		t.Error("pairCh should be nil after timeout")
	}
}

// TestBridge_Pair_ConcurrentRejected verifies that a second concurrent Pair()
// call receives ErrPairingInProgress while the first is still holding pairMu.
func TestBridge_Pair_ConcurrentRejected(t *testing.T) {
	t.Parallel()

	b, _ := newTestBridgeWithBot(t)

	// Pre-lock pairMu to simulate a pairing already in progress.
	b.pairMu.Lock()
	defer b.pairMu.Unlock()

	ctx := context.Background()
	_, err := b.Pair(ctx, 5*time.Second)
	if !errors.Is(err, ErrPairingInProgress) {
		t.Errorf("Pair() error = %v, want ErrPairingInProgress", err)
	}
}

// TestBridge_Pair_NotRunning verifies that Pair() returns ErrBridgeNotRunning
// when the bridge is not in the running state.
func TestBridge_Pair_NotRunning(t *testing.T) {
	t.Parallel()

	cfg := config.TelegramConfig{
		Token:  "test-token",
		Target: "@test",
		UserID: "test",
	}
	b := New(cfg, "0")
	// running is false by default; bot pointer is nil.

	ctx := context.Background()
	_, err := b.Pair(ctx, 5*time.Second)
	if !errors.Is(err, ErrBridgeNotRunning) {
		t.Errorf("Pair() error = %v, want ErrBridgeNotRunning", err)
	}
}
