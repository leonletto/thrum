package rpc

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
	"github.com/stretchr/testify/require"
)

// TestSec8_DeleteByAgent_CallerMustMatchTarget proves that agent B cannot
// bulk-hard-delete agent A's messages via message.deleteByAgent, even with
// a valid peercred-resolved identity. The resolved caller must equal the
// target agent_id.
func TestSec8_DeleteByAgent_CallerMustMatchTarget(t *testing.T) {
	// Agent A sends a message.
	resolverA := &fakeResolver{
		id: &peercred.ResolvedIdentity{
			AgentID:  "impl_sec3_test",
			Worktree: "/tmp/fake-worktree",
			PID:      os.Getpid(),
		},
	}
	h := newSec3Harness(t, resolverA, true)
	h.server.RegisterHandler("message.deleteByAgent", h.msgHandler.HandleDeleteByAgent)

	sendResp, err := h.sendRPC(t, "message.send", map[string]any{
		"to":      "impl_sec3_test",
		"content": "target message",
	})
	require.NoError(t, err)
	require.Nil(t, sendResp.Error)

	// Agent B attempts to bulk-delete agent A's messages.
	now := "2026-04-15T00:00:00Z"
	_, err = h.state.DB().ExecContext(context.Background(), `
		INSERT INTO agents (agent_id, kind, role, module, display, hostname, agent_pid, registered_at, last_seen_at)
		VALUES (?, 'implementer', 'implementer', 'sec8', ?, '', 0, ?, ?)
	`, "impl_sec8_attacker", "Sec8 Attacker", now, now)
	require.NoError(t, err)
	_, err = h.state.DB().ExecContext(context.Background(), `
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
		VALUES (?, ?, ?, ?)
	`, "ses_attacker_sec8", "impl_sec8_attacker", now, now)
	require.NoError(t, err)

	h.setResolver(&fakeResolver{
		id: &peercred.ResolvedIdentity{
			AgentID:  "impl_sec8_attacker",
			Worktree: "/tmp/attacker",
			PID:      os.Getpid(),
		},
	})

	delResp, err := h.sendRPC(t, "message.deleteByAgent", map[string]any{
		"agent_id": "impl_sec3_test", // target is agent A, caller is B
	})
	require.NoError(t, err)
	require.NotNil(t, delResp.Error, "non-self deleteByAgent must be rejected")
	require.True(t,
		strings.Contains(delResp.Error.Message, "only the target agent can bulk-delete"),
		"error should explain self-only restriction, got: %q", delResp.Error.Message)

	// Prove messages survived.
	var count int
	row := h.state.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM messages WHERE agent_id = ?`, "impl_sec3_test")
	require.NoError(t, row.Scan(&count))
	require.Greater(t, count, 0, "messages must not have been deleted by non-owner")
}

// TestSec8_DeleteByAgent_CallerMatchesSelf proves the happy path: agent A
// CAN bulk-delete their own messages.
func TestSec8_DeleteByAgent_CallerMatchesSelf(t *testing.T) {
	resolver := &fakeResolver{
		id: &peercred.ResolvedIdentity{
			AgentID:  "impl_sec3_test",
			Worktree: "/tmp/fake-worktree",
			PID:      os.Getpid(),
		},
	}
	h := newSec3Harness(t, resolver, true)
	h.server.RegisterHandler("message.deleteByAgent", h.msgHandler.HandleDeleteByAgent)

	// Send a couple messages.
	for i := 0; i < 3; i++ {
		resp, err := h.sendRPC(t, "message.send", map[string]any{
			"to":      "impl_sec3_test",
			"content": "msg to self-delete",
		})
		require.NoError(t, err)
		require.Nil(t, resp.Error)
	}

	// Self-delete: agent A deletes their own messages.
	delResp, err := h.sendRPC(t, "message.deleteByAgent", map[string]any{
		"agent_id": "impl_sec3_test",
	})
	require.NoError(t, err)
	require.Nil(t, delResp.Error, "self deleteByAgent should succeed; got: %+v", delResp.Error)

	var result struct {
		DeletedCount int `json:"deleted_count"`
	}
	require.NoError(t, json.Unmarshal(delResp.Result, &result))
	require.GreaterOrEqual(t, result.DeletedCount, 3, "should have deleted all sent messages")

	// Verify messages are gone.
	var count int
	row := h.state.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM messages WHERE agent_id = ?`, "impl_sec3_test")
	require.NoError(t, row.Scan(&count))
	require.Equal(t, 0, count, "all messages should be hard-deleted")
}

// TestSec8_DeleteByScope_RejectedForExternalCallers proves that
// message.deleteByScope is restricted to daemon-internal callers. When the
// request arrives over a unix-socket connection (peercred was injected
// into ctx), the handler rejects it regardless of who the caller is.
func TestSec8_DeleteByScope_RejectedForExternalCallers(t *testing.T) {
	resolver := &fakeResolver{
		id: &peercred.ResolvedIdentity{
			AgentID:  "impl_sec3_test",
			Worktree: "/tmp/fake-worktree",
			PID:      os.Getpid(),
		},
	}
	h := newSec3Harness(t, resolver, true)
	h.server.RegisterHandler("message.deleteByScope", h.msgHandler.HandleDeleteByScope)

	delResp, err := h.sendRPC(t, "message.deleteByScope", map[string]any{
		"scope_type":  "group",
		"scope_value": "anything",
	})
	require.NoError(t, err)
	require.NotNil(t, delResp.Error, "deleteByScope must be rejected for external callers")
	require.True(t,
		strings.Contains(delResp.Error.Message, "restricted to daemon-internal"),
		"error should explain daemon-internal restriction, got: %q", delResp.Error.Message)
}
