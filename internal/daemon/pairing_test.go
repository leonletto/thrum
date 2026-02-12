package daemon

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestPairingManager(t *testing.T) *PairingManager {
	t.Helper()
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	return NewPairingManager(reg, "d_local", "local-machine")
}

func TestPairing_HappyPath(t *testing.T) {
	pm := newTestPairingManager(t)

	// Start pairing
	code, err := pm.StartPairing(5 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}
	if len(code) != 4 {
		t.Errorf("code length = %d, want 4", len(code))
	}

	// Handle pair request with correct code
	token, daemonID, name, err := pm.HandlePairRequest(code, "d_remote", "remote-machine", "100.64.1.2:9100")
	if err != nil {
		t.Fatalf("HandlePairRequest: %v", err)
	}
	if token == "" {
		t.Error("token should not be empty")
	}
	if daemonID != "d_local" {
		t.Errorf("daemonID = %q, want %q", daemonID, "d_local")
	}
	if name != "local-machine" {
		t.Errorf("name = %q, want %q", name, "local-machine")
	}

	// Verify peer was stored
	peer := pm.peers.FindPeerByToken(token)
	if peer == nil {
		t.Fatal("peer should be stored after pairing")
	}
	if peer.Name != "remote-machine" {
		t.Errorf("peer.Name = %q, want %q", peer.Name, "remote-machine")
	}
	if peer.Address != "100.64.1.2:9100" {
		t.Errorf("peer.Address = %q, want %q", peer.Address, "100.64.1.2:9100")
	}
	if peer.DaemonID != "d_remote" {
		t.Errorf("peer.DaemonID = %q, want %q", peer.DaemonID, "d_remote")
	}

	// Session should be cleared after successful pairing
	if pm.HasActiveSession() {
		t.Error("session should be cleared after pairing")
	}
}

func TestPairing_WrongCode(t *testing.T) {
	pm := newTestPairingManager(t)

	code, err := pm.StartPairing(5 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}

	// Send wrong code
	_, _, _, err = pm.HandlePairRequest("0000", "d_remote", "remote", "100.64.1.2:9100")
	if err == nil {
		t.Fatal("expected error for wrong code")
	}
	if !strings.Contains(err.Error(), "invalid pairing code") {
		t.Errorf("error = %q, want to contain 'invalid pairing code'", err.Error())
	}

	// Session should still be active (can retry)
	if !pm.HasActiveSession() {
		t.Error("session should still be active after wrong code")
	}

	// Correct code should still work
	_, _, _, err = pm.HandlePairRequest(code, "d_remote", "remote", "100.64.1.2:9100")
	if err != nil {
		t.Fatalf("HandlePairRequest with correct code: %v", err)
	}
}

func TestPairing_MaxAttempts(t *testing.T) {
	pm := newTestPairingManager(t)

	_, err := pm.StartPairing(5 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}

	// Exhaust all attempts with wrong codes
	for i := 0; i < MaxPairingAttempts; i++ {
		_, _, _, err = pm.HandlePairRequest("9999", "d_remote", "remote", "100.64.1.2:9100")
		if err == nil {
			t.Fatalf("expected error on attempt %d", i+1)
		}
	}

	// Next attempt should fail with "too many"
	_, _, _, err = pm.HandlePairRequest("1234", "d_remote", "remote", "100.64.1.2:9100")
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Errorf("expected 'too many' error, got: %v", err)
	}
}

func TestPairing_Timeout(t *testing.T) {
	pm := newTestPairingManager(t)

	code, err := pm.StartPairing(1 * time.Millisecond)
	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}

	// Wait for timeout
	time.Sleep(10 * time.Millisecond)

	_, _, _, err = pm.HandlePairRequest(code, "d_remote", "remote", "100.64.1.2:9100")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestPairing_NoActiveSession(t *testing.T) {
	pm := newTestPairingManager(t)

	_, _, _, err := pm.HandlePairRequest("1234", "d_remote", "remote", "100.64.1.2:9100")
	if err == nil || !strings.Contains(err.Error(), "no active pairing") {
		t.Errorf("expected 'no active pairing' error, got: %v", err)
	}
}

func TestPairing_AlreadyInProgress(t *testing.T) {
	pm := newTestPairingManager(t)

	_, err := pm.StartPairing(5 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}

	// Second start should fail
	_, err = pm.StartPairing(5 * time.Minute)
	if err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("expected 'already in progress' error, got: %v", err)
	}
}

func TestPairing_Cancel(t *testing.T) {
	pm := newTestPairingManager(t)

	code, err := pm.StartPairing(5 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}

	pm.CancelPairing()

	if pm.HasActiveSession() {
		t.Error("session should be cleared after cancel")
	}

	_, _, _, err = pm.HandlePairRequest(code, "d_remote", "remote", "100.64.1.2:9100")
	if err == nil {
		t.Error("expected error after cancellation")
	}
}

func TestPairing_WaitForCompletion(t *testing.T) {
	pm := newTestPairingManager(t)

	code, err := pm.StartPairing(5 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}

	// Complete pairing in background
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _, _, _ = pm.HandlePairRequest(code, "d_remote", "remote-machine", "100.64.1.2:9100")
	}()

	result, err := pm.WaitForPairing()
	if err != nil {
		t.Fatalf("WaitForPairing: %v", err)
	}
	if result.PeerName != "remote-machine" {
		t.Errorf("PeerName = %q, want %q", result.PeerName, "remote-machine")
	}
}

func TestPairing_WaitTimeout(t *testing.T) {
	pm := newTestPairingManager(t)

	_, err := pm.StartPairing(50 * time.Millisecond)
	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}

	_, err = pm.WaitForPairing()
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestGeneratePairingCode(t *testing.T) {
	codes := make(map[string]bool)
	for i := 0; i < 100; i++ {
		code, err := generatePairingCode()
		if err != nil {
			t.Fatalf("generatePairingCode: %v", err)
		}
		if len(code) != 4 {
			t.Errorf("code length = %d, want 4", len(code))
		}
		// Should be all digits
		for _, c := range code {
			if c < '0' || c > '9' {
				t.Errorf("code %q contains non-digit character %c", code, c)
			}
		}
		codes[code] = true
	}
	// Should have some variety (not all the same)
	if len(codes) < 10 {
		t.Errorf("only %d unique codes out of 100, expected more variety", len(codes))
	}
}

func TestGeneratePairingToken(t *testing.T) {
	token, err := generatePairingToken()
	if err != nil {
		t.Fatalf("generatePairingToken: %v", err)
	}
	// 32 bytes = 64 hex characters
	if len(token) != 64 {
		t.Errorf("token length = %d, want 64", len(token))
	}

	// Second token should be different
	token2, _ := generatePairingToken()
	if token == token2 {
		t.Error("two generated tokens should not be identical")
	}
}
