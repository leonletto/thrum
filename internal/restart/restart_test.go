package restart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to write a JSONL file with test entries.
func writeTestJSONL(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	path := filepath.Join(dir, "test-session.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600))
	return path
}

func TestExtractConversation_BasicUserAssistant(t *testing.T) {
	dir := t.TempDir()
	path := writeTestJSONL(t, dir,
		`{"type":"user","message":{"role":"user","content":"Hello world"},"isSidechain":false}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hi there!"}]},"isSidechain":false}`,
	)

	result, err := ExtractConversation(path, 1000)
	require.NoError(t, err)
	assert.Contains(t, result, "=== USER ===")
	assert.Contains(t, result, "Hello world")
	assert.Contains(t, result, "=== ASSISTANT ===")
	assert.Contains(t, result, "Hi there!")
}

func TestExtractConversation_SkipsToolUse(t *testing.T) {
	dir := t.TempDir()
	path := writeTestJSONL(t, dir,
		`{"type":"user","message":{"role":"user","content":"Read a file"},"isSidechain":false}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{}}]},"isSidechain":false}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"file contents"}]},"isSidechain":false}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Here is the file."}]},"isSidechain":false}`,
	)

	result, err := ExtractConversation(path, 1000)
	require.NoError(t, err)
	assert.Contains(t, result, "Read a file")
	assert.Contains(t, result, "Here is the file.")
	assert.NotContains(t, result, "file contents")
	assert.NotContains(t, result, "tool_use")
}

func TestExtractConversation_SkipsSidechain(t *testing.T) {
	dir := t.TempDir()
	path := writeTestJSONL(t, dir,
		`{"type":"user","message":{"role":"user","content":"Main thread"},"isSidechain":false}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Sidechain response"}]},"isSidechain":true}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Main response"}]},"isSidechain":false}`,
	)

	result, err := ExtractConversation(path, 1000)
	require.NoError(t, err)
	assert.Contains(t, result, "Main thread")
	assert.Contains(t, result, "Main response")
	assert.NotContains(t, result, "Sidechain response")
}

func TestExtractConversation_SkipsNonConversationTypes(t *testing.T) {
	dir := t.TempDir()
	path := writeTestJSONL(t, dir,
		`{"type":"permission-mode","isSidechain":false}`,
		`{"type":"file-history-snapshot","isSidechain":false}`,
		`{"type":"user","message":{"role":"user","content":"Actual question"},"isSidechain":false}`,
		`{"type":"system","isSidechain":false}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Actual answer"}]},"isSidechain":false}`,
	)

	result, err := ExtractConversation(path, 1000)
	require.NoError(t, err)
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		assert.NotContains(t, line, "permission-mode")
		assert.NotContains(t, line, "file-history-snapshot")
	}
	assert.Contains(t, result, "Actual question")
	assert.Contains(t, result, "Actual answer")
}

func TestExtractConversation_SkipsThinking(t *testing.T) {
	dir := t.TempDir()
	path := writeTestJSONL(t, dir,
		`{"type":"user","message":{"role":"user","content":"Think about this"},"isSidechain":false}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"deep thoughts"},{"type":"text","text":"My answer"}]},"isSidechain":false}`,
	)

	result, err := ExtractConversation(path, 1000)
	require.NoError(t, err)
	assert.Contains(t, result, "My answer")
	assert.NotContains(t, result, "deep thoughts")
}

func TestExtractConversation_Truncation(t *testing.T) {
	dir := t.TempDir()
	// Create a conversation with many exchanges that exceeds maxLines
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines,
			fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"Question %d"},"isSidechain":false}`, i),
			fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Answer %d"}]},"isSidechain":false}`, i),
		)
	}
	path := writeTestJSONL(t, dir, lines...)

	result, err := ExtractConversation(path, 10)
	require.NoError(t, err)
	assert.Contains(t, result, "[Conversation continued from earlier")
	// Should start with USER marker after truncation
	resultLines := strings.Split(result, "\n")
	// Find first non-header line
	for _, line := range resultLines {
		if strings.HasPrefix(line, "===") {
			assert.Equal(t, "=== USER ===", line)
			break
		}
	}
}

func TestFormatRestartSnapshot(t *testing.T) {
	snapshot := FormatRestartSnapshot("test-agent", "ses_123", "manual", "conversation text")
	assert.Contains(t, snapshot, "# Restart Snapshot — test-agent")
	assert.Contains(t, snapshot, "**Session:** ses_123")
	assert.Contains(t, snapshot, "**Reason:** manual")
	assert.Contains(t, snapshot, "conversation text")
}

func TestExtractConversation_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(""), 0600))

	result, err := ExtractConversation(path, 1000)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestExtractConversation_FileNotFound(t *testing.T) {
	_, err := ExtractConversation("/nonexistent/path.jsonl", 1000)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "open JSONL")
}
