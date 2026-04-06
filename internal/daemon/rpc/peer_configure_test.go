package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestPeerConfigureHandler_AddAgent(t *testing.T) {
	var gotPeer, gotAgent string
	addFn := func(peerName, agentName string) error {
		gotPeer = peerName
		gotAgent = agentName
		return nil
	}
	h := NewPeerConfigureHandler(addFn, nil)

	params, _ := json.Marshal(map[string]string{
		"peer_name":  "alice",
		"action":     "add-agent",
		"agent_name": "bot",
	})
	result, err := h.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPeer != "alice" || gotAgent != "bot" {
		t.Errorf("addFn called with (%q, %q), want (alice, bot)", gotPeer, gotAgent)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type %T, want map[string]any", result)
	}
	if m["action"] != "added" || m["agent"] != "bot" {
		t.Errorf("result = %v", m)
	}
}

func TestPeerConfigureHandler_RemoveAgent(t *testing.T) {
	var gotPeer, gotAgent string
	removeFn := func(peerName, agentName string) error {
		gotPeer = peerName
		gotAgent = agentName
		return nil
	}
	h := NewPeerConfigureHandler(nil, removeFn)

	params, _ := json.Marshal(map[string]string{
		"peer_name":  "bob",
		"action":     "remove-agent",
		"agent_name": "proxy",
	})
	result, err := h.Handle(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPeer != "bob" || gotAgent != "proxy" {
		t.Errorf("removeFn called with (%q, %q), want (bob, proxy)", gotPeer, gotAgent)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type %T, want map[string]any", result)
	}
	if m["action"] != "removed" || m["agent"] != "proxy" {
		t.Errorf("result = %v", m)
	}
}

func TestPeerConfigureHandler_UnknownAction(t *testing.T) {
	h := NewPeerConfigureHandler(nil, nil)

	params, _ := json.Marshal(map[string]string{
		"peer_name":  "alice",
		"action":     "explode",
		"agent_name": "bot",
	})
	_, err := h.Handle(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for unknown action, got nil")
	}
}

func TestPeerConfigureHandler_MissingParams(t *testing.T) {
	h := NewPeerConfigureHandler(nil, nil)

	// Missing agent_name
	params, _ := json.Marshal(map[string]string{
		"peer_name": "alice",
		"action":    "add-agent",
	})
	_, err := h.Handle(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing agent_name, got nil")
	}

	// Missing peer_name
	params, _ = json.Marshal(map[string]string{
		"action":     "add-agent",
		"agent_name": "bot",
	})
	_, err = h.Handle(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing peer_name, got nil")
	}
}

func TestPeerConfigureHandler_FuncError(t *testing.T) {
	addFn := func(peerName, agentName string) error {
		return fmt.Errorf("peer not found: %s", peerName)
	}
	h := NewPeerConfigureHandler(addFn, nil)

	params, _ := json.Marshal(map[string]string{
		"peer_name":  "nobody",
		"action":     "add-agent",
		"agent_name": "bot",
	})
	_, err := h.Handle(context.Background(), params)
	if err == nil {
		t.Fatal("expected error from addFn, got nil")
	}
}
