package rpc

import (
	"context"
	"encoding/json"
	"time"
)

// HealthResponse represents the response from the health check RPC.
type HealthResponse struct {
	Status    string `json:"status"`     // "ok" or "degraded"
	Uptime    int64  `json:"uptime_ms"`  // Uptime in milliseconds
	Version   string `json:"version"`    // Daemon version
	RepoID    string `json:"repo_id"`    // Repository ID
	SyncState string `json:"sync_state"` // "synced", "pending", "error"
}

// HealthHandler creates a health check handler.
type HealthHandler struct {
	startTime time.Time
	version   string
	repoID    string
}

// NewHealthHandler creates a new health check handler.
func NewHealthHandler(startTime time.Time, version string, repoID string) *HealthHandler {
	return &HealthHandler{
		startTime: startTime,
		version:   version,
		repoID:    repoID,
	}
}

// Handle handles the health check request.
func (h *HealthHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	// Calculate uptime
	uptime := time.Since(h.startTime).Milliseconds()

	// Build response
	response := HealthResponse{
		Status:    "ok",
		Uptime:    uptime,
		Version:   h.version,
		RepoID:    h.repoID,
		SyncState: "synced", // For now, always report synced (Epic 5 will implement actual sync)
	}

	return response, nil
}
