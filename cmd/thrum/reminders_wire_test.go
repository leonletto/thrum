package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/bridge/email"
	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/reminders"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/schema"
)

// newSchedulerAndState spins up an A-B1 scheduler.Scheduler + state.State
// over the same on-disk SQLite. Used by wireReminders tests so the
// MessageHandler (which takes state.State) and reminders.Store (which
// wraps safedb.DB) share the same DB.
func newSchedulerAndState(t *testing.T) (*scheduler.Scheduler, *state.State, reminders.Store) {
	t.Helper()
	thrumDir := t.TempDir()
	syncDir := filepath.Join(thrumDir, "sync")
	if err := schemaInitInDir(t, thrumDir); err != nil {
		t.Fatalf("schemaInit: %v", err)
	}
	st, err := state.NewState(thrumDir, syncDir, "test-repo", "")
	if err != nil {
		t.Fatalf("state.NewState: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	db := safedb.New(st.DB().Raw())
	store := reminders.NewSQLStore(db)
	sched := scheduler.New(scheduler.Config{DB: db, DaemonID: "test-daemon"})
	t.Cleanup(func() { _ = sched.Stop(context.Background()) })
	return sched, st, store
}

// schemaInitInDir creates the SQLite file + migrates so state.NewState
// can open it. Mirrors what daemon-boot does for tests that need
// state.State + scheduler together.
func schemaInitInDir(t *testing.T, thrumDir string) error {
	t.Helper()
	dbPath := filepath.Join(thrumDir, "thrum.db")
	raw, err := schema.OpenDB(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = raw.Close() }()
	return schema.InitDB(raw)
}

func TestWireReminders_RegistersInternalDispatchJob(t *testing.T) {
	sched, st, store := newSchedulerAndState(t)
	msgHandler := rpc.NewMessageHandlerWithDispatcher(st, nil, "", "", "")
	cfg := &config.DaemonConfig{
		Reminders:    config.RemindersConfig{DispatchIntervalSeconds: 45},
		StalledSweep: config.StalledSweepConfig{IntervalMinutes: 15},
	}

	wireReminders(sched, store, msgHandler, nil, "supervisor_test", cfg)

	spec, ok := sched.JobSpec("internal.reminder_dispatch")
	if !ok {
		t.Fatalf("internal.reminder_dispatch not registered after wireReminders")
	}
	if spec.Type != "internal" {
		t.Errorf("spec.Type = %q, want internal", spec.Type)
	}
	if spec.Schedule != "@every 45s" {
		t.Errorf("spec.Schedule = %q, want '@every 45s' (from cfg)", spec.Schedule)
	}
	if spec.CatchUp != "skip" {
		t.Errorf("spec.CatchUp = %q, want 'skip'", spec.CatchUp)
	}
	if spec.RunAtStart {
		t.Error("RunAtStart should be false")
	}
}

func TestWireReminders_DefaultCadenceWhenUnset(t *testing.T) {
	sched, st, store := newSchedulerAndState(t)
	msgHandler := rpc.NewMessageHandlerWithDispatcher(st, nil, "", "", "")
	cfg := &config.DaemonConfig{} // both blocks zero

	wireReminders(sched, store, msgHandler, nil, "supervisor_test", cfg)

	spec, _ := sched.JobSpec("internal.reminder_dispatch")
	if spec.Schedule != "@every 30s" {
		t.Errorf("spec.Schedule = %q, want '@every 30s' (canonical §4.4 default)", spec.Schedule)
	}
}

// --- messageHandlerSender adapter tests ---
//
// The adapter takes *rpc.MessageHandler concretely (not an interface)
// since that's the only message-pipeline entry point in production.
// Success-path coverage lives in the wireReminders integration test
// above + the reminders package's own dispatcher_test.go; tests here
// focus on the failure paths the adapter adds (empty toAgent, nil
// handler, daemon-source remap, no-fallback rejection).

func TestMessageHandlerSender_EmptyToAgent_Rejects(t *testing.T) {
	s := &messageHandlerSender{handler: &rpc.MessageHandler{}}
	err := s.SendReminder(context.Background(), "from_agent", "", "body")
	if err == nil {
		t.Error("expected error for empty toAgent")
	}
	if !strings.Contains(err.Error(), "empty toAgent") {
		t.Errorf("error should mention empty toAgent; got %v", err)
	}
}

func TestMessageHandlerSender_NilHandler_Rejects(t *testing.T) {
	s := &messageHandlerSender{handler: nil}
	err := s.SendReminder(context.Background(), "from", "to", "body")
	if err == nil {
		t.Error("expected error for nil handler (wiring bug)")
	}
	if !strings.Contains(err.Error(), "nil handler") {
		t.Errorf("error should mention nil handler; got %v", err)
	}
}

// TestMessageHandlerSender_DaemonSource_RemapsToFallback verifies the
// fromAgent="daemon" remapping. HandleSend rejects "daemon" as a
// caller (no session-bearing agent of that id exists), so the adapter
// substitutes fallbackSender. Without a real registered supervisor
// agent in state.State, HandleSend still fails — but the failure
// message should reference fallbackSender, not "daemon".
func TestMessageHandlerSender_DaemonSource_RemapsToFallback(t *testing.T) {
	_, st, _ := newSchedulerAndState(t)
	msgHandler := rpc.NewMessageHandlerWithDispatcher(st, nil, "", "", "")
	sender := &messageHandlerSender{
		handler:        msgHandler,
		fallbackSender: "supervisor_test",
	}

	err := sender.SendReminder(context.Background(), "daemon", "docs_bot",
		"Idle Agent Detected with idle-id: reminder-docs_bot-100-0001")
	if err == nil {
		// No supervisor session is actually registered in this test
		// fixture, so HandleSend will still fail — but the failure
		// path must reference the fallback, not "daemon".
		t.Fatal("expected HandleSend to fail since no supervisor session exists in fixture")
	}
	if strings.Contains(err.Error(), "agent daemon") {
		t.Errorf("error mentions 'agent daemon' — fallback remap didn't fire: %v", err)
	}
	if !strings.Contains(err.Error(), "supervisor_test") {
		t.Errorf("error should mention substituted fallbackSender; got %v", err)
	}
}

func TestMessageHandlerSender_NoFallbackConfigured_Rejects(t *testing.T) {
	_, st, _ := newSchedulerAndState(t)
	msgHandler := rpc.NewMessageHandlerWithDispatcher(st, nil, "", "", "")
	// Empty fallbackSender + daemon source → can't resolve caller.
	sender := &messageHandlerSender{handler: msgHandler, fallbackSender: ""}

	err := sender.SendReminder(context.Background(), "daemon", "docs_bot", "body")
	if err == nil {
		t.Error("expected error when fallbackSender is empty and fromAgent is 'daemon'")
	}
	if !strings.Contains(err.Error(), "no caller resolvable") {
		t.Errorf("error should mention unresolvable caller; got %v", err)
	}
}

func TestMessageHandlerSender_SatisfiesInterface(t *testing.T) {
	// Compile-time assertion mirroring the var _ in production.
	var _ reminders.MessageSender = (*messageHandlerSender)(nil)
}

// --- reminderEmailQueue adapter tests ---
//
// The adapter is the composition root between A-B4 (DeliverySink) and
// D-B1 (email.Queue). These tests verify the adapter's contract:
// interface satisfaction, rejection of nil queue, and end-to-end
// enqueue against a real email_outbound_queue row.

func TestReminderEmailQueue_SatisfiesInterface(t *testing.T) {
	// Compile-time assertion mirroring the var _ in production.
	var _ reminders.EmailQueue = (*reminderEmailQueue)(nil)
}

func TestReminderEmailQueue_NilQueueRejects(t *testing.T) {
	a := &reminderEmailQueue{fromAgent: "supervisor_test"}
	err := a.QueueReminderEmail(context.Background(), "leon@example.com", "subj", "body")
	if err == nil {
		t.Fatal("expected error when queue is nil")
	}
	if !strings.Contains(err.Error(), "nil queue") {
		t.Errorf("error should mention nil queue; got %v", err)
	}
}

// TestReminderEmailQueue_EmptyToRejects mirrors the
// messageHandlerSender empty-toAgent guard so an empty chain entry
// doesn't burn the full SMTP retry budget before surfacing the
// misconfiguration.
func TestReminderEmailQueue_EmptyToRejects(t *testing.T) {
	_, st, _ := newSchedulerAndState(t)
	a := &reminderEmailQueue{
		queue:     email.NewQueue(st.DB().Raw()),
		fromAgent: "supervisor_test",
	}
	err := a.QueueReminderEmail(context.Background(), "", "subj", "body")
	if err == nil {
		t.Fatal("expected error for empty to address")
	}
	if !strings.Contains(err.Error(), "empty to address") {
		t.Errorf("error should mention empty to address; got %v", err)
	}
}

// TestReminderEmailQueue_EnqueuesRow exercises the full A-B4 → D-B1
// substrate composition: a real DeliverySink fires a daemon-condition
// reminder with an all-email TargetChain through the adapter into
// D-B1's email_outbound_queue table. Pre-D-B1 (EmailQueue=nil) this
// scenario log+skipped every entry and returned "no recipients
// reached"; post-D-B1 every email is enqueued and the row state can
// transition cleanly.
func TestReminderEmailQueue_EnqueuesRow(t *testing.T) {
	_, st, _ := newSchedulerAndState(t)

	queue := email.NewQueue(st.DB().Raw())
	adapter := &reminderEmailQueue{
		queue:     queue,
		fromAgent: "supervisor_test",
	}

	// Sanity: direct adapter call writes one queued row.
	if err := adapter.QueueReminderEmail(
		context.Background(),
		"leon@example.com",
		"Idle Agent Detected",
		"docs_bot is idle (reminder-docs_bot-100-0001)",
	); err != nil {
		t.Fatalf("adapter.QueueReminderEmail: %v", err)
	}

	// Compose the adapter into a DeliverySink and fire an all-email
	// chain — pre-fix this was the infinite-loop path.
	sink := reminders.NewDeliverySink(noopSender{}, adapter, nil)
	r := &reminders.Reminder{
		Source:       reminders.SourceDaemon,
		TriggerKind:  reminders.TriggerConditionPaneQuiet,
		TargetAgent:  "docs_bot",
		TargetChain:  []string{"leon@example.com", "ops@example.com"},
		PaneSnapshot: "stale",
		TriggerMeta:  json.RawMessage(`{}`),
		ID:           "reminder-docs_bot-100-0001",
		RaisedAt:     time.Now().Add(-time.Hour),
	}
	if err := sink.Fire(context.Background(), r); err != nil {
		t.Fatalf("DeliverySink.Fire on all-email chain: %v (pre-D-B1 this returned 'no recipients reached')", err)
	}

	// Verify three rows landed (1 direct + 2 chain entries).
	var rowCount int
	row := st.DB().Raw().QueryRow(`SELECT COUNT(*) FROM email_outbound_queue WHERE status = 'queued'`)
	if err := row.Scan(&rowCount); err != nil {
		t.Fatalf("count queued rows: %v", err)
	}
	if rowCount != 3 {
		t.Errorf("expected 3 queued rows (1 sanity + 2 chain entries); got %d", rowCount)
	}

	// Spot-check from_agent + to_address fidelity on the chain rows.
	rows, err := st.DB().Raw().Query(
		`SELECT from_agent, to_address, subject FROM email_outbound_queue WHERE to_address IN (?, ?) ORDER BY to_address`,
		"leon@example.com", "ops@example.com",
	)
	if err != nil {
		t.Fatalf("query chain rows: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var seenLeon, seenOps bool
	for rows.Next() {
		var fromAgent, toAddr string
		var subject *string
		if err := rows.Scan(&fromAgent, &toAddr, &subject); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if fromAgent != "supervisor_test" {
			t.Errorf("from_agent = %q, want supervisor_test (adapter's fallback)", fromAgent)
		}
		if subject == nil || *subject == "" {
			t.Errorf("subject is empty for %s; FormatEmail should produce a non-empty subject", toAddr)
		}
		switch toAddr {
		case "leon@example.com":
			seenLeon = true
		case "ops@example.com":
			seenOps = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration error: %v", err)
	}
	if !seenLeon || !seenOps {
		t.Errorf("missing chain rows: seenLeon=%v seenOps=%v", seenLeon, seenOps)
	}
}

// noopSender satisfies reminders.MessageSender for chain tests where
// no @agent entries are present.
type noopSender struct{}

func (noopSender) SendReminder(_ context.Context, _, _, _ string) error { return nil }
