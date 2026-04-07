//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/leonletto/thrum/internal/restart"
)

func TestRestartSnapshotLifecycle(t *testing.T) {
	thrumDir := t.TempDir()

	// Step 1: No snapshot should exist initially
	assert.False(t, restart.SnapshotExists(thrumDir, "test_agent"))

	// Step 2: Create a mock JSONL file
	claudeDir := t.TempDir()
	sessionsDir := filepath.Join(claudeDir, "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0700))

	require.NoError(t, os.WriteFile(
		filepath.Join(sessionsDir, "999.json"),
		[]byte(`{"pid":999,"sessionId":"test-sess","cwd":"/tmp/test"}`),
		0600,
	))

	projectDir := filepath.Join(claudeDir, "projects", "-tmp-test")
	require.NoError(t, os.MkdirAll(projectDir, 0700))

	jsonlContent := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"What is 2+2?"},"isSidechain":false}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"4"}]},"isSidechain":false}`,
	}, "\n")
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, "test-sess.jsonl"),
		[]byte(jsonlContent),
		0600,
	))

	// Step 3: Extract conversation
	jsonlPath, err := restart.FindSessionJSONL(claudeDir, 999)
	require.NoError(t, err)

	conversation, err := restart.ExtractConversation(jsonlPath, 1000)
	require.NoError(t, err)
	assert.Contains(t, conversation, "What is 2+2?")
	assert.Contains(t, conversation, "4")

	// Step 4: Save snapshot
	snapshot := restart.FormatRestartSnapshot("test_agent", "ses_test", "external", conversation)
	require.NoError(t, restart.SaveSnapshot(thrumDir, "test_agent", snapshot))
	assert.True(t, restart.SnapshotExists(thrumDir, "test_agent"))

	// Step 5: Consume in prime (rename to .consumed)
	consumed, err := restart.ConsumeInPrime(thrumDir, "test_agent")
	require.NoError(t, err)
	assert.Contains(t, consumed, "Restart Snapshot")
	assert.False(t, restart.SnapshotExists(thrumDir, "test_agent"))

	// .consumed file should exist
	consumedPath := filepath.Join(thrumDir, "restart", "test_agent.md.consumed")
	_, err = os.Stat(consumedPath)
	assert.NoError(t, err)

	// Step 6: Cleanup consumed
	restart.CleanupConsumed(thrumDir, "test_agent")
	_, err = os.Stat(consumedPath)
	assert.True(t, os.IsNotExist(err))
}
