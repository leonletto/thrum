package backup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SyncExportResult holds counts from a sync data export.
type SyncExportResult struct {
	EventLines   int
	MessageFiles int
}

// ExportSyncData copies JSONL event logs from the sync worktree to the backup directory.
// Layout: syncDir/events.jsonl → backupDir/events.jsonl
//
//	syncDir/messages/*.jsonl → backupDir/messages/*.jsonl
func ExportSyncData(syncDir, backupDir string) (SyncExportResult, error) {
	var result SyncExportResult

	// Verify sync dir exists
	if _, err := os.Stat(syncDir); err != nil {
		return result, fmt.Errorf("sync directory not found: %w", err)
	}

	// Copy events.jsonl
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	if _, err := os.Stat(eventsPath); err == nil {
		lines, err := atomicCopyFile(eventsPath, filepath.Join(backupDir, "events.jsonl"))
		if err != nil {
			return result, fmt.Errorf("copy events.jsonl: %w", err)
		}
		result.EventLines = lines
	}

	// Copy messages/*.jsonl
	messagesDir := filepath.Join(syncDir, "messages")
	entries, err := os.ReadDir(messagesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil // no messages dir is fine
		}
		return result, fmt.Errorf("read messages dir: %w", err)
	}

	backupMsgDir := filepath.Join(backupDir, "messages")
	if err := os.MkdirAll(backupMsgDir, 0750); err != nil {
		return result, fmt.Errorf("create messages backup dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		src := filepath.Join(messagesDir, entry.Name())
		dst := filepath.Join(backupMsgDir, entry.Name())
		if _, err := atomicCopyFile(src, dst); err != nil {
			return result, fmt.Errorf("copy %s: %w", entry.Name(), err)
		}
		result.MessageFiles++
	}

	return result, nil
}

// atomicCopyFile copies src to dst using a temp file and rename.
// Returns the number of newline-delimited lines copied.
func atomicCopyFile(src, dst string) (int, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return 0, err
	}

	srcFile, err := os.Open(src) //nolint:gosec // G304 - paths from internal thrum dirs
	if err != nil {
		return 0, err
	}
	defer func() { _ = srcFile.Close() }()

	tmpPath := dst + ".tmp"
	tmpFile, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600) //nolint:gosec // G304
	if err != nil {
		return 0, err
	}

	// Use a counting writer to count newlines during the copy
	cw := &lineCountWriter{w: tmpFile}
	if _, err := io.Copy(cw, srcFile); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return 0, err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return 0, err
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return 0, err
	}

	return cw.lines, nil
}

// lineCountWriter wraps a writer and counts newlines during writes.
type lineCountWriter struct {
	w     io.Writer
	lines int
}

func (c *lineCountWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	for _, b := range p[:n] {
		if b == '\n' {
			c.lines++
		}
	}
	return n, err
}
