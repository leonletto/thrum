package permission

import (
	"testing"

	"github.com/leonletto/thrum/internal/types"
)

// ReplyToRef is the single-source-of-truth relevance predicate (thrum-4zqe):
// the SetOnEventWrite hook calls it BEFORE spawning the permission-intercept
// goroutine, and AfterMessageCreate calls it for its early-return. These tests
// pin the predicate so the hook's pre-filter and the dispatcher can never drift
// apart.

func TestReplyToRef_NoRefs(t *testing.T) {
	got := ReplyToRef(types.MessageCreateEvent{
		Type:      "message.create",
		MessageID: "msg_1",
		Body:      types.MessageBody{Content: "hello"},
	})
	if got != "" {
		t.Errorf("ReplyToRef with no refs = %q; want \"\"", got)
	}
}

func TestReplyToRef_NonReplyRefsIgnored(t *testing.T) {
	got := ReplyToRef(types.MessageCreateEvent{
		Type: "message.create",
		Refs: []types.Ref{
			{Type: "mention", Value: "agent_x"},
			{Type: "thread", Value: "msg_root"},
		},
	})
	if got != "" {
		t.Errorf("ReplyToRef with no reply_to ref = %q; want \"\"", got)
	}
}

func TestReplyToRef_ReturnsReplyToValue(t *testing.T) {
	got := ReplyToRef(types.MessageCreateEvent{
		Type: "message.create",
		Refs: []types.Ref{{Type: "reply_to", Value: "msg_target"}},
	})
	if got != "msg_target" {
		t.Errorf("ReplyToRef = %q; want \"msg_target\"", got)
	}
}

func TestReplyToRef_PicksReplyToAmongMixedRefs(t *testing.T) {
	got := ReplyToRef(types.MessageCreateEvent{
		Type: "message.create",
		Refs: []types.Ref{
			{Type: "mention", Value: "agent_x"},
			{Type: "reply_to", Value: "msg_target"},
			{Type: "thread", Value: "msg_root"},
		},
	})
	if got != "msg_target" {
		t.Errorf("ReplyToRef among mixed refs = %q; want \"msg_target\"", got)
	}
}

// TestReplyToRef_OriginAgnostic is the regression guard for the origin-filter
// ruling (thrum-4zqe): a reply that synced in from a PEER daemon must still be
// recognised as relevant, because cross-repo reply delivery depends on the
// owning daemon firing AfterMessageCreate for peer-origin replies. The
// relevance gate must key on reply_to alone, never on OriginDaemon.
func TestReplyToRef_OriginAgnostic(t *testing.T) {
	got := ReplyToRef(types.MessageCreateEvent{
		Type:         "message.create",
		OriginDaemon: "peer_daemon_B",
		Refs:         []types.Ref{{Type: "reply_to", Value: "msg_target"}},
	})
	if got != "msg_target" {
		t.Errorf("ReplyToRef for a peer-origin reply = %q; want \"msg_target\" (origin must not gate relevance)", got)
	}
}
