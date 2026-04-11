package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSender records every HandleSend call for unit-level assertions.
type fakeSender struct {
	calls []json.RawMessage
	err   error
}

func (f *fakeSender) HandleSend(_ context.Context, params json.RawMessage) (any, error) {
	f.calls = append(f.calls, params)
	return nil, f.err
}

// TestDelivery_SenderReceivesExactlyOneCall asserts that a single Deliver
// invocation results in exactly one HandleSend call.
func TestDelivery_SenderReceivesExactlyOneCall(t *testing.T) {
	fake := &fakeSender{}
	d := NewDelivery(fake)

	err := d.Deliver(context.Background(), "dev-errors", "@impl_api", "ERROR: boom")
	require.NoError(t, err)
	assert.Len(t, fake.calls, 1, "expected exactly one HandleSend call per Deliver")
}

// TestDelivery_CorrectSenderPrefix asserts that the caller_agent_id is
// "monitor:<name>" — the leading "monitor:" prefix must be present.
func TestDelivery_CorrectSenderPrefix(t *testing.T) {
	fake := &fakeSender{}
	d := NewDelivery(fake)

	err := d.Deliver(context.Background(), "dev-errors", "@impl_api", "ERROR: test")
	require.NoError(t, err)
	require.Len(t, fake.calls, 1)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(fake.calls[0], &payload))

	callerID, ok := payload["caller_agent_id"].(string)
	require.True(t, ok, "caller_agent_id must be a string")
	assert.Equal(t, "monitor:dev-errors", callerID,
		"sender must be 'monitor:<name>', got %q", callerID)
}

// TestDelivery_CorrectTarget asserts that the target is forwarded in the
// mentions list so the existing mention-resolution path routes the message.
func TestDelivery_CorrectTarget(t *testing.T) {
	fake := &fakeSender{}
	d := NewDelivery(fake)

	err := d.Deliver(context.Background(), "watchdog", "@coordinator_main", "WARN: disk full")
	require.NoError(t, err)
	require.Len(t, fake.calls, 1)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(fake.calls[0], &payload))

	mentions, ok := payload["mentions"].([]any)
	require.True(t, ok, "mentions must be present")
	require.Len(t, mentions, 1, "exactly one mention")
	assert.Equal(t, "@coordinator_main", mentions[0].(string))
}

// TestDelivery_CorrectContent asserts that the content is forwarded verbatim.
func TestDelivery_CorrectContent(t *testing.T) {
	fake := &fakeSender{}
	d := NewDelivery(fake)

	const content = "ERROR: connection refused on port 8080"
	err := d.Deliver(context.Background(), "net-watch", "@ops", content)
	require.NoError(t, err)
	require.Len(t, fake.calls, 1)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(fake.calls[0], &payload))

	got, ok := payload["content"].(string)
	require.True(t, ok, "content must be a string")
	assert.Equal(t, content, got)
}

// TestDelivery_MonitorNamePrefix asserts that different monitor names produce
// the expected "monitor:<name>" prefix, not just "monitor:".
func TestDelivery_MonitorNamePrefix(t *testing.T) {
	cases := []struct {
		name     string
		expected string
	}{
		{"dev-errors", "monitor:dev-errors"},
		{"heartbeat-check", "monitor:heartbeat-check"},
		{"a", "monitor:a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSender{}
			d := NewDelivery(fake)
			require.NoError(t, d.Deliver(context.Background(), tc.name, "@x", "line"))

			var payload map[string]any
			require.NoError(t, json.Unmarshal(fake.calls[0], &payload))
			assert.Equal(t, tc.expected, payload["caller_agent_id"])
		})
	}
}

// TestDelivery_MonitorScopeTagged asserts that every monitor-originated
// message carries a reserved {type: "monitor", value: <monitorName>} scope so
// subscription filters can match all monitor messages or a specific monitor
// in bulk. Review finding 6.
func TestDelivery_MonitorScopeTagged(t *testing.T) {
	fake := &fakeSender{}
	d := NewDelivery(fake)

	err := d.Deliver(context.Background(), "dev-errors", "@impl_api", "ERROR: boom")
	require.NoError(t, err)
	require.Len(t, fake.calls, 1)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(fake.calls[0], &payload))

	scopes, ok := payload["scopes"].([]any)
	require.True(t, ok, "scopes must be present in the payload")
	require.Len(t, scopes, 1, "exactly one scope per monitor message")
	scope := scopes[0].(map[string]any)
	assert.Equal(t, "monitor", scope["type"],
		"scope type must be the reserved 'monitor' tag")
	assert.Equal(t, "dev-errors", scope["value"],
		"scope value must be the monitor name for precise-filter support")
}

// TestDelivery_PropagatesSenderError asserts that a MessageSender error is
// wrapped and returned to the caller.
func TestDelivery_PropagatesSenderError(t *testing.T) {
	sentinelErr := errors.New("pipeline down")
	fake := &fakeSender{err: sentinelErr}
	d := NewDelivery(fake)

	err := d.Deliver(context.Background(), "watch", "@x", "line")
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinelErr,
		"original error must be reachable via errors.Is")
}

// TestDelivery_MessageLandsInDB is an integration-level test that uses a
// real MessageHandler and asserts the message row lands in the DB with
// agent_id = "monitor:dev-errors".
//
// It pre-inserts a synthetic agent + open session so HandleSend's session
// lookup succeeds for the "monitor:dev-errors" caller ID.
func TestDelivery_MessageLandsInDB(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := state.NewState(tmpDir, tmpDir, "test-repo", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	const monitorName = "dev-errors"
	const callerID = "monitor:" + monitorName

	// Pre-insert a synthetic agent row so HandleSend does not reject the sender.
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = st.DB().ExecContext(context.Background(), `
		INSERT INTO agents (agent_id, kind, role, module, display, hostname, agent_pid, registered_at, last_seen_at)
		VALUES (?, 'monitor', 'monitor', 'monitor', ?, '', 0, ?, ?)
	`, callerID, monitorName, now, now)
	require.NoError(t, err, "insert synthetic monitor agent")

	// Pre-insert an open session for the synthetic agent.
	sessionID := fmt.Sprintf("ses_monitor_%d", time.Now().UnixNano())
	_, err = st.DB().ExecContext(context.Background(), `
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
		VALUES (?, ?, ?, ?)
	`, sessionID, callerID, now, now)
	require.NoError(t, err, "insert synthetic monitor session")

	// Build a real MessageHandler (no dispatcher/thrumDir needed for this test).
	msgHandler := rpc.NewMessageHandler(st)

	d := NewDelivery(msgHandler)
	err = d.Deliver(context.Background(), monitorName, "", "ERROR: boom")
	require.NoError(t, err)

	// Assert the message landed with agent_id = "monitor:dev-errors".
	var count int
	row := st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM messages WHERE agent_id = ? AND body_content LIKE ?`,
		callerID, "%ERROR: boom%",
	)
	require.NoError(t, row.Scan(&count))
	assert.Equal(t, 1, count,
		"expected exactly one message row with agent_id=%q", callerID)
}
