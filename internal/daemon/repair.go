package daemon

import (
	"fmt"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/identity"
)

// xir.27 sub-4: listener-side of the peer.repair RPC.
//
// Repair is the manual escalation path when auto-reconcile (xir.29) cannot
// resolve peer drift. It uses the Token stored in peers.json as its trust
// anchor: a dialer presenting a token that matches an existing peer entry
// may refresh that entry's DaemonID, Address, and identity metadata without
// re-pairing or minting a new token.
//
// Contrast with pair.request:
//   - pair.request MINTS a token from an active pairing code; the entry is NEW.
//   - peer.repair VERIFIES an existing token; the entry is UPDATED in place.
//
// The two protocols are intentionally separate (see thrum-xir.27 "Research
// notes: sub-4 graft surface analysis"). PairingManager is untouched.

// PeerRepairManager handles incoming peer.repair requests. It wraps the peer
// registry (for lookup + update) and carries the local identity metadata
// that is returned to the dialer after a successful repair.
//
// The internal mutex serializes repair calls across goroutines so the
// FindByToken → mutate → Remove → Add sequence is atomic with respect to
// other repair requests. Two concurrent repair calls presenting the same
// stored token would otherwise race between the RemovePeer of the old key
// and the AddPeer of the new key, producing divergent state.
type PeerRepairManager struct {
	mu            sync.Mutex
	peers         *PeerRegistry
	localIdentity identity.Identity
	localName     string
}

// NewPeerRepairManager creates a manager bound to the given peer registry
// and local identity. LocalName is the display name returned to the dialer
// (typically the hostname, matching PairingManager's convention).
func NewPeerRepairManager(peers *PeerRegistry, localIdent identity.Identity, localName string) *PeerRepairManager {
	return &PeerRepairManager{
		peers:         peers,
		localIdentity: localIdent,
		localName:     localName,
	}
}

// HandleRepairRequest processes an incoming peer.repair call.
//
// Token is the dialer's stored authenticator from the original pair; it
// MUST match an existing entry in peers.json. The entry's DaemonID,
// Address, and remote identity fields are refreshed with the dialer-
// supplied values — this covers the common drift scenarios that auto-
// reconcile (xir.29) cannot resolve on its own (e.g., daemon_id rotation).
//
// Returns the local daemon's current identity so the dialer can refresh
// its own entry in turn. Name is preserved; repair does not rename peers.
//
// Errors are deliberately terse — leaking details about why verification
// failed would aid token-guessing attacks. "no matching peer" covers both
// "token unknown" and "token empty".
func (m *PeerRepairManager) HandleRepairRequest(token string, dialer PairMetadata) (local PairMetadata, err error) {
	if token == "" {
		return PairMetadata{}, fmt.Errorf("peer.repair: token is required")
	}

	// Serialize repair calls so FindByToken → RemovePeer → AddPeer is
	// atomic against other repair goroutines. Without this lock, two
	// concurrent calls on the same token can interleave RemovePeer and
	// AddPeer and leave the registry with a stale key or duplicate entry.
	m.mu.Lock()
	defer m.mu.Unlock()

	existing := m.peers.FindPeerByToken(token)
	if existing == nil {
		return PairMetadata{}, fmt.Errorf("peer.repair: no matching peer")
	}

	// If the dialer's daemon_id rotated, the old entry's key becomes stale.
	// RemovePeer the old key before AddPeer stores under the new one;
	// otherwise both keys would survive and drift further. Preserve the
	// existing Name (repair is not a rename).
	oldDaemonID := existing.DaemonID
	refreshed := *existing
	refreshed.DaemonID = dialer.DaemonID
	if dialer.Address != "" {
		refreshed.Address = dialer.Address
	}
	if dialer.RepoName != "" {
		refreshed.RemoteRepoName = dialer.RepoName
	}
	if dialer.Hostname != "" {
		refreshed.RemoteHostname = dialer.Hostname
	}
	if dialer.RepoPath != "" {
		refreshed.RemoteRepoPath = dialer.RepoPath
	}
	if dialer.GitOriginURL != "" {
		refreshed.RemoteGitOriginURL = dialer.GitOriginURL
	}
	refreshed.LastSync = time.Now()

	if oldDaemonID != dialer.DaemonID && oldDaemonID != "" {
		if rmErr := m.peers.RemovePeer(oldDaemonID); rmErr != nil {
			return PairMetadata{}, fmt.Errorf("peer.repair: remove stale entry: %w", rmErr)
		}
	}
	if err := m.peers.AddPeer(&refreshed); err != nil {
		return PairMetadata{}, fmt.Errorf("peer.repair: update entry: %w", err)
	}

	return PairMetadata{
		DaemonID:     m.localIdentity.DaemonID,
		Name:         m.localName,
		RepoName:     m.localIdentity.RepoName,
		Hostname:     m.localIdentity.Hostname,
		RepoPath:     m.localIdentity.RepoPath,
		GitOriginURL: m.localIdentity.GitOriginURL,
	}, nil
}
