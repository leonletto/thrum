package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/leonletto/thrum/internal/identity"
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

// LocalConfig holds this daemon's local peering configuration.
type LocalConfig struct {
	DaemonID string `json:"daemon_id"`
	Port     int    `json:"port,omitempty"`
}

// peersFile is the on-disk format for peers.json (object schema).
type peersFile struct {
	Local LocalConfig `json:"local"`
	Peers []*PeerInfo `json:"peers"`
}

// PeerRegistry tracks paired peer daemons for sync.
// Thread-safe and persisted to disk.
type PeerRegistry struct {
	mu       sync.RWMutex
	peers    map[string]*PeerInfo // keyed by DaemonID
	local    LocalConfig
	filePath string // path to peers.json for persistence
}

// NewPeerRegistry creates a new peer registry. If filePath exists, peers are loaded from it.
func NewPeerRegistry(filePath string) (*PeerRegistry, error) {
	r := &PeerRegistry{
		peers:    make(map[string]*PeerInfo),
		filePath: filePath,
	}

	if _, err := os.Stat(filePath); err == nil {
		// File exists — load (handles both old array and new object format)
		if err := r.load(); err != nil {
			return nil, fmt.Errorf("load peer registry: %w", err)
		}
	} else {
		// No file — generate fresh local config
		r.local.DaemonID = identity.GenerateDaemonID()
	}

	return r, nil
}

// LocalDaemonID returns this daemon's persistent ID.
func (r *PeerRegistry) LocalDaemonID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.local.DaemonID
}

// LocalPort returns the tsnet listener port, or 0 if not yet assigned.
func (r *PeerRegistry) LocalPort() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.local.Port
}

// SetLocalPort sets the tsnet listener port and persists to disk.
func (r *PeerRegistry) SetLocalPort(port int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.local.Port = port
	return r.saveLocked()
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
	cloned := *p
	return &cloned
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
			cloned := *p
			return &cloned
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
			cloned := *p
			return &cloned
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
		cloned := *p
		result = append(result, &cloned)
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
		if err := r.saveLocked(); err != nil {
			log.Printf("peer_registry: failed to save after removing stale peers: %v", err)
		}
	}
	return removed
}

// Len returns the number of peers in the registry.
func (r *PeerRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.peers)
}

// load reads the peers.json file, auto-detecting old array or new object format.
func (r *PeerRegistry) load() error {
	data, err := os.ReadFile(r.filePath)
	if err != nil {
		return fmt.Errorf("read peers file: %w", err)
	}

	// Try new object format first
	var pf peersFile
	if err := json.Unmarshal(data, &pf); err == nil && pf.Local.DaemonID != "" {
		r.local = pf.Local
		for _, p := range pf.Peers {
			r.peers[p.DaemonID] = p
		}
		return nil
	}

	// Fall back to old array format — migrate
	var peers []*PeerInfo
	if err := json.Unmarshal(data, &peers); err != nil {
		return fmt.Errorf("unmarshal peers: %w", err)
	}

	r.local.DaemonID = identity.GenerateDaemonID()
	for _, p := range peers {
		r.peers[p.DaemonID] = p
	}

	// Persist migration to new format
	return r.saveLocked()
}

// saveLocked writes the peers to disk using atomic temp-file + rename.
// Caller must hold mu.
func (r *PeerRegistry) saveLocked() error {
	pf := peersFile{
		Local: r.local,
		Peers: make([]*PeerInfo, 0, len(r.peers)),
	}
	for _, p := range r.peers {
		pf.Peers = append(pf.Peers, p)
	}

	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal peers: %w", err)
	}

	dir := filepath.Dir(r.filePath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create peers directory: %w", err)
	}

	// Atomic write: temp file + rename
	tmpPath := r.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write peers temp file: %w", err)
	}
	if err := os.Rename(tmpPath, r.filePath); err != nil {
		return fmt.Errorf("rename peers file: %w", err)
	}

	return nil
}
