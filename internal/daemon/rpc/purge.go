package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/jsonl"
)

// PurgeRequest is the input for the purge.execute RPC method.
type PurgeRequest struct {
	Before string `json:"before"` // RFC 3339 cutoff timestamp
	DryRun bool   `json:"dry_run"`
}

// PurgeResponse is the result of the purge.execute RPC method.
type PurgeResponse struct {
	Before             string `json:"before"`
	DryRun             bool   `json:"dry_run"`
	MessagesDeleted    int    `json:"messages_deleted"`
	SessionsDeleted    int    `json:"sessions_deleted"`
	EventsDeleted      int    `json:"events_deleted"`
	SyncMessageFiles   int    `json:"sync_message_files"`
	SyncEventsFiltered int    `json:"sync_events_filtered"`
}

// PurgeHandler handles the purge.execute RPC method.
type PurgeHandler struct {
	state *state.State
}

// NewPurgeHandler creates a new PurgeHandler.
func NewPurgeHandler(st *state.State) *PurgeHandler {
	return &PurgeHandler{state: st}
}

// Handle handles the purge.execute RPC method.
func (h *PurgeHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	var req PurgeRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	if req.Before == "" {
		return nil, fmt.Errorf("before is required")
	}

	// Parse the cutoff timestamp (try RFC3339Nano first, fall back to RFC3339)
	cutoff, err := time.Parse(time.RFC3339Nano, req.Before)
	if err != nil {
		cutoff, err = time.Parse(time.RFC3339, req.Before)
		if err != nil {
			return nil, fmt.Errorf("invalid before timestamp %q: must be RFC 3339", req.Before)
		}
	}

	if req.DryRun {
		return h.dryRun(ctx, req.Before, cutoff)
	}
	return h.execute(ctx, req.Before, cutoff)
}

// dryRun counts what would be deleted without modifying anything.
func (h *PurgeHandler) dryRun(ctx context.Context, before string, cutoff time.Time) (*PurgeResponse, error) {
	resp := &PurgeResponse{
		Before: before,
		DryRun: true,
	}

	// Count messages
	if err := h.state.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE created_at < ?`, before,
	).Scan(&resp.MessagesDeleted); err != nil {
		return nil, fmt.Errorf("count messages: %w", err)
	}

	// Count sessions
	if err := h.state.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE started_at < ?`, before,
	).Scan(&resp.SessionsDeleted); err != nil {
		return nil, fmt.Errorf("count sessions: %w", err)
	}

	// Count events
	if err := h.state.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE timestamp < ?`, before,
	).Scan(&resp.EventsDeleted); err != nil {
		return nil, fmt.Errorf("count events: %w", err)
	}

	// Count sync files (events.jsonl + messages/*.jsonl)
	eventsFiltered, messageFiles, err := h.countSyncFiles(cutoff)
	if err != nil {
		return nil, fmt.Errorf("count sync files: %w", err)
	}
	resp.SyncEventsFiltered = eventsFiltered
	resp.SyncMessageFiles = messageFiles

	return resp, nil
}

// execute performs the actual purge.
func (h *PurgeHandler) execute(ctx context.Context, before string, cutoff time.Time) (*PurgeResponse, error) {
	resp := &PurgeResponse{
		Before: before,
		DryRun: false,
	}

	h.state.Lock()

	// --- Count what will be deleted (for response) ---
	if err := h.state.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE created_at < ?`, before,
	).Scan(&resp.MessagesDeleted); err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("count messages: %w", err)
	}
	if err := h.state.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE started_at < ?`, before,
	).Scan(&resp.SessionsDeleted); err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("count sessions: %w", err)
	}
	if err := h.state.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE timestamp < ?`, before,
	).Scan(&resp.EventsDeleted); err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("count events: %w", err)
	}

	// --- Delete message child tables first (FK safety) ---
	childMessageTables := []string{
		"message_edits",
		"message_reads",
		"message_deliveries",
		"message_refs",
		"message_scopes",
	}
	for _, table := range childMessageTables {
		//nolint:gosec // table name is a hardcoded constant, not user input
		q := `DELETE FROM ` + table + ` WHERE message_id IN (SELECT message_id FROM messages WHERE created_at < ?)`
		if _, err := h.state.DB().ExecContext(ctx, q, before); err != nil {
			h.state.Unlock()
			return nil, fmt.Errorf("delete %s: %w", table, err)
		}
	}

	// Delete messages
	if _, err := h.state.DB().ExecContext(ctx,
		`DELETE FROM messages WHERE created_at < ?`, before,
	); err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete messages: %w", err)
	}

	// --- Delete session child tables ---
	childSessionTables := []string{
		"session_refs",
		"session_scopes",
	}
	for _, table := range childSessionTables {
		//nolint:gosec // table name is a hardcoded constant, not user input
		q := `DELETE FROM ` + table + ` WHERE session_id IN (SELECT session_id FROM sessions WHERE started_at < ?)`
		if _, err := h.state.DB().ExecContext(ctx, q, before); err != nil {
			h.state.Unlock()
			return nil, fmt.Errorf("delete %s: %w", table, err)
		}
	}

	// Delete sessions
	if _, err := h.state.DB().ExecContext(ctx,
		`DELETE FROM sessions WHERE started_at < ?`, before,
	); err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete sessions: %w", err)
	}

	// Delete events
	if _, err := h.state.DB().ExecContext(ctx,
		`DELETE FROM events WHERE timestamp < ?`, before,
	); err != nil {
		h.state.Unlock()
		return nil, fmt.Errorf("delete events: %w", err)
	}

	h.state.Unlock()

	// --- Filter sync JSONL files ---
	eventsFiltered, messageFiles, err := h.filterSyncFiles(cutoff)
	if err != nil {
		return nil, fmt.Errorf("filter sync files: %w", err)
	}
	resp.SyncEventsFiltered = eventsFiltered
	resp.SyncMessageFiles = messageFiles

	return resp, nil
}

// countSyncFiles counts how many lines in events.jsonl and messages/*.jsonl are
// older than cutoff, without modifying any files. Returns (eventsCount, filesWithRemovals, error).
func (h *PurgeHandler) countSyncFiles(cutoff time.Time) (int, int, error) {
	syncDir := h.state.SyncDir()
	totalEventsRemoved := 0
	filesWithRemovals := 0

	// Count events.jsonl
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	n, err := countJSONLBeforeTimestamp(eventsPath, "timestamp", cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("count events.jsonl: %w", err)
	}
	totalEventsRemoved += n

	// Count messages/*.jsonl
	messagesDir := filepath.Join(syncDir, "messages")
	entries, err := os.ReadDir(messagesDir)
	if err != nil && !os.IsNotExist(err) {
		return 0, 0, fmt.Errorf("read messages dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(messagesDir, entry.Name())
		removed, err := countJSONLBeforeTimestamp(path, "created_at", cutoff)
		if err != nil {
			return 0, 0, fmt.Errorf("count %s: %w", entry.Name(), err)
		}
		if removed > 0 {
			filesWithRemovals++
		}
	}

	return totalEventsRemoved, filesWithRemovals, nil
}

// filterSyncFiles removes lines older than cutoff from events.jsonl and
// messages/*.jsonl. Returns (eventsRemoved, filesModified, error).
func (h *PurgeHandler) filterSyncFiles(cutoff time.Time) (int, int, error) {
	syncDir := h.state.SyncDir()
	totalEventsRemoved := 0
	filesModified := 0

	// Filter events.jsonl
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	n, err := jsonl.RemoveBeforeTimestamp(eventsPath, "timestamp", cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("filter events.jsonl: %w", err)
	}
	totalEventsRemoved += n

	// Filter messages/*.jsonl
	messagesDir := filepath.Join(syncDir, "messages")
	entries, err := os.ReadDir(messagesDir)
	if err != nil && !os.IsNotExist(err) {
		return 0, 0, fmt.Errorf("read messages dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(messagesDir, entry.Name())
		removed, err := jsonl.RemoveBeforeTimestamp(path, "created_at", cutoff)
		if err != nil {
			return 0, 0, fmt.Errorf("filter %s: %w", entry.Name(), err)
		}
		if removed > 0 {
			filesModified++
		}
	}

	return totalEventsRemoved, filesModified, nil
}

// countJSONLBeforeTimestamp counts lines in a JSONL file where the given field
// is a timestamp strictly before cutoff. Missing files return 0, nil.
func countJSONLBeforeTimestamp(path, field string, cutoff time.Time) (int, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is an internal JSONL file path
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read file: %w", err)
	}

	count := 0
	for _, line := range splitJSONLLines(data) {
		if len(line) == 0 {
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}
		raw, ok := obj[field]
		if !ok {
			continue
		}
		var val string
		if json.Unmarshal(raw, &val) != nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, val)
		if err != nil {
			ts, err = time.Parse(time.RFC3339, val)
		}
		if err == nil && ts.Before(cutoff) {
			count++
		}
	}
	return count, nil
}

// splitJSONLLines splits a byte slice into individual non-empty lines.
func splitJSONLLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			line := data[start:i]
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(data) {
		line := data[start:]
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}
