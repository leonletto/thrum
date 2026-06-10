package projection_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/projection"
	"github.com/leonletto/thrum/internal/types"
)

// TestApplyMessageCreate_DuplicateMessageID_NoOp is the thrum-lv9x core
// regression. Cross-host-relayed history can carry the SAME message_id under
// DIFFERENT event_ids (the i057-class dup the 0.11 line prevents at the mint
// site; on this line the dups are already in history and still being minted by
// peers). The projector's messages INSERT was the ONLY non-idempotent projector
// write left — a dup aborted the whole apply batch with 'UNIQUE constraint
// failed: messages.message_id', permanently stalling inbound sync and feeding
// the notify storm. A duplicate message.create must now apply as a clean no-op:
// first write wins (content pinned), no error, and the dup's scopes/refs must
// NOT be re-inserted or duplicated.
func TestApplyMessageCreate_DuplicateMessageID_NoOp(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	p := projection.NewProjector(safedb.New(db))
	ctx := context.Background()

	apply := func(eventID, content string) error {
		ev := types.MessageCreateEvent{
			Type:      "message.create",
			EventID:   eventID,
			Timestamp: "2026-06-10T08:00:00Z",
			MessageID: "msg_LV9X_DUP",
			AgentID:   "agent:author:A",
			SessionID: "ses_x",
			Body:      types.MessageBody{Format: "markdown", Content: content},
			Scopes:    []types.Scope{{Type: "module", Value: "auth"}},
			Refs:      []types.Ref{{Type: "spec", Value: "docs/spec.md"}},
		}
		data, _ := json.Marshal(ev)
		return p.Apply(ctx, data)
	}

	if err := apply("evt_LV9X_FIRST", "first content"); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	// The dup: different event_id, same message_id, different content.
	if err := apply("evt_LV9X_SECOND", "second content"); err != nil {
		t.Fatalf("duplicate message.create must be a no-op, not an error (the lv9x stall): %v", err)
	}

	// First write wins — content is pinned, not overwritten.
	var content string
	if err := db.QueryRow(`SELECT body_content FROM messages WHERE message_id = 'msg_LV9X_DUP'`).Scan(&content); err != nil {
		t.Fatalf("query message: %v", err)
	}
	if content != "first content" {
		t.Errorf("content = %q, want first-write-wins 'first content'", content)
	}

	// Exactly one message row, and the dup must not re-insert scopes/refs.
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM messages WHERE message_id = 'msg_LV9X_DUP'`:                           1,
		`SELECT COUNT(*) FROM message_scopes WHERE message_id = 'msg_LV9X_DUP'`:                     1,
		`SELECT COUNT(*) FROM message_refs WHERE message_id = 'msg_LV9X_DUP' AND ref_type = 'spec'`: 1,
	} {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if n != want {
			t.Errorf("%s = %d, want %d (dup must not duplicate rows)", q, n, want)
		}
	}
}
