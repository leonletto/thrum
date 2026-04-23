package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/identity"
)

func newTestPairingManager(t *testing.T) *PairingManager {
	t.Helper()
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	return NewPairingManager(reg, identity.Identity{
		DaemonID: "d_local",
		RepoName: "local-repo",
		Hostname: "local-host",
		RepoPath: "/local/path",
	}, "local-machine")
}

func TestPairing_HappyPath(t *testing.T) {
	pm := newTestPairingManager(t)

	// Start pairing
	code, err := pm.StartPairing(5 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}
	if len(code) != 16 {
		t.Errorf("code length = %d, want 16", len(code))
	}

	// Handle pair request with correct code
	token, local, err := pm.HandlePairRequest(code, PairMetadata{
		DaemonID: "d_remote",
		Name:     "remote-machine",
		Address:  "100.64.1.2:9100",
	})
	if err != nil {
		t.Fatalf("HandlePairRequest: %v", err)
	}
	if token == "" {
		t.Error("token should not be empty")
	}
	if local.DaemonID != "d_local" {
		t.Errorf("local.DaemonID = %q, want %q", local.DaemonID, "d_local")
	}
	if local.Name != "local-machine" {
		t.Errorf("local.Name = %q, want %q", local.Name, "local-machine")
	}

	// Verify peer was stored with listener role
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
	if peer.Role != "listener" {
		t.Errorf("peer.Role = %q, want %q", peer.Role, "listener")
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
	_, _, err = pm.HandlePairRequest("0000", PairMetadata{DaemonID: "d_remote", Name: "remote", Address: "100.64.1.2:9100"})
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
	_, _, err = pm.HandlePairRequest(code, PairMetadata{DaemonID: "d_remote", Name: "remote", Address: "100.64.1.2:9100"})
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
		_, _, err = pm.HandlePairRequest("9999", PairMetadata{DaemonID: "d_remote", Name: "remote", Address: "100.64.1.2:9100"})
		if err == nil {
			t.Fatalf("expected error on attempt %d", i+1)
		}
	}

	// Next attempt should fail with "too many"
	_, _, err = pm.HandlePairRequest("1234", PairMetadata{DaemonID: "d_remote", Name: "remote", Address: "100.64.1.2:9100"})
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

	_, _, err = pm.HandlePairRequest(code, PairMetadata{DaemonID: "d_remote", Name: "remote", Address: "100.64.1.2:9100"})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestPairing_NoActiveSession(t *testing.T) {
	pm := newTestPairingManager(t)

	_, _, err := pm.HandlePairRequest("1234", PairMetadata{DaemonID: "d_remote", Name: "remote", Address: "100.64.1.2:9100"})
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

	_, _, err = pm.HandlePairRequest(code, PairMetadata{DaemonID: "d_remote", Name: "remote", Address: "100.64.1.2:9100"})
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
		_, _, _ = pm.HandlePairRequest(code, PairMetadata{DaemonID: "d_remote", Name: "remote-machine", Address: "100.64.1.2:9100"})
	}()

	result, err := pm.WaitForPairing(context.Background())
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

	_, err = pm.WaitForPairing(context.Background())
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestPairing_WaitContextCanceled(t *testing.T) {
	pm := newTestPairingManager(t)

	_, err := pm.StartPairing(5 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}

	// Create a context with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = pm.WaitForPairing(ctx)
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got: %v", err)
	}
}

func TestGeneratePairingCode(t *testing.T) {
	for _, length := range []int{4, 8, 16} {
		codes := make(map[string]bool)
		for i := 0; i < 100; i++ {
			code, err := generatePairingCode(length)
			if err != nil {
				t.Fatalf("generatePairingCode(%d): %v", length, err)
			}
			if len(code) != length {
				t.Errorf("code length = %d, want %d", len(code), length)
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
			t.Errorf("length=%d: only %d unique codes out of 100, expected more variety", length, len(codes))
		}
	}
}

func TestGeneratePairingCode_InvalidLength(t *testing.T) {
	for _, length := range []int{0, 3, 17, 100} {
		_, err := generatePairingCode(length)
		if err == nil {
			t.Errorf("generatePairingCode(%d) should return error", length)
		}
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

// TestPairing_MetadataExchange verifies that both sides of a pair handshake
// have the remote peer's identity metadata stored in peers.json.
func TestPairing_MetadataExchange(t *testing.T) {
	// Set up side A (listener)
	dirA := t.TempDir()
	regA, err := NewPeerRegistry(filepath.Join(dirA, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry A: %v", err)
	}
	identA := identity.Identity{
		DaemonID:     "d_alpha",
		RepoName:     "repo-alpha",
		Hostname:     "host-alpha",
		RepoPath:     "/repos/alpha",
		GitOriginURL: "https://github.com/example/alpha",
	}
	pmA := NewPairingManager(regA, identA, "machine-alpha")

	// Set up side B (dialer)
	dirB := t.TempDir()
	regB, err := NewPeerRegistry(filepath.Join(dirB, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry B: %v", err)
	}
	identB := identity.Identity{
		DaemonID:     "d_beta",
		RepoName:     "repo-beta",
		Hostname:     "host-beta",
		RepoPath:     "/repos/beta",
		GitOriginURL: "https://github.com/example/beta",
	}

	// A starts listening
	code, err := pmA.StartPairing(5 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairing: %v", err)
	}

	// B sends pair request to A (simulating what JoinPeer/RequestPairing do)
	bMeta := PairMetadata{
		DaemonID:     identB.DaemonID,
		Name:         "machine-beta",
		Address:      "100.64.2.1:9100",
		RepoName:     identB.RepoName,
		Hostname:     identB.Hostname,
		RepoPath:     identB.RepoPath,
		GitOriginURL: identB.GitOriginURL,
	}
	token, localMeta, err := pmA.HandlePairRequest(code, bMeta)
	if err != nil {
		t.Fatalf("HandlePairRequest: %v", err)
	}

	// B stores A's returned metadata (simulating what JoinPeer does after RequestPairing)
	peerInfoOnB := &PeerInfo{
		DaemonID:           localMeta.DaemonID,
		Name:               localMeta.Name,
		Address:            "100.64.1.1:9100",
		Token:              token,
		RemoteRepoName:     localMeta.RepoName,
		RemoteHostname:     localMeta.Hostname,
		RemoteRepoPath:     localMeta.RepoPath,
		RemoteGitOriginURL: localMeta.GitOriginURL,
	}
	if err := regB.AddPeer(peerInfoOnB); err != nil {
		t.Fatalf("regB.AddPeer: %v", err)
	}

	// --- Assert A's peers.json has B's metadata ---
	peerBOnA := regA.GetPeer("d_beta")
	if peerBOnA == nil {
		t.Fatal("A should have B as a peer after pairing")
	}
	if peerBOnA.RemoteRepoName != "repo-beta" {
		t.Errorf("A.peer[B].RemoteRepoName = %q, want %q", peerBOnA.RemoteRepoName, "repo-beta")
	}
	if peerBOnA.RemoteHostname != "host-beta" {
		t.Errorf("A.peer[B].RemoteHostname = %q, want %q", peerBOnA.RemoteHostname, "host-beta")
	}
	if peerBOnA.RemoteRepoPath != "/repos/beta" {
		t.Errorf("A.peer[B].RemoteRepoPath = %q, want %q", peerBOnA.RemoteRepoPath, "/repos/beta")
	}
	if peerBOnA.RemoteGitOriginURL != "https://github.com/example/beta" {
		t.Errorf("A.peer[B].RemoteGitOriginURL = %q, want %q", peerBOnA.RemoteGitOriginURL, "https://github.com/example/beta")
	}

	// --- Assert B's peers.json has A's metadata ---
	peerAOnB := regB.GetPeer("d_alpha")
	if peerAOnB == nil {
		t.Fatal("B should have A as a peer after pairing")
	}
	if peerAOnB.RemoteRepoName != "repo-alpha" {
		t.Errorf("B.peer[A].RemoteRepoName = %q, want %q", peerAOnB.RemoteRepoName, "repo-alpha")
	}
	if peerAOnB.RemoteHostname != "host-alpha" {
		t.Errorf("B.peer[A].RemoteHostname = %q, want %q", peerAOnB.RemoteHostname, "host-alpha")
	}
	if peerAOnB.RemoteRepoPath != "/repos/alpha" {
		t.Errorf("B.peer[A].RemoteRepoPath = %q, want %q", peerAOnB.RemoteRepoPath, "/repos/alpha")
	}
	if peerAOnB.RemoteGitOriginURL != "https://github.com/example/alpha" {
		t.Errorf("B.peer[A].RemoteGitOriginURL = %q, want %q", peerAOnB.RemoteGitOriginURL, "https://github.com/example/alpha")
	}

	// Verify the returned local metadata from A has correct identity fields
	if localMeta.DaemonID != "d_alpha" {
		t.Errorf("localMeta.DaemonID = %q, want %q", localMeta.DaemonID, "d_alpha")
	}
	if localMeta.RepoName != "repo-alpha" {
		t.Errorf("localMeta.RepoName = %q, want %q", localMeta.RepoName, "repo-alpha")
	}
	if localMeta.Hostname != "host-alpha" {
		t.Errorf("localMeta.Hostname = %q, want %q", localMeta.Hostname, "host-alpha")
	}
	if localMeta.GitOriginURL != "https://github.com/example/alpha" {
		t.Errorf("localMeta.GitOriginURL = %q, want %q", localMeta.GitOriginURL, "https://github.com/example/alpha")
	}
}
