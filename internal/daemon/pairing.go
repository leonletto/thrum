package daemon

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/identity"
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

// PairMetadata carries the identity metadata exchanged during a pair handshake.
// Both sides send their own metadata and receive the other side's.
type PairMetadata struct {
	DaemonID     string
	Name         string
	Address      string
	RepoName     string
	Hostname     string
	RepoPath     string
	GitOriginURL string
}

// PairingResult contains the outcome of a completed pairing.
type PairingResult struct {
	PeerName     string
	PeerAddress  string
	PeerDaemonID string
}

// PairingManager manages pairing sessions for the local daemon.
type PairingManager struct {
	mu            sync.Mutex
	session       *PairingSession
	peers         *PeerRegistry
	daemonID      string
	name          string
	localIdentity identity.Identity
	// done is signaled when a pairing completes successfully
	done chan *PairingResult
}

// NewPairingManager creates a new pairing manager.
// LocalIdentity provides the full identity metadata sent to the remote peer during pairing.
// The name parameter is the human-readable hostname/machine name for display.
func NewPairingManager(peers *PeerRegistry, localIdentity identity.Identity, name string) *PairingManager {
	return &PairingManager{
		peers:         peers,
		daemonID:      localIdentity.DaemonID,
		name:          name,
		localIdentity: localIdentity,
	}
}

// StartPairing begins a new pairing session. Returns the 16-digit code for display.
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

	code, err := generatePairingCode(16)
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
func (pm *PairingManager) WaitForPairing(ctx context.Context) (*PairingResult, error) {
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
	case <-ctx.Done():
		pm.CancelPairing()
		return nil, ctx.Err()
	case <-time.After(timeout):
		pm.CancelPairing()
		return nil, fmt.Errorf("pairing timed out")
	}
}

// HandlePairRequest handles an incoming pair.request from a remote peer.
// The remote peer sends its PairMetadata (daemon_id, name, address, and identity fields).
// If the code matches, the peer is stored and the local identity metadata is returned.
func (pm *PairingManager) HandlePairRequest(code string, peer PairMetadata) (token string, local PairMetadata, err error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.session == nil {
		return "", PairMetadata{}, fmt.Errorf("no active pairing session")
	}

	if pm.session.IsExpired() {
		pm.session = nil
		pm.done = nil
		return "", PairMetadata{}, fmt.Errorf("pairing timed out")
	}

	pm.session.attempts++
	if pm.session.attempts > MaxPairingAttempts {
		pm.session = nil
		pm.done = nil
		return "", PairMetadata{}, fmt.Errorf("too many failed attempts")
	}

	if code != pm.session.Code {
		remaining := MaxPairingAttempts - pm.session.attempts
		return "", PairMetadata{}, fmt.Errorf("invalid pairing code (%d attempts remaining)", remaining)
	}

	// Check if already paired
	if existing := pm.peers.FindPeerByToken(pm.session.Token); existing != nil {
		return "", PairMetadata{}, fmt.Errorf("already paired with %s", existing.Name)
	}

	// Store the peer (listener side — we accepted the incoming pair request)
	err = pm.peers.AddPeer(&PeerInfo{
		Name:               peer.Name,
		Address:            peer.Address,
		DaemonID:           peer.DaemonID,
		Token:              pm.session.Token,
		Role:               "listener",
		RemoteRepoName:     peer.RepoName,
		RemoteHostname:     peer.Hostname,
		RemoteRepoPath:     peer.RepoPath,
		RemoteGitOriginURL: peer.GitOriginURL,
	})
	if err != nil {
		return "", PairMetadata{}, fmt.Errorf("store peer: %w", err)
	}

	token = pm.session.Token
	local = PairMetadata{
		DaemonID:     pm.localIdentity.DaemonID,
		Name:         pm.name,
		RepoName:     pm.localIdentity.RepoName,
		Hostname:     pm.localIdentity.Hostname,
		RepoPath:     pm.localIdentity.RepoPath,
		GitOriginURL: pm.localIdentity.GitOriginURL,
	}

	// Signal completion
	if pm.done != nil {
		pm.done <- &PairingResult{
			PeerName:     peer.Name,
			PeerAddress:  peer.Address,
			PeerDaemonID: peer.DaemonID,
		}
	}

	log.Printf("[pairing] Paired with %s (%s) at %s", peer.Name, peer.DaemonID, peer.Address)
	pm.session = nil

	return token, local, nil
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

// generatePairingCode generates a random numeric code of the given length (4-16 digits).
func generatePairingCode(length int) (string, error) {
	if length < 4 || length > 16 {
		return "", fmt.Errorf("pairing code length must be 4-16, got %d", length)
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate pairing code: %w", err)
	}
	n := binary.BigEndian.Uint64(b)
	mod := uint64(1)
	for i := 0; i < length; i++ {
		mod *= 10
	}
	n = n % mod
	return fmt.Sprintf("%0*d", length, n), nil
}

// generatePairingToken generates a random 32-byte hex-encoded token.
func generatePairingToken() (string, error) {
	b := make([]byte, PairingTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
