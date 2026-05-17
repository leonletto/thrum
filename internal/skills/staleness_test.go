package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/reminders"
)

// fakeStore records every Mint/Cancel call and fails t.Fatalf on any
// other Store method (the StoreSurfaceTight regression per plan AC).
type fakeStore struct {
	mu      sync.Mutex
	minted  []*reminders.Reminder
	cancels []cancelCall
	t       *testing.T
}

type cancelCall struct {
	ID string
	By string
}

func newFakeStore(t *testing.T) *fakeStore { return &fakeStore{t: t} }

func (f *fakeStore) Mint(_ context.Context, r *reminders.Reminder) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.minted = append(f.minted, r)
	return nil
}
func (f *fakeStore) Cancel(_ context.Context, id, by string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels = append(f.cancels, cancelCall{ID: id, By: by})
	return nil
}
func (f *fakeStore) Get(context.Context, string) (*reminders.Reminder, error) {
	f.t.Fatalf("forbidden Store call: Get")
	return nil, nil
}
func (f *fakeStore) List(context.Context, reminders.ListFilter) ([]*reminders.Reminder, error) {
	f.t.Fatalf("forbidden Store call: List")
	return nil, nil
}
func (f *fakeStore) OpenForAgent(context.Context, string) ([]*reminders.Reminder, error) {
	f.t.Fatalf("forbidden Store call: OpenForAgent")
	return nil, nil
}
func (f *fakeStore) Defer(context.Context, string, time.Time, string) error {
	f.t.Fatalf("forbidden Store call: Defer")
	return nil
}
func (f *fakeStore) Clear(context.Context, string, string) error {
	f.t.Fatalf("forbidden Store call: Clear")
	return nil
}
func (f *fakeStore) Fire(context.Context, string, time.Time) error {
	f.t.Fatalf("forbidden Store call: Fire")
	return nil
}
func (f *fakeStore) FireAndRearm(context.Context, string, time.Time, time.Time) error {
	f.t.Fatalf("forbidden Store call: FireAndRearm")
	return nil
}
func (f *fakeStore) DueOpen(context.Context, time.Time) ([]*reminders.Reminder, error) {
	f.t.Fatalf("forbidden Store call: DueOpen")
	return nil, nil
}
func (f *fakeStore) MintConditionForAgent(_ context.Context, _ string, _ json.RawMessage, _ []string, _ string, _ time.Time) (*reminders.Reminder, bool, error) {
	f.t.Fatalf("forbidden Store call: MintConditionForAgent")
	return nil, false, nil
}

// chainResolverFn is a tiny helper to wrap a static slice into a
// ChainResolver — keeps the test setup terse.
func chainResolverFn(agents []string, err error) ChainResolver {
	return func(_ context.Context) ([]string, error) { return agents, err }
}

func newTestStaleness(t *testing.T, store reminders.Store, resolver ChainResolver) (*Staleness, *bytes.Buffer) {
	t.Helper()
	mapPath := filepath.Join(t.TempDir(), "staleness.jsonl")
	s := NewStaleness(store, resolver, mapPath, 48*time.Hour)
	buf := &bytes.Buffer{}
	s.SetLogger(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return s, buf
}

func TestStaleness_MintCallsStore(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	s, _ := newTestStaleness(t, store, chainResolverFn([]string{"@coord1", "@coord2"}, nil))

	id, err := s.MintProposalReminder(context.Background(),
		"/repo/.thrum/agents/@alice/proposed-skills/widget/SKILL.md")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if id == "" {
		t.Fatal("empty reminderID")
	}
	if len(store.minted) != 1 {
		t.Fatalf("Store.Mint calls = %d, want 1", len(store.minted))
	}
	r := store.minted[0]
	if r.Source != reminders.SourceDaemon {
		t.Errorf("Source = %q, want daemon", r.Source)
	}
	if r.TriggerKind != reminders.TriggerTime {
		t.Errorf("TriggerKind = %q, want time", r.TriggerKind)
	}
	if r.TriggerAt == nil {
		t.Fatal("TriggerAt is nil; want a future time")
	}
	if !strings.Contains(r.Body, "widget") || !strings.Contains(r.Body, "@alice") {
		t.Errorf("Body missing skill/author: %q", r.Body)
	}
}

func TestStaleness_MintIdempotent(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	s, _ := newTestStaleness(t, store, chainResolverFn([]string{"@coord"}, nil))
	path := "/repo/.thrum/agents/@alice/proposed-skills/widget/SKILL.md"

	id1, err := s.MintProposalReminder(context.Background(), path)
	if err != nil {
		t.Fatalf("first Mint: %v", err)
	}
	id2, err := s.MintProposalReminder(context.Background(), path)
	if err != nil {
		t.Fatalf("second Mint: %v", err)
	}
	if id1 != id2 {
		t.Errorf("idempotent mint returned different IDs: %s vs %s", id1, id2)
	}
	if len(store.minted) != 1 {
		t.Errorf("Store.Mint called %d times, want 1", len(store.minted))
	}
}

func TestStaleness_CancelCallsStore(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	s, _ := newTestStaleness(t, store, chainResolverFn([]string{"@coord"}, nil))
	path := "/repo/.thrum/agents/@alice/proposed-skills/widget/SKILL.md"

	id, _ := s.MintProposalReminder(context.Background(), path)
	if err := s.CancelProposalReminder(context.Background(), path); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(store.cancels) != 1 {
		t.Fatalf("Store.Cancel calls = %d, want 1", len(store.cancels))
	}
	if store.cancels[0].ID != id {
		t.Errorf("Cancel ID = %q, want %q", store.cancels[0].ID, id)
	}
	if store.cancels[0].By == "" {
		t.Error("Cancel by-field should be non-empty audit string")
	}
}

func TestStaleness_CancelAbsentMapEntryNoOp(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	s, buf := newTestStaleness(t, store, chainResolverFn([]string{"@coord"}, nil))

	if err := s.CancelProposalReminder(context.Background(), "/no/such/path"); err != nil {
		t.Errorf("Cancel without prior Mint should be no-op; got: %v", err)
	}
	if len(store.cancels) != 0 {
		t.Errorf("Store.Cancel should NOT be called for absent entry")
	}
	if !strings.Contains(buf.String(), "cancel without mint") {
		t.Errorf("expected warn log; got: %s", buf.String())
	}
}

func TestStaleness_SidecarPersistAcrossRestart(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	mapPath := filepath.Join(t.TempDir(), "sidecar.jsonl")
	silentLogger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	s1 := NewStaleness(store, chainResolverFn([]string{"@c"}, nil), mapPath, 48*time.Hour)
	s1.SetLogger(silentLogger)
	id1, _ := s1.MintProposalReminder(context.Background(), "/p1")
	id2, _ := s1.MintProposalReminder(context.Background(), "/p2")

	// "Restart" — fresh Staleness, same sidecar path.
	s2 := NewStaleness(store, chainResolverFn([]string{"@c"}, nil), mapPath, 48*time.Hour)
	s2.SetLogger(silentLogger)
	// Re-minting the same path returns the previously-recorded ID without hitting Store.Mint.
	gotID1, err := s2.MintProposalReminder(context.Background(), "/p1")
	if err != nil {
		t.Fatalf("re-mint /p1: %v", err)
	}
	if gotID1 != id1 {
		t.Errorf("post-restart /p1 ID = %q, want %q", gotID1, id1)
	}
	gotID2, _ := s2.MintProposalReminder(context.Background(), "/p2")
	if gotID2 != id2 {
		t.Errorf("post-restart /p2 ID = %q, want %q", gotID2, id2)
	}
	if len(store.minted) != 2 {
		t.Errorf("Store.Mint calls across restart = %d, want 2 (idempotent)", len(store.minted))
	}
}

func TestStaleness_ReconcileMintsMissing(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	root := t.TempDir()
	// 3 proposals on disk.
	for _, p := range []string{"@a/w1", "@b/w2", "@c/w3"} {
		parts := strings.SplitN(p, "/", 2)
		dir := filepath.Join(root, ".thrum", "agents", parts[0], "proposed-skills", parts[1])
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\nbody"), 0o600); err != nil {
			t.Fatalf("write SKILL.md: %v", err)
		}
	}

	s, _ := newTestStaleness(t, store, chainResolverFn([]string{"@coord"}, nil))
	// Pre-mint one to simulate "1 in sidecar already".
	preminted := filepath.Join(root, ".thrum", "agents", "@a", "proposed-skills", "w1", "SKILL.md")
	if _, err := s.MintProposalReminder(context.Background(), preminted); err != nil {
		t.Fatalf("pre-mint: %v", err)
	}
	before := len(store.minted)

	if err := s.ReconcileProposals(context.Background(), root); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	added := len(store.minted) - before
	if added != 2 {
		t.Errorf("Reconcile minted %d, want 2 (the 2 missing)", added)
	}
}

func TestStaleness_TargetChainCoordinatorAgents(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	s, _ := newTestStaleness(t, store, chainResolverFn([]string{"@coord1", "@coord2"}, nil))

	if _, err := s.MintProposalReminder(context.Background(),
		"/repo/.thrum/agents/@alice/proposed-skills/widget/SKILL.md"); err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if len(store.minted) != 1 {
		t.Fatalf("Store.Mint calls = %d, want 1", len(store.minted))
	}
	chain := store.minted[0].TargetChain
	if len(chain) != 2 || chain[0] != "@coord1" || chain[1] != "@coord2" {
		t.Errorf("TargetChain = %v, want [@coord1 @coord2]", chain)
	}
}

func TestStaleness_ChainResolverEmpty(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	s, buf := newTestStaleness(t, store, chainResolverFn(nil, nil))

	id, err := s.MintProposalReminder(context.Background(),
		"/repo/.thrum/agents/@alice/proposed-skills/widget/SKILL.md")
	if err != nil {
		t.Errorf("empty chain should NOT error; got: %v", err)
	}
	if id != "" {
		t.Errorf("empty chain should return empty ID; got %q", id)
	}
	if len(store.minted) != 0 {
		t.Errorf("Store.Mint should NOT be called for empty chain; got %d", len(store.minted))
	}
	if !strings.Contains(buf.String(), "empty coordinator chain") {
		t.Errorf("expected warn log; got: %s", buf.String())
	}
}

func TestStaleness_ChainResolverError(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	resolverErr := errors.New("db down")
	s, _ := newTestStaleness(t, store, chainResolverFn(nil, resolverErr))

	_, err := s.MintProposalReminder(context.Background(),
		"/repo/.thrum/agents/@alice/proposed-skills/widget/SKILL.md")
	if err == nil {
		t.Fatal("expected error propagation from resolver")
	}
	if !errors.Is(err, resolverErr) {
		t.Errorf("err does not wrap resolverErr: %v", err)
	}
	if len(store.minted) != 0 {
		t.Errorf("Store.Mint should NOT be called on resolver error")
	}
}

// TestStaleness_StoreSurfaceTight pins the C-B1 invariant that this
// package only calls Mint and Cancel on reminders.Store. fakeStore's
// other methods t.Fatalf — exercising every staleness path here proves
// no forbidden method is reachable.
func TestStaleness_StoreSurfaceTight(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	root := t.TempDir()
	dir := filepath.Join(root, ".thrum", "agents", "@a", "proposed-skills", "wt")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: wt\n---\nbody"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s, _ := newTestStaleness(t, store, chainResolverFn([]string{"@coord"}, nil))
	// Exercise every C-B1 path: mint, cancel, reconcile.
	id, _ := s.MintProposalReminder(context.Background(), filepath.Join(dir, "SKILL.md"))
	_ = s.CancelProposalReminder(context.Background(), filepath.Join(dir, "SKILL.md"))
	_ = s.ReconcileProposals(context.Background(), root)
	// Asserts here are implicit — if any forbidden method fired,
	// fakeStore.t.Fatalf has already failed the test.
	if id == "" {
		t.Error("Mint returned empty ID")
	}
}

// TestStaleness_SidecarJSONFormat sanity-checks the on-disk format
// so consumers debugging a corrupt sidecar can read the file directly.
func TestStaleness_SidecarJSONFormat(t *testing.T) {
	t.Parallel()
	store := newFakeStore(t)
	mapPath := filepath.Join(t.TempDir(), "sidecar.jsonl")
	s := NewStaleness(store, chainResolverFn([]string{"@c"}, nil), mapPath, 48*time.Hour)
	s.SetLogger(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	_, _ = s.MintProposalReminder(context.Background(), "/p1")
	_ = s.CancelProposalReminder(context.Background(), "/p1")

	raw, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("sidecar lines = %d, want 2 (mint + tombstone); contents:\n%s", len(lines), raw)
	}
	var mintRec, cancelRec sidecarRecord
	if err := json.Unmarshal([]byte(lines[0]), &mintRec); err != nil {
		t.Fatalf("parse mint: %v", err)
	}
	if mintRec.Path != "/p1" || mintRec.ReminderID == "" || mintRec.MintedAt.IsZero() {
		t.Errorf("mint record malformed: %+v", mintRec)
	}
	if err := json.Unmarshal([]byte(lines[1]), &cancelRec); err != nil {
		t.Fatalf("parse cancel: %v", err)
	}
	if cancelRec.Path != "/p1" || cancelRec.TombstonedAt.IsZero() {
		t.Errorf("cancel record malformed: %+v", cancelRec)
	}
}
