package tmux

import (
	"strings"
	"testing"
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
