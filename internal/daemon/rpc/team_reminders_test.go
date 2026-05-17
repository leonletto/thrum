package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/reminders"
)

// fakeReminderStore is a minimal reminders.Store impl for testing
// team.list's decoration step. Only OpenForAgent is exercised by the
// decoration code path; other methods return errors so accidental
// callers (future changes) get a loud failure rather than silent zero
// values.
type fakeReminderStore struct {
	rowsByAgent map[string][]*reminders.Reminder
	errByAgent  map[string]error
}

func (f *fakeReminderStore) OpenForAgent(_ context.Context, agent string) ([]*reminders.Reminder, error) {
	if err, ok := f.errByAgent[agent]; ok {
		return nil, err
	}
	return f.rowsByAgent[agent], nil
}

// Stub-out the remaining Store methods.
func (f *fakeReminderStore) Mint(context.Context, *reminders.Reminder) error {
	return errors.New("fakeReminderStore.Mint not implemented")
}
func (f *fakeReminderStore) Get(context.Context, string) (*reminders.Reminder, error) {
	return nil, errors.New("fakeReminderStore.Get not implemented")
}
func (f *fakeReminderStore) List(context.Context, reminders.ListFilter) ([]*reminders.Reminder, error) {
	return nil, errors.New("fakeReminderStore.List not implemented")
}
func (f *fakeReminderStore) Defer(context.Context, string, time.Time, string) error {
	return errors.New("fakeReminderStore.Defer not implemented")
}
func (f *fakeReminderStore) Clear(context.Context, string, string) error {
	return errors.New("fakeReminderStore.Clear not implemented")
}
func (f *fakeReminderStore) Cancel(context.Context, string, string) error {
	return errors.New("fakeReminderStore.Cancel not implemented")
}
func (f *fakeReminderStore) Fire(context.Context, string, time.Time) error {
	return errors.New("fakeReminderStore.Fire not implemented")
}
func (f *fakeReminderStore) FireAndRearm(context.Context, string, time.Time, time.Time) error {
	return errors.New("fakeReminderStore.FireAndRearm not implemented")
}
func (f *fakeReminderStore) DueOpen(context.Context, time.Time) ([]*reminders.Reminder, error) {
	return nil, errors.New("fakeReminderStore.DueOpen not implemented")
}
func (f *fakeReminderStore) MintConditionForAgent(context.Context, string, json.RawMessage, []string, string, time.Time) (*reminders.Reminder, bool, error) {
	return nil, false, errors.New("fakeReminderStore.MintConditionForAgent not implemented")
}

func TestDecorateWithReminders_NilStoreLeavesMembersUntouched(t *testing.T) {
	h := &TeamHandler{remindersStore: nil}
	members := []TeamMember{{AgentID: "docs_bot"}}
	got := h.decorateWithReminders(context.Background(), members)
	if got[0].Reminders != nil {
		t.Errorf("nil store should leave Reminders nil, got %v", got[0].Reminders)
	}
}

func TestDecorateWithReminders_PopulatesIDs(t *testing.T) {
	store := &fakeReminderStore{
		rowsByAgent: map[string][]*reminders.Reminder{
			"docs_bot": {
				{ID: "reminder-docs_bot-100-0001"},
				{ID: "reminder-docs_bot-100-0002"},
			},
		},
	}
	h := &TeamHandler{remindersStore: store}
	members := []TeamMember{{AgentID: "docs_bot"}, {AgentID: "coordinator_main"}}
	got := h.decorateWithReminders(context.Background(), members)

	if len(got[0].Reminders) != 2 {
		t.Errorf("docs_bot Reminders len = %d, want 2", len(got[0].Reminders))
	}
	if got[0].Reminders[0] != "reminder-docs_bot-100-0001" {
		t.Errorf("docs_bot first id = %q", got[0].Reminders[0])
	}
	if got[1].Reminders != nil {
		t.Errorf("coordinator_main should have nil Reminders (no rows), got %v", got[1].Reminders)
	}
}

func TestDecorateWithReminders_ContinuesOnError(t *testing.T) {
	store := &fakeReminderStore{
		rowsByAgent: map[string][]*reminders.Reminder{
			"good_agent": {{ID: "reminder-good_agent-1-1"}},
		},
		errByAgent: map[string]error{
			"bad_agent": errors.New("simulated SQL error"),
		},
	}
	h := &TeamHandler{remindersStore: store}
	members := []TeamMember{
		{AgentID: "bad_agent"},
		{AgentID: "good_agent"},
	}
	got := h.decorateWithReminders(context.Background(), members)

	// Bad agent gets no reminders but the iteration continues.
	if got[0].Reminders != nil {
		t.Errorf("bad_agent should have nil Reminders on error, got %v", got[0].Reminders)
	}
	if len(got[1].Reminders) != 1 {
		t.Errorf("good_agent Reminders len = %d, want 1 (iteration must continue past prior error)", len(got[1].Reminders))
	}
}

func TestCapReminderIDs_UnderCap_Unchanged(t *testing.T) {
	in := []string{"a", "b", "c"}
	got := capReminderIDs(in, 10)
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestCapReminderIDs_AtCap_Unchanged(t *testing.T) {
	in := []string{"a", "b", "c", "d", "e"}
	got := capReminderIDs(in, 5)
	if len(got) != 5 || got[4] != "e" {
		t.Errorf("got %v, want full slice", got)
	}
}

func TestCapReminderIDs_OverCap_AddsMoreMarker(t *testing.T) {
	ids := make([]string, 15)
	for i := range ids {
		ids[i] = fmt.Sprintf("reminder-x-%d-%d", i, i)
	}
	got := capReminderIDs(ids, 10)
	if len(got) != 10 {
		t.Errorf("len = %d, want 10", len(got))
	}
	// First 9 should be the original ids[:9]; last should be the marker.
	for i := 0; i < 9; i++ {
		if got[i] != ids[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], ids[i])
		}
	}
	want := "... +6 more" // 15 - 9 = 6 elided
	if got[9] != want {
		t.Errorf("marker = %q, want %q", got[9], want)
	}
}

// TestDecorateWithReminders_TruncatesOverCap exercises the cap +
// decoration in combination.
func TestDecorateWithReminders_TruncatesOverCap(t *testing.T) {
	rows := make([]*reminders.Reminder, 15)
	for i := range rows {
		rows[i] = &reminders.Reminder{ID: fmt.Sprintf("reminder-x-%d-%d", i, i)}
	}
	store := &fakeReminderStore{
		rowsByAgent: map[string][]*reminders.Reminder{"x": rows},
	}
	h := &TeamHandler{remindersStore: store}
	got := h.decorateWithReminders(context.Background(), []TeamMember{{AgentID: "x"}})
	if len(got[0].Reminders) != teamReminderCompactCap {
		t.Errorf("len = %d, want %d (capped)", len(got[0].Reminders), teamReminderCompactCap)
	}
}
