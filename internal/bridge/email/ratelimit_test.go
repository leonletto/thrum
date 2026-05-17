package email

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/schema"
)

// recordingNotifier captures Notify calls for assertion in coordinator-alert
// tests. Thread-safe so concurrent tests don't race on msgs.
type recordingNotifier struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recordingNotifier) Notify(_ context.Context, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, message)
	return nil
}

func (r *recordingNotifier) Messages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.msgs))
	copy(out, r.msgs)
	return out
}

// newTestLimiter returns a Limiter with tight per-peer thresholds suitable
// for unit tests (threshold=3 means the 3rd call causes a pause).
func newTestLimiter(t *testing.T, notifier CoordinatorNotifier) *Limiter {
	t.Helper()
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "rl.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("init test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := LimiterConfig{
		InboundPerPeerPerHour:  3,
		OutboundPerPeerPerHour: 3,
		GlobalInboundPerMinute: 100,
	}
	return NewLimiter(db, cfg, notifier)
}

func TestRateLimit_InboundIncrementAllowed(t *testing.T) {
	l := newTestLimiter(t, nil)
	ctx := context.Background()

	allowed, paused, err := l.IncrementInbound(ctx, "peer@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected allowed=true for first increment below threshold")
	}
	if paused {
		t.Error("expected paused=false for first increment")
	}

	in, _, isPaused, ok := l.snapshotForTesting("peer@example.com")
	if !ok {
		t.Fatal("peer not found in in-memory map")
	}
	if in != 1 {
		t.Errorf("inbound_count: got %d want 1", in)
	}
	if isPaused {
		t.Error("peer should not be paused after one increment")
	}
}

func TestRateLimit_InboundExceedThresholdPauses(t *testing.T) {
	l := newTestLimiter(t, nil)
	ctx := context.Background()

	// Two increments below threshold (cfg.InboundPerPeerPerHour = 3).
	for i := 0; i < 2; i++ {
		if allowed, _, err := l.IncrementInbound(ctx, "p"); err != nil || !allowed {
			t.Fatalf("increment %d: allowed=%v err=%v", i+1, allowed, err)
		}
	}

	// Third increment hits the threshold.
	allowed, paused, err := l.IncrementInbound(ctx, "p")
	if err != nil {
		t.Fatalf("threshold increment error: %v", err)
	}
	if allowed {
		t.Error("expected allowed=false at threshold")
	}
	if !paused {
		t.Error("expected paused=true at threshold")
	}

	// Subsequent call: still paused, allowed=false.
	allowed2, paused2, err := l.IncrementInbound(ctx, "p")
	if err != nil {
		t.Fatalf("post-pause increment error: %v", err)
	}
	if allowed2 {
		t.Error("expected allowed=false when already paused")
	}
	if !paused2 {
		t.Error("expected paused=true when already paused")
	}
}

func TestRateLimit_OutboundExceedThresholdPauses(t *testing.T) {
	l := newTestLimiter(t, nil)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if allowed, _, err := l.IncrementOutbound(ctx, "p"); err != nil || !allowed {
			t.Fatalf("increment %d: allowed=%v err=%v", i+1, allowed, err)
		}
	}

	allowed, paused, err := l.IncrementOutbound(ctx, "p")
	if err != nil {
		t.Fatalf("threshold increment error: %v", err)
	}
	if allowed {
		t.Error("expected allowed=false at outbound threshold")
	}
	if !paused {
		t.Error("expected paused=true at outbound threshold")
	}
}

func TestRateLimit_DirectionalCountersIndependent(t *testing.T) {
	l := newTestLimiter(t, nil)
	ctx := context.Background()

	// Drive inbound to threshold.
	for i := 0; i < 2; i++ {
		_, _, _ = l.IncrementInbound(ctx, "p")
	}
	l.IncrementInbound(ctx, "p") //nolint:errcheck // causes pause

	// Unblock so we can inspect pure counter state, then check outbound is 0.
	_ = l.Unblock(ctx, "p")

	_, out, _, _ := l.snapshotForTesting("p")
	if out != 0 {
		t.Errorf("outbound_count should be 0 after inbound-only increments, got %d", out)
	}

	// Now drive outbound — inbound count should stay as-is.
	l2 := newTestLimiter(t, nil)
	for i := 0; i < 2; i++ {
		_, _, _ = l2.IncrementOutbound(ctx, "q")
	}
	in2, _, _, _ := l2.snapshotForTesting("q")
	if in2 != 0 {
		t.Errorf("inbound_count should be 0 after outbound-only increments, got %d", in2)
	}
}

func TestRateLimit_HourlyRolloverFlushesAndZeroes(t *testing.T) {
	l := newTestLimiter(t, nil)
	ctx := context.Background()

	// Seed the peer with two increments.
	_, _, _ = l.IncrementInbound(ctx, "p")
	_, _, _ = l.IncrementInbound(ctx, "p")

	in, _, _, _ := l.snapshotForTesting("p")
	if in != 2 {
		t.Fatalf("pre-rollover inbound count: got %d want 2", in)
	}

	// Backdate the windowStart by 2 hours to simulate an aged window.
	l.setWindowStartForTesting("p", time.Now().UTC().Add(-2*time.Hour))

	// The next increment triggers flushAndReset internally.
	allowed, _, err := l.IncrementInbound(ctx, "p")
	if err != nil {
		t.Fatalf("post-backdate increment error: %v", err)
	}
	if !allowed {
		t.Error("expected allowed=true after rollover (new window)")
	}

	in2, _, _, _ := l.snapshotForTesting("p")
	// After rollover the old counters reset; the new increment counts as 1.
	if in2 != 1 {
		t.Errorf("post-rollover inbound count: got %d want 1", in2)
	}

	// Verify the old window was flushed to SQLite. flushAndReset writes the
	// old window's counters (2) to SQLite and then resets in-memory to 0.
	// The subsequent IncrementInbound bumps the NEW window to 1 in-memory,
	// but that new-window count is NOT yet persisted (no pause occurred, no
	// second rollover). So the SQLite row reflects the flushed old-window
	// count (2), not the current in-memory count (1).
	var cnt int
	err = l.db.QueryRowContext(ctx,
		`SELECT inbound_count FROM email_peer_rate_state WHERE peer_key = 'p'`,
	).Scan(&cnt)
	if err != nil {
		t.Fatalf("sqlite query after rollover: %v", err)
	}
	// flushAndReset persists the old window's value (2); the new window's
	// in-memory state (1) is only durably written on the next rollover or pause.
	if cnt != 2 {
		t.Errorf("sqlite inbound_count after rollover flush: got %d want 2 (old window value)", cnt)
	}
}

func TestRateLimit_RestartRecoverySeedsFromSqlite(t *testing.T) {
	ctx := context.Background()
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "rl.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("init db: %v", err)
	}

	cfg := LimiterConfig{
		InboundPerPeerPerHour:  10,
		OutboundPerPeerPerHour: 10,
		GlobalInboundPerMinute: 100,
	}

	// Pre-populate a row in the current hour.
	now := time.Now().UTC()
	windowStart := now.Truncate(time.Hour)
	_, err = db.ExecContext(ctx,
		`INSERT INTO email_peer_rate_state (peer_key, window_start_at, inbound_count, outbound_count, paused_at)
		 VALUES ('alice@example.com', ?, 5, 2, NULL)`,
		windowStart.UnixMilli(),
	)
	if err != nil {
		t.Fatalf("pre-populate: %v", err)
	}

	l := NewLimiter(db, cfg, nil)
	if err := l.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	in, out, paused, ok := l.snapshotForTesting("alice@example.com")
	if !ok {
		t.Fatal("peer not found after Init")
	}
	if in != 5 {
		t.Errorf("inbound_count: got %d want 5", in)
	}
	if out != 2 {
		t.Errorf("outbound_count: got %d want 2", out)
	}
	if paused {
		t.Error("peer should not be paused")
	}
}

func TestRateLimit_UnblockClearsPausedAt(t *testing.T) {
	l := newTestLimiter(t, nil)
	ctx := context.Background()

	// Drive to pause.
	for i := 0; i < 3; i++ {
		_, _, _ = l.IncrementInbound(ctx, "p")
	}
	if ok, _ := l.IsPaused(ctx, "p"); !ok {
		t.Fatal("peer should be paused after threshold")
	}

	if err := l.Unblock(ctx, "p"); err != nil {
		t.Fatalf("Unblock: %v", err)
	}

	// In-memory state.
	_, _, paused, _ := l.snapshotForTesting("p")
	if paused {
		t.Error("in-memory pausedAt should be cleared after Unblock")
	}

	// SQLite state.
	var pausedAt *int64
	err := l.db.QueryRowContext(ctx,
		`SELECT paused_at FROM email_peer_rate_state WHERE peer_key = 'p'`,
	).Scan(&pausedAt)
	if err != nil {
		t.Fatalf("sqlite query: %v", err)
	}
	if pausedAt != nil {
		t.Error("sqlite paused_at should be NULL after Unblock")
	}
}

func TestRateLimit_PausedPeersQueryUsesPartialIndex(t *testing.T) {
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "rl.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("init db: %v", err)
	}

	rows, err := db.Query(
		`EXPLAIN QUERY PLAN SELECT peer_key FROM email_peer_rate_state WHERE paused_at IS NOT NULL`,
	)
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer rows.Close()

	var found bool
	for rows.Next() {
		// EXPLAIN QUERY PLAN returns (id, parent, notused, detail) in SQLite.
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan explain row: %v", err)
		}
		if strings.Contains(detail, "idx_peer_rate_paused") {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("explain rows error: %v", err)
	}
	if !found {
		t.Error("EXPLAIN QUERY PLAN did not reference idx_peer_rate_paused; partial index not used")
	}
}

func TestRateLimit_GlobalCeilingDropsWithoutPause(t *testing.T) {
	db, err := schema.OpenDB(filepath.Join(t.TempDir(), "rl.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if err := schema.InitDB(db); err != nil {
		t.Fatalf("init db: %v", err)
	}

	// Global ceiling = 2 so the third call triggers it.
	cfg := LimiterConfig{
		InboundPerPeerPerHour:  1000,
		OutboundPerPeerPerHour: 1000,
		GlobalInboundPerMinute: 2,
	}
	l := NewLimiter(db, cfg, nil)
	ctx := context.Background()

	_, _, _ = l.IncrementInbound(ctx, "a")
	_, _, _ = l.IncrementInbound(ctx, "b")

	// Third call exceeds global ceiling.
	allowed, paused, err := l.IncrementInbound(ctx, "c")
	if err != nil {
		t.Fatalf("global ceiling increment error: %v", err)
	}
	if allowed {
		t.Error("expected allowed=false when global ceiling exceeded")
	}
	if paused {
		t.Error("expected paused=false (per-peer state untouched on global ceiling)")
	}

	// Per-peer state for "c" should not exist (never got past global check).
	_, _, isPaused, ok := l.snapshotForTesting("c")
	if ok && isPaused {
		t.Error("peer 'c' should not be paused from global ceiling")
	}
}

func TestRateLimit_CoordinatorAlertOnPause(t *testing.T) {
	n := &recordingNotifier{}
	l := newTestLimiter(t, n)
	ctx := context.Background()

	// Threshold = 3: drive peer to pause.
	for i := 0; i < 3; i++ {
		_, _, _ = l.IncrementInbound(ctx, "alert-peer@x.com")
	}

	msgs := n.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 coordinator alert, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "alert-peer@x.com") {
		t.Errorf("alert message should contain peer key; got: %q", msgs[0])
	}

	// Additional increments must NOT fire more alerts.
	l.IncrementInbound(ctx, "alert-peer@x.com") //nolint:errcheck
	l.IncrementInbound(ctx, "alert-peer@x.com") //nolint:errcheck

	if got := len(n.Messages()); got != 1 {
		t.Errorf("expected exactly 1 alert total, got %d", got)
	}
}

func TestRateLimit_ConcurrentIncrementsAtomic(t *testing.T) {
	l := newTestLimiter(t, nil)
	// Use a threshold high enough that 100 goroutines never pause.
	l.cfg.InboundPerPeerPerHour = 10000
	ctx := context.Background()

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, _, _ = l.IncrementInbound(ctx, "shared-peer")
		}()
	}
	wg.Wait()

	in, _, _, ok := l.snapshotForTesting("shared-peer")
	if !ok {
		t.Fatal("peer not found after concurrent increments")
	}
	if in != n {
		t.Errorf("concurrent inbound_count: got %d want %d (lost updates)", in, n)
	}
}
