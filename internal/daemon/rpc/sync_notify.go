package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// SyncNotifyRequest represents the params for a sync.notify RPC call.
type SyncNotifyRequest struct {
	Token      string `json:"token"`
	DaemonID   string `json:"daemon_id"`
	LatestSeq  int64  `json:"latest_seq"`
	EventCount int    `json:"event_count"`
}

// SyncTriggerFunc is called to initiate a pull sync from a peer.
// It receives the daemon ID and peer address (resolved by the caller).
type SyncTriggerFunc func(daemonID string)

// SyncNotifyHandler handles the sync.notify RPC method.
// When a peer notifies us of new events, we trigger an async pull sync.
type SyncNotifyHandler struct {
	triggerSync SyncTriggerFunc
}

// NewSyncNotifyHandler creates a new sync.notify handler.
// triggerSync is called asynchronously to pull events from the notifying peer.
func NewSyncNotifyHandler(triggerSync SyncTriggerFunc) *SyncNotifyHandler {
	return &SyncNotifyHandler{
		triggerSync: triggerSync,
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

	go h.triggerSync(req.DaemonID)

	return map[string]string{"status": "ok"}, nil
}
