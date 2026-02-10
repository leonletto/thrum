package rpc

import (
	"context"
	"encoding/json"

	"github.com/leonletto/thrum/internal/sync"
)

// SyncForceRequest represents a request to force a sync.
type SyncForceRequest struct{}

// SyncForceResponse represents the response from a force sync.
type SyncForceResponse struct {
	Triggered  bool   `json:"triggered"`    // Whether sync was triggered
	LastSyncAt string `json:"last_sync_at"` // ISO 8601 timestamp of last sync
	SyncState  string `json:"sync_state"`   // "running", "idle"
	LocalOnly  bool   `json:"local_only"`   // Whether running in local-only mode
}

// SyncStatusRequest represents a request for sync status.
type SyncStatusRequest struct{}

// SyncStatusResponse represents the current sync status.
type SyncStatusResponse struct {
	Running    bool   `json:"running"`      // Whether sync loop is running
	LastSyncAt string `json:"last_sync_at"` // ISO 8601 timestamp of last sync
	LastError  string `json:"last_error,omitempty"`
	SyncState  string `json:"sync_state"`  // "running", "idle", "error"
	LocalOnly  bool   `json:"local_only"`  // Whether running in local-only mode
}

// SyncForceHandler handles forced sync requests.
type SyncForceHandler struct {
	syncLoop *sync.SyncLoop
}

// NewSyncForceHandler creates a new sync force handler.
func NewSyncForceHandler(syncLoop *sync.SyncLoop) *SyncForceHandler {
	return &SyncForceHandler{
		syncLoop: syncLoop,
	}
}

// Handle triggers a manual sync.
func (h *SyncForceHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	// Trigger manual sync (non-blocking)
	h.syncLoop.TriggerSync()

	// Get current status
	status := h.syncLoop.GetStatus()

	response := SyncForceResponse{
		Triggered: true,
		SyncState: getSyncState(status),
		LocalOnly: status.LocalOnly,
	}

	if !status.LastSyncAt.IsZero() {
		response.LastSyncAt = status.LastSyncAt.Format("2006-01-02T15:04:05Z07:00")
	}

	return response, nil
}

// SyncStatusHandler handles sync status requests.
type SyncStatusHandler struct {
	syncLoop *sync.SyncLoop
}

// NewSyncStatusHandler creates a new sync status handler.
func NewSyncStatusHandler(syncLoop *sync.SyncLoop) *SyncStatusHandler {
	return &SyncStatusHandler{
		syncLoop: syncLoop,
	}
}

// Handle returns the current sync status.
func (h *SyncStatusHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	status := h.syncLoop.GetStatus()

	response := SyncStatusResponse{
		Running:   status.Running,
		LastError: status.LastError,
		SyncState: getSyncState(status),
		LocalOnly: status.LocalOnly,
	}

	if !status.LastSyncAt.IsZero() {
		response.LastSyncAt = status.LastSyncAt.Format("2006-01-02T15:04:05Z07:00")
	}

	return response, nil
}

// getSyncState derives the sync state from the status.
func getSyncState(status sync.SyncStatus) string {
	if !status.Running {
		return "stopped"
	}
	if status.LastError != "" {
		return "error"
	}
	if status.LastSyncAt.IsZero() {
		return "idle"
	}
	return "synced"
}
