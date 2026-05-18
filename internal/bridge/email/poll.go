package email

import (
	"context"
	"time"

	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// PollHandler is the A-B1 substrate handler behind
// `internal.email_poll`. Each Dispatch reads from the long-lived IMAP
// connection owned by Bridge.run() and feeds raw messages through the
// inbound pipeline. The handler no-ops cleanly when the bridge is not
// running so the scheduler can keep ticking across bridge restart cycles.
//
// thrum-6qmf.8 substrate-adoption: replaces the in-bridge
// `inboundPumpLoop` ticker that shipped with D-B1.14.
type PollHandler struct {
	bridge *Bridge
}

// NewPollHandler binds a handler to the email bridge. The bridge may be
// stopped at registration time — Dispatch tolerates a nil IMAP / Inbound.
func NewPollHandler(b *Bridge) *PollHandler { return &PollHandler{bridge: b} }

// Stages declares the single execution phase consumed by A-B4
// stalled-sweep dwell tracking. The 5-minute ceiling accommodates large
// 24-hour-window backfills on slow IMAP servers without nudging.
func (h *PollHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"polling": 5 * time.Minute}
}

// Reconcile reports completed unconditionally — a poll interrupted by a
// crash leaves no durable claim and the next tick covers the same
// 24-hour lookback window. The dedup table makes any double-processing
// a no-op.
func (h *PollHandler) Reconcile(_ context.Context, _ scheduler.JobSpec, _ string, _ scheduler.State) (scheduler.State, error) {
	return scheduler.StateCompleted, nil
}

// Dispatch performs one fetch+process+mark-seen cycle.
func (h *PollHandler) Dispatch(ctx context.Context, _ scheduler.JobSpec, _ string, reporter scheduler.StateReporter, _ <-chan *scheduler.Completion) error {
	if err := reporter.Transition(scheduler.StateRunning, "polling inbox", nil); err != nil {
		return err
	}
	if err := reporter.Stage("polling"); err != nil {
		return err
	}

	// imap/inbound may become nil between the two loads if the bridge
	// restarts mid-tick; the nil-check below covers the common case. A
	// rarer in-flight restart (both non-nil at load, then bridge closes
	// the IMAP connection before Fetch) surfaces as IMAPClient.Fetch
	// returning "not connected" — handled below as StateFailed, which
	// the scheduler tolerates as a transient error.
	imap := h.bridge.IMAPClient()
	inbound := h.bridge.Inbound()
	if imap == nil || inbound == nil {
		// Bridge not running (or in a restart gap) — treat as a successful
		// no-op tick rather than a failure so consecutive_failures stays
		// bounded across long bridge-down windows.
		return reporter.Transition(scheduler.StateCompleted, "bridge not running; skipped", nil)
	}

	msgs, err := imap.Fetch(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		if ctx.Err() != nil {
			return reporter.Transition(scheduler.StateCompleted, "cancelled", nil)
		}
		return reporter.Transition(scheduler.StateFailed, "fetch: "+err.Error(), nil)
	}

	for _, msg := range msgs {
		if ctx.Err() != nil {
			break
		}
		action, perr := inbound.ProcessMessage(ctx, msg.Bytes, msg.UID)
		if perr != nil {
			// Log via bridge but keep going — one malformed message
			// doesn't poison the tick.
			h.bridge.logger.Printf("inbound process uid=%d: %v", msg.UID, perr)
			continue
		}
		if action.Kind == ActionRouted {
			h.bridge.inboundProcessed.Add(1)
		}
		// Mark seen so the IMAP server stops returning the same UID on
		// the next 24-hour-window fetch. Errors here are logged and
		// swallowed; dedup catches any re-arrival.
		if merr := imap.MarkSeen(ctx, msg.UID); merr != nil && ctx.Err() == nil {
			h.bridge.logger.Printf("mark seen uid=%d: %v", msg.UID, merr)
		}
	}

	return reporter.Transition(scheduler.StateCompleted, "poll complete", nil)
}

// DedupCleanupHandler is the substrate handler behind
// `internal.email_dedup_cleanup`. Daily it sweeps the dedup table,
// dropping rows older than DefaultDedupTTL (30 days).
type DedupCleanupHandler struct {
	bridge *Bridge
}

// NewDedupCleanupHandler binds a handler to the email bridge.
func NewDedupCleanupHandler(b *Bridge) *DedupCleanupHandler { return &DedupCleanupHandler{bridge: b} }

func (h *DedupCleanupHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"sweeping": 2 * time.Minute}
}

func (h *DedupCleanupHandler) Reconcile(_ context.Context, _ scheduler.JobSpec, _ string, _ scheduler.State) (scheduler.State, error) {
	return scheduler.StateCompleted, nil
}

func (h *DedupCleanupHandler) Dispatch(ctx context.Context, _ scheduler.JobSpec, _ string, reporter scheduler.StateReporter, _ <-chan *scheduler.Completion) error {
	if err := reporter.Transition(scheduler.StateRunning, "sweeping dedup", nil); err != nil {
		return err
	}
	if err := reporter.Stage("sweeping"); err != nil {
		return err
	}

	dedup := h.bridge.Dedup()
	if dedup == nil {
		return reporter.Transition(scheduler.StateCompleted, "bridge not running; skipped", nil)
	}

	cutoff := time.Now().Add(-DefaultDedupTTL)
	n, err := dedup.Sweep(ctx, cutoff)
	if err != nil {
		if ctx.Err() != nil {
			return reporter.Transition(scheduler.StateCompleted, "cancelled", nil)
		}
		return reporter.Transition(scheduler.StateFailed, "sweep: "+err.Error(), nil)
	}
	if n > 0 {
		h.bridge.logger.Printf("dedup sweep: deleted %d stale rows", n)
	}
	return reporter.Transition(scheduler.StateCompleted, "sweep complete", nil)
}

// QueueDrainHandler is the substrate handler behind
// `internal.email_queue_drain`. Each tick claims due rows from the
// outbound queue and submits them via SMTP.
type QueueDrainHandler struct {
	bridge *Bridge
}

// NewQueueDrainHandler binds a handler to the email bridge.
func NewQueueDrainHandler(b *Bridge) *QueueDrainHandler { return &QueueDrainHandler{bridge: b} }

func (h *QueueDrainHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{"draining": 5 * time.Minute}
}

func (h *QueueDrainHandler) Reconcile(_ context.Context, _ scheduler.JobSpec, _ string, _ scheduler.State) (scheduler.State, error) {
	return scheduler.StateCompleted, nil
}

func (h *QueueDrainHandler) Dispatch(ctx context.Context, _ scheduler.JobSpec, _ string, reporter scheduler.StateReporter, _ <-chan *scheduler.Completion) error {
	if err := reporter.Transition(scheduler.StateRunning, "draining queue", nil); err != nil {
		return err
	}
	if err := reporter.Stage("draining"); err != nil {
		return err
	}

	worker := h.bridge.Worker()
	if worker == nil {
		return reporter.Transition(scheduler.StateCompleted, "bridge not running; skipped", nil)
	}

	if _, _, _, err := worker.Drain(ctx); err != nil {
		if ctx.Err() != nil {
			return reporter.Transition(scheduler.StateCompleted, "cancelled", nil)
		}
		return reporter.Transition(scheduler.StateFailed, "drain: "+err.Error(), nil)
	}
	return reporter.Transition(scheduler.StateCompleted, "drain complete", nil)
}
