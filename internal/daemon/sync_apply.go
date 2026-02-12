package daemon

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/daemon/checkpoint"
	"github.com/leonletto/thrum/internal/daemon/eventlog"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// SyncApplier applies remote events to the local event store.
type SyncApplier struct {
	state *state.State
}

// NewSyncApplier creates a new sync applier.
func NewSyncApplier(st *state.State) *SyncApplier {
	return &SyncApplier{state: st}
}

// ApplyRemoteEvents applies a batch of remote events to the local store.
// Returns the number of events applied and skipped (duplicates).
func (a *SyncApplier) ApplyRemoteEvents(events []eventlog.Event) (applied, skipped int, err error) {
	db := a.state.DB()

	for _, evt := range events {
		// Deduplication: check if event already exists
		exists, err := eventlog.HasEvent(db, evt.EventID)
		if err != nil {
			return applied, skipped, fmt.Errorf("check event %s: %w", evt.EventID, err)
		}
		if exists {
			skipped++
			continue
		}

		// Apply the event via State.WriteEvent which handles:
		// - JSONL routing (messages/{agent}.jsonl vs events.jsonl)
		// - SQLite events table insert (with new local sequence)
		// - Projection update
		if err := a.applyEvent(evt); err != nil {
			return applied, skipped, fmt.Errorf("apply event %s: %w", evt.EventID, err)
		}
		applied++
	}

	return applied, skipped, nil
}

// ApplyAndCheckpoint applies remote events and updates the checkpoint for the peer.
func (a *SyncApplier) ApplyAndCheckpoint(peerID string, events []eventlog.Event, peerNextSeq int64) (applied, skipped int, err error) {
	applied, skipped, err = a.ApplyRemoteEvents(events)
	if err != nil {
		return applied, skipped, err
	}

	// Update checkpoint
	if applied > 0 || skipped > 0 {
		if err := checkpoint.UpdateCheckpoint(a.state.DB(), peerID, peerNextSeq, time.Now().Unix()); err != nil {
			return applied, skipped, fmt.Errorf("update checkpoint: %w", err)
		}
	}

	return applied, skipped, nil
}

// GetCheckpoint returns the checkpoint for a peer daemon.
func (a *SyncApplier) GetCheckpoint(peerID string) (int64, error) {
	cp, err := checkpoint.GetCheckpoint(a.state.DB(), peerID)
	if err != nil {
		return 0, err
	}
	if cp == nil {
		return 0, nil
	}
	return cp.LastSyncedSeq, nil
}

// applyEvent applies a single remote event to the local store.
// The event's JSON payload is parsed into a map and written via State.WriteEvent.
func (a *SyncApplier) applyEvent(evt eventlog.Event) error {
	// Parse the event JSON to a map so WriteEvent can process it
	var eventMap map[string]any
	if err := json.Unmarshal(evt.EventJSON, &eventMap); err != nil {
		return fmt.Errorf("unmarshal event JSON: %w", err)
	}

	// Ensure key fields are set from the Event struct
	eventMap["event_id"] = evt.EventID
	eventMap["type"] = evt.Type
	eventMap["timestamp"] = evt.Timestamp
	eventMap["origin_daemon"] = evt.OriginDaemon

	// Write via State.WriteEvent which handles JSONL routing, sequence, and projection
	return a.state.WriteEvent(eventMap)
}

// DB returns the database for direct queries (used by tests).
func (a *SyncApplier) DB() *sql.DB {
	return a.state.DB()
}
