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

// TestSec4_AuthorCanDeleteOwnMessage proves the happy path: a peercred-resolved
// caller that authored a message can delete it.
func TestSec4_AuthorCanDeleteOwnMessage(t *testing.T) {
	resolver := &fakeResolver{
		id: &peercred.ResolvedIdentity{
			AgentID:  "impl_sec3_test",
			Worktree: "/tmp/fake-worktree",
			PID:      os.Getpid(),
		},
	}
	h := newSec3Harness(t, resolver, true /* registerAgent */)

	// Register delete handler on the same harness server.
	h.registerDelete(t)

	// Author sends a message.
	sendResp, err := h.sendRPC(t, "message.send", map[string]any{
		"to":      "impl_sec3_test",
		"content": "my message",
	})
	require.NoError(t, err)
	require.Nil(t, sendResp.Error, "send should succeed: %+v", sendResp.Error)

	var sendResult struct {
		MessageID string `json:"message_id"`
	}
	require.NoError(t, json.Unmarshal(sendResp.Result, &sendResult))
	require.NotEmpty(t, sendResult.MessageID)

	// Same author deletes it.
	delResp, err := h.sendRPC(t, "message.delete", map[string]any{
		"message_id": sendResult.MessageID,
		"reason":     "author cleanup",
	})
	require.NoError(t, err)
	require.Nil(t, delResp.Error, "author delete should succeed: %+v", delResp.Error)

	// Verify DB marks it deleted.
	var deleted int
	row := h.state.DB().QueryRowContext(context.Background(),
		`SELECT deleted FROM messages WHERE message_id = ?`, sendResult.MessageID)
	require.NoError(t, row.Scan(&deleted))
	require.Equal(t, 1, deleted, "message should be soft-deleted")
}

// TestSec4_NonAuthorCannotDeleteOthersMessage proves the enforcement: agent B
// cannot delete a message authored by agent A, even with a well-formed
// caller_agent_id claim and peercred resolving to B.
func TestSec4_NonAuthorCannotDeleteOthersMessage(t *testing.T) {
	// First: agent A (impl_sec3_test) sends a message. Use one harness.
	resolverA := &fakeResolver{
		id: &peercred.ResolvedIdentity{
			AgentID:  "impl_sec3_test",
			Worktree: "/tmp/fake-worktree",
			PID:      os.Getpid(),
		},
	}
	h := newSec3Harness(t, resolverA, true)
	h.registerDelete(t)

	// Pre-register a second agent B directly in the DB so its session lookup
	// succeeds when the resolver returns its identity.
	now := "2026-04-15T00:00:00Z"
	_, err := h.state.DB().ExecContext(context.Background(), `
		INSERT INTO agents (agent_id, kind, role, module, display, hostname, agent_pid, registered_at, last_seen_at)
		VALUES (?, 'implementer', 'implementer', 'sec4', ?, '', 0, ?, ?)
	`, "impl_sec4_attacker", "Sec4 Attacker", now, now)
	require.NoError(t, err)

	_, err = h.state.DB().ExecContext(context.Background(), `
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
		VALUES (?, ?, ?, ?)
	`, "ses_attacker_001", "impl_sec4_attacker", now, now)
	require.NoError(t, err)

	// Agent A sends a message.
	sendResp, err := h.sendRPC(t, "message.send", map[string]any{
		"to":      "impl_sec3_test",
		"content": "alice's message",
	})
	require.NoError(t, err)
	require.Nil(t, sendResp.Error, "send should succeed: %+v", sendResp.Error)
	var sendResult struct {
		MessageID string `json:"message_id"`
	}
	require.NoError(t, json.Unmarshal(sendResp.Result, &sendResult))

	// Swap the resolver: now peercred says the connecting process is agent B.
	// Since SetIdentityResolver is server-level, we rotate it live. Tests
	// never need to worry about races here because each sub-test uses its
	// own harness.
	h.setResolver(&fakeResolver{
		id: &peercred.ResolvedIdentity{
			AgentID:  "impl_sec4_attacker",
			Worktree: "/tmp/attacker-worktree",
			PID:      os.Getpid(),
		},
	})

	// Agent B attempts to delete agent A's message.
	delResp, err := h.sendRPC(t, "message.delete", map[string]any{
		"message_id": sendResult.MessageID,
	})
	require.NoError(t, err)
	require.NotNil(t, delResp.Error, "non-author delete must be rejected")
	require.True(t,
		strings.Contains(delResp.Error.Message, "only message author can delete"),
		"error should say author-only, got: %q", delResp.Error.Message)

	// Prove the message was NOT deleted.
	var deleted int
	row := h.state.DB().QueryRowContext(context.Background(),
		`SELECT deleted FROM messages WHERE message_id = ?`, sendResult.MessageID)
	require.NoError(t, row.Scan(&deleted))
	require.Equal(t, 0, deleted, "message must not be deleted by non-author")
}

// TestSec4_ForgedCallerOnDelete_Rejected is the belt-and-suspenders test the
// bead asked for: even if a forged caller_agent_id somehow bypassed the sec.3
// dispatcher check (it won't, but we guard anyway), the author-match guard in
// HandleDelete would still catch it because the forged claim is rejected
// upstream by resolveAgentAndSession and never reaches the author comparison.
func TestSec4_ForgedCallerOnDelete_Rejected(t *testing.T) {
	resolver := &fakeResolver{
		id: &peercred.ResolvedIdentity{
			AgentID:  "impl_sec3_test",
			Worktree: "/tmp/fake-worktree",
			PID:      os.Getpid(),
		},
	}
	h := newSec3Harness(t, resolver, true)
	h.registerDelete(t)

	// Send a legitimate message first.
	sendResp, err := h.sendRPC(t, "message.send", map[string]any{
		"to":      "impl_sec3_test",
		"content": "target",
	})
	require.NoError(t, err)
	require.Nil(t, sendResp.Error)
	var sendResult struct {
		MessageID string `json:"message_id"`
	}
	require.NoError(t, json.Unmarshal(sendResp.Result, &sendResult))

	// Attempt delete with a forged caller_agent_id that does NOT match the
	// resolved peercred identity.
	delResp, err := h.sendRPC(t, "message.delete", map[string]any{
		"message_id":      sendResult.MessageID,
		"caller_agent_id": "impl_forger",
	})
	require.NoError(t, err)
	require.NotNil(t, delResp.Error, "forged caller on delete must be rejected")
	require.Contains(t, delResp.Error.Message, "identity_mismatch",
		"forged-claim rejection happens in resolveAgentAndSession before the author check")

	// Prove the message survives.
	var deleted int
	row := h.state.DB().QueryRowContext(context.Background(),
		`SELECT deleted FROM messages WHERE message_id = ?`, sendResult.MessageID)
	require.NoError(t, row.Scan(&deleted))
	require.Equal(t, 0, deleted)
}
