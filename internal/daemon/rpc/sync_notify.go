package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// SyncNotifyRequest represents the params for a sync.notify RPC call.
type SyncNotifyRequest struct {
	DaemonID   string `json:"daemon_id"`
	LatestSeq  int64  `json:"latest_seq"`
	EventCount int    `json:"event_count"`
}

// SyncTriggerFunc is called to initiate a pull sync from a peer.
// It receives the daemon ID and peer address (resolved by the caller).
type SyncTriggerFunc func(daemonID string)

// SyncNotifyHandler handles the sync.notify RPC method.
// When a peer notifies us of new events, we trigger an async pull sync.
// Notifications are debounced per-peer to avoid redundant syncs.
type SyncNotifyHandler struct {
	triggerSync SyncTriggerFunc

	mu       sync.Mutex
	syncing  map[string]bool      // daemonID -> currently syncing
	pending  map[string]time.Time // daemonID -> queued notification time
	debounce time.Duration
}

// NewSyncNotifyHandler creates a new sync.notify handler.
// triggerSync is called asynchronously to pull events from the notifying peer.
func NewSyncNotifyHandler(triggerSync SyncTriggerFunc) *SyncNotifyHandler {
	return &SyncNotifyHandler{
		triggerSync: triggerSync,
		syncing:     make(map[string]bool),
		pending:     make(map[string]time.Time),
		debounce:    2 * time.Second,
	}
}

// Handle handles a sync.notify request. Returns immediately; sync happens async.
func (h *SyncNotifyHandler) Handle(_ context.Context, params json.RawMessage) (any, error) {
	var req SyncNotifyRequest
	if params == nil {
		return nil, fmt.Errorf("missing params")
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if req.DaemonID == "" {
		return nil, fmt.Errorf("daemon_id is required")
	}

	log.Printf("sync.notify received from %s (latest_seq=%d, event_count=%d)", req.DaemonID, req.LatestSeq, req.EventCount)

	h.mu.Lock()
	if h.syncing[req.DaemonID] {
		// Already syncing from this peer — queue for later
		h.pending[req.DaemonID] = time.Now()
		h.mu.Unlock()
		log.Printf("sync.notify: already syncing from %s, queued", req.DaemonID)
		return map[string]string{"status": "queued"}, nil
	}

	h.syncing[req.DaemonID] = true
	h.mu.Unlock()

	// Trigger async pull sync
	go h.doSync(req.DaemonID)

	return map[string]string{"status": "ok"}, nil
}

// doSync performs the sync and processes any pending notifications that arrived during sync.
func (h *SyncNotifyHandler) doSync(daemonID string) {
	h.triggerSync(daemonID)

	for {
		h.mu.Lock()
		pendingTime, hasPending := h.pending[daemonID]
		if !hasPending {
			// No pending notifications — done
			delete(h.syncing, daemonID)
			h.mu.Unlock()
			return
		}

		// Check debounce window
		if time.Since(pendingTime) < h.debounce {
			// Pending notification is too recent — wait briefly then re-check
			delete(h.pending, daemonID)
			h.mu.Unlock()
			time.Sleep(h.debounce)
			continue
		}

		// Process pending notification
		delete(h.pending, daemonID)
		h.mu.Unlock()

		log.Printf("sync.notify: processing queued notification from %s", daemonID)
		h.triggerSync(daemonID)
	}
}

// IsSyncing returns whether a sync is currently in progress for the given peer.
func (h *SyncNotifyHandler) IsSyncing(daemonID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.syncing[daemonID]
}

// HasPending returns whether there's a pending notification for the given peer.
func (h *SyncNotifyHandler) HasPending(daemonID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.pending[daemonID]
	return ok
}
