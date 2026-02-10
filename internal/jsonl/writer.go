package jsonl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Writer provides append-only JSONL writing with file locking.
type Writer struct {
	path string
	mu   sync.Mutex
}

// NewWriter creates a new JSONL writer for the given path.
// Creates the file and parent directories if they don't exist.
func NewWriter(path string) (*Writer, error) {
	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	// Create file if it doesn't exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600) //nolint:gosec // G304 - path from internal JSONL config
		if err != nil {
			return nil, fmt.Errorf("create file: %w", err)
		}
		_ = f.Close()
	}

	return &Writer{path: path}, nil
}

// Append marshals the event to JSON and appends it to the file atomically.
// Uses file locking and atomic write-then-rename for safety.
func (w *Writer) Append(event any) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Marshal to JSON
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}

	// Append newline
	data = append(data, '\n')

	// Write to temporary file first (atomic operation)
	tmpPath := w.path + ".tmp"
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) //nolint:gosec // G304 - path from internal JSONL config
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpPath) }() // Clean up temp file in case of error

	// Acquire exclusive lock on temp file
	if err := syscall.Flock(int(tmpFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("lock temp file: %w", err)
	}

	// Write the new line to temp file
	if _, err := tmpFile.Write(data); err != nil {
		_ = syscall.Flock(int(tmpFile.Fd()), syscall.LOCK_UN)
		_ = tmpFile.Close()
		return fmt.Errorf("write to temp file: %w", err)
	}

	// Sync to disk
	if err := tmpFile.Sync(); err != nil {
		_ = syscall.Flock(int(tmpFile.Fd()), syscall.LOCK_UN)
		_ = tmpFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}

	// Release lock and close temp file
	_ = syscall.Flock(int(tmpFile.Fd()), syscall.LOCK_UN)
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// Now append temp file contents to main file
	mainFile, err := os.OpenFile(w.path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open main file: %w", err)
	}
	defer func() { _ = mainFile.Close() }()

	// Acquire exclusive lock on main file
	if err := syscall.Flock(int(mainFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock main file: %w", err)
	}
	defer func() { _ = syscall.Flock(int(mainFile.Fd()), syscall.LOCK_UN) }()

	// Read temp file and append to main
	tmpData, err := os.ReadFile(tmpPath) //nolint:gosec // G304 - path from internal JSONL config
	if err != nil {
		return fmt.Errorf("read temp file: %w", err)
	}

	if _, err := mainFile.Write(tmpData); err != nil {
		return fmt.Errorf("append to main file: %w", err)
	}

	// Sync main file to disk
	if err := mainFile.Sync(); err != nil {
		return fmt.Errorf("sync main file: %w", err)
	}

	return nil
}

// Close is a no-op for Writer as it doesn't hold file handles.
func (w *Writer) Close() error {
	return nil
}

// Reader provides reading from JSONL files.
type Reader struct {
	path string
}

// NewReader creates a new JSONL reader for the given path.
func NewReader(path string) (*Reader, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}
	return &Reader{path: path}, nil
}

// ReadAll reads all lines from the JSONL file.
func (r *Reader) ReadAll() ([]json.RawMessage, error) {
	file, err := os.Open(r.path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Acquire shared lock for reading
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH); err != nil {
		return nil, fmt.Errorf("lock file: %w", err)
	}
	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()

	var messages []json.RawMessage
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue // Skip empty lines
		}

		// Create a copy of the line (scanner reuses buffer)
		msg := make(json.RawMessage, len(line))
		copy(msg, line)
		messages = append(messages, msg)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan file: %w", err)
	}

	return messages, nil
}

// Stream reads lines from the JSONL file and sends them to a channel.
// Closes the channel when done or context is canceled.
func (r *Reader) Stream(ctx context.Context) <-chan json.RawMessage {
	ch := make(chan json.RawMessage)

	go func() {
		defer close(ch)

		file, err := os.Open(r.path)
		if err != nil {
			return
		}
		defer func() { _ = file.Close() }()

		// Acquire shared lock for reading
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH); err != nil {
			return
		}
		defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()

		scanner := bufio.NewScanner(file)

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			// Create a copy of the line
			msg := make(json.RawMessage, len(line))
			copy(msg, line)

			select {
			case ch <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch
}
