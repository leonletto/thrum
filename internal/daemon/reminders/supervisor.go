package reminders

import (
	"context"

	"github.com/leonletto/thrum/internal/daemon/permission"
)

// AgentRuntimeResolver looks up the runtime (claude/codex/cursor/etc.)
// and tmux target (session:window.pane) for an agent. Satisfied at
// daemon wiring time (thrum-6qmf.3.15) by an adapter over state.State
// + the per-agent identity files.
//
// Returns ok=false when the agent has no tmux session (e.g. remote-only
// peer, headless service) — the caller treats that as "supervisor
// can't help; fall through to normal delivery".
type AgentRuntimeResolver interface {
	AgentRuntime(ctx context.Context, agentName string) (runtime string, tmuxTarget string, ok bool)
}

// CapturePaneFn wraps the tmux capture-pane invocation. Function type
// (not interface) because production code uses internal/tmux.CapturePane
// directly — wrapping it in a method-bearing struct would be ceremony
// for one function. Tests substitute closures.
type CapturePaneFn func(target string, lines int) (string, error)

// SpoolWriter queues a message for supervisor-mediated delivery. The
// supervisor pane reads the spool and either types the message into the
// target pane once it's safe, or surfaces it to the operator if the
// permission prompt doesn't resolve. Real implementation lands in B-B1
// supervisor work; A-B4 only needs the interface here.
type SpoolWriter interface {
	EnqueueSupervisorMessage(ctx context.Context, targetAgent, body string) error
}

// SupervisorRouter decides whether to route a reminder through
// supervisor-delivery (target is at a permission prompt) or let the
// caller proceed with normal inbox delivery. Implements the
// SupervisorMaybeRouter interface declared by DeliverySink in
// thrum-6qmf.3.8.
//
// Detection per plan §Task 26 + dual-review IMPORTANT #10: the
// permission package does NOT expose a per-agent AtPermissionPrompt()
// API. Detection works on raw pane content via tmux.CapturePane +
// permission.IsPaneSafeToType. The router orchestrates those calls.
type SupervisorRouter struct {
	agents  AgentRuntimeResolver
	capture CapturePaneFn
	spool   SpoolWriter
}

// NewSupervisorRouter wires the three collaborators. None are nil-safe
// at construction; if a real-world wiring has nil for any of these,
// the daemon should not construct a SupervisorRouter — pass nil for
// the SupervisorMaybeRouter slot on DeliverySink instead so the whole
// supervisor pass is skipped.
func NewSupervisorRouter(agents AgentRuntimeResolver, capture CapturePaneFn, spool SpoolWriter) *SupervisorRouter {
	return &SupervisorRouter{agents: agents, capture: capture, spool: spool}
}

// MaybeRoute returns (routed, err) per the SupervisorMaybeRouter
// contract:
//   - routed=true: spool wrote the supervisor-mediated message; caller
//     skips normal inbox delivery
//   - routed=false, err=nil: caller falls through to normal delivery
//     (supervisor declined OR couldn't help)
//   - routed=false, err!=nil: unexpected internal failure; caller
//     also falls through but logs the error
//
// Five fall-through paths (all routed=false, nil):
//  1. Agent has no tmux session registered (remote peer, headless)
//  2. tmux capture-pane failed (pane gone, tmux unhappy)
//  3. Pane is safe to type (no permission prompt detected)
//  4. (covered by 3) IsPaneSafeToType returns true
//
// The conservative default is "fall through to normal delivery" —
// per the dual-review IMPORTANT #10 commentary, better to over-deliver
// than drop a reminder.
func (s *SupervisorRouter) MaybeRoute(ctx context.Context, r *Reminder) (bool, error) {
	if r == nil || r.TargetAgent == "" {
		return false, nil
	}

	runtime, target, ok := s.agents.AgentRuntime(ctx, r.TargetAgent)
	if !ok || target == "" {
		// Agent isn't in the registry or has no tmux session. The
		// supervisor can't intercept a pane that doesn't exist; let
		// the caller handle normal delivery.
		return false, nil
	}

	// 50 lines is enough to catch permission prompts (Claude trust
	// gates are 5-10 lines; agent commit-confirmation prompts are
	// 1-3 lines). Larger captures cost more tmux IPC without
	// changing detection accuracy.
	pane, err := s.capture(target, 50)
	if err != nil {
		// tmux is unhappy. Conservative default: don't claim
		// supervisor ownership; the caller's normal delivery path
		// might still succeed (e.g. inbox is a DB write, not a
		// tmux operation).
		return false, nil
	}

	if permission.IsPaneSafeToType(runtime, pane) {
		// No permission prompt detected; normal delivery is fine.
		return false, nil
	}

	// Pane is at a permission prompt or trust gate. Spool the
	// terse-agent body (matches the fire message format) so the
	// supervisor can deliver it once the prompt resolves.
	body := FormatAgentBody(r)
	if err := s.spool.EnqueueSupervisorMessage(ctx, r.TargetAgent, body); err != nil {
		// Spool failed; the supervisor can't help. Return the error
		// so the caller can log it, then fall through to normal
		// delivery (DeliverySink does this — supervisor errors
		// don't hide reminders from the target).
		return false, err
	}
	return true, nil
}

// Compile-time check that SupervisorRouter satisfies the
// SupervisorMaybeRouter interface declared in delivery.go.
var _ SupervisorMaybeRouter = (*SupervisorRouter)(nil)
