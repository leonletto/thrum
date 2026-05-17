package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// compactionThresholdBytes is the file-size gate below which Compact()
// is a no-op. Mesh gossip events are rare (peer churn is occasional);
// rewriting the WAL on every Append churns disk for no benefit.
const compactionThresholdBytes = 1024 * 1024

// PendingMeshUpdatesLog is the WAL-style intent log behind D-B1.13's
// mesh.go gossip-driven config mutations. Three-step protocol per
// design-spec §5:
//
//  1. AppendIntent → record what we're about to write
//  2. (caller atomically writes config.json)
//  3. AppendCommitted → mark the intent landed
//
// On boot, replay any intent without a matching committed marker BEFORE
// normal startup. The mesh handler re-applies the intent idempotently —
// the previous config write may or may not have landed (atomic-rename
// makes this binary: full success or no change), so replay catches the
// "config was written but committed marker missed" race and re-emits the
// committed marker + audit log line.
type PendingMeshUpdatesLog struct {
	path string
	mu   sync.Mutex

	file   *os.File
	writer *bufio.Writer
}

// PendingMeshUpdate is one row from the WAL. Stage is "intent" or
// "committed". Payload is verb-specific (the mesh handler interprets).
type PendingMeshUpdate struct {
	UpdateID  string          `json:"update_id"`
	Stage     string          `json:"stage"` // "intent" | "committed"
	Verb      string          `json:"verb,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// NewPendingMeshUpdatesLog opens (or creates) the WAL at path and
// readies it for appends. Creates the parent directory with 0700 if
// absent. Does NOT replay pending entries — that's the caller's
// responsibility (call Pending() + apply each).
func NewPendingMeshUpdatesLog(path string) (*PendingMeshUpdatesLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mesh-wal: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) // #nosec G304 -- daemon-config-derived path
	if err != nil {
		return nil, fmt.Errorf("mesh-wal: open: %w", err)
	}
	return &PendingMeshUpdatesLog{
		path:   path,
		file:   f,
		writer: bufio.NewWriter(f),
	}, nil
}

// Path returns the underlying file path. Test helper.
func (l *PendingMeshUpdatesLog) Path() string { return l.path }

// AppendIntent records the start of a mesh-driven config mutation.
// verb is one of peer.pair/announce/welcome/rebind/revoke; payload is
// verb-specific metadata (e.g., the new peer entry). Caller writes the
// config atomically AFTER this returns; on success, follows with
// AppendCommitted(updateID).
func (l *PendingMeshUpdatesLog) AppendIntent(updateID, verb string, payload any) error {
	return l.append(PendingMeshUpdate{
		UpdateID:  updateID,
		Stage:     "intent",
		Verb:      verb,
		Payload:   marshalPayload(payload),
		Timestamp: time.Now().UTC(),
	})
}

// AppendCommitted records that the config write succeeded for an
// earlier intent. Pending() will no longer report this updateID.
func (l *PendingMeshUpdatesLog) AppendCommitted(updateID string) error {
	return l.append(PendingMeshUpdate{
		UpdateID:  updateID,
		Stage:     "committed",
		Timestamp: time.Now().UTC(),
	})
}

// Pending returns the set of updates with intent recorded but no
// matching committed marker. Caller re-applies these on boot (the
// mesh handler is idempotent on the underlying config — write happens
// under the config-mutex, atomic-rename means binary outcome). After
// re-apply, caller emits AppendCommitted to close the loop.
func (l *PendingMeshUpdatesLog) Pending() ([]PendingMeshUpdate, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.writer != nil {
		if err := l.writer.Flush(); err != nil {
			return nil, fmt.Errorf("mesh-wal: flush: %w", err)
		}
	}

	f, err := os.Open(l.path) // #nosec G304 -- same path as constructor
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("mesh-wal: open for read: %w", err)
	}
	defer func() { _ = f.Close() }()

	intents := make(map[string]PendingMeshUpdate)
	committed := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec PendingMeshUpdate
		if err := json.Unmarshal(line, &rec); err != nil {
			// Skip malformed lines — they're stale partial writes from
			// a crash before the line was fully flushed.
			continue
		}
		switch rec.Stage {
		case "intent":
			intents[rec.UpdateID] = rec
		case "committed":
			committed[rec.UpdateID] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("mesh-wal: scan: %w", err)
	}

	out := make([]PendingMeshUpdate, 0)
	for id, intent := range intents {
		if !committed[id] {
			out = append(out, intent)
		}
	}
	return out, nil
}

// Compact rewrites the WAL with only uncommitted entries, dropping the
// committed history. No-op when file is under compactionThresholdBytes
// — mesh gossip is rare; we avoid churning rewrites on every event.
func (l *PendingMeshUpdatesLog) Compact() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	info, err := os.Stat(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("mesh-wal: stat: %w", err)
	}
	if info.Size() < compactionThresholdBytes {
		return nil
	}

	pending, err := l.pendingLocked()
	if err != nil {
		return err
	}

	if l.writer != nil {
		_ = l.writer.Flush()
	}
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
		l.writer = nil
	}

	tmp := l.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- daemon-config-derived
	if err != nil {
		return fmt.Errorf("mesh-wal: open temp: %w", err)
	}
	w := bufio.NewWriter(f)
	for _, p := range pending {
		buf, err := json.Marshal(p)
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("mesh-wal: marshal: %w", err)
		}
		buf = append(buf, '\n')
		if _, err := w.Write(buf); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("mesh-wal: write temp: %w", err)
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
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mesh-wal: rename: %w", err)
	}

	// Reopen for further appends.
	nf, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) // #nosec G304 -- daemon-config-derived
	if err != nil {
		return fmt.Errorf("mesh-wal: reopen: %w", err)
	}
	l.file = nf
	l.writer = bufio.NewWriter(nf)
	return nil
}

// Close flushes pending writes + closes the file. Safe to call twice.
func (l *PendingMeshUpdatesLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writer != nil {
		if err := l.writer.Flush(); err != nil {
			return err
		}
		l.writer = nil
	}
	if l.file != nil {
		if err := l.file.Close(); err != nil {
			return err
		}
		l.file = nil
	}
	return nil
}

// --- internals ---

func (l *PendingMeshUpdatesLog) append(rec PendingMeshUpdate) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writer == nil {
		return fmt.Errorf("mesh-wal: log is closed")
	}
	buf, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("mesh-wal: marshal: %w", err)
	}
	buf = append(buf, '\n')
	if _, err := l.writer.Write(buf); err != nil {
		return fmt.Errorf("mesh-wal: write: %w", err)
	}
	// Flush after each Append so a crash before Close doesn't lose the
	// intent line (the load-bearing crash-safety invariant).
	return l.writer.Flush()
}

// pendingLocked is the mu-held variant of Pending used internally by
// Compact (which already holds the mutex).
func (l *PendingMeshUpdatesLog) pendingLocked() ([]PendingMeshUpdate, error) {
	if l.writer != nil {
		_ = l.writer.Flush()
	}
	f, err := os.Open(l.path) // #nosec G304 -- daemon-config-derived
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	intents := make(map[string]PendingMeshUpdate)
	committed := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec PendingMeshUpdate
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		switch rec.Stage {
		case "intent":
			intents[rec.UpdateID] = rec
		case "committed":
			committed[rec.UpdateID] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	out := make([]PendingMeshUpdate, 0)
	for id, intent := range intents {
		if !committed[id] {
			out = append(out, intent)
		}
	}
	return out, nil
}

// marshalPayload serializes the verb-specific payload to a json.RawMessage.
// Nil or marshaling failure → empty RawMessage. The payload is opaque to
// the WAL — the mesh handler that re-applies the intent owns the shape.
func marshalPayload(p any) json.RawMessage {
	if p == nil {
		return nil
	}
	buf, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	return buf
}
