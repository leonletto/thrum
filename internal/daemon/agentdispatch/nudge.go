package agentdispatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// TypeNudge is the canonical scheduler job-type identifier for the
// nudge handler per spec §7.2. E6.5 wires the handler with
// scheduler.RegisterTypeHandler(TypeNudge, nudgeHandler) at daemon
// boot. Exporting the constant here means E6.5's daemon-startup
// code can reference it by symbol rather than by string literal —
// drift between the registration call and the validator's switch-
// case (internal/daemon/scheduler/validator.go) surfaces as a
// compile error rather than a silent runtime mismatch.
const TypeNudge = "nudge"

// Sentinel errors for the nudge dispatch path. Callers errors.Is
// against these to distinguish the two canonical operator-facing
// failure classes ("the agent isn't running" vs "the agent isn't
// even registered") from generic infrastructure errors.
var (
	// ErrTargetOffline fires when the pre-enqueue liveness check
	// (TmuxRPC.CheckPane) reports the target's pane is not alive.
	// Refusing the wake protects the inbox from filling with stale
	// prods aimed at a dead agent.
	ErrTargetOffline = errors.New("nudge target offline at fire time")

	// ErrTargetNotRegistered fires when the registry has no row for
	// the nudge target. Distinct from ErrTargetOffline so operators
	// can tell from `thrum cron history` whether the agent is
	// missing entirely or just down.
	ErrTargetNotRegistered = errors.New("nudge target not in agent registry")
)

// NudgeHandler implements scheduler.Handler for the "nudge" job type
// per spec §7.2. Distinct from ScheduledAgentHandler (E6.1) — a
// nudge is a single-stage message-send to a target agent, not a
// nine-stage agent-wake protocol. Both handlers share the daemon's
// MessageRPC + TmuxRPC + AgentRegistry deps (declared in
// scheduled_agent.go) so the wiring at cmd/thrum/main.go is uniform.
//
// All per-run state (jobspec, runID, reporter) flows through
// Dispatch parameters — the handler struct holds only constant
// references to its deps. Per the IMPORTANT #7 dual-review
// invariant from E6.1, NudgeHandler is shared across concurrent
// dispatches and must have no mutable per-run fields.
type NudgeHandler struct {
	deps NudgeDeps
}

// NudgeDeps carries the dependency-injection points NudgeHandler
// needs from cmd/thrum/main.go's wiring layer. Interfaces are
// reused from scheduled_agent.go (MessageRPC, TmuxRPC) so the
// daemon-side adapter wires both handlers from the same RPC surface.
type NudgeDeps struct {
	// Tmux's CheckPane is the pre-enqueue liveness gate per spec
	// §7.2 — a nudge sent to an offline agent is wasted I/O, so
	// the dispatcher refuses with ErrTargetOffline before reaching
	// the message bus.
	Tmux TmuxRPC

	// Message is the message-send surface. Same interface
	// ScheduledAgentHandler stage 2 uses, so the daemon-boot
	// adapter ties one rpc.MessageHandler to both.
	Message MessageRPC

	// Registry is the agents-table read surface. NudgeHandler uses
	// Lookup to confirm the target is registered before message
	// enqueue; an unregistered target is ErrTargetNotRegistered
	// (canonical operator-facing failure class).
	Registry agent.AgentRegistry
}

// NewNudgeHandler returns a handler ready to register with the
// A-B1 scheduler via Scheduler.RegisterTypeHandler("nudge", h).
// The caller owns the Deps lifecycle — the handler stores them
// as-is and never mutates the struct after construction.
func NewNudgeHandler(deps NudgeDeps) *NudgeHandler {
	return &NudgeHandler{deps: deps}
}

// Stages declares the canonical single-stage budget per spec §7.2.
// A-B4's stalled-sweep consults this map to know when a wedged
// nudge run has exhausted its dwell. The "delivering" name is the
// canonical operator-facing stage marker emitted on Dispatch entry.
func (h *NudgeHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{
		"delivering": 10 * time.Second,
	}
}

// Reconcile implements scheduler.Handler.Reconcile for boot-time
// recovery. Nudges hold no in-flight state — they're either
// delivered or not — so a non-terminal nudge found at boot is a
// crash mid-Dispatch and gets marked failed. Distinct from
// ScheduledAgentHandler.Reconcile which delegates to E6.9; nudges
// have no equivalent recovery path because there's nothing to
// resume.
func (h *NudgeHandler) Reconcile(_ context.Context, _ scheduler.JobSpec, _ string, _ scheduler.State) (scheduler.State, error) {
	return scheduler.StateFailed, errors.New("nudge reconciliation: lost across daemon restart")
}

// Dispatch implements scheduler.Handler.Dispatch for nudge jobs per
// spec §7.2. Four-step sequence: stage marker → liveness check →
// registry presence → message enqueue → completion. Two gates fire
// before message enqueue (CheckPane + Registry.Lookup) so a nudge
// against an offline-or-missing target never reaches the message
// bus — wasted I/O at the inbox is worse than the cost of the two
// guard calls.
//
// State machine per AC 9.4.4: nudges skip StateRunning. The dispatch
// IS the work; there's no long-running stage. Successful flow:
// dispatched (substrate-emitted) → delivering (stage marker) →
// completed.
func (h *NudgeHandler) Dispatch(ctx context.Context, job scheduler.JobSpec, _ string, reporter scheduler.StateReporter, _ <-chan *scheduler.Completion) error {
	target := ""
	message := ""
	if job.Nudge != nil {
		target = job.Nudge.Target
		message = job.Nudge.Message
	}

	if err := reporter.Stage("delivering"); err != nil {
		return err
	}

	// Pre-enqueue liveness check. CheckPane error and pane-not-alive
	// are distinct failure classes (operator-facing diagnostics
	// matter): error = "could not determine"; not-alive = "definitely
	// offline".
	paneAlive, err := h.deps.Tmux.CheckPane(ctx, target)
	if err != nil {
		_ = reporter.Transition(scheduler.StateFailed,
			fmt.Sprintf("nudge target liveness check failed: %v", err),
			map[string]any{"target": target})
		return fmt.Errorf("nudge liveness check: %w", err)
	}
	if !paneAlive {
		_ = reporter.Transition(scheduler.StateFailed,
			"nudge target offline at fire time",
			map[string]any{"target": target})
		return ErrTargetOffline
	}

	// Registry presence check per BLOCKING #6 dual-review fix:
	// AgentRegistry.Lookup returns (Agent, error) with
	// ErrAgentNotFound for the missing case. errors.Is (NOT a
	// boolean-ok pattern) is mandated so wrapped sentinels still
	// match — defensive against future registry implementations
	// that wrap the canonical sentinel with context.
	if _, err := h.deps.Registry.Lookup(ctx, target); err != nil {
		if errors.Is(err, agent.ErrAgentNotFound) {
			_ = reporter.Transition(scheduler.StateFailed,
				"nudge target not in agent registry",
				map[string]any{"target": target})
			return ErrTargetNotRegistered
		}
		_ = reporter.Transition(scheduler.StateFailed,
			fmt.Sprintf("nudge target registry lookup failed: %v", err),
			map[string]any{"target": target})
		return fmt.Errorf("nudge registry lookup: %w", err)
	}

	// Enqueue. The subject prefix "Nudge: <job_id>" disambiguates
	// nudge messages from agent.wake messages in the operator
	// inbox view; `thrum inbox` filters key off the prefix.
	if _, err := h.deps.Message.MessageSend(ctx, target, "Nudge: "+job.ID, message); err != nil {
		_ = reporter.Transition(scheduler.StateFailed,
			fmt.Sprintf("nudge enqueue failed: %v", err), nil)
		return fmt.Errorf("nudge enqueue: %w", err)
	}

	if err := reporter.Transition(scheduler.StateCompleted, "nudge delivered", nil); err != nil {
		return err
	}
	return nil
}
