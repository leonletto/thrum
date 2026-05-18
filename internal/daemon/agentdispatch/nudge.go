package agentdispatch

import (
	"context"
	"errors"
	"time"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
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

// Dispatch is not yet implemented — Task 30 ships pre-enqueue
// liveness check + send. Returning an error here pins the test
// surface so Task 29's compile-time assertion passes without an
// accidental dispatch path slipping through.
func (h *NudgeHandler) Dispatch(_ context.Context, _ scheduler.JobSpec, _ string, _ scheduler.StateReporter, _ <-chan *scheduler.Completion) error {
	return errors.New("nudge.Dispatch: not yet implemented (E6.3 Task 30)")
}
