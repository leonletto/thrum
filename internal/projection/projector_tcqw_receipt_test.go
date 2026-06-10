package projection_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/projection"
	"github.com/leonletto/thrum/internal/types"
)

// tcqwInsertMessage inserts a bare message row directly (bypassing
// applyMessageCreate so no delivery row is created) — isolates the receipt
// gate. NOT-NULL columns: message_id, agent_id, session_id, created_at,
// body_format, body_content.
func tcqwInsertMessage(t *testing.T, db *sql.DB, msgID, author string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO messages
		(message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES (?, ?, ?, ?, ?, ?)`,
		msgID, author, "ses_x", "2026-01-01T00:00:00Z", "markdown", "body"); err != nil {
		t.Fatalf("insert message %s: %v", msgID, err)
	}
}

func tcqwInsertMentionRef(t *testing.T, db *sql.DB, msgID, mentionValue string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO message_refs (message_id, ref_type, ref_value)
		VALUES (?, 'mention', ?)`, msgID, mentionValue); err != nil {
		t.Fatalf("insert mention ref %s->%s: %v", msgID, mentionValue, err)
	}
}

func tcqwApplyReadReceipt(t *testing.T, p *projection.Projector, msgID, agentID string) {
	t.Helper()
	data, _ := json.Marshal(types.MessageReceiptEvent{
		Type:        "message.receipt",
		Timestamp:   "2026-01-01T01:00:00Z",
		MessageID:   msgID,
		AgentID:     agentID,
		ReceiptType: "read",
	})
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("apply receipt: %v", err)
	}
}

func tcqwReadAt(t *testing.T, db *sql.DB, msgID, agentID string) (string, bool) {
	t.Helper()
	var readAt sql.NullString
	err := db.QueryRow(`SELECT read_at FROM message_deliveries
		WHERE message_id = ? AND recipient_agent_id = ?`, msgID, agentID).Scan(&readAt)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatalf("query delivery: %v", err)
	}
	return readAt.String, true
}

// TestApplyReceipt_AuthoredSelf_CreatesReadStampedRow is thrum-b6qw T2 (port of
// the tcqw authored-self arm). An agent's own sent message (it authored it, is
// NOT mentioned, the message is NOT legacy-broadcast because it carries a
// mention ref to someone else, and there is no delivery row) must get a
// read-stamped delivery row created when the author marks it read — so the
// self-authored no-delivery-row phantom-unread class converges. Without the
// authored-self arm in the qb62 gate, no row is created and the count never
// drains.
func TestApplyReceipt_AuthoredSelf_CreatesReadStampedRow(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	p := projection.NewProjector(safedb.New(db))

	const author = "agent:me:ABC"
	tcqwInsertMessage(t, db, "msg_self", author)
	// Mention someone ELSE so the message is targeted (not legacy-broadcast) and
	// the author is not reached via any existing arm — only the authored-self
	// arm can create the row.
	tcqwInsertMentionRef(t, db, "msg_self", "agent:other:XYZ")

	tcqwApplyReadReceipt(t, p, "msg_self", author)

	readAt, ok := tcqwReadAt(t, db, "msg_self", author)
	if !ok {
		t.Fatal("expected a read-stamped self-delivery row for the author; got none (authored-self arm missing)")
	}
	if readAt == "" {
		t.Errorf("self-delivery row exists but read_at is NULL; want stamped read")
	}
}

// TestApplyReceipt_NonRecipient_CreatesNoRow is the leak guard for the
// authored-self arm: an agent that is NOT the author, NOT mentioned, and the
// message is targeted to a third party (so not legacy-broadcast) must NOT get a
// fabricated delivery row when it marks the message read. The authored-self arm
// must not widen the gate for non-authors.
func TestApplyReceipt_NonRecipient_CreatesNoRow(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	p := projection.NewProjector(safedb.New(db))

	tcqwInsertMessage(t, db, "msg_other", "agent:author:OTH")
	tcqwInsertMentionRef(t, db, "msg_other", "agent:thirdparty:TP")

	// "me" is neither author nor mentioned nor a broadcast/group recipient.
	tcqwApplyReadReceipt(t, p, "msg_other", "agent:me:ABC")

	if _, ok := tcqwReadAt(t, db, "msg_other", "agent:me:ABC"); ok {
		t.Fatal("non-recipient must NOT get a fabricated delivery row (authored-self arm leaked)")
	}
}
