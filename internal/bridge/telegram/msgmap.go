package telegram

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/leonletto/thrum/internal/bridge"
)

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
