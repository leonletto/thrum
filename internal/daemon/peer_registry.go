package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PeerInfo represents a known sync peer daemon.
type PeerInfo struct {
	DaemonID  string    `json:"daemon_id"`
	Hostname  string    `json:"hostname"`
	FQDN      string    `json:"fqdn"`
	Port      int       `json:"port"`
	PublicKey string    `json:"public_key,omitempty"` // Ed25519 public key (placeholder — Epic 4)
	LastSeen  time.Time `json:"last_seen"`
	Status    string    `json:"status"` // "active", "stale", "offline"
}

// Addr returns the network address for connecting to this peer (FQDN:Port).
func (p *PeerInfo) Addr() string {
	host := p.FQDN
	if host == "" {
		host = p.Hostname
	}
	return fmt.Sprintf("%s:%d", host, p.Port)
}

// PeerRegistry tracks known peer daemons for sync.
// Thread-safe and persisted to disk.
type PeerRegistry struct {
	mu       sync.RWMutex
	peers    map[string]*PeerInfo // keyed by DaemonID
	filePath string               // path to peers.json for persistence
}

// NewPeerRegistry creates a new peer registry. If filePath exists, peers are loaded from it.
func NewPeerRegistry(filePath string) (*PeerRegistry, error) {
	r := &PeerRegistry{
		peers:    make(map[string]*PeerInfo),
		filePath: filePath,
	}

	// Load existing peers if file exists
	if _, err := os.Stat(filePath); err == nil {
		if err := r.load(); err != nil {
			return nil, fmt.Errorf("load peer registry: %w", err)
		}
	}

	return r, nil
}

// AddPeer adds or updates a peer in the registry and persists to disk.
// If the peer already exists with a public key and the new key differs,
// the key change is rejected (TOFU: trust on first use) and an error is returned.
// Use ForceUpdatePeerKey for manual key rotation after verification.
func (r *PeerRegistry) AddPeer(info *PeerInfo) error {
	if info.DaemonID == "" {
		return fmt.Errorf("peer daemon_id is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	info.LastSeen = time.Now()
	if info.Status == "" {
		info.Status = "active"
	}

	// TOFU key pinning: reject public key changes
	if existing, ok := r.peers[info.DaemonID]; ok {
		if existing.PublicKey != "" && info.PublicKey != "" && existing.PublicKey != info.PublicKey {
			log.Printf("[peer_registry] WARNING: Public key change REJECTED for peer %s (daemon_id=%s). "+
				"Existing key fingerprint differs from new key. "+
				"If this is a legitimate key rotation, use 'thrum daemon peers trust %s' to update.",
				info.Hostname, info.DaemonID, info.DaemonID)
			return fmt.Errorf("public key change rejected for peer %s (TOFU): use ForceUpdatePeerKey for manual rotation", info.DaemonID)
		}
		// Preserve existing key if new info doesn't include one
		if info.PublicKey == "" && existing.PublicKey != "" {
			info.PublicKey = existing.PublicKey
		}
	} else if info.PublicKey != "" {
		// First time seeing this peer's key — log fingerprint for verification
		log.Printf("[peer_registry] TOFU: Pinning public key for peer %s (daemon_id=%s): key=%s...",
			info.Hostname, info.DaemonID, truncateKey(info.PublicKey))
	}

	r.peers[info.DaemonID] = info

	return r.saveLocked()
}

// ForceUpdatePeerKey updates the public key for a peer, bypassing TOFU protection.
// Use this for manual key rotation after out-of-band verification.
func (r *PeerRegistry) ForceUpdatePeerKey(daemonID string, newPublicKey string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.peers[daemonID]
	if !ok {
		return fmt.Errorf("peer %s not found", daemonID)
	}

	log.Printf("[peer_registry] Public key FORCE UPDATED for peer %s (daemon_id=%s): old=%s... new=%s...",
		p.Hostname, daemonID, truncateKey(p.PublicKey), truncateKey(newPublicKey))
	p.PublicKey = newPublicKey
	p.LastSeen = time.Now()

	return r.saveLocked()
}

// truncateKey returns the first 16 characters of a key for safe logging.
func truncateKey(key string) string {
	if len(key) > 16 {
		return key[:16]
	}
	return key
}

// GetPeer returns the peer info for the given daemon ID, or nil if not found.
func (r *PeerRegistry) GetPeer(daemonID string) *PeerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.peers[daemonID]
	if !ok {
		return nil
	}

	// Return a copy to avoid race conditions
	copy := *p
	return &copy
}

// ListPeers returns a snapshot of all peers.
func (r *PeerRegistry) ListPeers() []*PeerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*PeerInfo, 0, len(r.peers))
	for _, p := range r.peers {
		copy := *p
		result = append(result, &copy)
	}
	return result
}

// RemovePeer removes a peer by daemon ID and persists to disk.
func (r *PeerRegistry) RemovePeer(daemonID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.peers, daemonID)
	return r.saveLocked()
}

// UpdatePeerLastSeen updates the last seen timestamp for a peer and sets status to active.
func (r *PeerRegistry) UpdatePeerLastSeen(daemonID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.peers[daemonID]
	if !ok {
		return fmt.Errorf("peer %s not found", daemonID)
	}

	p.LastSeen = time.Now()
	p.Status = "active"
	return r.saveLocked()
}

// RemoveStalePeers removes peers whose LastSeen is older than the given timeout.
// Returns the number of peers removed.
func (r *PeerRegistry) RemoveStalePeers(timeout time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-timeout)
	removed := 0
	for id, p := range r.peers {
		if p.LastSeen.Before(cutoff) {
			delete(r.peers, id)
			removed++
		}
	}

	if removed > 0 {
		_ = r.saveLocked()
	}
	return removed
}

// Len returns the number of peers in the registry.
func (r *PeerRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.peers)
}

// load reads the peers.json file. Must be called without holding mu.
func (r *PeerRegistry) load() error {
	data, err := os.ReadFile(r.filePath)
	if err != nil {
		return fmt.Errorf("read peers file: %w", err)
	}

	var peers []*PeerInfo
	if err := json.Unmarshal(data, &peers); err != nil {
		return fmt.Errorf("unmarshal peers: %w", err)
	}

	for _, p := range peers {
		r.peers[p.DaemonID] = p
	}
	return nil
}

// saveLocked writes the peers to disk. Caller must hold mu.
func (r *PeerRegistry) saveLocked() error {
	peers := make([]*PeerInfo, 0, len(r.peers))
	for _, p := range r.peers {
		peers = append(peers, p)
	}

	data, err := json.MarshalIndent(peers, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal peers: %w", err)
	}

	dir := filepath.Dir(r.filePath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create peers directory: %w", err)
	}

	if err := os.WriteFile(r.filePath, data, 0600); err != nil {
		return fmt.Errorf("write peers file: %w", err)
	}

	return nil
}
