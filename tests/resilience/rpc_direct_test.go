//go:build resilience

package resilience

import (
	"testing"
)

func TestRPC_FixtureIntegrity(t *testing.T) {
	thrumDir := setupFixture(t)
	st, _, _ := startTestDaemon(t, thrumDir)

	// Verify agent count
	var agentCount int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM agents").Scan(&agentCount); err != nil {
		t.Fatalf("count agents: %v", err)
	}
	if agentCount != 50 {
		t.Errorf("expected 50 agents, got %d", agentCount)
	}

	// Verify message count
	var msgCount int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM messages").Scan(&msgCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if msgCount != 10000 {
		t.Errorf("expected 10000 messages, got %d", msgCount)
	}

	// Verify session count
	var sesCount int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sesCount); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sesCount != 100 {
		t.Errorf("expected 100 sessions, got %d", sesCount)
	}

	// Verify group count
	var grpCount int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM groups").Scan(&grpCount); err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if grpCount != 20 {
		t.Errorf("expected 20 groups, got %d", grpCount)
	}

	// Verify message reads exist
	var readCount int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM message_reads").Scan(&readCount); err != nil {
		t.Fatalf("count reads: %v", err)
	}
	if readCount == 0 {
		t.Error("expected some message reads")
	}
	t.Logf("Fixture integrity: agents=%d messages=%d sessions=%d groups=%d reads=%d",
		agentCount, msgCount, sesCount, grpCount, readCount)
}

func TestRPC_HealthCheck(t *testing.T) {
	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	var result map[string]any
	rpcCall(t, socketPath, "health", nil, &result)

	if result["status"] != "ok" {
		t.Errorf("expected status ok, got %v", result["status"])
	}
}

func TestRPC_AgentList(t *testing.T) {
	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	var result struct {
		Agents []map[string]any `json:"agents"`
	}
	rpcCall(t, socketPath, "agent.list", map[string]any{}, &result)

	if len(result.Agents) != 50 {
		t.Errorf("expected 50 agents, got %d", len(result.Agents))
	}
}

func TestRPC_AgentListFilterByRole(t *testing.T) {
	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	var result struct {
		Agents []map[string]any `json:"agents"`
	}
	rpcCall(t, socketPath, "agent.list", map[string]any{"role": "coordinator"}, &result)

	for _, a := range result.Agents {
		if a["role"] != "coordinator" {
			t.Errorf("expected role coordinator, got %v", a["role"])
		}
	}
	if len(result.Agents) == 0 {
		t.Error("expected some coordinator agents")
	}
	t.Logf("Found %d coordinator agents", len(result.Agents))
}

func TestRPC_SendBroadcast(t *testing.T) {
	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	var sendResult map[string]any
	rpcCall(t, socketPath, "message.send", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"content":         "Test broadcast from resilience suite",
		"format":          "markdown",
	}, &sendResult)

	if sendResult["message_id"] == nil || sendResult["message_id"] == "" {
		t.Error("expected message_id in response")
	}
	t.Logf("Sent broadcast, message_id=%v", sendResult["message_id"])
}

func TestRPC_SendDirected(t *testing.T) {
	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	var sendResult map[string]any
	rpcCall(t, socketPath, "message.send", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"content":         "Test directed message",
		"format":          "markdown",
		"scopes": []map[string]any{
			{"type": "agent", "value": "implementer_0001"},
		},
	}, &sendResult)

	if sendResult["message_id"] == nil {
		t.Error("expected message_id in response")
	}
}

func TestRPC_InboxPagination(t *testing.T) {
	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	// First page
	var page1 struct {
		Messages []map[string]any `json:"messages"`
		Total    int              `json:"total"`
	}
	rpcCall(t, socketPath, "message.list", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"page_size":       50,
		"page":            1,
	}, &page1)

	if len(page1.Messages) == 0 {
		t.Fatal("expected messages in page 1")
	}

	// Second page
	var page2 struct {
		Messages []map[string]any `json:"messages"`
	}
	rpcCall(t, socketPath, "message.list", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"page_size":       50,
		"page":            2,
	}, &page2)

	// Pages should have different messages
	if len(page2.Messages) > 0 && len(page1.Messages) > 0 {
		id1 := page1.Messages[0]["message_id"]
		id2 := page2.Messages[0]["message_id"]
		if id1 == id2 {
			t.Error("page 1 and page 2 have the same first message — pagination broken")
		}
	}

	t.Logf("Pagination: page1=%d page2=%d total=%d messages", len(page1.Messages), len(page2.Messages), page1.Total)
}

func TestRPC_GroupList(t *testing.T) {
	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	var result struct {
		Groups []map[string]any `json:"groups"`
	}
	rpcCall(t, socketPath, "group.list", nil, &result)

	if len(result.Groups) != 20 {
		t.Errorf("expected 20 groups, got %d", len(result.Groups))
	}
}

func TestRPC_GroupInfo(t *testing.T) {
	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	var result map[string]any
	rpcCall(t, socketPath, "group.info", map[string]any{"name": "coordinators"}, &result)

	if result["name"] != "coordinators" {
		t.Errorf("expected group name 'coordinators', got %v", result["name"])
	}
}

func TestRPC_SessionList(t *testing.T) {
	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	var result struct {
		Sessions []map[string]any `json:"sessions"`
	}
	rpcCall(t, socketPath, "session.list", map[string]any{}, &result)

	if len(result.Sessions) == 0 {
		t.Error("expected sessions in list")
	}
	t.Logf("Session list returned %d sessions", len(result.Sessions))
}

func TestRPC_MessageReadTracking(t *testing.T) {
	thrumDir := setupFixture(t)
	_, _, socketPath := startTestDaemon(t, thrumDir)

	// Send a new message
	var sendResult map[string]any
	rpcCall(t, socketPath, "message.send", map[string]any{
		"caller_agent_id": "coordinator_0000",
		"content":         "Read tracking test",
		"format":          "markdown",
	}, &sendResult)

	msgID := sendResult["message_id"].(string)

	// Mark it as read by another agent
	rpcCall(t, socketPath, "message.markRead", map[string]any{
		"caller_agent_id": "implementer_0001",
		"message_ids":     []string{msgID},
	}, nil)

	// Query inbox for the reader with unread filter — the message should not appear
	var inbox struct {
		Messages []map[string]any `json:"messages"`
	}
	rpcCall(t, socketPath, "message.list", map[string]any{
		"caller_agent_id":  "implementer_0001",
		"unread_for_agent": "implementer_0001",
		"page_size":        100,
	}, &inbox)

	for _, m := range inbox.Messages {
		if m["message_id"] == msgID {
			t.Errorf("message %s should be marked read but appeared in unread inbox", msgID)
		}
	}
}
