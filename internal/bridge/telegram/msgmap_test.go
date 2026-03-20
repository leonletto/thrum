package telegram

import (
	"fmt"
	"sync"
	"testing"
)

func TestMessageMapStore(t *testing.T) {
	m := NewMessageMap(100)
	m.Store(123, 1, "msg_aaa")

	thrumID, ok := m.ThrumID(123, 1)
	if !ok || thrumID != "msg_aaa" {
		t.Errorf("ThrumID = %q, %v; want msg_aaa, true", thrumID, ok)
	}

	chatID, msgID, ok := m.TeleID("msg_aaa")
	if !ok || chatID != 123 || msgID != 1 {
		t.Errorf("TeleID = %d, %d, %v; want 123, 1, true", chatID, msgID, ok)
	}
}

func TestMessageMapNotFound(t *testing.T) {
	m := NewMessageMap(100)

	_, ok := m.ThrumID(123, 999)
	if ok {
		t.Error("expected not found for ThrumID")
	}

	_, _, ok = m.TeleID("msg_nonexistent")
	if ok {
		t.Error("expected not found for TeleID")
	}
}

func TestMessageMapEviction(t *testing.T) {
	m := NewMessageMap(3)

	m.Store(1, 1, "msg_1")
	m.Store(1, 2, "msg_2")
	m.Store(1, 3, "msg_3")

	if m.Len() != 3 {
		t.Fatalf("expected len=3, got %d", m.Len())
	}

	// Adding a 4th should evict the oldest (1:1 → msg_1)
	m.Store(1, 4, "msg_4")

	if m.Len() != 3 {
		t.Fatalf("expected len=3 after eviction, got %d", m.Len())
	}

	// msg_1 should be gone
	_, ok := m.ThrumID(1, 1)
	if ok {
		t.Error("expected evicted entry to be gone from ThrumID")
	}
	_, _, ok = m.TeleID("msg_1")
	if ok {
		t.Error("expected evicted entry to be gone from TeleID")
	}

	// msg_2 should still exist
	thrumID, ok := m.ThrumID(1, 2)
	if !ok || thrumID != "msg_2" {
		t.Errorf("expected msg_2 to survive, got %q, %v", thrumID, ok)
	}
}

func TestMessageMapConcurrency(t *testing.T) {
	m := NewMessageMap(10000)
	var wg sync.WaitGroup

	for i := range 100 {
		wg.Go(func() {
			for j := range 100 {
				m.Store(int64(i), j, fmt.Sprintf("msg_%d_%d", i, j))
				m.ThrumID(int64(i), j)
				m.TeleID(fmt.Sprintf("msg_%d_%d", i, j))
			}
		})
	}

	wg.Wait()

	if m.Len() > 10000 {
		t.Errorf("expected len <= 10000, got %d", m.Len())
	}
}
