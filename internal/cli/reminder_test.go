package cli

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// fakeRPC captures method + params for assertion and lets a test
// supply a canned response. Each call is recorded so a test can verify
// the exact wire shape that ReminderXxx produced.
type fakeRPC struct {
	t           *testing.T
	expectMethod string
	respond     func(method string, params any) any
	gotMethod   string
	gotParams   map[string]any
	callCount   int
}

func newFakeRPC(t *testing.T) *fakeRPC {
	t.Helper()
	return &fakeRPC{t: t}
}

func (f *fakeRPC) expect(method string) { f.expectMethod = method }

func (f *fakeRPC) Call(method string, params any, result any) error {
	f.callCount++
	if f.expectMethod != "" && method != f.expectMethod {
		f.t.Errorf("Call method = %q, want %q", method, f.expectMethod)
	}
	f.gotMethod = method
	// Round-trip through JSON to capture the wire shape (map keys, types,
	// omitted fields) — this is what the daemon will actually see.
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		return err
	}
	f.gotParams = got

	// Supply a canned response if the test set one.
	if f.respond != nil && result != nil {
		rb, err := json.Marshal(f.respond(method, params))
		if err != nil {
			return err
		}
		return json.Unmarshal(rb, result)
	}
	return nil
}

func TestReminderSet_ShapesRPCParams(t *testing.T) {
	fake := newFakeRPC(t)
	fake.expect("reminder.set")
	fake.respond = func(_ string, _ any) any {
		return map[string]any{
			"id":               "reminder-docs_bot-123-4567",
			"raised_at":        int64(1700000000),
			"next_reminder_at": int64(1700003600),
		}
	}
	got, err := ReminderSet(fake, ReminderSetOpts{
		Source:      "agent",
		SourceAgent: "docs_bot",
		TriggerAt:   time.Unix(1700000000, 0).UTC(),
		TargetAgent: "docs_bot",
		Body:        "finish release notes",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "reminder-docs_bot-123-4567" {
		t.Errorf("ID = %q", got.ID)
	}
	if !got.NextReminderAt.Equal(time.Unix(1700003600, 0).UTC()) {
		t.Errorf("NextReminderAt = %v", got.NextReminderAt)
	}
	// Wire shape: trigger_at must be unix int64, not RFC3339 string.
	if v, ok := fake.gotParams["trigger_at"].(float64); !ok || int64(v) != 1700000000 {
		t.Errorf("trigger_at wire shape = %v (%T), want 1700000000 int", fake.gotParams["trigger_at"], fake.gotParams["trigger_at"])
	}
	if fake.gotParams["source"] != "agent" {
		t.Errorf("source = %v", fake.gotParams["source"])
	}
	if fake.gotParams["source_agent"] != "docs_bot" {
		t.Errorf("source_agent = %v", fake.gotParams["source_agent"])
	}
	if fake.gotParams["body"] != "finish release notes" {
		t.Errorf("body = %v", fake.gotParams["body"])
	}
}

func TestReminderSet_RPCErrorPropagates(t *testing.T) {
	fake := &fakeRPC{t: t}
	fake.respond = func(string, any) any {
		// json.Marshal of a function returns an error
		return make(chan int)
	}
	// The Call won't error; but a simpler way: define a sink that returns error.
	// Build a separate fake for this:
	failing := failingRPC{err: errors.New("connection refused")}
	_, err := ReminderSet(failing, ReminderSetOpts{TriggerAt: time.Now()})
	if err == nil {
		t.Error("expected error to propagate")
	}
}

type failingRPC struct{ err error }

func (f failingRPC) Call(string, any, any) error { return f.err }

func TestReminderDefer_ShapesRPCParams(t *testing.T) {
	fake := newFakeRPC(t)
	fake.expect("reminder.defer")
	fake.respond = func(string, any) any { return map[string]any{"ok": true} }

	until := time.Unix(1700003600, 0).UTC()
	if err := ReminderDefer(fake, "reminder-x-1-1", until, "leon"); err != nil {
		t.Fatal(err)
	}
	if v, ok := fake.gotParams["until"].(float64); !ok || int64(v) != 1700003600 {
		t.Errorf("until wire shape = %v", fake.gotParams["until"])
	}
	if fake.gotParams["id"] != "reminder-x-1-1" {
		t.Errorf("id = %v", fake.gotParams["id"])
	}
	if fake.gotParams["by"] != "leon" {
		t.Errorf("by = %v", fake.gotParams["by"])
	}
}

func TestReminderClear_ShapesRPCParams(t *testing.T) {
	fake := newFakeRPC(t)
	fake.expect("reminder.clear")
	fake.respond = func(string, any) any { return map[string]any{"ok": true} }

	if err := ReminderClear(fake, "reminder-x-1-1", "leon"); err != nil {
		t.Fatal(err)
	}
	if fake.gotParams["id"] != "reminder-x-1-1" {
		t.Errorf("id = %v", fake.gotParams["id"])
	}
	if fake.gotParams["by"] != "leon" {
		t.Errorf("by = %v", fake.gotParams["by"])
	}
}

func TestReminderCancel_ShapesRPCParams(t *testing.T) {
	fake := newFakeRPC(t)
	fake.expect("reminder.cancel")
	fake.respond = func(string, any) any { return map[string]any{"ok": true} }

	if err := ReminderCancel(fake, "reminder-x-1-1", "leon"); err != nil {
		t.Fatal(err)
	}
	if fake.gotParams["id"] != "reminder-x-1-1" {
		t.Errorf("id = %v", fake.gotParams["id"])
	}
}

func TestReminderGet_DecodesWireShape(t *testing.T) {
	triggerAt := time.Unix(1700000000, 0).UTC()
	deferWhen := time.Unix(1700001000, 0).UTC()
	deferTo := time.Unix(1700002000, 0).UTC()
	fake := newFakeRPC(t)
	fake.expect("reminder.get")
	fake.respond = func(string, any) any {
		return reminderWire{
			ID:          "reminder-docs_bot-100-0001",
			Source:      "agent",
			SourceAgent: "docs_bot",
			TriggerKind: "time",
			TriggerAt:   &triggerAt,
			TargetAgent: "docs_bot",
			Body:        "do thing",
			RaisedAt:    time.Unix(1699999900, 0).UTC(),
			State:       "open",
			DeferHistory: []DeferEntry{
				{DeferredBy: "leon", DeferTo: deferTo, When: deferWhen},
			},
		}
	}
	got, err := ReminderGet(fake, "reminder-docs_bot-100-0001")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "reminder-docs_bot-100-0001" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.TriggerAt == nil || !got.TriggerAt.Equal(triggerAt) {
		t.Errorf("TriggerAt = %v, want %v", got.TriggerAt, triggerAt)
	}
	if got.State != "open" {
		t.Errorf("State = %q", got.State)
	}
	// DeferHistory round-trip (brainstormer-review IMPORTANT #2):
	// daemon's defer_history must surface through the CLI wire layer
	// so the lookup view can render it per plan §Task 14 AC.
	if len(got.DeferHistory) != 1 {
		t.Fatalf("DeferHistory len = %d, want 1", len(got.DeferHistory))
	}
	if got.DeferHistory[0].DeferredBy != "leon" {
		t.Errorf("DeferHistory[0].DeferredBy = %q, want leon", got.DeferHistory[0].DeferredBy)
	}
	if !got.DeferHistory[0].DeferTo.Equal(deferTo) {
		t.Errorf("DeferHistory[0].DeferTo = %v, want %v", got.DeferHistory[0].DeferTo, deferTo)
	}
}

func TestReminderList_OmitsEmptyFilters(t *testing.T) {
	fake := newFakeRPC(t)
	fake.expect("reminder.list")
	fake.respond = func(string, any) any { return []reminderWire{} }

	// Only TargetAgent set — other fields must be absent from the wire.
	if _, err := ReminderList(fake, ReminderListOpts{TargetAgent: "alice"}); err != nil {
		t.Fatal(err)
	}
	if fake.gotParams["target_agent"] != "alice" {
		t.Errorf("target_agent = %v", fake.gotParams["target_agent"])
	}
	for _, omitted := range []string{"source", "trigger_kind", "state", "source_agent", "limit"} {
		if _, present := fake.gotParams[omitted]; present {
			t.Errorf("%s should be omitted when zero-valued (got %v)", omitted, fake.gotParams[omitted])
		}
	}
}

func TestReminderList_DecodesMultipleRows(t *testing.T) {
	fake := newFakeRPC(t)
	fake.respond = func(string, any) any {
		return []reminderWire{
			{ID: "reminder-a-1-1", Source: "agent", State: "open"},
			{ID: "reminder-a-2-2", Source: "agent", State: "open"},
		}
	}
	got, err := ReminderList(fake, ReminderListOpts{TargetAgent: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("rows = %d, want 2", len(got))
	}
}
