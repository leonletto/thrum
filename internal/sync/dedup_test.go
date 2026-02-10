package sync

import (
	"encoding/json"
	"testing"
)

func TestDeduplicateEvents(t *testing.T) {
	tests := []struct {
		name     string
		input    []*Event
		expected int
	}{
		{
			name:     "empty slice",
			input:    []*Event{},
			expected: 0,
		},
		{
			name: "no duplicates",
			input: []*Event{
				{ID: "event-1", Type: "message.create"},
				{ID: "event-2", Type: "message.create"},
				{ID: "event-3", Type: "message.create"},
			},
			expected: 3,
		},
		{
			name: "with duplicates",
			input: []*Event{
				{ID: "event-1", Type: "message.create"},
				{ID: "event-2", Type: "message.create"},
				{ID: "event-1", Type: "message.create"}, // Duplicate
				{ID: "event-3", Type: "message.create"},
				{ID: "event-2", Type: "message.create"}, // Duplicate
			},
			expected: 3, // Should have 3 unique events
		},
		{
			name: "all duplicates",
			input: []*Event{
				{ID: "event-1", Type: "message.create"},
				{ID: "event-1", Type: "message.create"},
				{ID: "event-1", Type: "message.create"},
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DeduplicateEvents(tt.input)
			if len(result) != tt.expected {
				t.Errorf("Expected %d events, got %d", tt.expected, len(result))
			}

			// Verify no duplicates in result
			seen := make(map[string]bool)
			for _, event := range result {
				if seen[event.ID] {
					t.Errorf("Duplicate ID %q in result", event.ID)
				}
				seen[event.ID] = true
			}
		})
	}
}

func TestDeduplicateEvents_PreservesOrder(t *testing.T) {
	input := []*Event{
		{ID: "event-1", Type: "message.create", Timestamp: "2024-01-01T00:00:00Z"},
		{ID: "event-2", Type: "message.create", Timestamp: "2024-01-02T00:00:00Z"},
		{ID: "event-1", Type: "message.create", Timestamp: "2024-01-03T00:00:00Z"}, // Duplicate
		{ID: "event-3", Type: "message.create", Timestamp: "2024-01-04T00:00:00Z"},
	}

	result := DeduplicateEvents(input)

	// Should preserve first occurrence
	if len(result) != 3 {
		t.Fatalf("Expected 3 events, got %d", len(result))
	}

	// Check order and first occurrence
	expected := []string{"event-1", "event-2", "event-3"}
	for i, event := range result {
		if event.ID != expected[i] {
			t.Errorf("Position %d: expected ID %q, got %q", i, expected[i], event.ID)
		}
	}

	// Verify first occurrence was kept (by timestamp)
	if result[0].Timestamp != "2024-01-01T00:00:00Z" {
		t.Errorf("Expected first occurrence to be kept, got timestamp %q", result[0].Timestamp)
	}
}

func TestEventsEqual(t *testing.T) {
	tests := []struct {
		name     string
		a        *Event
		b        *Event
		expected bool
	}{
		{
			name:     "both nil",
			a:        nil,
			b:        nil,
			expected: true,
		},
		{
			name:     "first nil",
			a:        nil,
			b:        &Event{ID: "event-1"},
			expected: false,
		},
		{
			name:     "second nil",
			a:        &Event{ID: "event-1"},
			b:        nil,
			expected: false,
		},
		{
			name:     "same ID",
			a:        &Event{ID: "event-1", Type: "message.create"},
			b:        &Event{ID: "event-1", Type: "message.create"},
			expected: true,
		},
		{
			name:     "different IDs",
			a:        &Event{ID: "event-1", Type: "message.create"},
			b:        &Event{ID: "event-2", Type: "message.create"},
			expected: false,
		},
		{
			name:     "same ID different types",
			a:        &Event{ID: "event-1", Type: "message.create"},
			b:        &Event{ID: "event-1", Type: "message.edit"},
			expected: true, // Events are equal by ID alone
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EventsEqual(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestDeduplicateRawEvents(t *testing.T) {
	tests := []struct {
		name     string
		input    []json.RawMessage
		expected int
	}{
		{
			name:     "empty slice",
			input:    []json.RawMessage{},
			expected: 0,
		},
		{
			name: "no duplicates",
			input: []json.RawMessage{
				json.RawMessage(`{"type":"message.create","event_id":"evt_001","message_id":"msg-1","timestamp":"2024-01-01T00:00:00Z"}`),
				json.RawMessage(`{"type":"message.create","event_id":"evt_002","message_id":"msg-2","timestamp":"2024-01-02T00:00:00Z"}`),
			},
			expected: 2,
		},
		{
			name: "with duplicates",
			input: []json.RawMessage{
				json.RawMessage(`{"type":"message.create","event_id":"evt_001","message_id":"msg-1","timestamp":"2024-01-01T00:00:00Z"}`),
				json.RawMessage(`{"type":"message.create","event_id":"evt_002","message_id":"msg-2","timestamp":"2024-01-02T00:00:00Z"}`),
				json.RawMessage(`{"type":"message.create","event_id":"evt_001","message_id":"msg-1","timestamp":"2024-01-03T00:00:00Z"}`), // Duplicate
			},
			expected: 2,
		},
		{
			name: "invalid JSON",
			input: []json.RawMessage{
				json.RawMessage(`{invalid json`),
				json.RawMessage(`{"type":"message.create","event_id":"evt_001","message_id":"msg-1","timestamp":"2024-01-01T00:00:00Z"}`),
			},
			expected: 1, // Invalid JSON is skipped
		},
		{
			name: "missing ID field",
			input: []json.RawMessage{
				json.RawMessage(`{"type":"message.create","timestamp":"2024-01-01T00:00:00Z"}`), // Missing event_id
				json.RawMessage(`{"type":"message.create","event_id":"evt_001","message_id":"msg-1","timestamp":"2024-01-02T00:00:00Z"}`),
			},
			expected: 1, // Events without ID are skipped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := DeduplicateRawEvents(tt.input)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(result) != tt.expected {
				t.Errorf("Expected %d events, got %d", tt.expected, len(result))
			}
		})
	}
}

func TestContainsEvent(t *testing.T) {
	events := []*Event{
		{ID: "event-1", Type: "message.create"},
		{ID: "event-2", Type: "message.create"},
		{ID: "event-3", Type: "message.create"},
	}

	tests := []struct {
		name     string
		events   []*Event
		id       string
		expected bool
	}{
		{
			name:     "empty slice",
			events:   []*Event{},
			id:       "event-1",
			expected: false,
		},
		{
			name:     "event exists",
			events:   events,
			id:       "event-2",
			expected: true,
		},
		{
			name:     "event does not exist",
			events:   events,
			id:       "event-99",
			expected: false,
		},
		{
			name:     "empty ID",
			events:   events,
			id:       "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ContainsEvent(tt.events, tt.id)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestDeduplicateEvents_DoesNotModifyInput(t *testing.T) {
	input := []*Event{
		{ID: "event-1", Type: "message.create"},
		{ID: "event-2", Type: "message.create"},
		{ID: "event-1", Type: "message.create"}, // Duplicate
	}

	originalLen := len(input)

	_ = DeduplicateEvents(input)

	// Verify input wasn't modified
	if len(input) != originalLen {
		t.Errorf("Input slice was modified: expected length %d, got %d", originalLen, len(input))
	}
}
