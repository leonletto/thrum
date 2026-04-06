package bridge_test

import (
	"testing"

	"github.com/leonletto/thrum/internal/bridge"
)

func TestMessageMap_StoreAndLookup(t *testing.T) {
	m := bridge.NewMessageMap(100)

	m.Store("ext-key-1", "thrum-msg-1")
	m.Store("ext-key-2", "thrum-msg-2")

	if id, ok := m.ThrumID("ext-key-1"); !ok || id != "thrum-msg-1" {
		t.Fatalf("ThrumID(ext-key-1) = %q, %v; want thrum-msg-1, true", id, ok)
	}
	if key, ok := m.ExternalKey("thrum-msg-1"); !ok || key != "ext-key-1" {
		t.Fatalf("ExternalKey(thrum-msg-1) = %q, %v; want ext-key-1, true", key, ok)
	}
}

func TestMessageMap_FIFOEviction(t *testing.T) {
	m := bridge.NewMessageMap(2)

	m.Store("a", "t1")
	m.Store("b", "t2")
	m.Store("c", "t3") // evicts "a"

	if _, ok := m.ThrumID("a"); ok {
		t.Fatal("expected 'a' to be evicted")
	}
	if _, ok := m.ThrumID("b"); !ok {
		t.Fatal("expected 'b' to still exist")
	}
	if _, ok := m.ThrumID("c"); !ok {
		t.Fatal("expected 'c' to still exist")
	}
}

func TestMessageMap_UpdateExisting(t *testing.T) {
	m := bridge.NewMessageMap(100)

	m.Store("ext-1", "thrum-1")
	m.Store("ext-1", "thrum-2") // update

	if id, _ := m.ThrumID("ext-1"); id != "thrum-2" {
		t.Fatalf("ThrumID after update = %q, want thrum-2", id)
	}
	if m.Len() != 1 {
		t.Fatalf("Len() = %d after update, want 1", m.Len())
	}
}

func TestMessageMap_ReverseAfterEviction(t *testing.T) {
	m := bridge.NewMessageMap(2)

	m.Store("a", "t1")
	m.Store("b", "t2")
	m.Store("c", "t3") // evicts "a"

	// Reverse lookup for evicted entry should fail
	if _, ok := m.ExternalKey("t1"); ok {
		t.Fatal("expected reverse lookup for evicted 't1' to fail")
	}
	// Reverse lookup for remaining entries should work
	if key, ok := m.ExternalKey("t2"); !ok || key != "b" {
		t.Fatalf("ExternalKey(t2) = %q, %v; want b, true", key, ok)
	}
}
