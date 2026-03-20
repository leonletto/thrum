package telegram

import (
	"fmt"
	"sync"
)

// MessageMap maintains a bidirectional mapping between Telegram message IDs
// and Thrum message IDs. Used for threading: when a Telegram user replies
// to a message, we look up the corresponding Thrum message_id to set reply_to,
// and vice versa for outbound relay.
//
// Bounded at maxSize entries with FIFO eviction to prevent unbounded growth.
type MessageMap struct {
	mu          sync.RWMutex
	teleToThrum map[string]string // "chatID:msgID" → thrum_message_id
	thrumToTele map[string]string // thrum_message_id → "chatID:msgID"
	order       []string          // insertion order keys (teleToThrum keys) for eviction
	maxSize     int
}

// NewMessageMap creates a new bounded message ID map.
func NewMessageMap(maxSize int) *MessageMap {
	return &MessageMap{
		teleToThrum: make(map[string]string, maxSize),
		thrumToTele: make(map[string]string, maxSize),
		order:       make([]string, 0, maxSize),
		maxSize:     maxSize,
	}
}

func teleKey(chatID int64, msgID int) string {
	return fmt.Sprintf("%d:%d", chatID, msgID)
}

// Store records a bidirectional mapping. Evicts the oldest entry if at capacity.
func (m *MessageMap) Store(chatID int64, teleMsgID int, thrumMsgID string) {
	key := teleKey(chatID, teleMsgID)

	m.mu.Lock()
	defer m.mu.Unlock()

	// If key already exists, update the mapping without appending to order
	if oldThrumID, exists := m.teleToThrum[key]; exists {
		delete(m.thrumToTele, oldThrumID)
		m.teleToThrum[key] = thrumMsgID
		m.thrumToTele[thrumMsgID] = key
		return
	}

	// Evict oldest if at capacity
	if len(m.order) >= m.maxSize {
		oldest := m.order[0]
		m.order = m.order[1:]
		if thrumID, ok := m.teleToThrum[oldest]; ok {
			delete(m.thrumToTele, thrumID)
		}
		delete(m.teleToThrum, oldest)
	}

	m.teleToThrum[key] = thrumMsgID
	m.thrumToTele[thrumMsgID] = key
	m.order = append(m.order, key)
}

// ThrumID looks up the Thrum message ID for a given Telegram message.
func (m *MessageMap) ThrumID(chatID int64, teleMsgID int) (string, bool) {
	key := teleKey(chatID, teleMsgID)
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.teleToThrum[key]
	return id, ok
}

// TeleID looks up the Telegram chat ID and message ID for a given Thrum message.
func (m *MessageMap) TeleID(thrumMsgID string) (chatID int64, msgID int, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key, found := m.thrumToTele[thrumMsgID]
	if !found {
		return 0, 0, false
	}
	_, err := fmt.Sscanf(key, "%d:%d", &chatID, &msgID)
	return chatID, msgID, err == nil
}

// Len returns the current number of stored mappings.
func (m *MessageMap) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.teleToThrum)
}
