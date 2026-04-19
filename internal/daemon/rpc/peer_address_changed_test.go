package rpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/rpc"
)

func TestPeerAddressChangedHandler_Success(t *testing.T) {
	var gotToken, gotIP, gotPort string
	handler := rpc.NewPeerAddressChangedHandler(func(peerToken, newIP, newPort string) error {
		gotToken = peerToken
		gotIP = newIP
		gotPort = newPort
		return nil
	})

	params, _ := json.Marshal(map[string]any{
		"peer_token": "tok-abc",
		"new_ip":     "100.64.0.5",
		"new_port":   "9090",
	})

	result, err := handler.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m, ok := result.(map[string]any)
	if !ok || m["ok"] != true {
		t.Errorf("expected ok=true result, got %v", result)
	}

	if gotToken != "tok-abc" || gotIP != "100.64.0.5" || gotPort != "9090" {
		t.Errorf("updateFn called with (%q, %q, %q), want (tok-abc, 100.64.0.5, 9090)",
			gotToken, gotIP, gotPort)
	}
}

func TestPeerAddressChangedHandler_ValidationFailure(t *testing.T) {
	sentinel := errors.New("validation failed")
	handler := rpc.NewPeerAddressChangedHandler(func(peerToken, newIP, newPort string) error {
		return sentinel
	})

	params, _ := json.Marshal(map[string]any{
		"peer_token": "tok-abc",
		"new_ip":     "10.0.0.1",
		"new_port":   "8080",
	})

	_, err := handler.Handle(context.Background(), params)
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

func TestPeerAddressChangedHandler_MissingParams(t *testing.T) {
	handler := rpc.NewPeerAddressChangedHandler(func(peerToken, newIP, newPort string) error {
		return nil
	})

	cases := []struct {
		name   string
		params map[string]any
	}{
		{"missing peer_token", map[string]any{"new_ip": "1.2.3.4", "new_port": "8080"}},
		{"missing new_ip", map[string]any{"peer_token": "tok", "new_port": "8080"}},
		{"missing new_port", map[string]any{"peer_token": "tok", "new_ip": "1.2.3.4"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params, _ := json.Marshal(tc.params)
			_, err := handler.Handle(context.Background(), params)
			if err == nil {
				t.Error("expected error for missing params, got nil")
			}
		})
	}
}

func TestPeerAddressChangedHandler_InvalidJSON(t *testing.T) {
	handler := rpc.NewPeerAddressChangedHandler(func(peerToken, newIP, newPort string) error {
		return nil
	})

	_, err := handler.Handle(context.Background(), json.RawMessage(`{not valid json`))
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// xir.29: SubnetGuardFunc runs before updateFn. Same-subnet accept is the
// zero-guard return; cross-subnet must reject with an error naming
// --type repair so the CLI can route users to manual recovery.
func TestPeerAddressChanged_SameSubnetAccepted(t *testing.T) {
	var updated bool
	guard := func(transport, oldAddr, newAddr string) error { return nil }
	lookup := func(token string) (string, string, error) { return "192.168.1.5:7731", "network", nil }
	h := rpc.NewPeerAddressChangedHandlerWithGuard(
		func(token, ip, port string) error { updated = true; return nil },
		guard, lookup)

	params, _ := json.Marshal(map[string]any{
		"peer_token": "t", "new_ip": "192.168.1.42", "new_port": "7731",
	})
	if _, err := h.Handle(context.Background(), params); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !updated {
		t.Errorf("updateFn not called on same-subnet accept")
	}
}

func TestPeerAddressChanged_CrossSubnetRejectedWithRepairHint(t *testing.T) {
	var updated bool
	guard := func(transport, oldAddr, newAddr string) error {
		return errors.New("cross-subnet: expected 192.168.1.0/24, got 10.0.0.5")
	}
	lookup := func(token string) (string, string, error) { return "192.168.1.5:7731", "network", nil }
	h := rpc.NewPeerAddressChangedHandlerWithGuard(
		func(token, ip, port string) error { updated = true; return nil },
		guard, lookup)

	params, _ := json.Marshal(map[string]any{
		"peer_token": "t", "new_ip": "10.0.0.5", "new_port": "7731",
	})
	_, err := h.Handle(context.Background(), params)
	if err == nil {
		t.Fatalf("expected cross-subnet rejection")
	}
	if !containsAll(err.Error(), "--type repair") {
		t.Errorf("rejection missing --type repair hint: %v", err)
	}
	if updated {
		t.Errorf("updateFn wrongly called despite guard rejection")
	}
}

// Backwards compatibility: handlers constructed without the guard
// (e.g., in tests or if a deployment opts out of the same-subnet check)
// accept unconditionally — preserves pre-xir.29 behavior.
func TestPeerAddressChanged_NilGuardBackwardsCompatible(t *testing.T) {
	var updated bool
	h := rpc.NewPeerAddressChangedHandler(func(t, i, p string) error { updated = true; return nil })
	params, _ := json.Marshal(map[string]any{
		"peer_token": "t", "new_ip": "10.0.0.5", "new_port": "7731",
	})
	if _, err := h.Handle(context.Background(), params); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !updated {
		t.Errorf("nil-guard handler should pass through to updateFn")
	}
}

// M11 (plan-reviewer): when guard is set but lookupPeer is nil, handler
// must treat it as "cannot evaluate, accept" — preserves first-boot
// behavior where the cached address is not yet populated.
func TestPeerAddressChanged_GuardSetButLookupNil_AcceptsFirstChange(t *testing.T) {
	var updated bool
	guardCalled := false
	guard := func(transport, oldAddr, newAddr string) error {
		guardCalled = true
		// Guard receives empty oldAddr and transport.
		if oldAddr != "" || transport != "" {
			t.Errorf("expected empty oldAddr/transport from nil lookup; got oldAddr=%q transport=%q",
				oldAddr, transport)
		}
		return nil
	}
	h := rpc.NewPeerAddressChangedHandlerWithGuard(
		func(t, i, p string) error { updated = true; return nil },
		guard, nil)

	params, _ := json.Marshal(map[string]any{
		"peer_token": "t", "new_ip": "10.0.0.5", "new_port": "7731",
	})
	if _, err := h.Handle(context.Background(), params); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !guardCalled {
		t.Errorf("guard not invoked")
	}
	if !updated {
		t.Errorf("updateFn not called")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}
