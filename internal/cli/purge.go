package cli

import (
	"fmt"
	"strings"
)

// PurgeOptions contains options for the purge.execute RPC call.
type PurgeOptions struct {
	Before string // RFC 3339 cutoff (already resolved by caller)
	DryRun bool
}

// PurgeResponse represents the response from the purge.execute RPC.
type PurgeResponse struct {
	Before             string `json:"before"`
	DryRun             bool   `json:"dry_run"`
	MessagesDeleted    int    `json:"messages_deleted"`
	SessionsDeleted    int    `json:"sessions_deleted"`
	EventsDeleted      int    `json:"events_deleted"`
	SyncMessageFiles   int    `json:"sync_message_files"`
	SyncEventsFiltered int    `json:"sync_events_filtered"`
}

// Purge calls the purge.execute RPC and returns the result.
func Purge(client *Client, opts PurgeOptions) (*PurgeResponse, error) {
	req := map[string]any{
		"before":  opts.Before,
		"dry_run": opts.DryRun,
	}
	var result PurgeResponse
	if err := client.Call("purge.execute", req, &result); err != nil {
		return nil, fmt.Errorf("purge.execute RPC failed: %w", err)
	}
	return &result, nil
}

// FormatPurge formats a PurgeResponse for human-readable display.
//
// Dry-run output:
//
//	Purge preview (before <timestamp>):
//
//	  Messages:  142
//	  Sessions:   8
//	  Events:     47
//	  Sync files: 10 message files, 1 events file
//
//	Run with --confirm to execute.
//
// Execute output:
//
//	Purged (before <timestamp>):
//
//	  Messages:  142 deleted
//	  Sessions:   8 deleted
//	  Events:     47 deleted
//	  Sync files: 10 message files filtered, 1 events file filtered
//
//	Done.
func FormatPurge(result *PurgeResponse) string {
	var b strings.Builder

	if result.DryRun {
		fmt.Fprintf(&b, "Purge preview (before %s):\n\n", result.Before)
		fmt.Fprintf(&b, "  Messages:  %d\n", result.MessagesDeleted)
		fmt.Fprintf(&b, "  Sessions:  %d\n", result.SessionsDeleted)
		fmt.Fprintf(&b, "  Events:    %d\n", result.EventsDeleted)
		fmt.Fprintf(&b, "  Sync files: %d message files, %d events file\n",
			result.SyncMessageFiles, result.SyncEventsFiltered)
		b.WriteString("\nRun with --confirm to execute.\n")
	} else {
		fmt.Fprintf(&b, "Purged (before %s):\n\n", result.Before)
		fmt.Fprintf(&b, "  Messages:  %d deleted\n", result.MessagesDeleted)
		fmt.Fprintf(&b, "  Sessions:  %d deleted\n", result.SessionsDeleted)
		fmt.Fprintf(&b, "  Events:    %d deleted\n", result.EventsDeleted)
		fmt.Fprintf(&b, "  Sync files: %d message files filtered, %d events file filtered\n",
			result.SyncMessageFiles, result.SyncEventsFiltered)
		b.WriteString("\nDone.\n")
	}

	return b.String()
}
