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
// Returns the number of events applied and skipped (duplicates or before
// purge cutoff).
//
// thrum-1nkt.2: the per-event walker+compactor sync trigger is COALESCED
// into a single post-batch fire. applyEvent now returns its postCommit
// closure instead of invoking it inline; the loop accumulates the last
// non-nil closure across the batch and invokes it once after the loop
// (or once before an early error return). The walker's lastWalkAt
// monotonic + walker.mu serialization already makes the per-event walks
// near-noops, but each still paid the walker.mu acquire + compactor
// (~40ms each at bpq5 measured rates). Coalescing collapses N near-noop
// walks into 1 single useful walk that picks up the whole batch's
// state-file materializations in one pass. The trigger captures the
// shared ctx (batch-scoped), so any non-nil closure from the loop is
// equivalent — the "last" choice is arbitrary.
func (a *SyncApplier) ApplyRemoteEvents(ctx context.Context, events []eventlog.Event) (applied, skipped int, err error) {
	db := a.state.DB()

	// Load purge cutoff — events before this are discarded (RFC 3339 sorts lexicographically)
	purgeCutoff := a.loadPurgeCutoff(ctx)

	var coalescedPostCommit func() // single fire-at-end for the batch

	for _, evt := range events {
		// Skip events before purge cutoff
		if purgeCutoff != nil && evt.Timestamp < *purgeCutoff {
			skipped++
			continue
		}

		// Deduplication: check if event already exists
		exists, hasErr := eventlog.HasEvent(ctx, db, evt.EventID)
		if hasErr != nil {
			// Fire any accumulated trigger before bailing so events
			// that DID land propagate to other peers.
			if coalescedPostCommit != nil {
				coalescedPostCommit()
			}
			return applied, skipped, fmt.Errorf("check event %s: %w", evt.EventID, hasErr)
		}
		if exists {
			skipped++
			continue
		}

		// Apply the event via State.WriteEvent which handles:
		// - JSONL routing (messages/{agent}.jsonl vs events.jsonl)
		// - SQLite events table insert (with new local sequence)
		// - Projection update
		pc, applyErr := a.applyEvent(ctx, evt)
		if applyErr != nil {
			// Same partial-batch propagation guarantee as above.
			if coalescedPostCommit != nil {
				coalescedPostCommit()
			}
			return applied, skipped, fmt.Errorf("apply event %s: %w", evt.EventID, applyErr)
		}
		if pc != nil {
			coalescedPostCommit = pc
		}
		applied++
	}

	// Single coalesced fire for the whole batch's structural events.
	if coalescedPostCommit != nil {
		coalescedPostCommit()
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

// applyEvent applies a single remote event to the local store and
// returns the postCommit closure (or nil for non-structural events).
// See ApplyRemoteEvents above for the batch-level coalescing rationale
// and the bsn7-audit lock-discipline notes that motivate why
// sync_apply fires postCommit at all (without holding state.Lock).
func (a *SyncApplier) applyEvent(ctx context.Context, evt eventlog.Event) (func(), error) {
	// Parse the event JSON to a map so WriteEvent can process it
	var eventMap map[string]any
	if err := json.Unmarshal(evt.EventJSON, &eventMap); err != nil {
		return nil, fmt.Errorf("unmarshal event JSON: %w", err)
	}

	// Ensure key fields are set from the Event struct
	eventMap["event_id"] = evt.EventID
	eventMap["type"] = evt.Type
	eventMap["timestamp"] = evt.Timestamp
	eventMap["origin_daemon"] = evt.OriginDaemon

	postCommit, err := a.state.WriteEvent(ctx, eventMap)
	if err != nil {
		return nil, err
	}
	return postCommit, nil
}

// DB returns the database for direct queries (used by tests).
func (a *SyncApplier) DB() *sql.DB {
	return a.state.RawDB()
}
