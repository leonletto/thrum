package rpc

import (
	"context"
	"testing"
)

func TestPeerInfoHandler(t *testing.T) {
	h := NewPeerInfoHandler("d_alice123", "alice-laptop")
	result, err := h.Handle(context.Background(), nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	resp, ok := result.(PeerInfoResponse)
	if !ok {
		t.Fatalf("unexpected result type: %T", result)
	}

	if resp.DaemonID != "d_alice123" {
		t.Errorf("DaemonID = %q, want %q", resp.DaemonID, "d_alice123")
	}
	if resp.Hostname != "alice-laptop" {
		t.Errorf("Hostname = %q, want %q", resp.Hostname, "alice-laptop")
	}
	// PublicKey is placeholder for now
	if resp.PublicKey != "" {
		t.Errorf("PublicKey = %q, want empty (placeholder)", resp.PublicKey)
	}
}
