package compact_test

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/sync/compact"
	_ "modernc.org/sqlite"
)

// openTestDB creates an in-memory SQLite database with an events table.
func openTestDB(t *testing.T) *safedb.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	_, err = raw.Exec(`CREATE TABLE events (
		id        INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id  TEXT NOT NULL,
		timestamp TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create events table: %v", err)
	}
	return safedb.New(raw)
}

// seedEventsTable inserts events with the given timestamps into the SQLite events table.
func seedEventsTable(t *testing.T, db *safedb.DB, timestamps []time.Time) {
	t.Helper()
	ctx := context.Background()
	for i, ts := range timestamps {
		_, err := db.ExecContext(ctx,
			`INSERT INTO events (event_id, timestamp) VALUES (?, ?)`,
			fmt.Sprintf("evt_%04d", i),
			ts.UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			t.Fatalf("insert event %d: %v", i, err)
		}
	}
}

// countEventsTable returns the number of rows in the events table.
func countEventsTable(t *testing.T, db *safedb.DB) int {
	t.Helper()
	ctx := context.Background()
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&count)
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	return count
}

// writeJSONLFile writes a slice of JSON objects (as maps) to a file,
// one JSON object per line.
func writeJSONLFile(t *testing.T, path string, rows []map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create file %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	for _, row := range rows {
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		_, _ = w.Write(data)
		_ = w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

// countJSONLLines returns the number of non-empty lines in a JSONL file.
func countJSONLLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		if len(strings.TrimSpace(scanner.Text())) > 0 {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return count
}

// readJSONLRows reads all non-empty lines from a JSONL file as raw maps.
func readJSONLRows(t *testing.T, path string) []map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	var out []map[string]string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]string
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("unmarshal line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// TestCompactor_EventsJournal_RetentionCutoff seeds 1000 synthetic events
// spanning 5 days, runs CompactEventsJournal with a 2-day cutoff, and asserts:
//  1. The JSONL journal retains only events within the retention window.
//  2. The SQLite events table row count matches the journal line count post-compact.
func TestCompactor_EventsJournal_RetentionCutoff(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	syncDir := filepath.Join(dir, "sync")

	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	retentionDays := 2
	cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)

	// Build 1000 events: spread across 5 days, newest last.
	// Day 0 = 5 days ago (should be trimmed), Day 4 = today (should be kept).
	// Events per day: 200
	type event struct {
		EventID   string `json:"event_id"`
		Timestamp string `json:"timestamp"`
	}

	total := 1000
	eventsPerDay := total / 5 // 200 per day

	var timestamps []time.Time
	var jsonlRows []map[string]string
	for i := 0; i < total; i++ {
		// day index 0..4 (0 = oldest, 4 = newest)
		dayIdx := i / eventsPerDay
		if dayIdx > 4 {
			dayIdx = 4
		}
		// offset days from now: day 0 is 4 full days ago, day 4 is "now minus a bit"
		daysAgo := 4 - dayIdx
		ts := now.Add(-time.Duration(daysAgo)*24*time.Hour - time.Duration(i)*time.Second)
		timestamps = append(timestamps, ts)
		jsonlRows = append(jsonlRows, map[string]string{
			"event_id":  fmt.Sprintf("evt_%04d", i),
			"timestamp": ts.Format(time.RFC3339Nano),
		})
	}

	// Write events.jsonl
	journalPath := filepath.Join(thrumDir, "events.jsonl")
	writeJSONLFile(t, journalPath, jsonlRows)

	// Seed SQLite table with same timestamps
	db := openTestDB(t)
	seedEventsTable(t, db, timestamps)

	// Count how many events are within the cutoff
	expectedKept := 0
	for _, ts := range timestamps {
		if !ts.Before(cutoff) {
			expectedKept++
		}
	}
	if expectedKept == 0 || expectedKept == total {
		t.Fatalf("test setup error: expectedKept=%d (all or none), check day spacing", expectedKept)
	}

	c := compact.New(thrumDir, syncDir, retentionDays, 10)
	ctx := context.Background()
	removed, err := c.CompactEventsJournal(ctx, db)
	if err != nil {
		t.Fatalf("CompactEventsJournal: %v", err)
	}

	journalCount := countJSONLLines(t, journalPath)
	sqlCount := countEventsTable(t, db)

	// Both sides must agree
	if journalCount != sqlCount {
		t.Errorf("parity failure: journal=%d SQLite=%d", journalCount, sqlCount)
	}

	// Removed count should be positive
	if removed <= 0 {
		t.Errorf("expected positive rows removed, got %d", removed)
	}

	// Kept count must equal expected
	if journalCount != expectedKept {
		t.Errorf("journal retained %d rows, want %d (cutoff %v)", journalCount, expectedKept, cutoff)
	}

	// Total check: removed + kept = total
	if removed+journalCount != total {
		t.Errorf("removed(%d) + kept(%d) = %d != total(%d)", removed, journalCount, removed+journalCount, total)
	}
}

// TestCompactor_MessageStateFile_DedupByMessageID seeds messages-v2/<agentID>.jsonl
// with 1000 rows where 500 message_ids each appear twice (latest second), runs
// CompactMessageStateFile, and asserts 500 rows post-compact where each row is
// the latest version.
func TestCompactor_MessageStateFile_DedupByMessageID(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	syncDir := filepath.Join(dir, "sync")
	agentID := "agt_test01"

	// Build 1000 rows: 500 message IDs, each with 2 rows (v1 first, v2 second).
	// v2 row has body="latest" so we can assert the latest is kept.
	var rows []map[string]string
	for i := 0; i < 500; i++ {
		msgID := fmt.Sprintf("msg_%04d", i)
		rows = append(rows, map[string]string{
			"message_id": msgID,
			"author_id":  agentID,
			"body":       "first",
			"v":          "1",
		})
		rows = append(rows, map[string]string{
			"message_id": msgID,
			"author_id":  agentID,
			"body":       "latest",
			"v":          "1",
		})
	}

	msgDir := filepath.Join(syncDir, "messages-v2")
	filePath := filepath.Join(msgDir, agentID+".jsonl")
	writeJSONLFile(t, filePath, rows)

	// Ensure the file is large enough to exceed the threshold (use 0 for test)
	// We'll use a threshold of 0 to always compact in this test.
	c := compact.New(thrumDir, syncDir, 2, 0)
	ctx := context.Background()
	saved, err := c.CompactMessageStateFile(ctx, agentID)
	if err != nil {
		t.Fatalf("CompactMessageStateFile: %v", err)
	}

	postCount := countJSONLLines(t, filePath)
	if postCount != 500 {
		t.Errorf("post-compact row count = %d, want 500", postCount)
	}
	if saved <= 0 {
		t.Errorf("expected positive bytes saved, got %d", saved)
	}

	// Verify all kept rows have body="latest"
	kept := readJSONLRows(t, filePath)
	for _, row := range kept {
		if row["body"] != "latest" {
			t.Errorf("row for msg %q has body=%q, want %q", row["message_id"], row["body"], "latest")
		}
	}

	// Verify all 500 unique message IDs are present
	seen := make(map[string]bool)
	for _, row := range kept {
		seen[row["message_id"]] = true
	}
	if len(seen) != 500 {
		t.Errorf("got %d unique message IDs, want 500", len(seen))
	}
}

// TestCompactor_ReceiptStateFile_DedupByMessageAndAgent seeds receipts/<agentID>.jsonl
// with rows where the dedup key is (message_id, agent_id), runs
// CompactReceiptStateFile, and asserts dedup keeps the latest row per key.
func TestCompactor_ReceiptStateFile_DedupByMessageAndAgent(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	syncDir := filepath.Join(dir, "sync")
	issuerID := "agt_issuer01"

	// 300 unique (message_id, agent_id) pairs, each appearing twice.
	// 150 unique message IDs × 2 recipient agent IDs = 300 unique keys.
	// Each pair has an "early" and a "later" read_at.
	var rows []map[string]string
	early := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)
	later := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	uniqueKeys := 0
	for i := 0; i < 150; i++ {
		msgID := fmt.Sprintf("msg_%04d", i)
		for _, recpID := range []string{"agt_recip_A", "agt_recip_B"} {
			uniqueKeys++
			// early row first
			rows = append(rows, map[string]string{
				"message_id": msgID,
				"agent_id":   recpID,
				"read_at":    early,
			})
			// later row second (should be kept)
			rows = append(rows, map[string]string{
				"message_id": msgID,
				"agent_id":   recpID,
				"read_at":    later,
			})
		}
	}

	receiptDir := filepath.Join(syncDir, "receipts")
	filePath := filepath.Join(receiptDir, issuerID+".jsonl")
	writeJSONLFile(t, filePath, rows)

	// Use threshold 0 to always compact.
	c := compact.New(thrumDir, syncDir, 2, 0)
	ctx := context.Background()
	saved, err := c.CompactReceiptStateFile(ctx, issuerID)
	if err != nil {
		t.Fatalf("CompactReceiptStateFile: %v", err)
	}

	postCount := countJSONLLines(t, filePath)
	if postCount != uniqueKeys {
		t.Errorf("post-compact row count = %d, want %d", postCount, uniqueKeys)
	}
	if saved <= 0 {
		t.Errorf("expected positive bytes saved, got %d", saved)
	}

	// Verify all kept rows have read_at == later
	kept := readJSONLRows(t, filePath)
	for _, row := range kept {
		if row["read_at"] != later {
			t.Errorf("row (%s,%s) has read_at=%q, want %q", row["message_id"], row["agent_id"], row["read_at"], later)
		}
	}

	// Verify all unique keys present
	type key struct{ msgID, agentID string }
	seenKeys := make(map[key]bool)
	for _, row := range kept {
		seenKeys[key{row["message_id"], row["agent_id"]}] = true
	}
	if len(seenKeys) != uniqueKeys {
		t.Errorf("got %d unique keys, want %d", len(seenKeys), uniqueKeys)
	}
}

// TestCompactor_CompactAll_Idempotent runs CompactAll twice in a row and asserts
// the second run produces no further changes (idempotent).
func TestCompactor_CompactAll_Idempotent(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	syncDir := filepath.Join(dir, "sync")

	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	agentID := "agt_idem01"

	// Seed events journal with some old events (3 days ago, outside 2-day window)
	// and some recent ones.
	var jsonlRows []map[string]string
	var timestamps []time.Time
	for i := 0; i < 20; i++ {
		// 10 old events (3 days ago), 10 recent (today)
		var ts time.Time
		if i < 10 {
			ts = now.Add(-3 * 24 * time.Hour).Add(time.Duration(i) * time.Second)
		} else {
			ts = now.Add(-1 * time.Hour).Add(time.Duration(i) * time.Second)
		}
		timestamps = append(timestamps, ts)
		jsonlRows = append(jsonlRows, map[string]string{
			"event_id":  fmt.Sprintf("evt_%02d", i),
			"timestamp": ts.Format(time.RFC3339Nano),
		})
	}
	journalPath := filepath.Join(thrumDir, "events.jsonl")
	writeJSONLFile(t, journalPath, jsonlRows)

	db := openTestDB(t)
	seedEventsTable(t, db, timestamps)

	// Seed messages-v2 with some duplicate rows
	var msgRows []map[string]string
	for i := 0; i < 10; i++ {
		msgID := fmt.Sprintf("msg_%02d", i)
		msgRows = append(msgRows, map[string]string{
			"message_id": msgID,
			"body":       "first",
		})
		msgRows = append(msgRows, map[string]string{
			"message_id": msgID,
			"body":       "latest",
		})
	}
	msgDir := filepath.Join(syncDir, "messages-v2")
	msgFile := filepath.Join(msgDir, agentID+".jsonl")
	writeJSONLFile(t, msgFile, msgRows)

	// Seed receipts with some duplicate rows
	var recRows []map[string]string
	for i := 0; i < 10; i++ {
		msgID := fmt.Sprintf("msg_%02d", i)
		recRows = append(recRows, map[string]string{
			"message_id": msgID,
			"agent_id":   agentID,
			"read_at":    now.Add(-2 * time.Hour).Format(time.RFC3339Nano),
		})
		recRows = append(recRows, map[string]string{
			"message_id": msgID,
			"agent_id":   agentID,
			"read_at":    now.Add(-1 * time.Hour).Format(time.RFC3339Nano),
		})
	}
	recDir := filepath.Join(syncDir, "receipts")
	recFile := filepath.Join(recDir, agentID+".jsonl")
	writeJSONLFile(t, recFile, recRows)

	c := compact.New(thrumDir, syncDir, 2, 0)
	ctx := context.Background()

	// First run
	if err := c.CompactAll(ctx, db); err != nil {
		t.Fatalf("CompactAll (first): %v", err)
	}

	// Capture state after first run
	journalCount1 := countJSONLLines(t, journalPath)
	sqlCount1 := countEventsTable(t, db)
	msgCount1 := countJSONLLines(t, msgFile)
	recCount1 := countJSONLLines(t, recFile)

	// Second run — must be idempotent
	if err := c.CompactAll(ctx, db); err != nil {
		t.Fatalf("CompactAll (second): %v", err)
	}

	journalCount2 := countJSONLLines(t, journalPath)
	sqlCount2 := countEventsTable(t, db)
	msgCount2 := countJSONLLines(t, msgFile)
	recCount2 := countJSONLLines(t, recFile)

	if journalCount1 != journalCount2 {
		t.Errorf("journal changed on second run: %d → %d", journalCount1, journalCount2)
	}
	if sqlCount1 != sqlCount2 {
		t.Errorf("SQLite count changed on second run: %d → %d", sqlCount1, sqlCount2)
	}
	if msgCount1 != msgCount2 {
		t.Errorf("messages-v2 changed on second run: %d → %d", msgCount1, msgCount2)
	}
	if recCount1 != recCount2 {
		t.Errorf("receipts changed on second run: %d → %d", recCount1, recCount2)
	}

	// Sanity: first run DID compact something
	if journalCount1 >= 20 {
		t.Errorf("journal should have been trimmed on first run, got %d rows", journalCount1)
	}
	if msgCount1 >= 20 {
		t.Errorf("messages-v2 should have been deduped on first run, got %d rows", msgCount1)
	}
	if recCount1 >= 20 {
		t.Errorf("receipts should have been deduped on first run, got %d rows", recCount1)
	}

	// Parity: journal == SQLite after both runs
	if journalCount2 != sqlCount2 {
		t.Errorf("parity failure after second run: journal=%d SQLite=%d", journalCount2, sqlCount2)
	}
}

// TestCompactor_CompactMessageStateFile_BelowThresholdSkips verifies that when a
// messages-v2 file is smaller than sizeThresholdBytes, CompactMessageStateFile
// does NOT rewrite the file (returns 0 bytes saved, mtime unchanged).
func TestCompactor_CompactMessageStateFile_BelowThresholdSkips(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	syncDir := filepath.Join(dir, "sync")
	agentID := "agt_threshold01"

	// Write a small file (well below any realistic threshold)
	var rows []map[string]string
	for i := 0; i < 5; i++ {
		rows = append(rows, map[string]string{
			"message_id": fmt.Sprintf("msg_%02d", i),
			"body":       "hello",
		})
	}
	msgDir := filepath.Join(syncDir, "messages-v2")
	filePath := filepath.Join(msgDir, agentID+".jsonl")
	writeJSONLFile(t, filePath, rows)

	// Stat the file before
	infoBefore, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	// Use a very large threshold (1 GB) so the small file is always below it.
	largeThresholdMB := 1024
	c := compact.New(thrumDir, syncDir, 2, largeThresholdMB)
	ctx := context.Background()

	saved, err := c.CompactMessageStateFile(ctx, agentID)
	if err != nil {
		t.Fatalf("CompactMessageStateFile: %v", err)
	}

	if saved != 0 {
		t.Errorf("expected 0 bytes saved (below threshold), got %d", saved)
	}

	// Verify mtime unchanged (file not rewritten)
	infoAfter, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		t.Errorf("file mtime changed from %v to %v (should not have been rewritten)",
			infoBefore.ModTime(), infoAfter.ModTime())
	}

	// Row count unchanged
	postCount := countJSONLLines(t, filePath)
	if postCount != 5 {
		t.Errorf("row count changed: got %d, want 5", postCount)
	}
}

// TestCompactor_CompactReceiptStateFile_BelowThresholdSkips mirrors the message
// file below-threshold test for receipts.
func TestCompactor_CompactReceiptStateFile_BelowThresholdSkips(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	syncDir := filepath.Join(dir, "sync")
	issuerID := "agt_thresh_rec01"

	var rows []map[string]string
	for i := 0; i < 3; i++ {
		rows = append(rows, map[string]string{
			"message_id": fmt.Sprintf("msg_%02d", i),
			"agent_id":   issuerID,
			"read_at":    time.Now().Format(time.RFC3339Nano),
		})
	}
	recDir := filepath.Join(syncDir, "receipts")
	filePath := filepath.Join(recDir, issuerID+".jsonl")
	writeJSONLFile(t, filePath, rows)

	infoBefore, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	largeThresholdMB := 1024
	c := compact.New(thrumDir, syncDir, 2, largeThresholdMB)
	ctx := context.Background()

	saved, err := c.CompactReceiptStateFile(ctx, issuerID)
	if err != nil {
		t.Fatalf("CompactReceiptStateFile: %v", err)
	}

	if saved != 0 {
		t.Errorf("expected 0 bytes saved (below threshold), got %d", saved)
	}

	infoAfter, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		t.Errorf("file mtime changed (should not have been rewritten)")
	}
}
