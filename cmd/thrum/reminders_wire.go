package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/reminders"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// wireReminders constructs the A-B4 reminder Dispatcher with the real
// DeliverySink (swapping out the NoopFireSink placeholder from
// thrum-6qmf.3.27) and registers it as the internal.reminder_dispatch
// job (canonical §6.3). Mirrors wireScheduler + wireSweep so all
// daemon-boot internal-job wiring follows one pattern.
//
// Three collaborators come together:
//   - MessageSender: messageHandlerSender adapter wraps the existing
//     *rpc.MessageHandler.HandleSend so reminders deliver via the
//     canonical message-send pipeline (inbox + sync + events).
//   - EmailQueue: nil for now. D-B1 (email substrate) lands later in
//     v0.11; DeliverySink.fanToChain log-and-skips email chain entries
//     when this is nil. Operators get a log line ("email entry in
//     chain but no EmailQueue wired") rather than a silent drop.
//   - SupervisorMaybeRouter: nil for now. B-B1's supervisor pane
//     registry + tmux pane-state resolver land in a follow-on epic.
//     DeliverySink.deliverToTarget treats nil supervisor as "skip the
//     permission-prompt check" and routes straight to normal inbox
//     send (the conservative default: over-deliver rather than drop).
//
// Cadence comes from daemon.reminders.dispatch_interval_seconds
// (canonical §4.4 default 30s). The 30s default is much finer-grained
// than the 15-min stalled-sweep cadence — user-set reminders with
// minute-resolution trigger_at values (Leon-brainstorm-Q3.3) need
// sub-minute latency.
//
// PANICS if sched.RegisterInternal panics (programmer error: duplicate
// ID or bad shape; daemon should crash early per A-B1 spec §5.3).
func wireReminders(
	sched *scheduler.Scheduler,
	store reminders.Store,
	msgHandler *rpc.MessageHandler,
	supervisorID string,
	cfg *config.DaemonConfig,
) {
	dispatchSeconds := cfg.RemindersDispatchIntervalSeconds()
	sweepInterval := time.Duration(cfg.StalledSweepIntervalMinutes()) * time.Minute

	sink := reminders.NewDeliverySink(
		&messageHandlerSender{
			handler:         msgHandler,
			fallbackSender:  supervisorID,
		},
		nil, // EmailQueue — D-B1 substrate not yet wired
		nil, // SupervisorMaybeRouter — B-B1 supervisor + AgentRuntimeResolver pending
	)
	dispatcher := reminders.NewDispatcher(
		store,
		sink,
		reminders.SweepInterval{Interval: sweepInterval},
	)
	dispatcher.Register(sched, dispatchSeconds)
}

// messageHandlerSender satisfies reminders.MessageSender by adapting
// the existing *rpc.MessageHandler.HandleSend entry point. Each
// reminder delivery goes through the canonical message-send pipeline,
// which means recipients see the reminder body in `thrum inbox` like
// any other message + subscriptions/event-log/sync all fire normally.
//
// Routing per E4.5 plan §Task 24: agent_body is the terse fire message
// ("Idle Agent Detected with idle-id: <id> — run \`thrum agent reminder
// <id>\`" or "Reminder fired: ..." for time-triggered). The full body
// lives in the reminders table and surfaces via
// `thrum agent reminder <id>` lookup — the inbox message is just the
// pointer.
//
// fallbackSender is used when the caller passes fromAgent="daemon"
// or "" — those source labels don't map to a session-bearing agent
// in HandleSend's pipeline, so we substitute the synthetic
// supervisor agent (always registered at daemon boot with an active
// session). Agent-source reminders (fromAgent=SourceAgent) pass
// through unchanged because the source agent does have a session.
type messageHandlerSender struct {
	handler        *rpc.MessageHandler
	fallbackSender string
}

// SendReminder dispatches one reminder-message to toAgent. fromAgent
// is the source-of-truth label (DeliverySink passes "daemon" for
// daemon-source rows, SourceAgent for agent-source, empty for
// user-source). Daemon/empty source labels are remapped to
// fallbackSender so HandleSend can resolve the sender session.
// Errors from the message pipeline surface verbatim so the caller
// (DeliverySink) can decide partial-vs-total-failure handling.
func (s *messageHandlerSender) SendReminder(ctx context.Context, fromAgent, toAgent, body string) error {
	if toAgent == "" {
		return fmt.Errorf("messageHandlerSender.SendReminder: empty toAgent")
	}
	if s.handler == nil {
		return fmt.Errorf("messageHandlerSender.SendReminder: nil handler (wiring bug)")
	}

	caller := fromAgent
	if caller == "" || caller == "daemon" {
		// Daemon-source / user-source reminders don't have a session-
		// bearing source agent. The synthetic supervisor identity
		// (wired at daemon boot) carries an active session and is the
		// canonical sender for daemon-authored outbound messages.
		caller = s.fallbackSender
	}
	if caller == "" {
		return fmt.Errorf("messageHandlerSender.SendReminder: no caller resolvable (fromAgent=%q, no fallbackSender configured)", fromAgent)
	}

	params := rpc.SendRequest{
		Content:       body,
		Format:        "markdown",
		To:            toAgent,
		CallerAgentID: caller,
		Tags:          []string{"reminder"},
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("messageHandlerSender: marshal send params: %w", err)
	}
	if _, err := s.handler.HandleSend(ctx, raw); err != nil {
		return fmt.Errorf("messageHandlerSender: HandleSend to %s: %w", toAgent, err)
	}
	return nil
}

// Compile-time check that messageHandlerSender satisfies the
// reminders.MessageSender interface — catches signature drift on
// either side.
var _ reminders.MessageSender = (*messageHandlerSender)(nil)
