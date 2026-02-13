package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	DefaultPairingTimeout = 5 * time.Minute
	MaxPairingAttempts    = 3
	PairingTokenBytes     = 32
)

// PairingSession represents an active pairing session on the server side (Machine A).
type PairingSession struct {
	Code      string
	Token     string
	CreatedAt time.Time
	Timeout   time.Duration
	attempts  int
}

// IsExpired returns true if the session has timed out.
func (s *PairingSession) IsExpired() bool {
	return time.Since(s.CreatedAt) > s.Timeout
}

// PairingResult contains the outcome of a completed pairing.
type PairingResult struct {
	PeerName     string
	PeerAddress  string
	PeerDaemonID string
}

// PairingManager manages pairing sessions for the local daemon.
type PairingManager struct {
	mu       sync.Mutex
	session  *PairingSession
	peers    *PeerRegistry
	daemonID string
	name     string
	// done is signaled when a pairing completes successfully
	done chan *PairingResult
}

// NewPairingManager creates a new pairing manager.
func NewPairingManager(peers *PeerRegistry, daemonID, name string) *PairingManager {
	return &PairingManager{
		peers:    peers,
		daemonID: daemonID,
		name:     name,
	}
}

// StartPairing begins a new pairing session. Returns the 4-digit code for display.
// Returns an error if a pairing session is already active.
func (pm *PairingManager) StartPairing(timeout time.Duration) (string, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Clean up expired session
	if pm.session != nil && pm.session.IsExpired() {
		pm.session = nil
		pm.done = nil
	}

	if pm.session != nil {
		return "", fmt.Errorf("pairing already in progress (code generated %s ago)", time.Since(pm.session.CreatedAt).Truncate(time.Second))
	}

	code, err := generatePairingCode()
	if err != nil {
		return "", fmt.Errorf("generate pairing code: %w", err)
	}

	token, err := generatePairingToken()
	if err != nil {
		return "", fmt.Errorf("generate pairing token: %w", err)
	}

	pm.session = &PairingSession{
		Code:      code,
		Token:     token,
		CreatedAt: time.Now(),
		Timeout:   timeout,
	}
	pm.done = make(chan *PairingResult, 1)

	log.Printf("[pairing] Session started, code=%s, timeout=%s", code, timeout)
	return code, nil
}

// WaitForPairing blocks until a pairing completes or times out.
// Returns the pairing result on success, or an error on timeout/cancellation.
func (pm *PairingManager) WaitForPairing() (*PairingResult, error) {
	pm.mu.Lock()
	if pm.session == nil {
		pm.mu.Unlock()
		return nil, fmt.Errorf("no active pairing session")
	}
	timeout := pm.session.Timeout - time.Since(pm.session.CreatedAt)
	done := pm.done
	pm.mu.Unlock()

	if timeout <= 0 {
		pm.CancelPairing()
		return nil, fmt.Errorf("pairing timed out")
	}

	select {
	case result := <-done:
		if result == nil {
			return nil, fmt.Errorf("pairing canceled")
		}
		return result, nil
	case <-time.After(timeout):
		pm.CancelPairing()
		return nil, fmt.Errorf("pairing timed out")
	}
}

// HandlePairRequest handles an incoming pair.request from a remote peer.
// The remote peer sends: code, daemon_id, name, address.
// If the code matches, the peer is stored and the auth token is returned.
func (pm *PairingManager) HandlePairRequest(code, peerDaemonID, peerName, peerAddress string) (token, localDaemonID, localName string, err error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.session == nil {
		return "", "", "", fmt.Errorf("no active pairing session")
	}

	if pm.session.IsExpired() {
		pm.session = nil
		pm.done = nil
		return "", "", "", fmt.Errorf("pairing timed out")
	}

	pm.session.attempts++
	if pm.session.attempts > MaxPairingAttempts {
		pm.session = nil
		pm.done = nil
		return "", "", "", fmt.Errorf("too many failed attempts")
	}

	if code != pm.session.Code {
		remaining := MaxPairingAttempts - pm.session.attempts
		return "", "", "", fmt.Errorf("invalid pairing code (%d attempts remaining)", remaining)
	}

	// Check if already paired
	if existing := pm.peers.FindPeerByToken(pm.session.Token); existing != nil {
		return "", "", "", fmt.Errorf("already paired with %s", existing.Name)
	}

	// Store the peer
	err = pm.peers.AddPeer(&PeerInfo{
		Name:     peerName,
		Address:  peerAddress,
		DaemonID: peerDaemonID,
		Token:    pm.session.Token,
	})
	if err != nil {
		return "", "", "", fmt.Errorf("store peer: %w", err)
	}

	token = pm.session.Token
	localDaemonID = pm.daemonID
	localName = pm.name

	// Signal completion
	if pm.done != nil {
		pm.done <- &PairingResult{
			PeerName:     peerName,
			PeerAddress:  peerAddress,
			PeerDaemonID: peerDaemonID,
		}
	}

	log.Printf("[pairing] Paired with %s (%s) at %s", peerName, peerDaemonID, peerAddress)
	pm.session = nil

	return token, localDaemonID, localName, nil
}

// CancelPairing cancels the active pairing session.
func (pm *PairingManager) CancelPairing() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.done != nil {
		select {
		case pm.done <- nil:
		default:
		}
	}
	pm.session = nil
	pm.done = nil
}

// HasActiveSession returns true if there is an active (non-expired) pairing session.
func (pm *PairingManager) HasActiveSession() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.session == nil {
		return false
	}
	if pm.session.IsExpired() {
		pm.session = nil
		pm.done = nil
		return false
	}
	return true
}

// generatePairingCode generates a random 4-digit numeric code.
func generatePairingCode() (string, error) {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// Convert to 4-digit code (0000-9999)
	n := int(b[0])<<8 | int(b[1])
	return fmt.Sprintf("%04d", n%10000), nil
}

// generatePairingToken generates a random 32-byte hex-encoded token.
func generatePairingToken() (string, error) {
	b := make([]byte, PairingTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
