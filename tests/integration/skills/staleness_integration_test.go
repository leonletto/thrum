//go:build integration

// Package skills staleness_integration_test wires the C-B1
// Staleness type to the real A-B4 reminders.Store + Dispatcher
// substrate. Unit tests in internal/skills/staleness_test.go drive
// Staleness against a recording-stub Store; this file proves the
// integration seam called out verbatim in plan E10.9 step 8:
//
//	"Mint via reminders.Store.Mint; advance time via
//	 dispatcher.Tick(ctx, base.Add(49*time.Hour)); assert the reminder
//	 fires through to a fake FireSink."
//
// The 49h advance puts the synthetic clock 1h past the 48h default
// `skills.pending_reminder_after` (canonical §4.4). Tests also cover
// the cancel-before-tick path (real Store row transitions to
// 'cancelled', Dispatcher's DueOpen scan filters it out) and the
// boot-time ReconcileProposals path (walks .thrum/agents/*/proposed-
// skills/* and mints any missing reminders).
package skills

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/leonletto/thrum/internal/daemon/reminders"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/schema"
	"github.com/leonletto/thrum/internal/skills"
)

// stalenessFakeFireSink records every Fire call so the test can
// assert on the reminder IDs that the Dispatcher delivered. Mirrors
// the in-package fakeFireSink in internal/daemon/reminders/dispatcher_test.go
// — re-declared here because the production type is package-internal
// to reminders.
type stalenessFakeFireSink struct {
	calls []*reminders.Reminder
}

func (f *stalenessFakeFireSink) Fire(_ context.Context, r *reminders.Reminder) error {
	f.calls = append(f.calls, r)
	return nil
}

// stalenessRearmNoop satisfies reminders.ReArmPolicy. C-B1 minted
// reminders are time-triggered (one-shot), so the re-arm path is
// never invoked — but Dispatcher.New requires non-nil.
type stalenessRearmNoop struct{}

func (stalenessRearmNoop) NextAfter(_ *reminders.Reminder, fired time.Time) time.Time {
	return fired
}

// stalenessFixture wires every real component the integration seam
// depends on. The Dispatcher's three collaborators (Store, FireSink,
// RearmPolicy) are constructed against the SAME SQLite-backed Store
// the Staleness writes to — that single-Store guarantee is what makes
// "Mint via Staleness → Tick via Dispatcher" prove the integration.
type stalenessFixture struct {
	t           *testing.T
	repoRoot    string
	sidecarPath string
	rawDB       *sql.DB
	store       *reminders.SQLStore
	staleness   *skills.Staleness
	dispatcher  *reminders.Dispatcher
	sink        *stalenessFakeFireSink
	clock       time.Time
}

func newStalenessFixture(t *testing.T) *stalenessFixture {
	t.Helper()
	repoRoot := t.TempDir()
	mustMkdir(t, filepath.Join(repoRoot, ".thrum", "state"))
	mustMkdir(t, filepath.Join(repoRoot, ".thrum", "agents"))

	dbPath := filepath.Join(t.TempDir(), "staleness_test.db")
	rawDB, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("schema.OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	if err := schema.InitDB(rawDB); err != nil {
		t.Fatalf("schema.InitDB: %v", err)
	}

	store := reminders.NewSQLStore(safedb.New(rawDB))
	sidecarPath := filepath.Join(repoRoot, ".thrum", "state", "skill-proposal-reminders.jsonl")

	resolver := func(_ context.Context) ([]string, error) {
		return []string{"@coordinator_main"}, nil
	}
	// pendingAfter pinned to canonical §4.4 default (48h). Tests
	// advance via Tick(now + 49h) to put the synthetic clock 1h past
	// the trigger.
	staleness := skills.NewStaleness(store, resolver, sidecarPath, 48*time.Hour)

	sink := &stalenessFakeFireSink{}
	dispatcher := reminders.NewDispatcher(store, sink, stalenessRearmNoop{})

	return &stalenessFixture{
		t:           t,
		repoRoot:    repoRoot,
		sidecarPath: sidecarPath,
		rawDB:       rawDB,
		store:       store,
		staleness:   staleness,
		dispatcher:  dispatcher,
		sink:        sink,
		clock:       time.Now().UTC(),
	}
}

// writeProposalFile creates a SKILL.md under .thrum/agents/<author>/
// proposed-skills/<name>/ so MintProposalReminder + ReconcileProposals
// can derive (author, name) from the path. Frontmatter content is
// intentionally minimal — the staleness path doesn't validate it.
func (f *stalenessFixture) writeProposalFile(author, name string) string {
	f.t.Helper()
	rel := filepath.Join(".thrum", "agents", author, "proposed-skills", name, "SKILL.md")
	abs := filepath.Join(f.repoRoot, rel)
	mustMkdir(f.t, filepath.Dir(abs))
	content := "---\nname: " + name + "\ndescription: test\n---\nbody\n"
	if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
		f.t.Fatalf("write proposal: %v", err)
	}
	return abs
}

func TestStalenessIntegration_MintTickFiresThroughFakeSink(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"))
	f := newStalenessFixture(t)

	path := f.writeProposalFile("@alice", "widget")
	reminderID, err := f.staleness.MintProposalReminder(context.Background(), path)
	if err != nil {
		t.Fatalf("MintProposalReminder: %v", err)
	}
	if reminderID == "" {
		t.Fatal("Mint returned empty reminder ID")
	}

	// Pre-tick: the reminder is open, not yet fired.
	pre, err := f.store.Get(context.Background(), reminderID)
	if err != nil {
		t.Fatalf("Store.Get pre-tick: %v", err)
	}
	if pre.State != reminders.StateOpen {
		t.Fatalf("pre-tick reminder.State = %q, want open", pre.State)
	}
	if len(f.sink.calls) != 0 {
		t.Fatalf("pre-tick sink calls = %d, want 0", len(f.sink.calls))
	}

	// Advance the dispatcher's clock 49h forward (1h past the 48h
	// trigger). The plan E10.9 step 8 prescription verbatim.
	future := f.clock.Add(49 * time.Hour)
	if err := f.dispatcher.Tick(context.Background(), future); err != nil {
		t.Fatalf("Dispatcher.Tick: %v", err)
	}

	// FireSink must have received the reminder.
	if len(f.sink.calls) != 1 {
		t.Fatalf("post-tick sink calls = %d, want 1", len(f.sink.calls))
	}
	if f.sink.calls[0].ID != reminderID {
		t.Errorf("fired reminder ID = %q, want %q", f.sink.calls[0].ID, reminderID)
	}
	// Real Store must have transitioned to 'fired'.
	post, err := f.store.Get(context.Background(), reminderID)
	if err != nil {
		t.Fatalf("Store.Get post-tick: %v", err)
	}
	if post.State != reminders.StateFired {
		t.Errorf("post-tick reminder.State = %q, want fired", post.State)
	}
}

func TestStalenessIntegration_CancelBeforeTickPreventsFire(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"))
	f := newStalenessFixture(t)

	path := f.writeProposalFile("@alice", "widget")
	reminderID, err := f.staleness.MintProposalReminder(context.Background(), path)
	if err != nil {
		t.Fatalf("MintProposalReminder: %v", err)
	}

	// Cancel via Staleness BEFORE advancing the dispatcher clock.
	if err := f.staleness.CancelProposalReminder(context.Background(), path); err != nil {
		t.Fatalf("CancelProposalReminder: %v", err)
	}

	// Real Store row must be in 'cancelled' immediately after Cancel.
	post, err := f.store.Get(context.Background(), reminderID)
	if err != nil {
		t.Fatalf("Store.Get post-cancel: %v", err)
	}
	if post.State != reminders.StateCancelled {
		t.Errorf("post-cancel reminder.State = %q, want cancelled", post.State)
	}

	// Tick well past the trigger.
	future := f.clock.Add(49 * time.Hour)
	if err := f.dispatcher.Tick(context.Background(), future); err != nil {
		t.Fatalf("Dispatcher.Tick: %v", err)
	}

	// FireSink must have received NOTHING — DueOpen filters on
	// state='open', so cancelled rows are invisible to the dispatcher.
	if len(f.sink.calls) != 0 {
		t.Errorf("post-tick sink calls = %d, want 0 (cancelled before tick)", len(f.sink.calls))
	}
}

func TestStalenessIntegration_ReconcileMintsMissingAndDispatcherFiresThem(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"))
	f := newStalenessFixture(t)

	// Pre-seed two proposals on disk. The sidecar is empty — neither
	// has a reminder yet. This is the daemon-boot drift case:
	// proposals landed while the daemon was down.
	f.writeProposalFile("@alice", "widget")
	f.writeProposalFile("@bob", "gadget")

	if err := f.staleness.ReconcileProposals(context.Background(), f.repoRoot); err != nil {
		t.Fatalf("ReconcileProposals: %v", err)
	}

	// Both reminders must now be in the Store, both 'open', neither
	// fired yet.
	openState := reminders.StateOpen
	open, err := f.store.List(context.Background(), reminders.ListFilter{State: &openState})
	if err != nil {
		t.Fatalf("Store.List: %v", err)
	}
	if len(open) != 2 {
		t.Fatalf("open reminders after reconcile = %d, want 2", len(open))
	}

	// Dispatcher tick well past the trigger fires both.
	future := f.clock.Add(49 * time.Hour)
	if err := f.dispatcher.Tick(context.Background(), future); err != nil {
		t.Fatalf("Dispatcher.Tick: %v", err)
	}
	if len(f.sink.calls) != 2 {
		t.Errorf("post-tick sink calls = %d, want 2", len(f.sink.calls))
	}
}
