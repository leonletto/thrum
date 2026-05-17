package email

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// CoordinatorNotifier is the abstraction over WSClient.Send so the
// rate-limiter can be unit-tested with a stub. Bridge (D-B1.14) injects
// the real WSClient adapter.
type CoordinatorNotifier interface {
	Notify(ctx context.Context, message string) error
}

// LimiterConfig holds per-peer hourly thresholds and a separate global
// inbound ceiling.
type LimiterConfig struct {
	InboundPerPeerPerHour  int
	OutboundPerPeerPerHour int
	// GlobalInboundPerMinute is a separate global ceiling. Exceeding it
	// drops the message but does NOT pause any peer — the per-peer state
	// is untouched. Useful for burst-flood defence that is not attributable
	// to a single peer.
	GlobalInboundPerMinute int
}

// counterEntry is the in-memory hot-path state for a single peer.
type counterEntry struct {
	windowStart   time.Time
	inboundCount  int
	outboundCount int
	// pausedAt is non-zero when this peer is currently rate-limited.
	// The zero value means unpaused — same semantics as SQLite's NULL.
	pausedAt time.Time
}

// globalEntry tracks the minute-scoped global inbound counter.
type globalEntry struct {
	windowStart  time.Time
	inboundCount int
}

// Limiter is an in-memory rate-limiter with SQLite persistence on hourly
// rollover. The mutex protects all fields; Increment is always a write so
// sync.Mutex beats sync.RWMutex on the hot path.
type Limiter struct {
	mu       sync.Mutex
	peers    map[string]*counterEntry
	global   globalEntry
	db       *sql.DB
	cfg      LimiterConfig
	notifier CoordinatorNotifier
}

// NewLimiter returns a Limiter ready for use. Callers must call Init before
// serving traffic to seed in-memory state from the current SQLite window.
func NewLimiter(db *sql.DB, cfg LimiterConfig, notifier CoordinatorNotifier) *Limiter {
	return &Limiter{
		peers:    make(map[string]*counterEntry),
		db:       db,
		cfg:      cfg,
		notifier: notifier,
	}
}

// IncrementInbound records one inbound message from peerKey.
// Returns (allowed, paused, err):
//   - allowed=false when the per-peer inbound threshold is reached, OR when
//     the global inbound-per-minute ceiling is reached.
//   - paused mirrors whether the peer is now paused (even if already was).
//   - When the global ceiling is hit, paused=false and per-peer state is
//     untouched.
func (l *Limiter) IncrementInbound(ctx context.Context, peerKey string) (allowed bool, paused bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Global ceiling check — minute-scoped, does not affect per-peer state.
	if exceeded := l.bumpGlobalInbound(); exceeded {
		return false, false, nil
	}

	entry := l.ensureEntry(peerKey)
	if err := l.maybeRollover(ctx, peerKey, entry); err != nil {
		return false, false, err
	}

	// If already paused, return immediately — don't bump the counter further.
	if !entry.pausedAt.IsZero() {
		return false, true, nil
	}

	entry.inboundCount++

	if entry.inboundCount >= l.cfg.InboundPerPeerPerHour {
		wasUnpaused := entry.pausedAt.IsZero()
		entry.pausedAt = time.Now().UTC()
		if err := l.persistPause(ctx, peerKey, entry); err != nil {
			return false, true, err
		}
		// Alert only fires on the transition from unpaused → paused.
		if wasUnpaused {
			l.sendAlert(ctx, peerKey, "inbound")
		}
		return false, true, nil
	}

	return true, false, nil
}

// IncrementOutbound records one outbound message to peerKey.
// Semantics mirror IncrementInbound: the global ceiling does NOT apply to
// outbound; only the per-peer outbound threshold governs.
func (l *Limiter) IncrementOutbound(ctx context.Context, peerKey string) (allowed bool, paused bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.ensureEntry(peerKey)
	if err := l.maybeRollover(ctx, peerKey, entry); err != nil {
		return false, false, err
	}

	if !entry.pausedAt.IsZero() {
		return false, true, nil
	}

	entry.outboundCount++

	if entry.outboundCount >= l.cfg.OutboundPerPeerPerHour {
		wasUnpaused := entry.pausedAt.IsZero()
		entry.pausedAt = time.Now().UTC()
		if err := l.persistPause(ctx, peerKey, entry); err != nil {
			return false, true, err
		}
		if wasUnpaused {
			l.sendAlert(ctx, peerKey, "outbound")
		}
		return false, true, nil
	}

	return true, false, nil
}

// IsPaused reports whether peerKey is currently rate-limited.
func (l *Limiter) IsPaused(ctx context.Context, peerKey string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if entry, ok := l.peers[peerKey]; ok {
		return !entry.pausedAt.IsZero(), nil
	}
	// Peer not in-memory; check SQLite directly (e.g. after a restart where
	// Init wasn't called, or for a peer we've never seen in this window).
	var pausedAt sql.NullInt64
	err := l.db.QueryRowContext(ctx,
		`SELECT paused_at FROM email_peer_rate_state WHERE peer_key = ?`,
		peerKey,
	).Scan(&pausedAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("rate-limit is-paused: %w", err)
	}
	return pausedAt.Valid, nil
}

// Unblock clears the paused state for peerKey in both in-memory and SQLite.
func (l *Limiter) Unblock(ctx context.Context, peerKey string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if entry, ok := l.peers[peerKey]; ok {
		entry.pausedAt = time.Time{}
	}
	_, err := l.db.ExecContext(ctx,
		`UPDATE email_peer_rate_state SET paused_at = NULL WHERE peer_key = ?`,
		peerKey,
	)
	if err != nil {
		return fmt.Errorf("rate-limit unblock: %w", err)
	}
	return nil
}

// PausedPeers returns the peer_key values currently flagged as paused. It
// combines the in-memory map (authoritative for this window) with SQLite for
// any peer not yet loaded into memory (e.g. after a daemon restart where Init
// was not called). Results are deduplicated; ordering is unspecified.
// Uses the idx_peer_rate_paused partial index per Guard 6.
func (l *Limiter) PausedPeers(ctx context.Context) ([]string, error) {
	l.mu.Lock()
	inMem := make(map[string]bool, len(l.peers))
	for k, e := range l.peers {
		if !e.pausedAt.IsZero() {
			inMem[k] = true
		}
	}
	l.mu.Unlock()

	dbRows, err := l.db.QueryContext(ctx,
		`SELECT peer_key FROM email_peer_rate_state WHERE paused_at IS NOT NULL`,
	)
	if err != nil {
		return nil, fmt.Errorf("rate-limit paused-peers: %w", err)
	}
	defer func() { _ = dbRows.Close() }()

	for dbRows.Next() {
		var pk string
		if err := dbRows.Scan(&pk); err != nil {
			return nil, fmt.Errorf("rate-limit paused-peers scan: %w", err)
		}
		inMem[pk] = true
	}
	if err := dbRows.Err(); err != nil {
		return nil, fmt.Errorf("rate-limit paused-peers rows: %w", err)
	}

	out := make([]string, 0, len(inMem))
	for k := range inMem {
		out = append(out, k)
	}
	return out, nil
}

// WindowRoller is a long-running goroutine that flushes and zeroes any
// in-memory windows that have aged past the hour boundary. It also catches
// windows that were never triggered by Increment (e.g. a peer that was
// paused but never sent again). ctx cancellation exits within ~1s.
func (l *Limiter) WindowRoller(ctx context.Context) error {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			l.mu.Lock()
			for peerKey, entry := range l.peers {
				if now.Sub(entry.windowStart) >= time.Hour {
					if err := l.flushAndReset(ctx, peerKey, entry, now); err != nil {
						// Log but continue; losing one flush is recoverable on
						// next rollover or daemon restart via Init.
						_ = err
					}
				}
			}
			l.mu.Unlock()
		}
	}
}

// Init seeds in-memory counters from the current-window rows in SQLite.
// Call before serving traffic so a daemon restart doesn't lose mid-window
// counts. Rows whose window_start_at is outside the current hour are
// left in SQLite as history; they are overwritten on the next rollover
// for that peer.
func (l *Limiter) Init(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now().UTC()
	currentWindowStart := now.Truncate(time.Hour)

	rows, err := l.db.QueryContext(ctx,
		`SELECT peer_key, window_start_at, inbound_count, outbound_count, paused_at
		 FROM email_peer_rate_state`,
	)
	if err != nil {
		return fmt.Errorf("rate-limit init query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var peerKey string
		var windowStartMs int64
		var inbound, outbound int
		var pausedAt sql.NullInt64

		if err := rows.Scan(&peerKey, &windowStartMs, &inbound, &outbound, &pausedAt); err != nil {
			return fmt.Errorf("rate-limit init scan: %w", err)
		}

		windowStart := time.UnixMilli(windowStartMs).UTC()
		// Only restore rows from the current hour — older rows are stale.
		// We don't delete them here; flushAndReset will overwrite them the
		// next time that peer is active.
		if windowStart.Before(currentWindowStart) {
			continue
		}

		entry := &counterEntry{
			windowStart:   windowStart,
			inboundCount:  inbound,
			outboundCount: outbound,
		}
		if pausedAt.Valid {
			entry.pausedAt = time.UnixMilli(pausedAt.Int64).UTC()
		}
		l.peers[peerKey] = entry
	}

	return rows.Err()
}

// setWindowStartForTesting is a test-only escape hatch to simulate an aged
// window without sleeping. It backdates the windowStart of peerKey so that
// the next Increment (or WindowRoller tick) sees a rollover. Unexported so
// it is not callable from outside the package by accident.
func (l *Limiter) setWindowStartForTesting(peerKey string, t time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry, ok := l.peers[peerKey]; ok {
		entry.windowStart = t
	}
}

// snapshotForTesting returns a shallow copy of the in-memory state for a
// peer. Used by tests to verify counters without triggering a SQLite flush.
func (l *Limiter) snapshotForTesting(peerKey string) (inbound, outbound int, paused bool, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, found := l.peers[peerKey]
	if !found {
		return 0, 0, false, false
	}
	return entry.inboundCount, entry.outboundCount, !entry.pausedAt.IsZero(), true
}

// --- private helpers ---

// ensureEntry returns the in-memory entry for peerKey, creating it if absent.
// Caller must hold l.mu.
func (l *Limiter) ensureEntry(peerKey string) *counterEntry {
	if entry, ok := l.peers[peerKey]; ok {
		return entry
	}
	entry := &counterEntry{
		windowStart: time.Now().UTC().Truncate(time.Hour),
	}
	l.peers[peerKey] = entry
	return entry
}

// maybeRollover flushes and resets the entry when the current hour has
// elapsed since entry.windowStart. Caller must hold l.mu.
func (l *Limiter) maybeRollover(ctx context.Context, peerKey string, entry *counterEntry) error {
	if time.Since(entry.windowStart) < time.Hour {
		return nil
	}
	return l.flushAndReset(ctx, peerKey, entry, time.Now().UTC())
}

// flushAndReset UPSERTs the current counters to SQLite, then zeroes them and
// advances windowStart to the current hour boundary. The pause state carries
// forward — a paused peer stays paused across hour boundaries until an
// explicit Unblock. Caller must hold l.mu.
func (l *Limiter) flushAndReset(ctx context.Context, peerKey string, entry *counterEntry, now time.Time) error {
	var pausedArg any
	if !entry.pausedAt.IsZero() {
		pausedArg = entry.pausedAt.UnixMilli()
	}

	_, err := l.db.ExecContext(ctx,
		`INSERT INTO email_peer_rate_state (peer_key, window_start_at, inbound_count, outbound_count, paused_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(peer_key) DO UPDATE SET
		   window_start_at = excluded.window_start_at,
		   inbound_count   = excluded.inbound_count,
		   outbound_count  = excluded.outbound_count,
		   paused_at       = excluded.paused_at`,
		peerKey,
		entry.windowStart.UnixMilli(),
		entry.inboundCount,
		entry.outboundCount,
		pausedArg,
	)
	if err != nil {
		return fmt.Errorf("rate-limit flush: %w", err)
	}

	// Reset for the new window; carry pause state forward.
	entry.windowStart = now.Truncate(time.Hour)
	entry.inboundCount = 0
	entry.outboundCount = 0
	// pausedAt intentionally left unchanged — stays paused across windows
	// until an explicit Unblock.

	return nil
}

// persistPause writes the paused_at timestamp (and the current counter
// values) to SQLite immediately — we want the paused state durable even if
// the daemon crashes before the next hourly rollover. Caller must hold l.mu.
func (l *Limiter) persistPause(ctx context.Context, peerKey string, entry *counterEntry) error {
	_, err := l.db.ExecContext(ctx,
		`INSERT INTO email_peer_rate_state (peer_key, window_start_at, inbound_count, outbound_count, paused_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(peer_key) DO UPDATE SET
		   window_start_at = excluded.window_start_at,
		   inbound_count   = excluded.inbound_count,
		   outbound_count  = excluded.outbound_count,
		   paused_at       = excluded.paused_at`,
		peerKey,
		entry.windowStart.UnixMilli(),
		entry.inboundCount,
		entry.outboundCount,
		entry.pausedAt.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("rate-limit persist-pause: %w", err)
	}
	return nil
}

// bumpGlobalInbound increments the global per-minute inbound counter and
// returns true if the ceiling is exceeded. Caller must hold l.mu.
func (l *Limiter) bumpGlobalInbound() bool {
	now := time.Now().UTC()
	if now.Sub(l.global.windowStart) >= time.Minute {
		l.global.windowStart = now.Truncate(time.Minute)
		l.global.inboundCount = 0
	}
	l.global.inboundCount++
	return l.global.inboundCount > l.cfg.GlobalInboundPerMinute
}

// sendAlert fires a coordinator notification on the first pause transition.
// Caller must hold l.mu. If notifier is nil the alert is silently skipped
// (test scenarios that don't care about alerts).
func (l *Limiter) sendAlert(ctx context.Context, peerKey, direction string) {
	if l.notifier == nil {
		return
	}
	msg := fmt.Sprintf("@coordinator_main: email peer %s rate-limited (%s)", peerKey, direction)
	// Alert is best-effort. A failure here must not block the caller — a
	// broken WebSocket should never pause message ingestion.
	_ = l.notifier.Notify(ctx, msg)
}
