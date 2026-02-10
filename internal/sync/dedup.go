package sync

import "encoding/json"

// DeduplicateEvents removes duplicate events by event_id.
// The input slice is not modified; a new slice is returned.
// When duplicates are found (same event_id), the first occurrence is kept.
func DeduplicateEvents(events []*Event) []*Event {
	if len(events) == 0 {
		return events
	}

	seen := make(map[string]bool, len(events))
	result := make([]*Event, 0, len(events))

	for _, event := range events {
		if !seen[event.ID] {
			seen[event.ID] = true
			result = append(result, event)
		}
	}

	return result
}

// EventsEqual checks if two events are the same (by event_id).
// Events are considered equal if they have the same event_id.
// This follows the immutable event model where events with the same event_id
// are guaranteed to have identical content.
func EventsEqual(a, b *Event) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID
}

// DeduplicateRawEvents removes duplicate events from raw JSON messages by event_id.
// This is a convenience function that parses events, deduplicates by event_id,
// and returns the deduplicated raw messages.
func DeduplicateRawEvents(messages []json.RawMessage) ([]json.RawMessage, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	// Parse events to extract event_ids
	seen := make(map[string]bool, len(messages))
	result := make([]json.RawMessage, 0, len(messages))

	for _, msg := range messages {
		// Extract event type and event_id
		var base struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(msg, &base); err != nil {
			// Skip malformed events
			continue
		}

		id, err := extractEventID(msg, base.Type)
		if err != nil {
			// Skip events without valid event_id
			continue
		}

		if !seen[id] {
			seen[id] = true
			result = append(result, msg)
		}
	}

	return result, nil
}

// ContainsEvent checks if a slice of events contains an event with the given event_id.
func ContainsEvent(events []*Event, id string) bool {
	for _, event := range events {
		if event.ID == id {
			return true
		}
	}
	return false
}
