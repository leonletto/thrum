package permission

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/types"
)

// SendSupervisorMessage writes a message.create event authored by the
// permission supervisor pseudo-agent and returns the generated
// message_id. The caller stores that ID as the permission_nudges row
// primary key so incoming reply-to messages can be mapped back to the
// originating nudge.
//
// Mirrors internal/daemon/rpc/queue_rpc.go:474 sendSystemMessage but
// uses p.supervisorID (e.g. "supervisor_thrum") as the author instead
// of "system". @system messages cannot be replied to (rejectSystemReply
// blocks them), so the permission feature needs its own sender
// identity — that's what the reserved pseudo-agent from Task 4.2
// provides.
//
// IMPORTANT: the state.WriteEvent call MUST run under state.Lock().
// sendSystemMessage establishes this pattern and we copy it.
func (p *Permission) SendSupervisorMessage(ctx context.Context, to, body string) (string, error) {
	if p.state == nil {
		return "", fmt.Errorf("permission.SendSupervisorMessage: nil state")
	}
	if to == "" {
		return "", fmt.Errorf("permission.SendSupervisorMessage: empty recipient")
	}

	// Normalise the recipient to the bare-name form (no leading "@").
	// ResolveSupervisors deliberately returns @-prefixed strings as its
	// external contract, but the message_deliveries / message_refs
	// tables store bare agent IDs — the regular message.create path in
	// internal/daemon/rpc/message.go TrimPrefix's "@" before populating
	// Recipients/Refs, and the inbox query filters by bare agent_id.
	// Sending an @-prefixed recipient here silently routes to a ghost
	// agent that no inbox ever matches, so the nudge is invisible.
	bareTo := strings.TrimPrefix(strings.TrimSpace(to), "@")
	if bareTo == "" {
		return "", fmt.Errorf("permission.SendSupervisorMessage: recipient %q reduced to empty after normalisation", to)
	}

	msgID := identity.GenerateMessageID()
	event := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		EventID:   identity.GenerateEventID(),
		Version:   1,
		MessageID: msgID,
		AgentID:   p.supervisorID,
		// Sentinel session_id so the messages row stays queryable. The
		// supervisor pseudo-agent has no real session; see
		// supervisorSessionID (permission.go) for the rationale and
		// for the compile-time anchor that any future reply-parser or
		// inbox filter should reference.
		SessionID: supervisorSessionID,
		Body: types.MessageBody{
			Format:  "markdown",
			Content: body,
		},
		Refs:       []types.Ref{{Type: "mention", Value: bareTo}},
		Recipients: []string{bareTo},
	}

	p.state.Lock()
	defer p.state.Unlock()
	if err := p.state.WriteEvent(ctx, event); err != nil {
		return "", fmt.Errorf("write supervisor message: %w", err)
	}
	return msgID, nil
}
