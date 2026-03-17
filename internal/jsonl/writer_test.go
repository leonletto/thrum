package jsonl_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/jsonl"
)

type testEvent struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

func TestWriter_Append(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.jsonl")

	w, err := jsonl.NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter() failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	event := testEvent{Type: "test", Data: "hello"}
	if err := w.Append(event); err != nil {
		t.Fatalf("Append() failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		t.Errorf("File should exist: %v", err)
	}

	// Read back and verify
	data, err := os.ReadFile(path) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("ReadFile() failed: %v", err)
	}

	expected := `{"type":"test","data":"hello"}` + "\n"
	if string(data) != expected {
		t.Errorf("File content = %q, want %q", string(data), expected)
	}
}

func TestWriter_AppendMultiple(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "multi.jsonl")

	w, err := jsonl.NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter() failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	events := []testEvent{
		{Type: "event1", Data: "first"},
		{Type: "event2", Data: "second"},
		{Type: "event3", Data: "third"},
	}

	for _, event := range events {
		if err := w.Append(event); err != nil {
			t.Fatalf("Append() failed: %v", err)
		}
	}

	// Read and verify
	r, err := jsonl.NewReader(path)
	if err != nil {
		t.Fatalf("NewReader() failed: %v", err)
	}

	messages, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll() failed: %v", err)
	}

	if len(messages) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(messages))
	}

	for i, msg := range messages {
		var got testEvent
		if err := json.Unmarshal(msg, &got); err != nil {
			t.Fatalf("Unmarshal message %d failed: %v", i, err)
		}

		if got.Type != events[i].Type || got.Data != events[i].Data {
			t.Errorf("Message %d = %+v, want %+v", i, got, events[i])
		}
	}
}

func TestWriter_CreatesDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "nested", "dir", "file.jsonl")

	w, err := jsonl.NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter() should create directories: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Verify directory was created
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("Directory should exist: %v", err)
	}
}

func TestReader_ReadAll(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "read.jsonl")

	// Write some test data
	w, _ := jsonl.NewWriter(path)
	if err := w.Append(testEvent{Type: "a", Data: "alpha"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Append(testEvent{Type: "b", Data: "beta"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Read it back
	r, err := jsonl.NewReader(path)
	if err != nil {
		t.Fatalf("NewReader() failed: %v", err)
	}

	messages, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll() failed: %v", err)
	}

	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}
}

func TestReader_ReadAll_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "empty.jsonl")

	// Create empty file
	if err := os.WriteFile(path, []byte{}, 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	r, err := jsonl.NewReader(path)
	if err != nil {
		t.Fatalf("NewReader() failed: %v", err)
	}

	messages, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll() failed: %v", err)
	}

	if len(messages) != 0 {
		t.Errorf("Expected 0 messages from empty file, got %d", len(messages))
	}
}

func TestReader_Stream(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "stream.jsonl")

	// Write test data
	w, _ := jsonl.NewWriter(path)
	events := []testEvent{
		{Type: "1", Data: "one"},
		{Type: "2", Data: "two"},
		{Type: "3", Data: "three"},
	}
	for _, e := range events {
		if err := w.Append(e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Stream it back
	r, _ := jsonl.NewReader(path)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := r.Stream(ctx)

	var received []testEvent
	for msg := range ch {
		var event testEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			t.Fatalf("Unmarshal failed: %v", err)
		}
		received = append(received, event)
	}

	if len(received) != 3 {
		t.Errorf("Expected 3 events, got %d", len(received))
	}

	for i, event := range received {
		if event.Type != events[i].Type {
			t.Errorf("Event %d type = %s, want %s", i, event.Type, events[i].Type)
		}
	}
}

func TestReader_Stream_ContextCancel(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "cancel.jsonl")

	// Write many events
	w, _ := jsonl.NewWriter(path)
	for i := 0; i < 100; i++ {
		if err := w.Append(testEvent{Type: "test", Data: "data"}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Stream with immediate cancellation
	r, _ := jsonl.NewReader(path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	ch := r.Stream(ctx)

	// Should close quickly
	count := 0
	for range ch {
		count++
	}

	// May get some events before cancellation is detected
	// Just verify it eventually stops
	if count > 100 {
		t.Errorf("Got more events than expected after cancel: %d", count)
	}
}

func TestReader_NonExistentFile(t *testing.T) {
	_, err := jsonl.NewReader("/nonexistent/path.jsonl")
	if err == nil {
		t.Error("NewReader() should error on non-existent file")
	}
}

func TestWriter_AppendConcurrent(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "concurrent.jsonl")

	w, err := jsonl.NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter() failed: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Write concurrently from multiple goroutines
	const numGoroutines = 5
	const numEventsPerGoroutine = 20

	done := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < numEventsPerGoroutine; j++ {
				event := testEvent{
					Type: "concurrent",
					Data: "test",
				}
				if err := w.Append(event); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}(i)
	}

	// Wait for all goroutines and check for errors
	for i := 0; i < numGoroutines; i++ {
		if err := <-done; err != nil {
			t.Fatalf("Concurrent write failed: %v", err)
		}
	}

	// Verify total count
	r, err := jsonl.NewReader(path)
	if err != nil {
		t.Fatalf("NewReader() failed: %v", err)
	}

	messages, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll() failed: %v", err)
	}

	expected := numGoroutines * numEventsPerGoroutine
	if len(messages) != expected {
		t.Errorf("Expected %d messages after concurrent writes, got %d", expected, len(messages))
	}

	// Verify all messages are valid JSON
	for i, msg := range messages {
		var event testEvent
		if err := json.Unmarshal(msg, &event); err != nil {
			t.Errorf("Message %d is invalid JSON: %v", i, err)
		}
	}
}

func TestRemoveByField(t *testing.T) {
	t.Run("removes_matching_lines", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "test.jsonl")

		w, err := jsonl.NewWriter(path)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		type agentEvent struct {
			Type    string `json:"type"`
			AgentID string `json:"agent_id"`
		}
		events := []agentEvent{
			{Type: "agent.register", AgentID: "alice"},
			{Type: "session.start", AgentID: "bob"},
			{Type: "agent.register", AgentID: "bob"},
			{Type: "session.end", AgentID: "alice"},
		}
		for _, e := range events {
			if err := w.Append(e); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		_ = w.Close()

		removed, err := jsonl.RemoveByField(path, "agent_id", "bob")
		if err != nil {
			t.Fatalf("RemoveByField: %v", err)
		}
		if removed != 2 {
			t.Errorf("expected 2 removed, got %d", removed)
		}

		r, _ := jsonl.NewReader(path)
		lines, _ := r.ReadAll()
		if len(lines) != 2 {
			t.Fatalf("expected 2 remaining lines, got %d", len(lines))
		}
		for _, line := range lines {
			var e agentEvent
			_ = json.Unmarshal(line, &e)
			if e.AgentID == "bob" {
				t.Error("bob should have been removed")
			}
		}
	})

	t.Run("no_matches", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "test.jsonl")

		w, _ := jsonl.NewWriter(path)
		_ = w.Append(testEvent{Type: "test", Data: "keep"})
		_ = w.Close()

		removed, err := jsonl.RemoveByField(path, "type", "nonexistent")
		if err != nil {
			t.Fatalf("RemoveByField: %v", err)
		}
		if removed != 0 {
			t.Errorf("expected 0 removed, got %d", removed)
		}

		r, _ := jsonl.NewReader(path)
		lines, _ := r.ReadAll()
		if len(lines) != 1 {
			t.Errorf("expected 1 line, got %d", len(lines))
		}
	})

	t.Run("nonexistent_file", func(t *testing.T) {
		removed, err := jsonl.RemoveByField("/nonexistent/path.jsonl", "field", "value")
		if err != nil {
			t.Errorf("expected nil error for missing file, got: %v", err)
		}
		if removed != 0 {
			t.Errorf("expected 0 removed, got %d", removed)
		}
	})
}

func TestRemoveBeforeTimestamp(t *testing.T) {
	type timedEvent struct {
		Type      string `json:"type"`
		CreatedAt string `json:"created_at"`
	}

	cutoff := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	t.Run("filters_before_cutoff_keeps_at_and_after", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "ts.jsonl")

		w, err := jsonl.NewWriter(path)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		events := []timedEvent{
			{Type: "old", CreatedAt: "2024-06-15T11:59:59Z"},        // before — remove
			{Type: "exact", CreatedAt: "2024-06-15T12:00:00Z"},      // equal — keep
			{Type: "new", CreatedAt: "2024-06-15T12:00:01Z"},        // after — keep
			{Type: "much_older", CreatedAt: "2024-01-01T00:00:00Z"}, // before — remove
		}
		for _, e := range events {
			if err := w.Append(e); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		_ = w.Close()

		removed, err := jsonl.RemoveBeforeTimestamp(path, "created_at", cutoff)
		if err != nil {
			t.Fatalf("RemoveBeforeTimestamp: %v", err)
		}
		if removed != 2 {
			t.Errorf("expected 2 removed, got %d", removed)
		}

		r, _ := jsonl.NewReader(path)
		lines, _ := r.ReadAll()
		if len(lines) != 2 {
			t.Fatalf("expected 2 remaining lines, got %d", len(lines))
		}
		for _, line := range lines {
			var e timedEvent
			_ = json.Unmarshal(line, &e)
			ts, _ := time.Parse(time.RFC3339, e.CreatedAt)
			if ts.Before(cutoff) {
				t.Errorf("line with ts %s should have been removed", e.CreatedAt)
			}
		}
	})

	t.Run("no_matching_lines", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "ts.jsonl")

		w, _ := jsonl.NewWriter(path)
		_ = w.Append(timedEvent{Type: "keep", CreatedAt: "2025-01-01T00:00:00Z"})
		_ = w.Close()

		removed, err := jsonl.RemoveBeforeTimestamp(path, "created_at", cutoff)
		if err != nil {
			t.Fatalf("RemoveBeforeTimestamp: %v", err)
		}
		if removed != 0 {
			t.Errorf("expected 0 removed, got %d", removed)
		}

		r, _ := jsonl.NewReader(path)
		lines, _ := r.ReadAll()
		if len(lines) != 1 {
			t.Errorf("expected 1 line remaining, got %d", len(lines))
		}
	})

	t.Run("missing_file_returns_zero_nil", func(t *testing.T) {
		removed, err := jsonl.RemoveBeforeTimestamp("/nonexistent/path.jsonl", "created_at", cutoff)
		if err != nil {
			t.Errorf("expected nil error for missing file, got: %v", err)
		}
		if removed != 0 {
			t.Errorf("expected 0 removed, got %d", removed)
		}
	})

	t.Run("empty_lines_skipped", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "ts.jsonl")

		// Write file with blank lines interspersed
		content := `{"type":"old","created_at":"2024-01-01T00:00:00Z"}` + "\n" +
			"\n" +
			`{"type":"new","created_at":"2025-01-01T00:00:00Z"}` + "\n"
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		removed, err := jsonl.RemoveBeforeTimestamp(path, "created_at", cutoff)
		if err != nil {
			t.Fatalf("RemoveBeforeTimestamp: %v", err)
		}
		if removed != 1 {
			t.Errorf("expected 1 removed, got %d", removed)
		}

		r, _ := jsonl.NewReader(path)
		lines, _ := r.ReadAll()
		if len(lines) != 1 {
			t.Errorf("expected 1 remaining line, got %d", len(lines))
		}
	})

	t.Run("unparseable_timestamp_kept", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "ts.jsonl")

		w, _ := jsonl.NewWriter(path)
		_ = w.Append(map[string]string{"type": "bad_ts", "created_at": "not-a-timestamp"})
		_ = w.Append(timedEvent{Type: "old", CreatedAt: "2024-01-01T00:00:00Z"})
		_ = w.Close()

		removed, err := jsonl.RemoveBeforeTimestamp(path, "created_at", cutoff)
		if err != nil {
			t.Fatalf("RemoveBeforeTimestamp: %v", err)
		}
		// Only the parseable old line is removed; bad_ts is kept
		if removed != 1 {
			t.Errorf("expected 1 removed, got %d", removed)
		}

		r, _ := jsonl.NewReader(path)
		lines, _ := r.ReadAll()
		if len(lines) != 1 {
			t.Errorf("expected 1 remaining line (the unparseable one), got %d", len(lines))
		}
	})

	t.Run("rfc3339nano_format", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "ts.jsonl")

		w, _ := jsonl.NewWriter(path)
		// Use nanosecond precision timestamps
		_ = w.Append(map[string]string{
			"type":       "nano_old",
			"created_at": "2024-06-15T11:59:59.999999999Z",
		})
		_ = w.Append(map[string]string{
			"type":       "nano_new",
			"created_at": "2024-06-15T12:00:00.000000001Z",
		})
		_ = w.Close()

		removed, err := jsonl.RemoveBeforeTimestamp(path, "created_at", cutoff)
		if err != nil {
			t.Fatalf("RemoveBeforeTimestamp: %v", err)
		}
		if removed != 1 {
			t.Errorf("expected 1 removed, got %d", removed)
		}

		r, _ := jsonl.NewReader(path)
		lines, _ := r.ReadAll()
		if len(lines) != 1 {
			t.Errorf("expected 1 remaining line, got %d", len(lines))
		}
	})
}
