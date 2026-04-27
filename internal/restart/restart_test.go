package restart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestTruncateExchanges_NoTruncation(t *testing.T) {
	exchanges := []string{
		"=== USER ===\nShort question",
		"=== ASSISTANT ===\nShort answer",
	}
	result := truncateExchanges(exchanges, 1000)
	assert.NotContains(t, result, "truncated")
	assert.Contains(t, result, "Short question")
	assert.Contains(t, result, "Short answer")
}

func TestTruncateExchanges_StartsOnUserBoundary(t *testing.T) {
	var exchanges []string
	for i := 0; i < 50; i++ {
		exchanges = append(exchanges,
			fmt.Sprintf("=== USER ===\nQuestion %d with some padding text to make lines", i),
			fmt.Sprintf("=== ASSISTANT ===\nAnswer %d with some padding text to make lines", i),
		)
	}
	result := truncateExchanges(exchanges, 20)
	assert.Contains(t, result, "truncated")

	// After the header, first content line should be === USER ===
	lines := strings.Split(result, "\n")
	firstContentLine := ""
	for _, l := range lines {
		if l == "=== USER ===" || l == "=== ASSISTANT ===" {
			firstContentLine = l
			break
		}
	}
	assert.Equal(t, "=== USER ===", firstContentLine)
}

func TestTruncateExchanges_EmptyInput(t *testing.T) {
	result := truncateExchanges(nil, 1000)
	assert.Equal(t, "", result)
}

func TestFindSessionJSONL(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0700))

	sessFile := filepath.Join(sessionsDir, "12345.json")
	require.NoError(t, os.WriteFile(sessFile, []byte(`{
		"pid": 12345,
		"sessionId": "abc-def-123",
		"cwd": "/Users/test/myproject"
	}`), 0600))

	projectDir := filepath.Join(dir, "projects", "-Users-test-myproject")
	require.NoError(t, os.MkdirAll(projectDir, 0700))
	jsonlPath := filepath.Join(projectDir, "abc-def-123.jsonl")
	require.NoError(t, os.WriteFile(jsonlPath, []byte(`{}`), 0600))

	result, err := FindSessionJSONL(dir, 12345)
	require.NoError(t, err)
	assert.Equal(t, jsonlPath, result)
}

func TestFindSessionJSONL_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := FindSessionJSONL(dir, 99999)
	assert.Error(t, err)
}

func TestEncodeCwd(t *testing.T) {
	assert.Equal(t, "-Users-leon-dev-project", encodeCwd("/Users/leon/dev/project"))
	assert.Equal(t, "-home-user-work", encodeCwd("/home/user/work"))
	// Dot-prefixed directories (e.g. .workspaces) encode dots as dashes
	assert.Equal(t, "-Users-leon--workspaces-thrum-website-dev", encodeCwd("/Users/leon/.workspaces/thrum/website-dev"))
	assert.Equal(t, "-home-user--config-app", encodeCwd("/home/user/.config/app"))
	// Underscores also collapse to dashes (matches Claude Code's project-dir naming)
	assert.Equal(t, "-Users-leon--thrum-release-tests-run1-repo", encodeCwd("/Users/leon/.thrum_release_tests/run1/repo"))
	assert.Equal(t, "-home-user-foo-bar-baz", encodeCwd("/home/user/foo_bar_baz"))
}

func TestSnapshotSaveAndRestore(t *testing.T) {
	thrumDir := t.TempDir()

	content := "# Restart Snapshot — test_agent\n\n=== USER ===\nHello\n\n=== ASSISTANT ===\nHi"
	require.NoError(t, SaveSnapshot(thrumDir, "test_agent", content))

	assert.True(t, SnapshotExists(thrumDir, "test_agent"))

	restored, err := Restore(thrumDir, "test_agent")
	require.NoError(t, err)
	assert.Equal(t, content, restored)

	// File should be gone after restore
	assert.False(t, SnapshotExists(thrumDir, "test_agent"))
}

func TestRestore_NotFound(t *testing.T) {
	thrumDir := t.TempDir()
	_, err := Restore(thrumDir, "nonexistent")
	assert.Error(t, err)
}

func TestRestore_CrashSafety(t *testing.T) {
	thrumDir := t.TempDir()

	content := "snapshot content"
	require.NoError(t, SaveSnapshot(thrumDir, "agent1", content))

	// Restore should return content and clean up completely
	restored, err := Restore(thrumDir, "agent1")
	require.NoError(t, err)
	assert.Equal(t, content, restored)

	// Original file should be gone
	assert.False(t, SnapshotExists(thrumDir, "agent1"))

	// No .consumed file should remain (Restore does rename + immediate delete)
	consumedPath := filepath.Join(thrumDir, "restart", "agent1.md.consumed")
	_, err = os.Stat(consumedPath)
	assert.True(t, os.IsNotExist(err))
}

func TestSnapshotPath(t *testing.T) {
	path := restartSnapshotPath("/tmp/.thrum", "my_agent")
	assert.Equal(t, "/tmp/.thrum/restart/my_agent.md", path)
}

func TestConsumeInPrime(t *testing.T) {
	thrumDir := t.TempDir()

	content := "snapshot content"
	require.NoError(t, SaveSnapshot(thrumDir, "agent1", content))

	// ConsumeInPrime should return content and rename to .consumed
	result, err := ConsumeInPrime(thrumDir, "agent1")
	require.NoError(t, err)
	assert.Equal(t, content, result)

	// Original file should be gone
	assert.False(t, SnapshotExists(thrumDir, "agent1"))

	// .consumed file should exist
	consumedPath := filepath.Join(thrumDir, "restart", "agent1.md.consumed")
	_, err = os.Stat(consumedPath)
	assert.NoError(t, err)

	// CleanupConsumed should remove it
	CleanupConsumed(thrumDir, "agent1")
	_, err = os.Stat(consumedPath)
	assert.True(t, os.IsNotExist(err))
}

func TestConsumeInPrime_NotFound(t *testing.T) {
	thrumDir := t.TempDir()
	_, err := ConsumeInPrime(thrumDir, "nonexistent")
	assert.Error(t, err)
}

func TestFindLatestJSONLForCwd_PicksMostRecent(t *testing.T) {
	claudeDir := t.TempDir()
	cwd := "/Users/leon/.workspaces/thrum/team-fix"
	// encodeCwd produces the slug deterministically; mirror it for fixture setup.
	slug := "-Users-leon--workspaces-thrum-team-fix"
	projectDir := filepath.Join(claudeDir, "projects", slug)
	require.NoError(t, os.MkdirAll(projectDir, 0o750))

	// Write 3 JSONL files with staggered mtimes; oldest first so
	// os.Chtimes sticks even on filesystems with 1-second resolution.
	older := filepath.Join(projectDir, "sess-older.jsonl")
	middle := filepath.Join(projectDir, "sess-middle.jsonl")
	latest := filepath.Join(projectDir, "sess-latest.jsonl")
	for _, p := range []string{older, middle, latest} {
		require.NoError(t, os.WriteFile(p, []byte("{}\n"), 0o600))
	}
	base := time.Now().Add(-1 * time.Hour)
	require.NoError(t, os.Chtimes(older, base, base))
	require.NoError(t, os.Chtimes(middle, base.Add(10*time.Minute), base.Add(10*time.Minute)))
	require.NoError(t, os.Chtimes(latest, base.Add(50*time.Minute), base.Add(50*time.Minute)))

	got, err := FindLatestJSONLForCwd(claudeDir, cwd)
	require.NoError(t, err)
	assert.Equal(t, latest, got, "should pick newest-mtime JSONL")

	// Sanity: non-jsonl siblings must be ignored even if newer.
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "notes.txt"), []byte("x"), 0o600))
	got2, err := FindLatestJSONLForCwd(claudeDir, cwd)
	require.NoError(t, err)
	assert.Equal(t, latest, got2, "non-.jsonl files must not outrank the real JSONL")
}

func TestFindLatestJSONLForCwd_EmptyDir(t *testing.T) {
	claudeDir := t.TempDir()
	cwd := "/Users/leon/dev/empty"
	projectDir := filepath.Join(claudeDir, "projects", "-Users-leon-dev-empty")
	require.NoError(t, os.MkdirAll(projectDir, 0o750))

	got, err := FindLatestJSONLForCwd(claudeDir, cwd)
	require.NoError(t, err, "empty dir is not a hard error")
	assert.Equal(t, "", got, "empty result signals 'no fallback available'")
}

func TestFindLatestJSONLForCwd_MissingDir(t *testing.T) {
	claudeDir := t.TempDir() // no projects/ at all
	_, err := FindLatestJSONLForCwd(claudeDir, "/some/cwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read project dir")
}

func TestFindLatestJSONLForCwd_EmptyCwd(t *testing.T) {
	claudeDir := t.TempDir()
	_, err := FindLatestJSONLForCwd(claudeDir, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cwd required")
}
