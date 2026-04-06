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
