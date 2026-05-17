package reminders

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// MessageSender delivers a reminder body to a single agent's inbox.
// Real implementation wraps internal/daemon/rpc.MessageHandler.HandleSend
// or a lower-level state.State emit. The interface stays narrow so
// DeliverySink can be unit-tested without spinning up the JSON-RPC
// layer.
type MessageSender interface {
	SendReminder(ctx context.Context, fromAgent, toAgent, body string) error
}

// EmailQueue queues a reminder for outbound email delivery. Real impl
// writes to email_outbound_queue (D-B1 schema). Nil-safe: when
// DeliverySink is constructed without an EmailQueue, email-shaped
// chain entries are logged + skipped rather than aborting delivery.
type EmailQueue interface {
	QueueReminderEmail(ctx context.Context, address, subject, body string) error
}

// SupervisorMaybeRouter wraps the SupervisorRouter from
// thrum-6qmf.3.11. Separate interface here so .8 (DeliverySink) and
// .11 (SupervisorRouter) can land in either order — .8 declares the
// shape it needs; .11 satisfies it.
//
// MaybeRoute returns (routed=true, nil) when the supervisor took
// ownership of the delivery, (routed=false, nil) when the caller
// should fall through to normal inbox send, and (routed=false, err)
// only on unexpected internal errors (lookup failures are silent
// fall-throughs per the conservative default).
type SupervisorMaybeRouter interface {
	MaybeRoute(ctx context.Context, r *Reminder) (routed bool, err error)
}

// DeliverySink routes fired reminders by (source, trigger_kind, target).
// Replaces NoopFireSink at daemon-wiring time (thrum-6qmf.3.15). One
// instance per daemon; thread-safe by virtue of its dependencies
// (MessageHandler + safedb-backed EmailQueue + SupervisorRouter all
// hold their own locks).
//
// Routing matrix (per plan §Task 25 + canonical §3.5):
//
//	(daemon, condition_pane_quiet) → fan to target_chain
//	    @agent entries  → MessageSender.SendReminder
//	    email-address entries → EmailQueue.QueueReminderEmail
//	(daemon, time)                → same chain fan-out
//	(agent | user, time)           → SupervisorRouter.MaybeRoute → if
//	                                 not routed, MessageSender to
//	                                 target_agent
type DeliverySink struct {
	msg        MessageSender
	email      EmailQueue          // optional; nil → skip email chain entries
	supervisor SupervisorMaybeRouter // optional; nil → skip permission-prompt detection
}

// NewDeliverySink wires the three delivery seams. The msg sender is
// required; email + supervisor are optional (nil-safe paths log + skip
// or fall through to normal delivery).
func NewDeliverySink(msg MessageSender, email EmailQueue, supervisor SupervisorMaybeRouter) *DeliverySink {
	return &DeliverySink{msg: msg, email: email, supervisor: supervisor}
}

// Fire dispatches a single fired reminder per the routing matrix
// above. Returns an error only when every delivery path failed —
// partial-failure (e.g. one chain entry's email queue is down but
// the @agent send succeeded) is logged and treated as success so the
// Dispatcher's state transition still runs.
//
// Note: the at-least-once semantics live one layer up — Dispatcher
// aborts the Store.Fire transition when FireSink.Fire returns an
// error, leaving the row open for the next tick. So returning nil
// here when ANY recipient got the message is the right tradeoff:
// re-firing a fully-failed delivery makes sense; re-firing a
// partially-delivered one would duplicate-fire on the successful
// recipients.
func (d *DeliverySink) Fire(ctx context.Context, r *Reminder) error {
	if r == nil {
		return errors.New("DeliverySink.Fire: nil reminder")
	}
	switch {
	case r.Source == SourceDaemon:
		// Daemon-source reminders (both condition_pane_quiet and time)
		// route through the chain. The plan distinguishes them
		// rhetorically — same routing impl since the chain shape is
		// identical.
		return d.fanToChain(ctx, r)
	case (r.Source == SourceAgent || r.Source == SourceUser) && r.TriggerKind == TriggerTime:
		return d.deliverToTarget(ctx, r)
	default:
		// Polymorphism violation: validator should have caught this at
		// mint time. Log loudly and return an error so the dispatcher
		// re-fires (in case a future Store change accidentally
		// bypassed Validate).
		return fmt.Errorf("DeliverySink.Fire: unsupported (source=%q, trigger_kind=%q) for %s",
			r.Source, r.TriggerKind, r.ID)
	}
}

// fanToChain delivers to every entry in TargetChain. Agent entries
// (starting with "@") route to MessageSender; email entries (contain
// "@" but don't start with one) route to EmailQueue. Unknown shapes
// are logged and skipped — never abort the chain.
//
// At least one recipient must succeed for fanToChain to return nil.
// All-failure returns an error so the dispatcher leaves the row open
// for retry.
func (d *DeliverySink) fanToChain(ctx context.Context, r *Reminder) error {
	if len(r.TargetChain) == 0 {
		// Daemon-condition rows require target_chain per Validate;
		// a row reaching delivery without one indicates Store
		// corruption.
		return fmt.Errorf("DeliverySink.fanToChain: %s has empty target_chain", r.ID)
	}

	agentBody := FormatAgentBody(r)
	subject, emailBody := FormatEmail(r, time.Now().UTC())

	var (
		delivered int
		errs      []string
	)
	for _, entry := range r.TargetChain {
		switch {
		case isAgentRef(entry):
			agent := strings.TrimPrefix(entry, "@")
			if err := d.msg.SendReminder(ctx, "daemon", agent, agentBody); err != nil {
				errs = append(errs, fmt.Sprintf("send to @%s: %v", agent, err))
				continue
			}
			delivered++
		case isEmailAddress(entry):
			if d.email == nil {
				log.Printf("[reminders] %s: email entry %q in chain but no EmailQueue wired; skipping",
					r.ID, entry)
				continue
			}
			if err := d.email.QueueReminderEmail(ctx, entry, subject, emailBody); err != nil {
				errs = append(errs, fmt.Sprintf("queue email %s: %v", entry, err))
				continue
			}
			delivered++
		default:
			log.Printf("[reminders] %s: chain entry %q has unrecognized shape (not @agent, not email); skipping",
				r.ID, entry)
		}
	}

	if delivered == 0 {
		return fmt.Errorf("DeliverySink.fanToChain: no recipients reached for %s; errors: %s",
			r.ID, strings.Join(errs, "; "))
	}
	if len(errs) > 0 {
		log.Printf("[reminders] %s: partial chain delivery (%d ok, %d failed): %s",
			r.ID, delivered, len(errs), strings.Join(errs, "; "))
	}
	return nil
}

// deliverToTarget handles agent/user time reminders. Optional
// supervisor-routing (for at-permission-prompt targets) runs first;
// fall through to normal inbox send when supervisor declines.
func (d *DeliverySink) deliverToTarget(ctx context.Context, r *Reminder) error {
	if r.TargetAgent == "" {
		return fmt.Errorf("DeliverySink.deliverToTarget: %s has empty target_agent", r.ID)
	}

	// Supervisor pass: declines silently when the target isn't at a
	// permission prompt or when the router is nil.
	if d.supervisor != nil {
		routed, err := d.supervisor.MaybeRoute(ctx, r)
		if err != nil {
			// Supervisor unexpectedly errored. Log + fall through to
			// normal delivery — conservative default per Task 26
			// "better to over-deliver than drop".
			log.Printf("[reminders] %s: supervisor.MaybeRoute error: %v (falling through to inbox)",
				r.ID, err)
		} else if routed {
			return nil
		}
	}

	// Source label is "daemon" for daemon-authored time rows (handled
	// in fanToChain above so we won't reach here for those), the
	// originating agent for agent-source, and empty for user-source.
	from := ""
	if r.Source == SourceAgent {
		from = r.SourceAgent
	}
	return d.msg.SendReminder(ctx, from, r.TargetAgent, FormatAgentBody(r))
}

// isAgentRef recognizes "@agent_name" entries in target_chain.
func isAgentRef(entry string) bool {
	return strings.HasPrefix(entry, "@") && len(entry) > 1
}

// isEmailAddress recognizes "user@example.com" entries. Intentionally
// loose: no full RFC 5322 validation — the chain is operator-curated
// and the email queue does its own validation downstream. We just need
// to distinguish from @agent refs.
func isEmailAddress(entry string) bool {
	if strings.HasPrefix(entry, "@") {
		return false
	}
	at := strings.Index(entry, "@")
	if at <= 0 || at == len(entry)-1 {
		return false
	}
	// Bare-minimum domain check: at least one dot after the @.
	domain := entry[at+1:]
	return strings.Contains(domain, ".")
}

// Compile-time check that DeliverySink satisfies FireSink.
var _ FireSink = (*DeliverySink)(nil)
