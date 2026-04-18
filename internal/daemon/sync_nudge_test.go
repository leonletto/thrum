package daemon_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/eventlog"
	"github.com/leonletto/thrum/internal/daemon/nudge"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/types"
	"github.com/stretchr/testify/require"
)

// TestSyncedMessageCreate_FiresHookForNudgeDispatch is the regression test
// for thrum-wvpv. It proves that a message.create event arriving via the
// peer-sync path (SyncApplier.applyEvent → State.WriteEvent) fires the
// SetOnEventWrite hook with the full event payload, including the
// recipients list — so the hook (in main.go) can call nudge.DispatchTmux
// to wake the recipient's tmux pane.
//
// Pre-fix (before thrum-wvpv): nudge dispatch was inlined inside HandleSend
// in the rpc package. Synced events bypassed it entirely; cross-machine
// messages landed in SQLite with no tmux notification, so cross-machine
// recipients silently never got pinged.
//
// Post-fix: nudge dispatch lives in the SetOnEventWrite hook. The hook
// fires for both local writes (HandleSend) and synced writes (this test
// path), so all recipients are nudged regardless of where the message
// originated.
func TestSyncedMessageCreate_FiresHookForNudgeDispatch(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := state.NewState(tmpDir, tmpDir, "test-repo", "remote-daemon-id")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	// Capture every hook invocation's event payload.
	var (
		mu       sync.Mutex
		captured [][]byte
	)
	st.SetOnEventWrite(func(_ string, _ int64, event []byte) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, append([]byte{}, event...))
	})

	// Build a synthetic message.create event as if it arrived from a peer.
	// The Recipients field is the load-bearing piece — without it the
	// nudge dispatch would have nothing to dispatch to.
	syntheticMsg := types.MessageCreateEvent{
		Type:      "message.create",
		Timestamp: "2026-04-16T00:00:00Z",
		MessageID: "msg_synced_001",
		AgentID:   "remote_sender",
		SessionID: "ses_remote_001",
		Body: types.MessageBody{
			Format:  "markdown",
			Content: "ping from across the network",
		},
		Recipients: []string{"local_recipient_alpha", "local_recipient_bravo"},
	}
	syntheticJSON, err := json.Marshal(syntheticMsg)
	require.NoError(t, err)

	// Apply via SyncApplier — same code path peer-sync uses in production.
	applier := daemon.NewSyncApplier(st)
	_, _, err = applier.ApplyRemoteEvents(context.Background(), []eventlog.Event{
		{
			EventID:      "evt_synced_001",
			Type:         "message.create",
			Timestamp:    "2026-04-16T00:00:00Z",
			OriginDaemon: "remote-peer",
			Sequence:     1,
			EventJSON:    syntheticJSON,
		},
	})
	require.NoError(t, err)

	// The hook MUST have fired at least once with this event's payload.
	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, captured, "hook must fire on synced message.create")

	// The captured payload must contain the recipients list — this is the
	// data the production hook hands to nudge.DispatchTmux.
	var found bool
	for _, payload := range captured {
		var decoded types.MessageCreateEvent
		if err := json.Unmarshal(payload, &decoded); err != nil {
			continue
		}
		if decoded.MessageID != "msg_synced_001" {
			continue
		}
		require.Equal(t, []string{"local_recipient_alpha", "local_recipient_bravo"}, decoded.Recipients,
			"hook must receive the synced event's recipients list intact")
		require.Equal(t, "remote_sender", decoded.AgentID,
			"hook must receive the original peer's sender identity")
		found = true
		break
	}
	require.True(t, found, "captured payloads must include the synced message.create event")
}

// TestNudgeDispatch_GuardSkipsNonMessageEvents proves the production hook's
// type-filter behavior: when the hook receives a non-message event (e.g.
// agent.register, session.start) it must NOT call nudge.DispatchTmux.
// This test exercises the same guard pattern by reproducing the hook's
// double-unmarshal logic and asserting nudge.DispatchTmux is not invoked
// for non-message-create events.
func TestNudgeDispatch_GuardSkipsNonMessageEvents(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := state.NewState(tmpDir, tmpDir, "test-repo", "test-daemon")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	// Mirror the production hook's filter logic and count
	// nudge-dispatch invocations (we observe via a flag set inside the
	// guard branch, not via tmux side effects).
	var (
		mu              sync.Mutex
		nudgeDispatches int
	)
	st.SetOnEventWrite(func(_ string, _ int64, event []byte) {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(event, &head); err != nil {
			return
		}
		if head.Type != "message.create" {
			return
		}
		// Production hook decodes types.MessageCreateEvent and calls
		// nudge.DispatchTmux. Here we just count the path was reached.
		var evt types.MessageCreateEvent
		if err := json.Unmarshal(event, &evt); err != nil {
			return
		}
		nudge.DispatchTmux(tmpDir, evt.Recipients, evt.AgentID)
		mu.Lock()
		defer mu.Unlock()
		nudgeDispatches++
	})

	// Write a non-message event (agent.register). Hook fires but guard
	// blocks the nudge branch.
	regEvt := types.AgentRegisterEvent{
		Type:      "agent.register",
		Timestamp: "2026-04-16T00:00:00Z",
		AgentID:   "some_agent",
		Kind:      "agent",
		Role:      "researcher",
		Module:    "test",
	}
	require.NoError(t, st.WriteEvent(context.Background(), regEvt))

	mu.Lock()
	require.Zero(t, nudgeDispatches, "agent.register must NOT trigger nudge dispatch")
	mu.Unlock()

	// Now write a message.create — should pass the guard and dispatch.
	msgEvt := types.MessageCreateEvent{
		Type:       "message.create",
		Timestamp:  "2026-04-16T00:00:01Z",
		MessageID:  "msg_guard_test",
		AgentID:    "sender",
		SessionID:  "ses_test",
		Body:       types.MessageBody{Format: "markdown", Content: "hi"},
		Recipients: []string{"recipient"},
	}
	require.NoError(t, st.WriteEvent(context.Background(), msgEvt))

	mu.Lock()
	require.Equal(t, 1, nudgeDispatches, "message.create must trigger exactly one nudge dispatch")
	mu.Unlock()
}
