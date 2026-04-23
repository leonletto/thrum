package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// fakeRepair builds a PeerRepairFunc whose behavior is controlled by a map of
// token -> local-metadata tuple. If tok is missing, the func returns an error.
func fakeRepair(expectedToken, localDaemonID, localName string) PeerRepairFunc {
	return func(
		token, _, _, _, _, _, _, _ string,
	) (string, string, string, string, string, string, error) {
		if token != expectedToken {
			return "", "", "", "", "", "", fmt.Errorf("no matching peer")
		}
		return localDaemonID, localName, "", "", "", "", nil
	}
}

func callRepair(t *testing.T, h *PeerRepairHandler, body map[string]any) (any, error) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return h.Handle(context.Background(), raw)
}

func TestPeerRepairHandler_Success(t *testing.T) {
	h := NewPeerRepairHandler(fakeRepair("tok-x", "d_local", "local-host"))

	got, err := callRepair(t, h, map[string]any{
		"token":     "tok-x",
		"daemon_id": "d_dialer",
		"name":      "dialer-host",
		"address":   "10.0.0.1:9100",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	resp, ok := got.(peerRepairResponse)
	if !ok {
		t.Fatalf("response type = %T, want peerRepairResponse", got)
	}
	if resp.Status != "repaired" {
		t.Errorf("Status = %q, want repaired", resp.Status)
	}
	if resp.DaemonID != "d_local" || resp.Name != "local-host" {
		t.Errorf("local metadata not echoed: %+v", resp)
	}
}

func TestPeerRepairHandler_TokenRequired(t *testing.T) {
	h := NewPeerRepairHandler(fakeRepair("tok-x", "d_local", "local-host"))
	_, err := callRepair(t, h, map[string]any{
		"daemon_id": "d_dialer",
	})
	if err == nil {
		t.Fatalf("expected error on missing token")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error should mention token: %q", err.Error())
	}
}

func TestPeerRepairHandler_DaemonIDRequired(t *testing.T) {
	h := NewPeerRepairHandler(fakeRepair("tok-x", "d_local", "local-host"))
	_, err := callRepair(t, h, map[string]any{
		"token": "tok-x",
	})
	if err == nil {
		t.Fatalf("expected error on missing daemon_id")
	}
	if !strings.Contains(err.Error(), "daemon_id") {
		t.Errorf("error should mention daemon_id: %q", err.Error())
	}
}

func TestPeerRepairHandler_HandlerError(t *testing.T) {
	h := NewPeerRepairHandler(fakeRepair("expected", "d_local", "local-host"))
	_, err := callRepair(t, h, map[string]any{
		"token":     "wrong-token",
		"daemon_id": "d_dialer",
	})
	if err == nil {
		t.Fatalf("expected error on mismatched token")
	}
}

func TestPeerRepairHandler_InvalidJSON(t *testing.T) {
	h := NewPeerRepairHandler(fakeRepair("expected", "d_local", "local-host"))
	_, err := h.Handle(context.Background(), json.RawMessage(`{not valid`))
	if err == nil {
		t.Fatalf("expected JSON parse error")
	}
}
