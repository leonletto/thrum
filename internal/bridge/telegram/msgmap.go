package telegram

import (
	"fmt"

	"github.com/leonletto/thrum/internal/bridge"
)

// MessageMap wraps the shared bridge.MessageMap with Telegram-specific
// int64/int key formatting.
type MessageMap struct {
	inner *bridge.MessageMap
}

// NewMessageMap creates a new bounded message ID map.
func NewMessageMap(maxSize int) *MessageMap {
	return &MessageMap{inner: bridge.NewMessageMap(maxSize)}
}

func teleKey(chatID int64, msgID int) string {
	return fmt.Sprintf("%d:%d", chatID, msgID)
}

// Store records a bidirectional mapping. Evicts the oldest entry if at capacity.
func (m *MessageMap) Store(chatID int64, teleMsgID int, thrumMsgID string) {
	m.inner.Store(teleKey(chatID, teleMsgID), thrumMsgID)
}

// ThrumID looks up the Thrum message ID for a given Telegram message.
func (m *MessageMap) ThrumID(chatID int64, teleMsgID int) (string, bool) {
	return m.inner.ThrumID(teleKey(chatID, teleMsgID))
}

// TeleID looks up the Telegram chat ID and message ID for a given Thrum message.
func (m *MessageMap) TeleID(thrumMsgID string) (chatID int64, msgID int, ok bool) {
	key, found := m.inner.ExternalKey(thrumMsgID)
	if !found {
		return 0, 0, false
	}
	_, err := fmt.Sscanf(key, "%d:%d", &chatID, &msgID)
	return chatID, msgID, err == nil
}

// Len returns the current number of stored mappings.
func (m *MessageMap) Len() int { return m.inner.Len() }
