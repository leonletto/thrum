package rpc

import (
	"context"
	"encoding/json"
	"time"
)

// HealthResponse represents the response from the health check RPC.
type HealthResponse struct {
	Status          string             `json:"status"`     // "ok" or "degraded"
	Uptime          int64              `json:"uptime_ms"`  // Uptime in milliseconds
	Version         string             `json:"version"`    // Daemon version
	RepoID          string             `json:"repo_id"`    // Repository ID
	SyncState       string             `json:"sync_state"` // "synced", "pending", "error", "local-only"
	LocalOnly       bool               `json:"local_only"` // a-sync push/fetch held off this session
	LocalOnlyReason string             `json:"local_only_reason,omitempty"`
	Tailscale       *TailscaleSyncInfo `json:"tailscale,omitempty"` // Tailscale sync info (nil if disabled)
	Identity        *IdentityInfo      `json:"identity,omitempty"`  // Daemon identity fields
}

// IdentityInfo carries the daemon's persistent identity metadata.
type IdentityInfo struct {
	DaemonID     string `json:"daemon_id"`
	RepoName     string `json:"repo_name"`
	Hostname     string `json:"hostname"`
	RepoPath     string `json:"repo_path"`
	GitOriginURL string `json:"git_origin_url,omitempty"`
	InitAt       string `json:"init_at"`
}

// IdentityInfoProvider returns the local daemon's identity snapshot.
type IdentityInfoProvider func() *IdentityInfo

// TailscaleSyncInfo contains Tailscale sync status for the health response.
type TailscaleSyncInfo struct {
	Enabled        bool            `json:"enabled"`
	Hostname       string          `json:"hostname"`
	ConnectedPeers int             `json:"connected_peers"`
	Peers          []TailscalePeer `json:"peers,omitempty"`
	LastSync       string          `json:"last_sync,omitempty"`
	SyncStatus     string          `json:"sync_status"` // "idle", "syncing", "error"
}

// TailscalePeer represents a peer in the Tailscale sync status.
type TailscalePeer struct {
	DaemonID string `json:"daemon_id"`
	Name     string `json:"name"`
	LastSync string `json:"last_sync"`
}

// TailscaleSyncInfoProvider is called to get current Tailscale sync info.
type TailscaleSyncInfoProvider func() *TailscaleSyncInfo

// SyncStatusProvider returns the daemon's current sync state for the health
// response: the derived state string, whether remote sync is held off, and the
// reason (e.g. the a-sync exposure gate). Mirrors the TailscaleSyncInfoProvider
// injection pattern so the rpc package never imports the sync loop directly.
type SyncStatusProvider func() (state string, localOnly bool, localOnlyReason string)

// HealthHandler creates a health check handler.
type HealthHandler struct {
	startTime          time.Time
	version            string
	repoID             string
	tsInfoProvider     TailscaleSyncInfoProvider
	identityProvider   IdentityInfoProvider
	syncStatusProvider SyncStatusProvider
}

// NewHealthHandler creates a new health check handler.
func NewHealthHandler(startTime time.Time, version string, repoID string) *HealthHandler {
	return &HealthHandler{
		startTime: startTime,
		version:   version,
		repoID:    repoID,
	}
}

// SetTailscaleInfoProvider sets a callback to provide Tailscale sync info.
func (h *HealthHandler) SetTailscaleInfoProvider(provider TailscaleSyncInfoProvider) {
	h.tsInfoProvider = provider
}

// SetIdentityProvider sets a callback to provide the daemon's identity fields.
func (h *HealthHandler) SetIdentityProvider(provider IdentityInfoProvider) {
	h.identityProvider = provider
}

// SetSyncStatusProvider sets a callback to provide the daemon's sync state
// (including the exposure-gate local-only reason) for the health response.
func (h *HealthHandler) SetSyncStatusProvider(provider SyncStatusProvider) {
	h.syncStatusProvider = provider
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
		SyncState: "synced",
	}

	// Override the hardcoded "synced" with the real sync state (incl. the
	// exposure-gate local-only reason) when a provider is wired. Falling back to
	// "synced" preserves the prior behavior when no provider is set.
	if h.syncStatusProvider != nil {
		st, lo, reason := h.syncStatusProvider()
		response.SyncState, response.LocalOnly, response.LocalOnlyReason = st, lo, reason
	}

	// Add Tailscale sync info if available
	if h.tsInfoProvider != nil {
		response.Tailscale = h.tsInfoProvider()
	}

	// Add identity fields if available
	if h.identityProvider != nil {
		response.Identity = h.identityProvider()
	}

	return response, nil
}
