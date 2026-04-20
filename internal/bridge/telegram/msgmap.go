package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/leonletto/thrum/internal/bridge"
)

// DefaultMapTTL is the default retention window for telegram_msg_map
// rows used by the periodic sweep (thrum-48kt.6). 30 days is an
// intentional over-estimate: a Telegram↔Thrum mapping is only
// functionally needed for the lifetime of its pending supervisor
// nudge (seconds to hours), so the TTL is a backstop that catches
// rows abandoned by daemon crashes or bridge reconfigs. The
// pending-nudge cross-check inside SweepStale means a row that IS
// still live survives regardless of age — TTL is just the cadence
// of the orphan reap, not a hard expiry on live state.
const DefaultMapTTL = 30 * 24 * time.Hour

// DefaultSweepInterval is the cadence between periodic sweeps once
// the daemon has started. A 24h interval matches the coordinator's
// dispatch ("once at daemon start + every 24h") and is small relative
// to the TTL so no row lingers for more than TTL + 24h past its
// creation in steady state.
const DefaultSweepInterval = 24 * time.Hour

// MessageMap wraps the shared bridge.MessageMap with Telegram-specific
// int64/int key formatting, and optionally persists entries to SQLite
// so Telegram↔Thrum mappings survive a daemon restart.
//
// # Why the SQLite layer exists (thrum-48kt.2)
//
// The in-memory LRU is ephemeral. When the daemon restarts between
// sending a permission nudge to Telegram and receiving the supervisor's
// reply, the mapping is lost. The inbound relay then silently drops
// reply_to routing (inbound.go — ThrumID returns !ok); the reply lands
// as a top-level DM with no reply_to ref and TryResolve never fires.
// The nudge becomes permanently unresolvable via Telegram.
//
// The SQLite layer is transparent write-through behind the existing
// API. Cache remains authoritative for hot reads; SQLite takes over on
// cache miss. All callers (inbound.go, outbound.go) are unchanged.
type MessageMap struct {
	inner *bridge.MessageMap
	db    *sql.DB // optional; nil means in-memory-only (pre-48kt.2 behavior)
}

// NewMessageMap creates a new bounded message ID map with no persistent
// backing. Preserved for backward compatibility with the pre-48kt.2
// construction sites and kept for tests that don't need durability.
func NewMessageMap(maxSize int) *MessageMap {
	return &MessageMap{inner: bridge.NewMessageMap(maxSize)}
}

// NewMessageMapWithDB creates a MessageMap with SQLite-backed durable
// storage. Store writes through to the DB; ThrumID/TeleID fall back to
// the DB on cache miss. The caller owns db lifetime.
//
// If db is nil, behaves identically to NewMessageMap.
func NewMessageMapWithDB(maxSize int, db *sql.DB) *MessageMap {
	return &MessageMap{inner: bridge.NewMessageMap(maxSize), db: db}
}

func teleKey(chatID int64, msgID int) string {
	return fmt.Sprintf("%d:%d", chatID, msgID)
}

// Store records a bidirectional mapping. Evicts the oldest cache entry
// if at capacity. When a DB is wired, also persists the mapping so it
// survives daemon restart.
func (m *MessageMap) Store(chatID int64, teleMsgID int, thrumMsgID string) {
	key := teleKey(chatID, teleMsgID)
	m.inner.Store(key, thrumMsgID)

	if m.db != nil {
		// INSERT OR REPLACE matches the in-memory Store contract in
		// internal/bridge/msgmap.go, which overwrites when a key
		// already exists (retry, duplicate notification, etc.).
		if _, err := m.db.Exec(
			`INSERT OR REPLACE INTO telegram_msg_map
				(external_key, thrum_msg_id, created_at)
			 VALUES (?, ?, ?)`,
			key, thrumMsgID, time.Now().Unix(),
		); err != nil {
			// Cache write already succeeded, so the in-session flow stays
			// functional. Only a restart-window's worth of mappings risks
			// loss from a DB write error; log and continue.
			slog.Warn("[telegram.msgmap] DB write failed",
				"external_key", key,
				"thrum_msg_id", thrumMsgID,
				"err", err)
		}
	}
}

// ThrumID looks up the Thrum message ID for a given Telegram message.
// Cache-first; falls back to SQLite if the cache misses and a DB is
// wired. On DB hit, the cache is populated so the next lookup is fast.
func (m *MessageMap) ThrumID(chatID int64, teleMsgID int) (string, bool) {
	key := teleKey(chatID, teleMsgID)
	if id, ok := m.inner.ThrumID(key); ok {
		return id, true
	}
	if m.db == nil {
		return "", false
	}

	var id string
	err := m.db.QueryRow(
		`SELECT thrum_msg_id FROM telegram_msg_map WHERE external_key = ?`,
		key,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		slog.Warn("[telegram.msgmap] DB read failed",
			"external_key", key, "err", err)
		return "", false
	}

	// Warm the cache so subsequent lookups skip the SQL round-trip.
	m.inner.Store(key, id)
	return id, true
}

// TeleID looks up the Telegram chat ID and message ID for a given Thrum
// message. Cache-first; falls back to SQLite on miss. Used by outbound
// reply-routing to find the Telegram message a Thrum reply threads under.
func (m *MessageMap) TeleID(thrumMsgID string) (chatID int64, msgID int, ok bool) {
	if key, found := m.inner.ExternalKey(thrumMsgID); found {
		_, err := fmt.Sscanf(key, "%d:%d", &chatID, &msgID)
		return chatID, msgID, err == nil
	}
	if m.db == nil {
		return 0, 0, false
	}

	var key string
	err := m.db.QueryRow(
		`SELECT external_key FROM telegram_msg_map WHERE thrum_msg_id = ?`,
		thrumMsgID,
	).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false
	}
	if err != nil {
		slog.Warn("[telegram.msgmap] DB reverse-read failed",
			"thrum_msg_id", thrumMsgID, "err", err)
		return 0, 0, false
	}

	m.inner.Store(key, thrumMsgID)
	if _, err := fmt.Sscanf(key, "%d:%d", &chatID, &msgID); err != nil {
		return 0, 0, false
	}
	return chatID, msgID, true
}

// Len returns the current number of cached mappings. When a DB is
// wired, the persisted table may hold additional entries that haven't
// been loaded into the cache on this process's lifetime yet.
func (m *MessageMap) Len() int { return m.inner.Len() }

// SweepStale deletes telegram_msg_map rows that are older than ttl
// AND not referenced by any live row in permission_nudges. Returns
// the number of rows deleted.
//
// # Safety
//
// The SQL is a single DELETE ... WHERE with an age predicate on
// created_at plus a NOT EXISTS subquery against permission_nudges.
// Both predicates must be true for a row to be removed:
//
//  1. created_at < now - ttl (age cutoff)
//  2. thrum_msg_id ∉ permission_nudges.message_id (not pinned by a live nudge)
//
// This satisfies the thrum-48kt.6 acceptance criterion that the sweep
// "does not delete mappings for nudges that are still pending" — even
// if the mapping itself has aged past the TTL, the cross-check pins
// it until the pending_nudges row is removed. Today that removal is
// a DELETE executed by TryResolve / pendingNudgeStore.Delete — i.e.
// "resolved" is expressed as row absence, not a resolved-flag column.
// If a future schema change adds a resolved boolean instead, the
// cross-check here would need to filter on it. The inverse risk — a
// nudge resolves concurrently mid-sweep and we delete its mapping —
// is harmless because post-resolve the mapping is no longer needed.
//
// permission_nudges.message_id is declared TEXT PRIMARY KEY (see
// internal/schema/schema.go), which in SQLite is implicitly NOT NULL.
// The NOT EXISTS subquery therefore does not exhibit three-valued-
// logic NULL-matching surprises. If that schema changes, revisit.
//
// # TTL edge cases
//
// Callers should pass a positive ttl. ttl == 0 makes cutoff == now,
// which means any row whose created_at is earlier than "right now"
// is eligible — effectively delete-all-except-pinned. ttl < 0 makes
// cutoff a future timestamp, so every row with a past created_at is
// eligible — same practical effect. Neither is a crash, but neither
// is the intended use; DefaultMapTTL is positive and callers should
// prefer it unless they have a reason to sweep aggressively.
//
// # Concurrency
//
// Callers do not need to serialise this against bridge traffic.
// Concurrent Store writes race with the DELETE at the SQL layer; the
// INSERT OR REPLACE in Store timestamps created_at to the current
// clock, which by construction is newer than (now - ttl), so a
// freshly-stored mapping cannot be caught by the age predicate.
//
// # Nil-db
//
// A nil db handle is a no-op. This matches the defensive pattern
// elsewhere in this file (Store, ThrumID, TeleID all nil-guard before
// touching the DB) and means the daemon boot wiring can call
// SweepStale unconditionally without having to know whether the
// persistent backing was configured.
func SweepStale(ctx context.Context, db *sql.DB, ttl time.Duration) (int64, error) {
	if db == nil {
		return 0, nil
	}
	cutoff := time.Now().Add(-ttl).Unix()
	res, err := db.ExecContext(ctx, `
		DELETE FROM telegram_msg_map
		WHERE created_at < ?
		  AND NOT EXISTS (
		    SELECT 1 FROM permission_nudges
		    WHERE permission_nudges.message_id = telegram_msg_map.thrum_msg_id
		  )
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("telegram msgmap sweep: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		// RowsAffected only fails on drivers that don't support it;
		// SQLite does. Return 0 + err so callers can log without
		// losing the signal that the DELETE itself succeeded.
		return 0, fmt.Errorf("telegram msgmap sweep rows affected: %w", err)
	}
	return n, nil
}

// SweepLoop runs SweepStale once immediately, then on every tick of
// the given interval, returning when ctx is canceled. Intended to be
// launched as a goroutine from daemon boot:
//
//	go telegram.SweepLoop(ctx, db, telegram.DefaultMapTTL, telegram.DefaultSweepInterval)
//
// Errors are logged but do not stop the loop; a transient SQLite
// hiccup (or a context cancellation racing the next Exec) should not
// permanently disable the sweeper. The leading sweep matches the
// coordinator's dispatch ("once at daemon start + every 24h") and
// guarantees that rows which aged while the daemon was stopped get
// reaped promptly on the next boot rather than waiting out a full
// interval.
//
// Ctx-cancellation note: runDaemon today uses context.Background()
// for the long-lived ctx (cmd/thrum/main.go), so in production the
// select on ctx.Done() is effectively unreachable — the loop
// terminates via OS process exit rather than graceful cancellation.
// That matches every other long-running goroutine launched from
// runDaemon. If runDaemon is ever rewired to a cancellable ctx
// (SIGTERM-aware shutdown), SweepLoop will participate without
// change.
func SweepLoop(ctx context.Context, db *sql.DB, ttl, interval time.Duration) {
	runOnce := func() {
		n, err := SweepStale(ctx, db, ttl)
		if err != nil {
			slog.Warn("[telegram.msgmap] sweep failed", "err", err)
			return
		}
		if n > 0 {
			slog.Info("[telegram.msgmap] sweep deleted rows",
				"count", n, "ttl", ttl, "interval", interval)
		}
	}

	runOnce()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}
