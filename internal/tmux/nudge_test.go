package tmux

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatNudge(t *testing.T) {
	msg := FormatNudge("coordinator_main")
	if !strings.Contains(msg, "@coordinator_main") {
		t.Errorf("nudge should contain sender name, got: %s", msg)
	}
	if !strings.Contains(msg, "thrum inbox --unread") {
		t.Errorf("nudge should contain inbox command, got: %s", msg)
	}
}

func TestChunkText(t *testing.T) {
	short := "hello"
	chunks := ChunkText(short, 512)
	if len(chunks) != 1 {
		t.Errorf("short text should be 1 chunk, got %d", len(chunks))
	}

	long := strings.Repeat("a", 1500)
	chunks = ChunkText(long, 512)
	if len(chunks) != 3 {
		t.Errorf("1500 bytes should be 3 chunks of 512, got %d", len(chunks))
	}

	// Verify all chunks concatenate to original
	var reconstructed strings.Builder
	for _, c := range chunks {
		reconstructed.WriteString(c)
	}
	if reconstructed.String() != long {
		t.Error("chunks should reconstruct to original text")
	}
}

func TestChunkText_ExactMultiple(t *testing.T) {
	text := strings.Repeat("x", 1024)
	chunks := ChunkText(text, 512)
	if len(chunks) != 2 {
		t.Errorf("1024 bytes should be 2 chunks of 512, got %d", len(chunks))
	}
}

func TestGetSessionLock_ReturnsSameMutex(t *testing.T) {
	mu1 := getSessionLock("test-session")
	mu2 := getSessionLock("test-session")
	if mu1 != mu2 {
		t.Error("getSessionLock should return the same mutex for the same session")
	}

	mu3 := getSessionLock("other-session")
	if mu1 == mu3 {
		t.Error("getSessionLock should return different mutexes for different sessions")
	}
}

// recordedKey captures a single key operation sent during a nudge.
type recordedKey struct {
	kind string // "text" or "special"
	val  string
}

// captureNudgeKeys replaces the package-level senders with recording stubs and
// restores them via t.Cleanup. The returned slice accumulates every operation
// in call order.
func captureNudgeKeys(t *testing.T) *[]recordedKey {
	t.Helper()
	var ops []recordedKey
	var mu sync.Mutex

	origText := nudgeSendKeys
	origSpecial := nudgeSendSpecialKey
	nudgeSendKeys = func(target, text string) error {
		mu.Lock()
		defer mu.Unlock()
		ops = append(ops, recordedKey{kind: "text", val: text})
		return nil
	}
	nudgeSendSpecialKey = func(target, key string) error {
		mu.Lock()
		defer mu.Unlock()
		ops = append(ops, recordedKey{kind: "special", val: key})
		return nil
	}
	t.Cleanup(func() {
		nudgeSendKeys = origText
		nudgeSendSpecialKey = origSpecial
	})
	return &ops
}

// TestNudge_DoesNotSendEscape verifies the core invariant of the fix:
// safe Nudge must never send Escape (which would cancel in-progress agent work).
func TestNudge_DoesNotSendEscape(t *testing.T) {
	ops := captureNudgeKeys(t)
	err := Nudge("test:0.0", "coordinator_main")
	require.NoError(t, err)
	for _, op := range *ops {
		if op.kind == "special" && op.val == "Escape" {
			t.Fatalf("Nudge must never send Escape (interrupts in-progress work); ops=%v", *ops)
		}
	}
}

// TestNudge_SendsTextThenEnter verifies the sequence: text → Enter (no Escape).
func TestNudge_SendsTextThenEnter(t *testing.T) {
	ops := captureNudgeKeys(t)
	err := Nudge("test:0.0", "coordinator_main")
	require.NoError(t, err)
	require.NotEmpty(t, *ops)
	// First op must be text containing the sender name
	assert.Equal(t, "text", (*ops)[0].kind)
	assert.Contains(t, (*ops)[0].val, "coordinator_main")
	// Last op must be Enter
	last := (*ops)[len(*ops)-1]
	assert.Equal(t, "special", last.kind)
	assert.Equal(t, "Enter", last.val)
	// No Escape anywhere
	for _, op := range *ops {
		if op.kind == "special" {
			assert.NotEqual(t, "Escape", op.val)
		}
	}
}

// TestInterruptNudge_SendsEscapeBeforeEnter verifies the interrupt variant
// preserves the pre-fix behavior: text → Escape → Enter.
func TestInterruptNudge_SendsEscapeBeforeEnter(t *testing.T) {
	ops := captureNudgeKeys(t)
	err := InterruptNudge("test:0.0", "system")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(*ops), 3)
	// Full sequence for short sender: [text, Escape, Enter]
	assert.Equal(t, "text", (*ops)[0].kind)
	assert.Contains(t, (*ops)[0].val, "system")
	assert.Equal(t, "special", (*ops)[1].kind)
	assert.Equal(t, "Escape", (*ops)[1].val)
	assert.Equal(t, "special", (*ops)[2].kind)
	assert.Equal(t, "Enter", (*ops)[2].val)
}
