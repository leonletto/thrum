package bridge_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leonletto/thrum/internal/bridge"
)

// mockTransport implements TransportBridge for testing.
type mockTransport struct {
	name      string
	connected bool
	notifyCh  chan bridge.Notification
}

func (m *mockTransport) PeerName() string                         { return m.name }
func (m *mockTransport) Connected() bool                          { return m.connected }
func (m *mockTransport) Connect(ctx context.Context) error        { m.connected = true; return nil }
func (m *mockTransport) Close() error                             { m.connected = false; return nil }
func (m *mockTransport) Notifications() <-chan bridge.Notification { return m.notifyCh }
func (m *mockTransport) Call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func TestTransportBridge_InterfaceCompliance(t *testing.T) {
	var tb bridge.TransportBridge = &mockTransport{
		name:     "test-peer",
		notifyCh: make(chan bridge.Notification, 1),
	}

	if tb.PeerName() != "test-peer" {
		t.Fatalf("PeerName() = %q, want %q", tb.PeerName(), "test-peer")
	}
	if tb.Connected() {
		t.Fatal("should not be connected before Connect()")
	}
	if err := tb.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() = %v", err)
	}
	if !tb.Connected() {
		t.Fatal("should be connected after Connect()")
	}
	if err := tb.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestNotification_Fields(t *testing.T) {
	n := bridge.Notification{
		Method: "notification.message",
		Params: json.RawMessage(`{"id":"msg-1"}`),
	}
	if n.Method != "notification.message" {
		t.Fatalf("Method = %q, want %q", n.Method, "notification.message")
	}
}
