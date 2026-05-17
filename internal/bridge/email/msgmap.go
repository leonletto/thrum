package email

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MsgMap is the email-bridge threading map: external Message-Id →
// thrum_msg_id. Persisted as a JSONL sidecar at .thrum/state/
// email-msgmap.jsonl per canonical-ref §3.12 (no new SQLite table —
// msgmap is the explicit sidecar exception in the migration sequence).
//
// Append-on-insert + compact-on-startup: each Insert writes one JSONL
// record to the sidecar; constructor re-loads + collapses to dedup.
// Sweep rewrites the sidecar with entries newer than the cutoff.
//
// Thread-safe. Closing flushes any buffered writes — callers should
// always invoke Close before process exit.
type MsgMap struct {
	mu      sync.RWMutex
	entries map[string]msgMapEntry // messageID → entry
	path    string
	file    *os.File
	writer  *bufio.Writer
}

type msgMapEntry struct {
	ThrumMsgID string    `json:"thrum_msg_id"`
	InsertedAt time.Time `json:"inserted_at"`
}

// sidecarRecord is the on-disk JSONL row. MessageID lives here rather
// than as a map key so the sidecar file is self-describing — readable
// independently from the in-memory shape.
type sidecarRecord struct {
	MessageID  string    `json:"message_id"`
	ThrumMsgID string    `json:"thrum_msg_id"`
	InsertedAt time.Time `json:"inserted_at"`
}

// NewMsgMap opens (or creates) the sidecar file at the given path,
// loads existing entries (dedup-on-load → compact-on-startup), and
// reopens the file for append. The directory portion of path is
// created with 0700 if absent — sidecar lives under .thrum/state/
// which is always operator-private.
func NewMsgMap(path string) (*MsgMap, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("msgmap: mkdir state dir: %w", err)
	}

	m := &MsgMap{
		entries: make(map[string]msgMapEntry),
		path:    path,
	}

	if err := m.loadAndCompact(); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) // #nosec G304 -- path comes from daemon config
	if err != nil {
		return nil, fmt.Errorf("msgmap: open for append: %w", err)
	}
	m.file = f
	m.writer = bufio.NewWriter(f)

	return m, nil
}

// Insert records a Message-Id → thrum_msg_id mapping. Idempotent: a
// re-insert of the same (messageID, thrumMsgID) pair is a no-op for
// the in-memory map (sidecar still gets the JSONL row, which is
// collapsed by compact-on-startup; this preserves the strict
// append-only invariant on disk).
//
// Re-insert with a DIFFERENT thrumMsgID overwrites the in-memory entry
// — supports the rare reroute case (e.g., operator rewires a peer's
// daemon-id mid-stream).
func (m *MsgMap) Insert(messageID, thrumMsgID string) error {
	return m.InsertAt(messageID, thrumMsgID, time.Now().UTC())
}

// InsertAt is Insert with an explicit timestamp — exported for tests
// that need to pin entry age without sleeping. Production code calls
// Insert (which delegates here with time.Now).
func (m *MsgMap) InsertAt(messageID, thrumMsgID string, t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, present := m.entries[messageID]
	if present && existing.ThrumMsgID == thrumMsgID {
		// Pure duplicate — skip the disk write to avoid pointless growth.
		return nil
	}

	m.entries[messageID] = msgMapEntry{ThrumMsgID: thrumMsgID, InsertedAt: t}

	rec := sidecarRecord{MessageID: messageID, ThrumMsgID: thrumMsgID, InsertedAt: t}
	if err := m.appendRecord(rec); err != nil {
		return err
	}
	return nil
}

// Lookup returns the thrum_msg_id for an external Message-Id, or
// ok=false when no mapping exists.
func (m *MsgMap) Lookup(messageID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[messageID]
	if !ok {
		return "", false
	}
	return e.ThrumMsgID, true
}

// Len returns the current in-memory entry count.
func (m *MsgMap) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

// Sweep drops entries older than the given cutoff and rewrites the
// sidecar file with the surviving entries (in-memory + disk both
// reflect the surviving set). Default cadence is 90d behind now
// (the caller picks the cutoff to allow test injection).
func (m *MsgMap) Sweep(olderThan time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, e := range m.entries {
		if e.InsertedAt.Before(olderThan) {
			delete(m.entries, k)
		}
	}

	return m.rewriteSidecarLocked()
}

// Close flushes pending writes + closes the sidecar file. Safe to call
// multiple times — the second call is a no-op.
func (m *MsgMap) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writer != nil {
		if err := m.writer.Flush(); err != nil {
			return err
		}
		m.writer = nil
	}
	if m.file != nil {
		if err := m.file.Close(); err != nil {
			return err
		}
		m.file = nil
	}
	return nil
}

// --- internal ---

// loadAndCompact reads the existing sidecar (if any), dedups by latest
// InsertedAt, and rewrites the file in compact form. Called once at
// startup. A nonexistent sidecar is benign (first-run case).
func (m *MsgMap) loadAndCompact() error {
	f, err := os.Open(m.path) // #nosec G304 -- path comes from daemon config
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("msgmap: open for read: %w", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	// Allow large lines — message IDs are usually short but JSONL has
	// no guaranteed ceiling. 1MB matches the encoding/json default for
	// streaming reads and is well above realistic msgmap row sizes.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec sidecarRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			// Skip malformed lines rather than fail load — the daemon
			// may have crashed mid-write and left a partial record.
			continue
		}
		// Later wins (sidecar is append-ordered + InsertedAt is
		// monotonic for a given key).
		m.entries[rec.MessageID] = msgMapEntry{
			ThrumMsgID: rec.ThrumMsgID,
			InsertedAt: rec.InsertedAt,
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("msgmap: scan sidecar: %w", err)
	}

	// Compact: rewrite the file with only the surviving (deduplicated)
	// entries. Reduces file size; ensures any malformed-line skips
	// don't recur on the next load.
	return m.rewriteSidecarUnlocked()
}

// rewriteSidecarLocked rewrites m.path with the current entries set.
// Caller must hold m.mu.
func (m *MsgMap) rewriteSidecarLocked() error {
	if m.writer != nil {
		if err := m.writer.Flush(); err != nil {
			return err
		}
	}
	if m.file != nil {
		if err := m.file.Close(); err != nil {
			return err
		}
		m.file = nil
		m.writer = nil
	}
	if err := m.rewriteSidecarUnlocked(); err != nil {
		return err
	}
	f, err := os.OpenFile(m.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("msgmap: reopen for append: %w", err)
	}
	m.file = f
	m.writer = bufio.NewWriter(f)
	return nil
}

// rewriteSidecarUnlocked writes the in-memory map to disk via a temp
// file + rename so a crash mid-write leaves either the old or the new
// sidecar intact. Caller is responsible for any file-handle juggling.
func (m *MsgMap) rewriteSidecarUnlocked() error {
	tmp := m.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- temp file derived from daemon-config path
	if err != nil {
		return fmt.Errorf("msgmap: open temp: %w", err)
	}
	w := bufio.NewWriter(f)
	for k, e := range m.entries {
		rec := sidecarRecord{MessageID: k, ThrumMsgID: e.ThrumMsgID, InsertedAt: e.InsertedAt}
		buf, err := json.Marshal(rec)
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("msgmap: marshal record: %w", err)
		}
		buf = append(buf, '\n')
		if _, err := w.Write(buf); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("msgmap: write temp: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, m.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("msgmap: rename: %w", err)
	}
	return nil
}

func (m *MsgMap) appendRecord(rec sidecarRecord) error {
	buf, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("msgmap: marshal: %w", err)
	}
	buf = append(buf, '\n')
	if _, err := m.writer.Write(buf); err != nil {
		return fmt.Errorf("msgmap: append: %w", err)
	}
	// Flush after each write so Close doesn't lose the last record on
	// crash, and so a parallel reader (Sweep, Close) sees the data.
	return m.writer.Flush()
}
