package cli

import (
	"fmt"
	"time"
)

// SyncForceRequest represents a request to force a sync.
type SyncForceRequest struct{}

// SyncForceResponse represents the response from a force sync.
type SyncForceResponse struct {
	Triggered  bool   `json:"triggered"`
	LastSyncAt string `json:"last_sync_at"`
	SyncState  string `json:"sync_state"`
	LocalOnly  bool   `json:"local_only"`
}

// SyncStatusRequest represents a request for sync status.
type SyncStatusRequest struct{}

// SyncStatusResponse represents the current sync status.
type SyncStatusResponse struct {
	Running    bool   `json:"running"`
	LastSyncAt string `json:"last_sync_at"`
	LastError  string `json:"last_error,omitempty"`
	SyncState  string `json:"sync_state"`
	LocalOnly  bool   `json:"local_only"`
}

// SyncForce triggers an immediate sync.
func SyncForce(client *Client) (*SyncForceResponse, error) {
	req := SyncForceRequest{}

	var result SyncForceResponse
	if err := client.Call("sync.force", req, &result); err != nil {
		return nil, fmt.Errorf("sync.force RPC failed: %w", err)
	}

	return &result, nil
}

// SyncStatus retrieves current sync status.
func SyncStatus(client *Client) (*SyncStatusResponse, error) {
	req := SyncStatusRequest{}

	var result SyncStatusResponse
	if err := client.Call("sync.status", req, &result); err != nil {
		return nil, fmt.Errorf("sync.status RPC failed: %w", err)
	}

	return &result, nil
}

// FormatSyncForce formats the sync force response for display.
func FormatSyncForce(result *SyncForceResponse) string {
	output := "✓ Sync triggered\n"

	if result.LocalOnly {
		output += "  Mode:       local-only (remote sync disabled)\n"
	}

	// Show sync state
	switch result.SyncState {
	case "synced":
		output += "  State:      ✓ synced\n"
	case "idle":
		output += "  State:      idle (no syncs yet)\n"
	case "error":
		output += "  State:      ✗ error\n"
	case "stopped":
		output += "  State:      stopped\n"
	default:
		output += fmt.Sprintf("  State:      %s\n", result.SyncState)
	}

	// Show last sync time
	if result.LastSyncAt != "" {
		if t, err := time.Parse(time.RFC3339, result.LastSyncAt); err == nil {
			duration := time.Since(t)
			output += fmt.Sprintf("  Last sync:  %s (%s ago)\n",
				t.Format("2006-01-02 15:04:05"), formatDuration(duration))
		} else {
			output += fmt.Sprintf("  Last sync:  %s\n", result.LastSyncAt)
		}
	}

	return output
}

// FormatSyncStatus formats the sync status response for display.
func FormatSyncStatus(result *SyncStatusResponse) string {
	var output string

	// Running status
	if result.Running {
		output += "Sync loop:  ✓ running\n"
	} else {
		output += "Sync loop:  ✗ stopped\n"
	}

	// Mode
	if result.LocalOnly {
		output += "Mode:       local-only\n"
	} else {
		output += "Mode:       normal\n"
	}

	// Sync state
	switch result.SyncState {
	case "synced":
		output += "State:      ✓ synced\n"
	case "idle":
		output += "State:      idle (no syncs yet)\n"
	case "error":
		output += "State:      ✗ error\n"
	case "stopped":
		output += "State:      stopped\n"
	default:
		output += fmt.Sprintf("State:      %s\n", result.SyncState)
	}

	// Last sync time
	if result.LastSyncAt != "" {
		if t, err := time.Parse(time.RFC3339, result.LastSyncAt); err == nil {
			duration := time.Since(t)
			output += fmt.Sprintf("Last sync:  %s (%s ago)\n",
				t.Format("2006-01-02 15:04:05"), formatDuration(duration))
		} else {
			output += fmt.Sprintf("Last sync:  %s\n", result.LastSyncAt)
		}
	} else {
		output += "Last sync:  never\n"
	}

	// Last error (if any)
	if result.LastError != "" {
		output += fmt.Sprintf("Last error: %s\n", result.LastError)
	}

	return output
}
