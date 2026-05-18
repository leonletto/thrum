package rpc

import (
	"context"
	"database/sql"
	"errors"

	"github.com/leonletto/thrum/internal/daemon/state"
	ttmux "github.com/leonletto/thrum/internal/tmux"
)

// NewTmuxPaneCapture returns a PaneCaptureFunc backed by
// ttmux.CapturePane. The agent name is mapped 1:1 to the tmux session
// name (`agentName:0.0` target) — the canonical thrum convention
// where `thrum tmux create <agentName>` creates session <agentName>.
// When the session doesn't exist or capture-pane fails, the wrapper
// returns the underlying error; RenderBodyFallbackChain treats any
// non-nil error as "branch 1 unavailable" and falls through to the
// next branch.
func NewTmuxPaneCapture() PaneCaptureFunc {
	return func(_ context.Context, agentName string, lines int) (string, error) {
		if agentName == "" {
			return "", errors.New("empty agent name")
		}
		return ttmux.CapturePane(agentName+":0.0", lines)
	}
}

// NewMessagesOutboundLookup returns an OutboundLookupFunc that
// reads the most recent message authored by the agent from the
// `messages` table. "Outbound from agent" is defined per spec §7.6
// branch 3 ("what the agent SAID") — `messages.agent_id` carries
// the sender. Soft-deleted messages are excluded so the "last said"
// line stays consistent with what the operator sees in the agent's
// inbox UI.
//
// Returns (nil, nil) when the agent has no outbound history; the
// chain falls through to branch 4. SQL errors (other than sql.ErrNoRows)
// surface to the caller; RenderBodyFallbackChain absorbs them as
// "branch 3 unavailable" via its err == nil short-circuit.
func NewMessagesOutboundLookup(st *state.State) OutboundLookupFunc {
	return func(ctx context.Context, agentName string) (*OutboundMessage, error) {
		if st == nil {
			return nil, errors.New("nil state (wiring bug)")
		}
		if agentName == "" {
			return nil, errors.New("empty agent name")
		}
		// Bare subject derivation: messages don't carry a structured
		// subject field today, so we surface the first ~80 chars of
		// body_content as the "subject" line. Body is preserved
		// verbatim except whitespace-collapsed on the leading line.
		const q = `SELECT message_id, COALESCE(body_content, '')
		           FROM messages
		           WHERE agent_id = ?
		             AND COALESCE(deleted, 0) = 0
		           ORDER BY created_at DESC
		           LIMIT 1`
		var msgID, body string
		err := st.DB().QueryRowContext(ctx, q, agentName).Scan(&msgID, &body)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, nil
			}
			return nil, err
		}
		return &OutboundMessage{
			MessageID: msgID,
			Subject:   firstLineSnippet(body, 80),
		}, nil
	}
}

// firstLineSnippet trims the first newline-delimited line of s,
// capping the result at maxLen RUNES (not bytes — naive byte-slicing
// would split a multi-byte UTF-8 sequence and corrupt the output).
// Used by NewMessagesOutboundLookup so the "Last said:" line doesn't
// bleed into multiple rows.
func firstLineSnippet(s string, maxLen int) string {
	for i, r := range s {
		if r == '\n' || r == '\r' {
			s = s[:i]
			break
		}
	}
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "…"
	}
	return s
}
