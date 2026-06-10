package projection_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/projection"
	"github.com/leonletto/thrum/internal/types"
)

func tcqwApplyCreate(t *testing.T, p *projection.Projector, ev types.MessageCreateEvent) {
	t.Helper()
	ev.Type = "message.create"
	if ev.Timestamp == "" {
		ev.Timestamp = "2026-01-01T00:00:00Z"
	}
	if ev.Body.Format == "" {
		ev.Body = types.MessageBody{Format: "markdown", Content: "body"}
	}
	if ev.SessionID == "" {
		ev.SessionID = "ses_x"
	}
	data, _ := json.Marshal(ev)
	if err := p.Apply(context.Background(), data); err != nil {
		t.Fatalf("apply create: %v", err)
	}
}

// TestApplyCreate_AuthorGetsReadStampedSelfRow is thrum-b6qw T3 (port of the
// tcqw Option-C self-delivery). When an author sends a message and is NOT in
// their own Recipients (the broadcast/legacy case — HandleSend strips self
// from broadcast recipients), a read-stamped self-delivery row must still be
// created so the author's own send never counts as unread and the
// self-authored no-delivery-row class cannot accumulate going forward.
func TestApplyCreate_AuthorGetsReadStampedSelfRow(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	p := projection.NewProjector(safedb.New(db))

	const author = "agent:me:ABC"
	// No Recipients → exercises the unconditional author self-row (not the
	// in-loop author-in-Recipients branch).
	tcqwApplyCreate(t, p, types.MessageCreateEvent{
		MessageID: "msg_bcast",
		AgentID:   author,
	})

	readAt, ok := tcqwReadAt(t, db, "msg_bcast", author)
	if !ok {
		t.Fatal("expected a read-stamped author self-delivery row even with empty Recipients; got none")
	}
	if readAt == "" {
		t.Errorf("author self-delivery row exists but read_at is NULL; want stamped read")
	}
}

// TestApplyCreate_RecipientStillUnread guards that the author self-row does NOT
// leak read-state onto real recipients: a recipient (not the author) keeps
// read_at = NULL (unread) after create, while the author's own row is stamped.
func TestApplyCreate_RecipientStillUnread(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()
	p := projection.NewProjector(safedb.New(db))

	const author = "agent:me:ABC"
	const recipient = "agent:other:XYZ"
	tcqwApplyCreate(t, p, types.MessageCreateEvent{
		MessageID:  "msg_directed",
		AgentID:    author,
		Recipients: []string{recipient},
	})

	// Recipient remains unread.
	if readAt, ok := tcqwReadAt(t, db, "msg_directed", recipient); !ok {
		t.Fatal("expected a delivery row for the recipient")
	} else if readAt != "" {
		t.Errorf("recipient row must be unread (read_at NULL); got %q", readAt)
	}

	// Author's own row is read-stamped.
	if readAt, ok := tcqwReadAt(t, db, "msg_directed", author); !ok {
		t.Fatal("expected a read-stamped author self-delivery row")
	} else if readAt == "" {
		t.Errorf("author row must be read-stamped; got NULL")
	}
}
