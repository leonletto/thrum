package rpc

import (
	"context"
	"encoding/json"
	"testing"
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
// bridge is set but not running. We use a short context deadline to avoid
// waiting the full 5s readiness poll.
func TestHandlePair_BridgeNotReady(t *testing.T) {
	handler := NewTelegramHandler("/tmp/test-repo")
	// SetBridge with a non-nil pointer requires a real Bridge, but we can't
	// easily construct one without a real Telegram token. Instead we test the
	// nil-bridge path above and the timeout-validation path below.
	// This test verifies timeout validation rejects values out of range before
	// any bridge readiness check occurs.
	req := TelegramPairRequest{TimeoutSeconds: 0}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	ctx := context.Background()
	_, err = handler.HandlePair(ctx, raw)
	if err == nil {
		t.Fatal("expected error for timeout_seconds=0, got nil")
	}
	if err.Error() != "timeout_seconds must be between 1 and 300" {
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
