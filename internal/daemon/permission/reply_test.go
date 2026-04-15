package permission

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/types"
)

// recordingSender captures sendKeystroke calls for test assertions.
type recordingSender struct {
	mu    sync.Mutex
	calls []keystrokeCall
	fail  error // optional — when set, returned from every call
}

type keystrokeCall struct {
	target string
	key    string
}

func (r *recordingSender) fn() func(target, key string) error {
	return func(target, key string) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls = append(r.calls, keystrokeCall{target: target, key: key})
		return r.fail
	}
}

func (r *recordingSender) snapshot() []keystrokeCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]keystrokeCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// newReplyTestPermission constructs a Permission with an in-memory
// store and an injected keystroke sender. No real state.State is
// needed — the reply path only touches the store and the sender.
func newReplyTestPermission(t *testing.T, sender *recordingSender) *Permission {
	t.Helper()
	db := openTestDB(t)
	p := New(nil, db, "supervisor_thrum", "thrum", ".")
	p.keystrokeSender = sender.fn()
	return p
}

// seedPendingNudge inserts a nudge row with the given keys so
// TryResolve has something to match.
func seedPendingNudge(t *testing.T, p *Permission, msgID, approveKey, denyKey string) {
	t.Helper()
	row := &NudgeRow{
		MessageID:     msgID,
		Session:       "cursor-test",
		TmuxTarget:    "cursor-test:0.0",
		AgentName:     "researcher_cursor",
		PatternKey:    "cursor.not_in_allowlist",
		ApproveKey:    approveKey,
		DenyKey:       denyKey,
		FirstDetected: time.Now().UTC(),
		LastNudgeAt:   time.Now().UTC(),
		NudgeCount:    1,
		LastPaneHash:  sha256.Sum256([]byte("pane")),
		ExpiresAt:     time.Now().UTC().Add(time.Hour),
	}
	if err := p.store.InsertPendingNudge(context.Background(), row); err != nil {
		t.Fatalf("seed nudge: %v", err)
	}
}

func TestAfterMessageCreate_NoReplyTo_NoOp(t *testing.T) {
	sender := &recordingSender{}
	p := newReplyTestPermission(t, sender)

	p.AfterMessageCreate(context.Background(), types.MessageCreateEvent{
		Type:      "message.create",
		MessageID: "msg_random",
		Body:      types.MessageBody{Content: "hello"},
	})

	if len(sender.snapshot()) != 0 {
		t.Errorf("expected no keystroke sends, got %v", sender.snapshot())
	}
}

func TestAfterMessageCreate_NonMessageEvent_Tolerated(t *testing.T) {
	// AfterMessageCreate only gets called for message.create events in
	// production (the hook filters), but we defensively accept any
	// event shape and no-op on missing replyTo.
	sender := &recordingSender{}
	p := newReplyTestPermission(t, sender)
	p.AfterMessageCreate(context.Background(), types.MessageCreateEvent{})
	if len(sender.snapshot()) != 0 {
		t.Errorf("expected no keystrokes, got %v", sender.snapshot())
	}
}

func TestTryResolve_UnknownNudge_NoOp(t *testing.T) {
	sender := &recordingSender{}
	p := newReplyTestPermission(t, sender)

	p.TryResolve(context.Background(),
		types.MessageCreateEvent{Body: types.MessageBody{Content: "y"}},
		"msg_nonexistent")

	if len(sender.snapshot()) != 0 {
		t.Errorf("expected no keystroke sends, got %v", sender.snapshot())
	}
}

func TestTryResolve_ApproveSendsKeystroke(t *testing.T) {
	sender := &recordingSender{}
	p := newReplyTestPermission(t, sender)
	seedPendingNudge(t, p, "msg_nudge_1", "y", "Escape")

	p.TryResolve(context.Background(),
		types.MessageCreateEvent{Body: types.MessageBody{Content: "y"}},
		"msg_nudge_1")

	calls := sender.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 keystroke, got %d: %v", len(calls), calls)
	}
	if calls[0].target != "cursor-test:0.0" || calls[0].key != "y" {
		t.Errorf("expected (cursor-test:0.0, y), got %+v", calls[0])
	}

	// Row must be deleted after a successful approve.
	row, _ := p.store.LookupPendingNudgeByMessageID(context.Background(), "msg_nudge_1")
	if row != nil {
		t.Error("row should be deleted after approve")
	}
}

func TestTryResolve_ApproveCaseInsensitive(t *testing.T) {
	sender := &recordingSender{}
	p := newReplyTestPermission(t, sender)
	seedPendingNudge(t, p, "msg_nudge_1", "y", "Escape")

	// All of these should dispatch approve.
	for _, body := range []string{"Y", "YES", "yes", "approve", "Approve", "A", "a"} {
		// Re-seed between calls since approve deletes the row.
		if body != "Y" {
			seedPendingNudge(t, p, "msg_nudge_1", "y", "Escape")
		}
		p.TryResolve(context.Background(),
			types.MessageCreateEvent{Body: types.MessageBody{Content: body}},
			"msg_nudge_1")
	}

	calls := sender.snapshot()
	if len(calls) != 7 {
		t.Errorf("expected 7 approve keystrokes, got %d: %v", len(calls), calls)
	}
}

func TestTryResolve_DenySendsKeystroke(t *testing.T) {
	sender := &recordingSender{}
	p := newReplyTestPermission(t, sender)
	seedPendingNudge(t, p, "msg_nudge_1", "y", "Escape")

	p.TryResolve(context.Background(),
		types.MessageCreateEvent{Body: types.MessageBody{Content: "n"}},
		"msg_nudge_1")

	calls := sender.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 keystroke, got %d: %v", len(calls), calls)
	}
	if calls[0].key != "Escape" {
		t.Errorf("expected Escape, got %q", calls[0].key)
	}
	// Row must be deleted after a successful deny.
	row, _ := p.store.LookupPendingNudgeByMessageID(context.Background(), "msg_nudge_1")
	if row != nil {
		t.Error("row should be deleted after deny")
	}
}

func TestTryResolve_DenyButNoDenyKey_RowStays(t *testing.T) {
	sender := &recordingSender{}
	p := newReplyTestPermission(t, sender)
	seedPendingNudge(t, p, "msg_nudge_1", "A", "") // auggie tool — no in-prompt deny

	p.TryResolve(context.Background(),
		types.MessageCreateEvent{Body: types.MessageBody{Content: "n"}},
		"msg_nudge_1")

	if len(sender.snapshot()) != 0 {
		t.Errorf("expected 0 keystrokes for empty deny key, got %v", sender.snapshot())
	}
	row, _ := p.store.LookupPendingNudgeByMessageID(context.Background(), "msg_nudge_1")
	if row == nil {
		t.Error("row should stay in place when deny key is empty")
	}
}

func TestTryResolve_UnknownBody_PassThrough(t *testing.T) {
	sender := &recordingSender{}
	p := newReplyTestPermission(t, sender)
	seedPendingNudge(t, p, "msg_nudge_1", "y", "Escape")

	// Unknown reply body — not approve or deny. No keystrokes, row
	// stays so reminders continue firing.
	p.TryResolve(context.Background(),
		types.MessageCreateEvent{Body: types.MessageBody{Content: "hmm let me think"}},
		"msg_nudge_1")

	if len(sender.snapshot()) != 0 {
		t.Errorf("expected no keystrokes for unknown body, got %v", sender.snapshot())
	}
	row, _ := p.store.LookupPendingNudgeByMessageID(context.Background(), "msg_nudge_1")
	if row == nil {
		t.Error("row should stay in place for unknown reply bodies")
	}
}

func TestTryResolve_KeystrokeFailure_RowStays(t *testing.T) {
	sender := &recordingSender{fail: errors.New("tmux unreachable")}
	p := newReplyTestPermission(t, sender)
	seedPendingNudge(t, p, "msg_nudge_1", "y", "Escape")

	p.TryResolve(context.Background(),
		types.MessageCreateEvent{Body: types.MessageBody{Content: "y"}},
		"msg_nudge_1")

	// The sender was called and failed — the row must NOT be deleted
	// so the next reminder can retry the dispatch.
	row, _ := p.store.LookupPendingNudgeByMessageID(context.Background(), "msg_nudge_1")
	if row == nil {
		t.Error("row should stay in place when keystroke send fails")
	}
}

func TestAfterMessageCreate_ReplyToRefDispatches(t *testing.T) {
	sender := &recordingSender{}
	p := newReplyTestPermission(t, sender)
	seedPendingNudge(t, p, "msg_nudge_1", "y", "Escape")

	// Simulate an approved reply routed via the reply_to ref.
	p.AfterMessageCreate(context.Background(), types.MessageCreateEvent{
		Type:      "message.create",
		MessageID: "msg_reply_1",
		Body:      types.MessageBody{Content: "y"},
		Refs:      []types.Ref{{Type: "reply_to", Value: "msg_nudge_1"}},
	})

	calls := sender.snapshot()
	if len(calls) != 1 || calls[0].key != "y" {
		t.Errorf("expected approve dispatch, got %v", calls)
	}
}

func TestAfterMessageCreate_MultipleRefsPicksReplyTo(t *testing.T) {
	sender := &recordingSender{}
	p := newReplyTestPermission(t, sender)
	seedPendingNudge(t, p, "msg_nudge_1", "y", "Escape")

	p.AfterMessageCreate(context.Background(), types.MessageCreateEvent{
		Type:      "message.create",
		MessageID: "msg_reply_1",
		Body:      types.MessageBody{Content: "n"},
		Refs: []types.Ref{
			{Type: "mention", Value: "@coordinator_main"},
			{Type: "reply_to", Value: "msg_nudge_1"},
		},
	})

	calls := sender.snapshot()
	if len(calls) != 1 || calls[0].key != "Escape" {
		t.Errorf("expected deny dispatch, got %v", calls)
	}
}

func TestIsSpecialKeyName(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"Enter", true},
		{"Escape", true},
		{"Tab", true},
		{"BTab", true},
		{"Up", true},
		{"Down", true},
		{"Left", true},
		{"Right", true},
		{"Space", true},
		{"BSpace", true},
		{"Delete", true},
		{"Home", true},
		{"End", true},
		{"PgUp", true},
		{"PgDn", true},
		{"y", false},
		{"1", false},
		{"A", false},
		{"yes", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isSpecialKeyName(tc.key); got != tc.want {
			t.Errorf("isSpecialKeyName(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

// sendKeystroke with a comma-separated sequence (opencode DenyKey
// "End,Enter") should dispatch one send per segment. We test via the
// injected sender by wrapping it and counting.
func TestSendKeystroke_CommaSplit(t *testing.T) {
	var captured []string
	p := New(nil, openTestDB(t), "supervisor_thrum", "thrum", ".")
	p.keystrokeSender = func(target, key string) error {
		captured = append(captured, key)
		return nil
	}
	if err := p.sendKeystroke("cursor-test:0.0", "End,Enter"); err != nil {
		t.Fatalf("sendKeystroke: %v", err)
	}
	if len(captured) != 2 || captured[0] != "End" || captured[1] != "Enter" {
		t.Errorf("expected [End Enter], got %v", captured)
	}
}

// An injected sender that returns an error on the first segment
// should short-circuit — no second segment is attempted.
func TestSendKeystroke_CommaSplitShortCircuitsOnError(t *testing.T) {
	var captured []string
	boom := errors.New("first segment failed")
	p := New(nil, openTestDB(t), "supervisor_thrum", "thrum", ".")
	p.keystrokeSender = func(target, key string) error {
		captured = append(captured, key)
		return boom
	}
	err := p.sendKeystroke("cursor-test:0.0", "End,Enter")
	if !errors.Is(err, boom) {
		t.Errorf("expected first-segment error to propagate, got %v", err)
	}
	if len(captured) != 1 {
		t.Errorf("expected short-circuit after 1 segment, got %v", captured)
	}
}

// Regression: the approve/deny regexes must be anchored so bodies
// that merely contain "y" (e.g. "why not?") do NOT dispatch.
func TestTryResolve_AnchoredRegex(t *testing.T) {
	sender := &recordingSender{}
	p := newReplyTestPermission(t, sender)
	seedPendingNudge(t, p, "msg_nudge_1", "y", "Escape")

	p.TryResolve(context.Background(),
		types.MessageCreateEvent{Body: types.MessageBody{Content: "why not?"}},
		"msg_nudge_1")

	if len(sender.snapshot()) != 0 {
		t.Errorf("'why not?' should NOT dispatch approve, got %v", sender.snapshot())
	}
	if !strings.Contains("cursor-test:0.0", "cursor-test") {
		t.Skip("tautology")
	}
}
