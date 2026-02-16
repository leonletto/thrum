//go:build resilience

package resilience

import (
	"os"
	"testing"
)

func TestMultiDaemon_IndependentDaemons(t *testing.T) {
	// Two daemons running on separate fixtures should not interfere
	thrumDir1 := setupFixture(t)
	thrumDir2 := setupFixture(t)

	_, _, socketPath1 := startTestDaemon(t, thrumDir1)
	_, _, socketPath2 := startTestDaemon(t, thrumDir2)

	// Send a message to daemon 1
	var result1 map[string]any
	rpcCall(t, socketPath1, "message.send", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"content":         "Message to daemon 1",
		"format":          "markdown",
	}, &result1)

	// Send a different message to daemon 2
	var result2 map[string]any
	rpcCall(t, socketPath2, "message.send", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"content":         "Message to daemon 2",
		"format":          "markdown",
	}, &result2)

	// Verify the messages have different IDs
	if result1["message_id"] == result2["message_id"] {
		t.Error("two independent daemons generated the same message_id")
	}

	// Verify each daemon only sees its own recent message
	var inbox1 struct {
		Messages []map[string]any `json:"messages"`
	}
	rpcCall(t, socketPath1, "message.list", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"page_size":       5,
	}, &inbox1)

	found2InDaemon1 := false
	for _, m := range inbox1.Messages {
		if m["message_id"] == result2["message_id"] {
			found2InDaemon1 = true
		}
	}
	if found2InDaemon1 {
		t.Error("daemon 1 should not see daemon 2's message")
	}
}

func TestMultiDaemon_DaemonRestart(t *testing.T) {
	thrumDir := setupFixture(t)

	// Start daemon, write data, stop
	st1, srv1, socketPath := startTestDaemon(t, thrumDir)

	var sendResult map[string]any
	rpcCall(t, socketPath, "message.send", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"content":         "Persist test",
		"format":          "markdown",
	}, &sendResult)
	msgID := sendResult["message_id"].(string)

	srv1.Stop()
	st1.Close()

	// Remove socket
	os.Remove(socketPath)

	// Start fresh daemon on same data
	_, _, socketPath2 := startTestDaemon(t, thrumDir)

	// Verify state preserved
	var inbox struct {
		Messages []map[string]any `json:"messages"`
	}
	rpcCall(t, socketPath2, "message.list", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"page_size":       5,
	}, &inbox)

	found := false
	for _, m := range inbox.Messages {
		if m["message_id"] == msgID {
			found = true
		}
	}
	if !found {
		t.Errorf("message %s lost after daemon restart", msgID)
	}
}

func TestMultiDaemon_SharedFixture(t *testing.T) {
	// Two daemons from identical fixtures should diverge cleanly
	thrumDir1 := setupFixture(t)
	thrumDir2 := setupFixture(t)

	st1, _, socketPath1 := startTestDaemon(t, thrumDir1)
	st2, _, _ := startTestDaemon(t, thrumDir2)

	// Both start with same data
	var count1, count2 int
	st1.DB().QueryRow("SELECT COUNT(*) FROM messages").Scan(&count1)
	st2.DB().QueryRow("SELECT COUNT(*) FROM messages").Scan(&count2)

	if count1 != count2 {
		t.Errorf("fixtures should start with same message count: %d vs %d", count1, count2)
	}

	// Add messages to daemon 1 only
	for i := range 10 {
		rpcCall(t, socketPath1, "message.send", map[string]any{
			"caller_agent_id": "coordinator_0000",
			"content":         "Diverge " + string(rune('A'+i)),
			"format":          "markdown",
		}, nil)
	}

	// Daemon 1 should have more messages now
	st1.DB().QueryRow("SELECT COUNT(*) FROM messages").Scan(&count1)
	st2.DB().QueryRow("SELECT COUNT(*) FROM messages").Scan(&count2)

	if count1 != count2+10 {
		t.Errorf("expected daemon 1 to have 10 more messages: d1=%d d2=%d", count1, count2)
	}
}
