package daemon

import (
	"context"
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
// Returns the number of events applied and skipped (duplicates or before purge cutoff).
func (a *SyncApplier) ApplyRemoteEvents(ctx context.Context, events []eventlog.Event) (applied, skipped int, err error) {
	db := a.state.DB()

	// Load purge cutoff — events before this are discarded (RFC 3339 sorts lexicographically)
	purgeCutoff := a.loadPurgeCutoff(ctx)

	for _, evt := range events {
		// Skip events before purge cutoff
		if purgeCutoff != nil && evt.Timestamp < *purgeCutoff {
			skipped++
			continue
		}

		// Deduplication: check if event already exists
		exists, err := eventlog.HasEvent(ctx, db, evt.EventID)
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
		if err := a.applyEvent(ctx, evt); err != nil {
			return applied, skipped, fmt.Errorf("apply event %s: %w", evt.EventID, err)
		}
		applied++
	}

	return applied, skipped, nil
}

// loadPurgeCutoff reads the purge cutoff from purge_metadata.
// Returns nil if no cutoff is set.
func (a *SyncApplier) loadPurgeCutoff(ctx context.Context) *string {
	var cutoff string
	err := a.state.DB().QueryRowContext(ctx,
		`SELECT value FROM purge_metadata WHERE key = 'purge_cutoff'`,
	).Scan(&cutoff)
	if err != nil {
		return nil
	}
	return &cutoff
}

// ApplyAndCheckpoint applies remote events and updates the checkpoint for the peer.
// The checkpoint is derived from the actual events received, not the peer's claimed
// next_sequence, to prevent checkpoint manipulation attacks where a malicious peer
// skips events by sending an inflated next_sequence value.
func (a *SyncApplier) ApplyAndCheckpoint(ctx context.Context, peerID string, events []eventlog.Event, peerNextSeq int64) (applied, skipped int, err error) {
	// Get current checkpoint to validate monotonic progress
	currentSeq, err := a.GetCheckpoint(peerID)
	if err != nil {
		return 0, 0, fmt.Errorf("get current checkpoint: %w", err)
	}

	// Reject checkpoint regression
	if peerNextSeq < currentSeq {
		return 0, 0, fmt.Errorf("checkpoint regression: peer sent next_seq=%d but current is %d", peerNextSeq, currentSeq)
	}

	// Derive safe checkpoint from actual events rather than trusting peer's claim
	safeNextSeq := peerNextSeq
	if len(events) > 0 {
		maxEventSeq := events[0].Sequence
		for _, evt := range events[1:] {
			if evt.Sequence > maxEventSeq {
				maxEventSeq = evt.Sequence
			}
		}
		// Only advance to the max sequence we actually received
		if peerNextSeq > maxEventSeq {
			safeNextSeq = maxEventSeq
		}
	}

	applied, skipped, err = a.ApplyRemoteEvents(ctx, events)
	if err != nil {
		return applied, skipped, err
	}

	// Update checkpoint with safe sequence
	if applied > 0 || skipped > 0 {
		if err := checkpoint.UpdateCheckpoint(ctx, a.state.DB(), peerID, safeNextSeq, time.Now().Unix()); err != nil {
			return applied, skipped, fmt.Errorf("update checkpoint: %w", err)
		}
	}

	return applied, skipped, nil
}

// GetCheckpoint returns the checkpoint for a peer daemon.
func (a *SyncApplier) GetCheckpoint(peerID string) (int64, error) {
	cp, err := checkpoint.GetCheckpoint(context.Background(), a.state.DB(), peerID)
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
func (a *SyncApplier) applyEvent(ctx context.Context, evt eventlog.Event) error {
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

	// Write via State.WriteEvent which handles JSONL routing, sequence, and projection.
	// thrum-bsn7 audit: sync_apply does NOT hold state.Lock() during
	// WriteEvent (ApplyRemoteEvents is lock-free at this layer). Inbound
	// structural peer events DO fire local walker+compactor via the
	// returned postCommit closure — this is intentional so peer events
	// get materialized into our local state files for forwarding to
	// other peers. The pre-bsn7 inline-trigger behavior is preserved
	// exactly: invoke postCommit() immediately, lock-free.
	postCommit, err := a.state.WriteEvent(ctx, eventMap)
	if err != nil {
		return err
	}
	if postCommit != nil {
		postCommit()
	}
	return nil
}

// DB returns the database for direct queries (used by tests).
func (a *SyncApplier) DB() *sql.DB {
	return a.state.RawDB()
}
