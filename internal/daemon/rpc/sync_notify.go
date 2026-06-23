package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// syncNotifyPoolSize bounds the number of concurrent peer-sync goroutines
// kicked off by inbound sync.notify RPCs. bpq5's 8-concurrent send burst
// observed 327 received / 257 DROPPED at the previous ceiling of 10 (busy
// 2-peer cluster); 100 absorbs realistic multi-peer bursts. Peers do not
// retry dropped notifications (BroadcastNotify in sync_manager.go is
// fire-and-forget), so raising the ceiling does not amplify load.
const syncNotifyPoolSize = 100

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
	sem         chan struct{}
}

// NewSyncNotifyHandler creates a new sync.notify handler.
// TriggerSync is called asynchronously to pull events from the notifying peer.
func NewSyncNotifyHandler(triggerSync SyncTriggerFunc) *SyncNotifyHandler {
	return &SyncNotifyHandler{
		triggerSync: triggerSync,
		sem:         make(chan struct{}, syncNotifyPoolSize),
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

	select {
	case h.sem <- struct{}{}:
		go func() {
			defer func() { <-h.sem }()
			h.triggerSync(req.DaemonID)
		}()
	default:
		log.Printf("sync.notify: goroutine pool full, dropping notification from %s", req.DaemonID)
	}

	return map[string]string{"status": "ok"}, nil
}
