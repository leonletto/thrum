package reminders

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// newTestHandler wraps newTestStore + NewHandler for RPC tests.
func newTestHandler(t *testing.T) (*Handler, *SQLStore) {
	t.Helper()
	s := newTestStore(t)
	return NewHandler(s), s
}

func TestRPC_Set_AgentTime_Succeeds(t *testing.T) {
	h, s := newTestHandler(t)
	trigger := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	params := mustJSON(t, setParams{
		Source:      "agent",
		SourceAgent: "docs_bot",
		TriggerAt:   trigger.Unix(),
		TargetAgent: "docs_bot",
		Body:        "release notes",
	})
	out, err := h.HandleSet(ctx, params)
	if err != nil {
		t.Fatalf("HandleSet: %v", err)
	}
	resp, ok := out.(setResponse)
	if !ok {
		t.Fatalf("response type = %T", out)
	}
	if resp.ID == "" {
		t.Fatal("setResponse.ID empty")
	}
	if resp.NextReminderAt != trigger.Unix() {
		t.Errorf("NextReminderAt = %d, want %d", resp.NextReminderAt, trigger.Unix())
	}
	// Confirm round-trip through the Store.
	got, err := s.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("Store.Get: %v", err)
	}
	if got.Body != "release notes" {
		t.Errorf("Body = %q", got.Body)
	}
}

func TestRPC_Set_UserTime_Succeeds(t *testing.T) {
	h, _ := newTestHandler(t)
	trigger := time.Now().Add(time.Hour).UTC()
	params := mustJSON(t, setParams{
		Source:      "user",
		TriggerAt:   trigger.Unix(),
		TargetAgent: "leon",
		Body:        "stand-up",
	})
	out, err := h.HandleSet(ctx, params)
	if err != nil {
		t.Fatalf("HandleSet: %v", err)
	}
	resp := out.(setResponse)
	if resp.ID == "" {
		t.Fatal("user/time mint returned empty id")
	}
}

func TestRPC_Set_RejectsBadSource(t *testing.T) {
	h, _ := newTestHandler(t)
	params := mustJSON(t, setParams{
		Source:    "daemon", // daemon-source not user-facing
		TriggerAt: time.Now().Add(time.Hour).Unix(),
	})
	_, err := h.HandleSet(ctx, params)
	if err == nil {
		t.Fatal("expected error for source=daemon")
	}
	if !strings.Contains(err.Error(), "source must be") {
		t.Errorf("error message should mention source: %v", err)
	}
}

func TestRPC_Set_RejectsMissingTriggerAt(t *testing.T) {
	h, _ := newTestHandler(t)
	params := mustJSON(t, setParams{
		Source:      "agent",
		SourceAgent: "docs_bot",
		TargetAgent: "docs_bot",
		Body:        "x",
		// no TriggerAt → defaults to 0
	})
	_, err := h.HandleSet(ctx, params)
	if err == nil {
		t.Fatal("expected error for missing trigger_at")
	}
}

func TestRPC_Set_RejectsInvalidPayload(t *testing.T) {
	h, _ := newTestHandler(t)
	// agent/time without source_agent — validator rejects in Mint.
	params := mustJSON(t, setParams{
		Source:      "agent",
		TriggerAt:   time.Now().Add(time.Hour).Unix(),
		TargetAgent: "docs_bot",
		Body:        "x",
	})
	_, err := h.HandleSet(ctx, params)
	if err == nil {
		t.Fatal("expected validator error for missing source_agent")
	}
}

func TestRPC_Set_RejectsMalformedJSON(t *testing.T) {
	h, _ := newTestHandler(t)
	_, err := h.HandleSet(ctx, json.RawMessage(`not-json`))
	if err == nil {
		t.Fatal("expected error for malformed params")
	}
}

func TestRPC_Get_RoundTrip(t *testing.T) {
	h, s := newTestHandler(t)
	id := mintOpenTime(t, s)
	out, err := h.HandleGet(ctx, mustJSON(t, getParams{ID: id}))
	if err != nil {
		t.Fatalf("HandleGet: %v", err)
	}
	r := out.(*Reminder)
	if r.ID != id {
		t.Errorf("ID = %q, want %q", r.ID, id)
	}
}

func TestRPC_Get_NotFound(t *testing.T) {
	h, _ := newTestHandler(t)
	_, err := h.HandleGet(ctx, mustJSON(t, getParams{ID: "reminder-nobody-000-0000"}))
	if err == nil {
		t.Fatal("expected error for missing id")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should say not found, got %v", err)
	}
}

func TestRPC_List_FilterRoundTrip(t *testing.T) {
	h, s := newTestHandler(t)
	// Mint two rows for alice + one for bob.
	for _, who := range []string{"alice", "alice", "bob"} {
		trigger := time.Now().Add(time.Hour).UTC()
		if err := s.Mint(ctx, &Reminder{
			Source: SourceAgent, TriggerKind: TriggerTime, SourceAgent: who,
			TriggerAt: &trigger, TargetAgent: who, Body: "x",
		}); err != nil {
			t.Fatalf("mint %s: %v", who, err)
		}
	}
	out, err := h.HandleList(ctx, mustJSON(t, listParams{TargetAgent: "alice"}))
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	rows := out.([]*Reminder)
	if len(rows) != 2 {
		t.Errorf("got %d rows for target=alice, want 2", len(rows))
	}
}

func TestRPC_List_EmptyParamsReturnsAll(t *testing.T) {
	h, s := newTestHandler(t)
	_ = mintOpenTime(t, s)
	_ = mintOpenTime(t, s)
	out, err := h.HandleList(ctx, json.RawMessage(``))
	if err != nil {
		t.Fatalf("HandleList empty: %v", err)
	}
	rows := out.([]*Reminder)
	if len(rows) != 2 {
		t.Errorf("empty filter: got %d rows, want 2", len(rows))
	}
}

func TestRPC_Defer_HappyPath(t *testing.T) {
	h, s := newTestHandler(t)
	id := mintOpenTime(t, s)
	until := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	out, err := h.HandleDefer(ctx, mustJSON(t, deferParams{ID: id, Until: until.Unix(), By: "leon"}))
	if err != nil {
		t.Fatalf("HandleDefer: %v", err)
	}
	resp := out.(okResponse)
	if !resp.OK {
		t.Error("OK should be true on success")
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.NextReminderAt == nil || !got.NextReminderAt.Equal(until) {
		t.Errorf("NextReminderAt = %v, want %v", got.NextReminderAt, until)
	}
	if len(got.DeferHistory) != 1 || got.DeferHistory[0].DeferredBy != "leon" {
		t.Errorf("DeferHistory = %+v", got.DeferHistory)
	}
}

func TestRPC_Defer_RejectsTerminal(t *testing.T) {
	h, s := newTestHandler(t)
	id := mintOpenTime(t, s)
	if err := s.Clear(ctx, id, "leon"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	_, err := h.HandleDefer(ctx, mustJSON(t, deferParams{ID: id, Until: time.Now().Add(time.Hour).Unix()}))
	if err == nil {
		t.Fatal("expected ErrTerminalState on defer of cleared row")
	}
}

func TestRPC_Clear_HappyPath(t *testing.T) {
	h, s := newTestHandler(t)
	id := mintOpenTime(t, s)
	out, err := h.HandleClear(ctx, mustJSON(t, byParams{ID: id, By: "leon"}))
	if err != nil {
		t.Fatalf("HandleClear: %v", err)
	}
	if !out.(okResponse).OK {
		t.Error("OK false")
	}
	got, _ := s.Get(ctx, id)
	if got.State != StateCleared {
		t.Errorf("State = %q (want cleared)", got.State)
	}
}

func TestRPC_Clear_RejectsAlreadyCleared(t *testing.T) {
	h, s := newTestHandler(t)
	id := mintOpenTime(t, s)
	if _, err := h.HandleClear(ctx, mustJSON(t, byParams{ID: id})); err != nil {
		t.Fatalf("first clear: %v", err)
	}
	_, err := h.HandleClear(ctx, mustJSON(t, byParams{ID: id}))
	if err == nil {
		t.Fatal("expected ErrTerminalState on second clear")
	}
}

func TestRPC_Cancel_HappyPath(t *testing.T) {
	h, s := newTestHandler(t)
	id := mintOpenTime(t, s)
	if _, err := h.HandleCancel(ctx, mustJSON(t, byParams{ID: id, By: "leon"})); err != nil {
		t.Fatalf("HandleCancel: %v", err)
	}
	got, _ := s.Get(ctx, id)
	if got.State != StateCancelled {
		t.Errorf("State = %q (want cancelled)", got.State)
	}
}

func TestRPC_AllHandlers_RejectEmptyID(t *testing.T) {
	h, _ := newTestHandler(t)
	cases := map[string]func() error{
		"get":    func() error { _, e := h.HandleGet(ctx, mustJSON(t, getParams{})); return e },
		"defer":  func() error { _, e := h.HandleDefer(ctx, mustJSON(t, deferParams{Until: 1})); return e },
		"clear":  func() error { _, e := h.HandleClear(ctx, mustJSON(t, byParams{})); return e },
		"cancel": func() error { _, e := h.HandleCancel(ctx, mustJSON(t, byParams{})); return e },
	}
	for name, run := range cases {
		if err := run(); err == nil {
			t.Errorf("%s: expected error for empty id", name)
		}
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
