package bridge

import "sync"

// MessageMap is a bidirectional map between external message keys and Thrum
// message IDs. Both sides use string keys. Each transport formats its own
// external key (e.g., Telegram: "chatID:msgID", Peer: remote thrum msg ID).
// Thread-safe. FIFO eviction when full.
type MessageMap struct {
	mu         sync.RWMutex
	extToThrum map[string]string
	thrumToExt map[string]string
	order      []string
	maxSize    int
}

// NewMessageMap creates a new bounded message ID map.
func NewMessageMap(maxSize int) *MessageMap {
	return &MessageMap{
		extToThrum: make(map[string]string, maxSize),
		thrumToExt: make(map[string]string, maxSize),
		order:      make([]string, 0, maxSize),
		maxSize:    maxSize,
	}
}

// Store records a bidirectional mapping. Evicts the oldest entry if at capacity.
func (m *MessageMap) Store(externalKey, thrumMsgID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If key already exists, update the mapping without appending to order.
	if old, ok := m.extToThrum[externalKey]; ok {
		delete(m.thrumToExt, old)
		m.extToThrum[externalKey] = thrumMsgID
		m.thrumToExt[thrumMsgID] = externalKey
		return
	}

	// Evict oldest if at capacity.
	if len(m.order) >= m.maxSize {
		evict := m.order[0]
		m.order = m.order[1:]
		if tid, ok := m.extToThrum[evict]; ok {
			delete(m.thrumToExt, tid)
		}
		delete(m.extToThrum, evict)
	}

	m.extToThrum[externalKey] = thrumMsgID
	m.thrumToExt[thrumMsgID] = externalKey
	m.order = append(m.order, externalKey)
}

// ThrumID looks up the Thrum message ID for a given external key.
func (m *MessageMap) ThrumID(externalKey string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.extToThrum[externalKey]
	return id, ok
}

// ExternalKey looks up the external key for a given Thrum message ID.
func (m *MessageMap) ExternalKey(thrumMsgID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key, ok := m.thrumToExt[thrumMsgID]
	return key, ok
}

// Len returns the current number of stored mappings.
func (m *MessageMap) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.extToThrum)
}
