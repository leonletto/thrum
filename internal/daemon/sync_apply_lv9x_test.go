package daemon

// thrum-lv9x batch-level regressions: a relayed batch carrying a duplicate
// message_id (different event_id — invisible to the event-id dedup) must NOT
// abort the batch, must let later events land, and must advance the checkpoint
// past the dup. The pre-fix behavior pinned the checkpoint at the batch start,
// so every inbound sync.notify re-pulled and re-aborted the same batch forever
// (the leondev/leonair storm; 65-167 notifies/sec sustained).

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/eventlog"
)

// lv9xMessageCreateEvent builds a relayed message.create eventlog.Event.
func lv9xMessageCreateEvent(eventID string, seq int64, messageID, content string) eventlog.Event {
	payload := fmt.Sprintf(`{"type":"message.create","timestamp":"2026-06-10T08:00:00Z","event_id":%q,"origin_daemon":"d_remote","message_id":%q,"agent_id":"remote_author","session_id":"ses_r","body":{"format":"markdown","content":%q},"v":1}`,
		eventID, messageID, content)
	return eventlog.Event{
		EventID:      eventID,
		Sequence:     seq,
		Type:         "message.create",
		Timestamp:    "2026-06-10T08:00:00Z",
		OriginDaemon: "d_remote",
		EventJSON:    json.RawMessage(payload),
	}
}

// TestApplyAndCheckpoint_DurableLaneRow_EventCommitsAndAdvances is the
// CANONICAL lv9x collision (thrum-5pan forensics — fleet-wide, no minter):
// the duplicate-carrying event is the message's ORIGINAL (and only) create
// event; the messages row was already upserted by the DURABLE LANE
// (messages-v2 snapshot lines, LWW by message_id) which writes NO events-table
// record. Pre-fix: HasEvent=false (no record) → projector plain-INSERT
// collides → the event record never sticks → HasEvent stays false FOREVER →
// eternal re-pull/abort. Post-fix, applying the original event over a
// durable-lane-seeded row must do all three:
//  1. COMMIT the events-table record (HasEvent flips true) — the regression
//     lock: a rollback that discards the event record is THE eternal-loop
//     driver; any future refactor that wraps the events insert and projector
//     apply in one tx and rolls back on dup re-arms the storm.
//  2. No-op the messages insert — the durable-lane row is the LWW-delivered
//     version, so dropping the event's body content is CORRECT, not data loss.
//  3. Advance the checkpoint past the event.
func TestApplyAndCheckpoint_DurableLaneRow_EventCommitsAndAdvances(t *testing.T) {
	st := createTestStateForSync(t)
	applier := NewSyncApplier(st)
	ctx := context.Background()

	// Seed the durable-lane outcome directly: messages row present (LWW
	// content), NO events-table record for it.
	if _, err := st.RawDB().Exec(`INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES ('msg_LV9X_DL', 'remote_author', 'ses_r', '2026-06-10T08:00:00Z', 'markdown', 'durable-lane LWW content')`); err != nil {
		t.Fatalf("seed durable-lane row: %v", err)
	}

	// The message's ORIGINAL create event arrives via the event lane.
	ev := lv9xMessageCreateEvent("evt_LV9X_DL1", 300, "msg_LV9X_DL", "event-lane content")
	applied, skipped, err := applier.ApplyAndCheckpoint(ctx, "d_remote", []eventlog.Event{ev}, 301, false)
	if err != nil {
		t.Fatalf("original event over a durable-lane row must apply cleanly (the canonical lv9x stall): %v", err)
	}
	if applied != 1 || skipped != 0 {
		t.Errorf("applied=%d skipped=%d, want applied=1 (the event itself is new)", applied, skipped)
	}

	// 1. The events-table record COMMITTED — HasEvent is now true, so the
	// next pull skips instead of re-colliding (kills the eternal loop).
	var hasEvent int
	if err := st.RawDB().QueryRow(`SELECT COUNT(*) FROM events WHERE event_id = 'evt_LV9X_DL1'`).Scan(&hasEvent); err != nil {
		t.Fatalf("query events: %v", err)
	}
	if hasEvent != 1 {
		t.Errorf("events record = %d rows, want 1 — the dup-no-op must COMMIT the event record, not lose it to a rollback (eternal-loop regression lock)", hasEvent)
	}

	// 2. The messages insert no-op'd: durable-lane LWW content pinned.
	var content string
	if err := st.RawDB().QueryRow(`SELECT body_content FROM messages WHERE message_id = 'msg_LV9X_DL'`).Scan(&content); err != nil {
		t.Fatalf("query message: %v", err)
	}
	if content != "durable-lane LWW content" {
		t.Errorf("content = %q, want the durable-lane LWW version pinned (dropping the event body is correct — the lane already delivered latest)", content)
	}

	// 3. Checkpoint advanced past the event.
	seq, err := applier.GetCheckpoint("d_remote")
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if seq != 300 {
		t.Errorf("checkpoint = %d, want 300 (advanced past the formerly-pinning event)", seq)
	}

	// And the loop is dead: a re-pull of the same event is a pure skip.
	applied, skipped, err = applier.ApplyAndCheckpoint(ctx, "d_remote", []eventlog.Event{ev}, 301, false)
	if err != nil {
		t.Fatalf("re-pull: %v", err)
	}
	if applied != 0 || skipped != 1 {
		t.Errorf("re-pull applied=%d skipped=%d, want 0/1 (HasEvent now catches it)", applied, skipped)
	}
}

// TestApplyAndCheckpoint_DupMessageMidBatch_AdvancesPastIt is the stall
// regression: [msgA, dup-of-msgA under a new event_id, msgB] must apply
// without error, msgB must land, and the checkpoint must advance to the
// batch's max sequence — NOT stay pinned before the dup.
func TestApplyAndCheckpoint_DupMessageMidBatch_AdvancesPastIt(t *testing.T) {
	st := createTestStateForSync(t)
	applier := NewSyncApplier(st)
	ctx := context.Background()

	events := []eventlog.Event{
		lv9xMessageCreateEvent("evt_LV9X_A1", 100, "msg_LV9X_A", "original"),
		lv9xMessageCreateEvent("evt_LV9X_A2", 101, "msg_LV9X_A", "relayed dup"), // same message, new event id
		lv9xMessageCreateEvent("evt_LV9X_B1", 102, "msg_LV9X_B", "after the dup"),
	}

	applied, skipped, err := applier.ApplyAndCheckpoint(ctx, "d_remote", events, 103, false)
	if err != nil {
		t.Fatalf("batch with mid-batch dup must not abort (the lv9x stall): %v", err)
	}
	if applied+skipped != 3 {
		t.Errorf("applied=%d skipped=%d, want all 3 events accounted for", applied, skipped)
	}

	// The event AFTER the dup landed.
	var content string
	if err := st.RawDB().QueryRow(`SELECT body_content FROM messages WHERE message_id = 'msg_LV9X_B'`).Scan(&content); err != nil {
		t.Fatalf("msgB must land despite the earlier dup: %v", err)
	}

	// First write wins for the dup'd message.
	if err := st.RawDB().QueryRow(`SELECT body_content FROM messages WHERE message_id = 'msg_LV9X_A'`).Scan(&content); err != nil {
		t.Fatalf("query msgA: %v", err)
	}
	if content != "original" {
		t.Errorf("msgA content = %q, want first-write-wins 'original'", content)
	}

	// Checkpoint advanced past the dup to the batch max (102).
	seq, err := applier.GetCheckpoint("d_remote")
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if seq != 102 {
		t.Errorf("checkpoint = %d, want 102 (advanced past the dup; pre-fix it stayed pinned at 0)", seq)
	}
}

// TestApplyRemoteEvents_RePull_StormShape is the explicit storm-shape pin
// (coordinator decision 5): the SAME batch re-pulled (notify-driven retry with
// a pinned cursor was the storm engine) must be a clean no-op round —
// applied=0, every event skipped by the event-id dedup, no re-abort, and the
// checkpoint stays advanced.
func TestApplyRemoteEvents_RePull_StormShape(t *testing.T) {
	st := createTestStateForSync(t)
	applier := NewSyncApplier(st)
	ctx := context.Background()

	events := []eventlog.Event{
		lv9xMessageCreateEvent("evt_LV9X_S1", 200, "msg_LV9X_S", "original"),
		lv9xMessageCreateEvent("evt_LV9X_S2", 201, "msg_LV9X_S", "relayed dup"),
		lv9xMessageCreateEvent("evt_LV9X_S3", 202, "msg_LV9X_T", "tail event"),
	}

	// Round 1: applies (dup is a no-op within the batch).
	if _, _, err := applier.ApplyAndCheckpoint(ctx, "d_remote", events, 203, false); err != nil {
		t.Fatalf("round 1: %v", err)
	}

	// Round 2: the storm shape — identical batch re-pulled.
	applied, skipped, err := applier.ApplyAndCheckpoint(ctx, "d_remote", events, 203, false)
	if err != nil {
		t.Fatalf("round 2 (re-pull) must not re-abort: %v", err)
	}
	if applied != 0 {
		t.Errorf("round 2 applied = %d, want 0 (everything already ingested)", applied)
	}
	if skipped != 3 {
		t.Errorf("round 2 skipped = %d, want 3 (event-id dedup catches the whole batch)", skipped)
	}
	seq, err := applier.GetCheckpoint("d_remote")
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if seq != 202 {
		t.Errorf("checkpoint after re-pull = %d, want 202 (stays advanced)", seq)
	}
}
