// Package compact implements retention and dedup-compaction for the three
// growing local-data files produced by the sync re-architecture (E5).
//
// Responsibilities:
//   - CompactEventsJournal: trim .thrum/events.jsonl + SQLite events table to
//     retentionDays rolling window.
//   - CompactMessageStateFile: dedup messages-v2/<agentID>.jsonl by message_id
//     (last line wins) when file exceeds sizeThresholdBytes.
//   - CompactReceiptStateFile: dedup receipts/<agentID>.jsonl by
//     (message_id, agent_id) when file exceeds sizeThresholdBytes.
//   - CompactAll: orchestrates all three, idempotent, safe to call repeatedly.
//
// Anti-patterns enforced in this package:
//   - All SQL routed through safedb (no raw db.Exec/db.Query).
//   - All file rewrites are temp-file + atomic rename (crash-safe).
//   - No timers here; callers invoke CompactAll at sync-trigger time and
//     daemon startup only (spec §5.3).
package compact

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/jsonl"
)

// Compactor handles retention and compaction for events journals,
// messages-v2 files, and receipts files.
type Compactor struct {
	thrumDir           string
	syncDir            string
	retentionDays      int   // from daemon.events_retention_days; default 2
	sizeThresholdBytes int64 // 10 MB by default
}

// New constructs a Compactor.
//   - thrumDir: path to the local .thrum directory (holds events.jsonl).
//   - syncDir:  path to the sync worktree (holds messages-v2/ and receipts/).
//   - retentionDays: number of days to retain events (0 means keep everything).
//   - sizeThresholdMB: files below this size (in MiB) are skipped during
//     message/receipt dedup. Pass 0 to always compact regardless of size.
func New(thrumDir, syncDir string, retentionDays int, sizeThresholdMB int) *Compactor {
	threshold := int64(sizeThresholdMB) * 1024 * 1024
	if sizeThresholdMB == 0 {
		threshold = 0 // always compact
	}
	return &Compactor{
		thrumDir:           thrumDir,
		syncDir:            syncDir,
		retentionDays:      retentionDays,
		sizeThresholdBytes: threshold,
	}
}

// CompactEventsJournal removes events older than retentionDays from
// .thrum/events.jsonl AND from the SQLite events table.
// Idempotent. Returns the count of journal rows removed (used as truth;
// SQLite side may differ only if the two sides were already out of sync
// before compaction).
//
// Implementation note: the JSONL side reuses jsonl.RemoveBeforeTimestamp
// (spec §5.3 explicit requirement). The SQLite side uses safedb.ExecContext.
func (c *Compactor) CompactEventsJournal(ctx context.Context, db *safedb.DB) (int, error) {
	if c.retentionDays <= 0 {
		// retentionDays == 0 means "keep everything"
		return 0, nil
	}

	cutoff := time.Now().UTC().Add(-time.Duration(c.retentionDays) * 24 * time.Hour)

	journalPath := filepath.Join(c.thrumDir, "events.jsonl")

	// JSONL side: reuse the existing helper (spec §5.3, plan Task 6).
	journalRemoved, err := jsonl.RemoveBeforeTimestamp(journalPath, "timestamp", cutoff)
	if err != nil {
		return 0, fmt.Errorf("compact events journal: %w", err)
	}

	// SQLite side: delete rows older than cutoff.
	// ALL SQL through safedb per project rules (anti-pattern #1).
	_, err = db.ExecContext(ctx,
		`DELETE FROM events WHERE timestamp < ?`,
		cutoff.Format(time.RFC3339Nano),
	)
	if err != nil {
		return journalRemoved, fmt.Errorf("compact events table: %w", err)
	}

	if journalRemoved > 0 {
		slog.Info("compaction.trimmed",
			"path", journalPath,
			"rows_removed", journalRemoved,
			"bytes_saved", 0)
	}

	return journalRemoved, nil
}

// CompactMessageStateFile dedups messages-v2/<agentID>.jsonl by keeping only
// the latest row per message_id (last line wins, per spec §4.3).
//
// The file is rewritten atomically via a temp file + rename (anti-pattern #2).
// If the file is smaller than sizeThresholdBytes the function returns (0, nil)
// without touching the file, preserving the "fires when above threshold OR
// explicitly at sync-trigger time" semantic from spec §5.3.
//
// Returns the number of bytes saved (pre-size minus post-size).
func (c *Compactor) CompactMessageStateFile(ctx context.Context, agentID string) (int64, error) {
	path := filepath.Join(c.syncDir, "messages-v2", agentID+".jsonl")
	return c.compactJSONLByKey(ctx, path, func(row map[string]json.RawMessage) (string, bool) {
		raw, ok := row["message_id"]
		if !ok {
			return "", false
		}
		var id string
		if err := json.Unmarshal(raw, &id); err != nil {
			return "", false
		}
		return id, true
	})
}

// CompactReceiptStateFile dedups receipts/<agentID>.jsonl by keeping only the
// latest row per (message_id, agent_id) composite key (last line wins).
//
// Same atomic-rewrite + threshold semantics as CompactMessageStateFile.
// Returns bytes saved.
func (c *Compactor) CompactReceiptStateFile(ctx context.Context, agentID string) (int64, error) {
	path := filepath.Join(c.syncDir, "receipts", agentID+".jsonl")
	return c.compactJSONLByKey(ctx, path, func(row map[string]json.RawMessage) (string, bool) {
		msgRaw, ok1 := row["message_id"]
		agtRaw, ok2 := row["agent_id"]
		if !ok1 || !ok2 {
			return "", false
		}
		var msgID, agtID string
		if err := json.Unmarshal(msgRaw, &msgID); err != nil {
			return "", false
		}
		if err := json.Unmarshal(agtRaw, &agtID); err != nil {
			return "", false
		}
		return msgID + "\x00" + agtID, true
	})
}

// CompactAll runs all compactions in order: events journal first, then all
// messages-v2/*.jsonl files, then all receipts/*.jsonl files.
// Idempotent; safe to call repeatedly.
func (c *Compactor) CompactAll(ctx context.Context, db *safedb.DB) error {
	// 1. Events journal + SQLite parity.
	if _, err := c.CompactEventsJournal(ctx, db); err != nil {
		return fmt.Errorf("CompactAll events journal: %w", err)
	}

	// 2. messages-v2/*.jsonl
	msgsDir := filepath.Join(c.syncDir, "messages-v2")
	if err := c.compactDir(ctx, msgsDir, c.CompactMessageStateFile); err != nil {
		return fmt.Errorf("CompactAll messages-v2: %w", err)
	}

	// 3. receipts/*.jsonl
	receiptsDir := filepath.Join(c.syncDir, "receipts")
	if err := c.compactDir(ctx, receiptsDir, c.CompactReceiptStateFile); err != nil {
		return fmt.Errorf("CompactAll receipts: %w", err)
	}

	return nil
}

// compactDir enumerates *.jsonl files in dir and calls compact(ctx, stem) for
// each, where stem is the base filename without the .jsonl extension.
// Missing directories are silently skipped (idempotent).
func (c *Compactor) compactDir(ctx context.Context, dir string, compact func(context.Context, string) (int64, error)) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // directory not yet created — normal before first sync
		}
		return fmt.Errorf("read dir %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), ".jsonl")
		if _, err := compact(ctx, stem); err != nil {
			return fmt.Errorf("compact %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// compactJSONLByKey is the shared dedup engine for both message-state and
// receipt-state files.
//
// keyFn extracts the dedup key from a parsed JSON row.  If keyFn returns
// (_, false) the row is kept as-is (unparseable rows are never silently
// dropped).
//
// Algorithm: scan once, building key → lastLine map (last occurrence wins).
// Then write deduped lines in their ORIGINAL ORDER to a temp file and rename.
//
// Size-threshold check: if the file size is below c.sizeThresholdBytes,
// return (0, nil) immediately without touching the file.
func (c *Compactor) compactJSONLByKey(
	_ context.Context,
	path string,
	keyFn func(map[string]json.RawMessage) (string, bool),
) (int64, error) {
	// Stat to check existence and size.
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // nothing to compact
		}
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	preSize := info.Size()

	// Below-threshold skip (anti-pattern #6).
	if c.sizeThresholdBytes > 0 && preSize < c.sizeThresholdBytes {
		return 0, nil
	}

	// Read all lines.
	f, err := os.Open(path) // #nosec G304 -- path is an internal sync state file
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	type indexedLine struct {
		raw   []byte
		order int // position in file; used to preserve order for non-dedup rows
	}

	// Two-pass approach:
	// Pass 1: scan all lines, recording the LAST index for each key.
	// Pass 2: emit only lines that are the last occurrence of their key,
	//         in original file order.
	var lines [][]byte
	scanner := bufio.NewScanner(f)
	// Allow large lines (up to 4 MB) — message bodies can be long.
	const maxLineBytes = 4 * 1024 * 1024
	buf := make([]byte, maxLineBytes)
	scanner.Buffer(buf, maxLineBytes)

	for scanner.Scan() {
		b := scanner.Bytes()
		if len(b) == 0 {
			continue
		}
		cp := make([]byte, len(b))
		copy(cp, b)
		lines = append(lines, cp)
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan %s: %w", path, err)
	}
	_ = f.Close()

	// Build key → last-occurrence index map.
	lastIdx := make(map[string]int, len(lines))
	for i, line := range lines {
		var row map[string]json.RawMessage
		if err := json.Unmarshal(line, &row); err != nil {
			// Unparseable line: keep as-is; use the line content as key
			// so it is always kept (no dedup for broken lines).
			lastIdx[fmt.Sprintf("__raw_%d", i)] = i
			continue
		}
		key, ok := keyFn(row)
		if !ok {
			// No key extractable: keep as-is.
			lastIdx[fmt.Sprintf("__nokey_%d", i)] = i
			continue
		}
		lastIdx[key] = i // overwrite → last occurrence wins
	}

	// Build kept-index set.
	keepSet := make(map[int]bool, len(lastIdx))
	for _, idx := range lastIdx {
		keepSet[idx] = true
	}

	// Write deduped lines to a temp file in original order.
	tmpPath := path + ".compact.tmp"
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) // #nosec G304
	if err != nil {
		return 0, fmt.Errorf("create temp file %s: %w", tmpPath, err)
	}
	defer func() { _ = os.Remove(tmpPath) }() // clean up on error

	w := bufio.NewWriter(tmpFile)
	for i, line := range lines {
		if !keepSet[i] {
			continue
		}
		_, _ = w.Write(line)
		_ = w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		_ = tmpFile.Close()
		return 0, fmt.Errorf("flush temp file %s: %w", tmpPath, err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return 0, fmt.Errorf("sync temp file %s: %w", tmpPath, err)
	}
	_ = tmpFile.Close()

	// Atomic rename (anti-pattern #2).
	if err := os.Rename(tmpPath, path); err != nil {
		return 0, fmt.Errorf("rename %s → %s: %w", tmpPath, path, err)
	}

	// Bytes saved = pre - post.
	postInfo, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat post %s: %w", path, err)
	}
	saved := preSize - postInfo.Size()
	if saved < 0 {
		saved = 0 // should not happen; guard against edge cases
	}

	rowsRemoved := len(lines) - len(keepSet)
	if rowsRemoved > 0 {
		slog.Info("compaction.trimmed",
			"path", path,
			"rows_removed", rowsRemoved,
			"bytes_saved", saved)
	}

	return saved, nil
}
