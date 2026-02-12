package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PeerInfo represents a paired sync peer.
type PeerInfo struct {
	Name     string    `json:"name"`
	Address  string    `json:"address"`
	DaemonID string    `json:"daemon_id"`
	Token    string    `json:"token,omitempty"`
	PairedAt time.Time `json:"paired_at"`
	LastSync time.Time `json:"last_sync"`
}

// Addr returns the network address for connecting to this peer.
func (p *PeerInfo) Addr() string {
	return p.Address
}

// PeerRegistry tracks paired peer daemons for sync.
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
func (r *PeerRegistry) AddPeer(info *PeerInfo) error {
	if info.DaemonID == "" {
		return fmt.Errorf("peer daemon_id is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if info.PairedAt.IsZero() {
		info.PairedAt = time.Now()
	}
	if info.LastSync.IsZero() {
		info.LastSync = time.Now()
	}

	r.peers[info.DaemonID] = info

	return r.saveLocked()
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

// FindPeerByToken returns the peer with the given auth token, or nil if not found.
func (r *PeerRegistry) FindPeerByToken(token string) *PeerInfo {
	if token == "" {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.peers {
		if p.Token == token {
			copy := *p
			return &copy
		}
	}
	return nil
}

// FindPeerByName returns the peer with the given name, or nil if not found.
func (r *PeerRegistry) FindPeerByName(name string) *PeerInfo {
	if name == "" {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.peers {
		if p.Name == name {
			copy := *p
			return &copy
		}
	}
	return nil
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

// UpdateLastSync updates the last sync timestamp for a peer.
func (r *PeerRegistry) UpdateLastSync(daemonID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.peers[daemonID]
	if !ok {
		return fmt.Errorf("peer %s not found", daemonID)
	}

	p.LastSync = time.Now()
	return r.saveLocked()
}

// RemoveStalePeers removes peers whose LastSync is older than the given timeout.
// Returns the number of peers removed.
func (r *PeerRegistry) RemoveStalePeers(timeout time.Duration) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-timeout)
	removed := 0
	for id, p := range r.peers {
		if p.LastSync.Before(cutoff) {
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
