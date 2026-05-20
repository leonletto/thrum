package pending_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/sync/pending"
)

// ---------------------------------------------------------------------------
// Telemetry test helpers
// ---------------------------------------------------------------------------

// capturingHandler records all slog.Record values emitted to it.
type capturingHandler struct {
	records []slog.Record
	mu      sync.Mutex
}

func (h *capturingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}
func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

// recordsWithMessage returns all captured records whose Message equals msg.
func (h *capturingHandler) recordsWithMessage(msg string) []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.records {
		if r.Message == msg {
			out = append(out, r)
		}
	}
	return out
}

// attrValue returns the string value of a named attribute in a slog.Record,
// or "" if not found.
func attrValue(r slog.Record, key string) string {
	var val string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			val = a.Value.String()
			return false
		}
		return true
	})
	return val
}

// attrInt64 returns the int64 value of a named attribute in a slog.Record,
// or -1 if not found.
func attrInt64(r slog.Record, key string) int64 {
	val := int64(-1)
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			val = a.Value.Int64()
			return false
		}
		return true
	})
	return val
}

// stubResolver is a test double for the Resolver interface.
type stubResolver struct {
	mu      sync.Mutex
	results map[string]resolveResult
}

type resolveResult struct {
	ok  bool
	err error
}

func newStubResolver() *stubResolver {
	return &stubResolver{results: make(map[string]resolveResult)}
}

// setResult configures the stub to return (ok, err) for the given messageID.
func (s *stubResolver) setResult(messageID string, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[messageID] = resolveResult{ok: ok, err: err}
}

func (s *stubResolver) Resolve(_ context.Context, msg pending.OrphanedMessage) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.results[msg.MessageID]; ok {
		return r.ok, r.err
	}
	return false, nil
}

func makeOrphan(msgID string, blockedBy ...string) pending.OrphanedMessage {
	return pending.OrphanedMessage{
		MessageID:  msgID,
		AuthorID:   "agt:author",
		Recipients: []string{"agt:recipient"},
		BlockedBy:  blockedBy,
		LandedAt:   time.Now(),
	}
}

// T-pending-1: Add an orphan; Size() returns 1; ResolveOnStateLand with
// unrelated IDs leaves the orphan.
func TestPool_Add_TracksOrphan(t *testing.T) {
	t.Parallel()
	p := pending.New()
	if got := p.Size(); got != 0 {
		t.Fatalf("expected empty pool, got Size()=%d", got)
	}

	p.Add(makeOrphan("msg-1", "tg:foo"))
	if got := p.Size(); got != 1 {
		t.Fatalf("after Add, expected Size()=1, got %d", got)
	}

	// ResolveOnStateLand with unrelated IDs — orphan must stay.
	resolver := newStubResolver()
	n := p.ResolveOnStateLand(context.Background(), []string{"tg:unrelated"}, resolver)
	if n != 0 {
		t.Fatalf("expected 0 resolved, got %d", n)
	}
	if got := p.Size(); got != 1 {
		t.Fatalf("orphan should remain; expected Size()=1, got %d", got)
	}
}

// T7 unit slice: add orphan blocked by ["tg:foo"]; call ResolveOnStateLand
// with ["tg:foo"] and a stub resolver that returns (true, nil); orphan is
// removed; Size() returns 0; return value is 1.
func TestPool_ResolveOnStateLand_MatchingIDsResolves(t *testing.T) {
	t.Parallel()
	p := pending.New()
	p.Add(makeOrphan("msg-2", "tg:foo"))

	resolver := newStubResolver()
	resolver.setResult("msg-2", true, nil)

	n := p.ResolveOnStateLand(context.Background(), []string{"tg:foo"}, resolver)
	if n != 1 {
		t.Fatalf("expected 1 resolved, got %d", n)
	}
	if got := p.Size(); got != 0 {
		t.Fatalf("expected pool empty after resolve, got Size()=%d", got)
	}
}

// TestPool_ResolveOnStateLand_PartialBlockedBy: orphan blocked by
// ["tg:foo", "agt:bar"]; ResolveOnStateLand with only ["tg:foo"]; resolver
// returns (false, nil) because "agt:bar" is still missing; orphan NOT removed.
func TestPool_ResolveOnStateLand_PartialBlockedBy(t *testing.T) {
	t.Parallel()
	p := pending.New()
	p.Add(makeOrphan("msg-3", "tg:foo", "agt:bar"))

	// Resolver decides the orphan is still not fully resolvable; it is the
	// source of truth here, not the pool.
	resolver := newStubResolver()
	resolver.setResult("msg-3", false, nil)

	n := p.ResolveOnStateLand(context.Background(), []string{"tg:foo"}, resolver)
	if n != 0 {
		t.Fatalf("expected 0 resolved (partial block still open), got %d", n)
	}
	if got := p.Size(); got != 1 {
		t.Fatalf("orphan should remain; expected Size()=1, got %d", got)
	}
}

// TestPool_ResolveOnStateLand_ResolverError_KeepsOrphan: stub Resolver returns
// (false, error); orphan stays in pool; no panic.
func TestPool_ResolveOnStateLand_ResolverError_KeepsOrphan(t *testing.T) {
	t.Parallel()
	p := pending.New()
	p.Add(makeOrphan("msg-4", "tg:baz"))

	resolver := newStubResolver()
	resolver.setResult("msg-4", false, errors.New("transient error"))

	n := p.ResolveOnStateLand(context.Background(), []string{"tg:baz"}, resolver)
	if n != 0 {
		t.Fatalf("expected 0 resolved on error, got %d", n)
	}
	if got := p.Size(); got != 1 {
		t.Fatalf("orphan should remain on error; expected Size()=1, got %d", got)
	}
}

// TestPool_ConcurrentAddAndResolve_RaceFree: multiple goroutines alternating
// Add / ResolveOnStateLand. The -race detector validates mutex correctness.
func TestPool_ConcurrentAddAndResolve_RaceFree(t *testing.T) {
	t.Parallel()
	p := pending.New()

	const N = 50
	var wg sync.WaitGroup

	// Always-success resolver.
	resolver := newStubResolver()

	for i := 0; i < N; i++ {
		wg.Add(2)
		msgID := "concurrent-msg-" + string(rune('A'+i%26))

		go func(id string) {
			defer wg.Done()
			p.Add(makeOrphan(id, "tg:race"))
		}(msgID)

		go func(id string) {
			defer wg.Done()
			resolver.setResult(id, true, nil)
			p.ResolveOnStateLand(context.Background(), []string{"tg:race"}, resolver)
		}(msgID)
	}

	wg.Wait()
	// After all goroutines finish the pool may or may not be empty depending
	// on scheduling; we only need it to be race-free (enforced by -race).
	_ = p.Size()
}

// TestPool_Add_Idempotent: adding the same message ID twice does not double-count.
func TestPool_Add_Idempotent(t *testing.T) {
	t.Parallel()
	p := pending.New()
	o := makeOrphan("msg-5", "tg:foo")
	p.Add(o)
	p.Add(o)
	if got := p.Size(); got != 1 {
		t.Fatalf("duplicate Add should be idempotent; expected Size()=1, got %d", got)
	}
}

// TestPool_List_ReturnsSnapshot covers the diagnostics surface used by
// the sync.pending_pool.list RPC (Task 13 / thrum-s6os.9). The returned
// slice is a snapshot copy; mutating the pool after List() must not
// affect the previously-returned slice.
func TestPool_List_ReturnsSnapshot(t *testing.T) {
	t.Parallel()
	p := pending.New()
	p.Add(makeOrphan("msg-A", "tg:foo"))
	p.Add(makeOrphan("msg-B", "agt:bar"))

	got := p.List()
	if len(got) != 2 {
		t.Fatalf("expected 2 orphans, got %d", len(got))
	}

	// Snapshot must be independent of subsequent pool mutations.
	p.Add(makeOrphan("msg-C", "tg:baz"))
	if len(got) != 2 {
		t.Errorf("List() result must not reflect post-call mutations; got %d", len(got))
	}
	if newGot := p.List(); len(newGot) != 3 {
		t.Errorf("subsequent List() should see new orphan; got %d", len(newGot))
	}
}

// TestPool_List_EmptyPool returns an empty (non-nil) slice — the
// RPC handler can JSON-marshal it as [] rather than null.
func TestPool_List_EmptyPool(t *testing.T) {
	t.Parallel()
	p := pending.New()
	got := p.List()
	if got == nil {
		t.Error("List() on empty pool should return non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected empty list, got %d entries", len(got))
	}
}

// ---------------------------------------------------------------------------
// E8 Telemetry tests
// ---------------------------------------------------------------------------

// TestPool_Add_Emits_PendingPoolAdded verifies that Pool.Add emits a
// "pending_pool.added" slog.Info event with the correct message_id and
// blocked_by fields. Also verifies no event fires when Add is idempotent.
func TestPool_Add_Emits_PendingPoolAdded(t *testing.T) {
	h := &capturingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)

	p := pending.New()
	o := makeOrphan("msg-tel-1", "tg:grp1", "agt:bar")
	p.Add(o)

	recs := h.recordsWithMessage("pending_pool.added")
	if len(recs) != 1 {
		t.Fatalf("expected 1 pending_pool.added record, got %d", len(recs))
	}
	r := recs[0]
	if got := attrValue(r, "message_id"); got != "msg-tel-1" {
		t.Errorf("message_id = %q, want %q", got, "msg-tel-1")
	}
	// blocked_by is a []string; slog renders it as a string attr value.
	blocked := attrValue(r, "blocked_by")
	if blocked == "" {
		t.Error("blocked_by attr should be non-empty")
	}

	// Second Add of the same orphan must be a no-op (idempotent) — no new event.
	p.Add(o)
	recs2 := h.recordsWithMessage("pending_pool.added")
	if len(recs2) != 1 {
		t.Errorf("idempotent Add must not re-emit; got %d events total", len(recs2))
	}
}

// TestPool_ResolveOnStateLand_Emits_PendingPoolResolved verifies that
// "pending_pool.resolved" fires with the correct message_id and a non-negative
// wait_ms when an orphan is successfully resolved.
func TestPool_ResolveOnStateLand_Emits_PendingPoolResolved(t *testing.T) {
	h := &capturingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)

	p := pending.New()
	o := pending.OrphanedMessage{
		MessageID:  "msg-tel-2",
		AuthorID:   "agt:author",
		Recipients: []string{"agt:recipient"},
		BlockedBy:  []string{"tg:foo"},
		LandedAt:   time.Now(),
	}
	p.Add(o)

	resolver := newStubResolver()
	resolver.setResult("msg-tel-2", true, nil)

	n := p.ResolveOnStateLand(context.Background(), []string{"tg:foo"}, resolver)
	if n != 1 {
		t.Fatalf("expected 1 resolved, got %d", n)
	}

	recs := h.recordsWithMessage("pending_pool.resolved")
	if len(recs) != 1 {
		t.Fatalf("expected 1 pending_pool.resolved record, got %d", len(recs))
	}
	r := recs[0]
	if got := attrValue(r, "message_id"); got != "msg-tel-2" {
		t.Errorf("message_id = %q, want %q", got, "msg-tel-2")
	}
	waitMS := attrInt64(r, "wait_ms")
	if waitMS < 0 {
		t.Errorf("wait_ms = %d, want >= 0", waitMS)
	}
}

// TestPool_ResolveOnStateLand_NoEvent_WhenNotResolved verifies that
// "pending_pool.resolved" does NOT fire when the resolver returns (false, nil).
func TestPool_ResolveOnStateLand_NoEvent_WhenNotResolved(t *testing.T) {
	h := &capturingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)

	p := pending.New()
	p.Add(makeOrphan("msg-tel-3", "tg:baz"))

	resolver := newStubResolver()
	resolver.setResult("msg-tel-3", false, nil)

	p.ResolveOnStateLand(context.Background(), []string{"tg:baz"}, resolver)

	recs := h.recordsWithMessage("pending_pool.resolved")
	if len(recs) != 0 {
		t.Errorf("expected 0 pending_pool.resolved events when resolver returns false, got %d", len(recs))
	}
}
