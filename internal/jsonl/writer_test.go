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
