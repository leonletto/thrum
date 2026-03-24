package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	telegram "github.com/leonletto/thrum/internal/bridge/telegram"
	"github.com/leonletto/thrum/internal/config"
)

// TestHandlePair_Success tests that HandlePair returns "not configured" when
// the bridge is nil (no bridge set on the handler).
func TestHandlePair_Success(t *testing.T) {
	handler := NewTelegramHandler("/tmp/test-repo")
	// bridge is nil by default

	req := TelegramPairRequest{TimeoutSeconds: 30}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	ctx := context.Background()
	_, err = handler.HandlePair(ctx, raw)
	if err == nil {
		t.Fatal("expected error when bridge is nil, got nil")
	}
	if err.Error() != "telegram bridge not configured" {
		t.Errorf("expected 'telegram bridge not configured', got %q", err.Error())
	}
}

// TestHandlePair_BridgeNotReady tests that HandlePair returns an error when the
// bridge is set but not running (Running() returns false). Uses a short context
// deadline to avoid waiting the full 5s readiness poll.
func TestHandlePair_BridgeNotReady(t *testing.T) {
	handler := NewTelegramHandler("/tmp/test-repo")

	// Construct a real Bridge without starting it — Running() returns false.
	bridge := telegram.New(config.TelegramConfig{Token: "fake:token"}, "0")
	handler.SetBridge(bridge)

	req := TelegramPairRequest{TimeoutSeconds: 10}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	// Short context deadline so we don't wait the full 5s readiness poll.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err = handler.HandlePair(ctx, raw)
	if err == nil {
		t.Fatal("expected error for bridge not ready, got nil")
	}
	// Either the readiness poll times out or the context deadline fires first.
	if !strings.Contains(err.Error(), "not connected") && !strings.Contains(err.Error(), "context deadline") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestHandlePair_TimeoutValidation tests that timeout_seconds values outside
// the valid range [1, 300] are rejected.
func TestHandlePair_TimeoutValidation(t *testing.T) {
	handler := NewTelegramHandler("/tmp/test-repo")
	ctx := context.Background()

	cases := []struct {
		name    string
		timeout int
	}{
		{"zero", 0},
		{"too_large", 500},
		{"negative", -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := TelegramPairRequest{TimeoutSeconds: tc.timeout}
			raw, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("failed to marshal request: %v", err)
			}

			_, err = handler.HandlePair(ctx, raw)
			if err == nil {
				t.Fatalf("expected error for timeout_seconds=%d, got nil", tc.timeout)
			}
			if err.Error() != "timeout_seconds must be between 1 and 300" {
				t.Errorf("unexpected error for timeout_seconds=%d: %v", tc.timeout, err)
			}
		})
	}
}

// TestHandlePair_InvalidJSON tests that malformed JSON returns a parse error.
func TestHandlePair_InvalidJSON(t *testing.T) {
	handler := NewTelegramHandler("/tmp/test-repo")
	ctx := context.Background()

	_, err := handler.HandlePair(ctx, json.RawMessage(`{not valid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
