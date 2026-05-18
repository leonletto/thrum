package agentdispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/leonletto/thrum/internal/daemon/escalation"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	ttmux "github.com/leonletto/thrum/internal/tmux"
	"github.com/leonletto/thrum/internal/worktree"
)

// tmuxRPCAdapter bridges agentdispatch's narrow TmuxRPC interface to
// the daemon's tmux machinery. Simple operations (CheckPane,
// TmuxKillSession, PaneSendCtrlCExit) call ttmux primitives directly;
// operations requiring handler business logic (TmuxCreate, TmuxLaunch,
// WaitForPaneReady) round-trip through rpc.TmuxHandler's JSON-RPC
// entry points so the identity-banner, quickstart, and runtime-detect
// logic stays the single source of truth. PaneInjectPrompt routes
// through TmuxHandler.PaneInjectPrompt (Step 0 export) so the
// canonical 200ms text→Enter gap lives in one place.
type tmuxRPCAdapter struct {
	handler *rpc.TmuxHandler
}

// NewTmuxRPCAdapter wraps the daemon's *rpc.TmuxHandler in the
// narrower agentdispatch.TmuxRPC interface. Returned value is
// stateless beyond the handler reference — safe to share across
// concurrent dispatches.
func NewTmuxRPCAdapter(h *rpc.TmuxHandler) TmuxRPC {
	return &tmuxRPCAdapter{handler: h}
}

// CheckPane consults ttmux.HasSession directly. The richer
// rpc.HandleCheckPane includes permission-prompt detection + queue
// dispatch that agentdispatch doesn't need — Stage 0's only
// question is "does a live session named target exist?" — and the
// extra business logic would just slow the boot path.
func (a *tmuxRPCAdapter) CheckPane(_ context.Context, target string) (bool, error) {
	return ttmux.HasSession(target), nil
}

// TmuxCreate routes through HandleCreate so the identity-banner
// + quickstart-bootstrap logic fires for newly-scheduled agents.
// agentdispatch supplies only Cwd + SessionName; the richer fields
// (AgentName/Role/Module/Intent/Runtime/Force/NoAgent) default to
// empty — HandleCreate's "no agent" / "minimal create" path
// handles that case (the runtime + identity get attached at
// TmuxLaunch via the runtime adapter).
func (a *tmuxRPCAdapter) TmuxCreate(ctx context.Context, target string, opts TmuxCreateOpts) error {
	req := rpc.TmuxCreateRequest{
		Name:    target,
		Cwd:     opts.Cwd,
		NoAgent: true,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("tmuxRPCAdapter.TmuxCreate: marshal: %w", err)
	}
	if _, err := a.handler.HandleCreate(ctx, payload); err != nil {
		return fmt.Errorf("tmuxRPCAdapter.TmuxCreate(%q): %w", target, err)
	}
	return nil
}

// TmuxLaunch routes through HandleLaunch. The runtime is resolved
// from the agent identity file (which Stage 3's worktree path holds)
// inside HandleLaunch, so the adapter doesn't need to pass it.
func (a *tmuxRPCAdapter) TmuxLaunch(ctx context.Context, target string) error {
	req := rpc.TmuxLaunchRequest{Name: target}
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("tmuxRPCAdapter.TmuxLaunch: marshal: %w", err)
	}
	if _, err := a.handler.HandleLaunch(ctx, payload); err != nil {
		return fmt.Errorf("tmuxRPCAdapter.TmuxLaunch(%q): %w", target, err)
	}
	return nil
}

// WaitForPaneReady delegates to the exported TmuxHandler entry
// point. The internal waitForPaneReady helper takes runtime +
// timeout parameters; the exported wrapper resolves runtime from
// the agent identity file and uses canonical defaults so the
// adapter contract stays narrow.
//
// Per spec §7.1 Stage 6: timeout is 60s + the silence-loop fires
// fast enough that an unresponsive pane surfaces well within
// agentdispatch.Stages()'s StageWaitingForPaneReady budget.
func (a *tmuxRPCAdapter) WaitForPaneReady(ctx context.Context, target string) error {
	return a.handler.WaitForPaneReady(ctx, target)
}

// TmuxKillSession uses ttmux.KillSession directly. The richer
// HandleKill path adds inbox-clear + identity-clear side effects
// agentdispatch doesn't need at stage-8 teardown (the worktree
// destroy step handles those concerns differently).
func (a *tmuxRPCAdapter) TmuxKillSession(_ context.Context, target string) error {
	return ttmux.KillSession(target)
}

// PaneSendCtrlCExit emits the SIGTERM-equivalent sequence: Ctrl-C,
// then literal "exit", then Enter. ttmux.SendSpecialKey covers the
// special keystrokes; ttmux.SendKeys handles the literal text.
// Errors from any step are joined so the caller sees the full
// picture without short-circuiting on the first failure.
func (a *tmuxRPCAdapter) PaneSendCtrlCExit(_ context.Context, target string) error {
	var errs []error
	if err := ttmux.SendSpecialKey(target, "C-c"); err != nil {
		errs = append(errs, fmt.Errorf("send C-c: %w", err))
	}
	if err := ttmux.SendKeys(target, "exit"); err != nil {
		errs = append(errs, fmt.Errorf("send exit: %w", err))
	}
	if err := ttmux.SendSpecialKey(target, "Enter"); err != nil {
		errs = append(errs, fmt.Errorf("send Enter: %w", err))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// PaneInjectPrompt routes to TmuxHandler.PaneInjectPrompt (Step 0
// export) so the 200ms text→Enter gap stays the single source of
// truth in rpc/tmux.go.
func (a *tmuxRPCAdapter) PaneInjectPrompt(ctx context.Context, target, text string) error {
	return a.handler.PaneInjectPrompt(ctx, target, text)
}

// Compile-time satisfaction check.
var _ TmuxRPC = (*tmuxRPCAdapter)(nil)

// --- MessageRPC adapter ---

// MessageRPCAdapter wraps the daemon's *rpc.MessageHandler so
// agentdispatch can enqueue agent.wake messages through the
// canonical message-send pipeline (inbox + sync + events).
type MessageRPCAdapter struct {
	handler *rpc.MessageHandler
	// callerAgentID is the synthetic supervisor agent id used as
	// the caller for daemon-source enqueues. The MessageHandler
	// requires a session-bearing caller; the daemon itself has no
	// session, so wiring at cmd/thrum/main.go supplies the
	// supervisor identity (same pattern as messageHandlerSender in
	// reminders_wire.go).
	callerAgentID string
}

// NewMessageRPCAdapter wraps the rpc.MessageHandler with the
// caller agent id used for daemon-authored sends. Returns the
// concrete pointer (not the narrow agentdispatch.MessageRPC
// interface) so the same instance can satisfy
// escalation.MessageRPC at the cmd/thrum/ composition root —
// both interfaces have the identical MessageSend signature.
func NewMessageRPCAdapter(h *rpc.MessageHandler, callerAgentID string) *MessageRPCAdapter {
	return &MessageRPCAdapter{handler: h, callerAgentID: callerAgentID}
}

// MessageSend marshals the request through HandleSend so reminders
// + scheduled wake messages share one delivery path (subscriptions,
// event log, sync all fire normally).
//
// Composition contract (matches messageHandlerSender in
// reminders_wire.go): callers pre-compose any subject context they
// want visible in the recipient's inbox into `body`; the adapter
// passes body verbatim to SendRequest.Content. The `subject`
// parameter is part of the agentdispatch.MessageRPC interface for
// caller-symmetry (escalation.RouteEscalation pre-composes
// `subject + "\n\n" + body` then re-passes subject) but is NOT
// re-folded into Content here — folding twice was the root cause
// of a double-subject-in-Content bug surfaced in Phase 3 review.
// SendRequest has no Subject field on the wire, so the subject
// argument is effectively a label that callers can choose to
// embed in body if they want it visible inline.
func (a *MessageRPCAdapter) MessageSend(ctx context.Context, target, subject, body string) (string, error) {
	_ = subject // see composition contract above — caller pre-composes if needed
	if target == "" {
		return "", errors.New("MessageRPCAdapter.MessageSend: empty target")
	}
	if a.handler == nil {
		return "", errors.New("MessageRPCAdapter.MessageSend: nil handler (wiring bug)")
	}
	caller := a.callerAgentID
	if caller == "" {
		return "", errors.New("MessageRPCAdapter.MessageSend: empty callerAgentID (wiring bug)")
	}

	params := rpc.SendRequest{
		Content:       body,
		Format:        "markdown",
		To:            target,
		CallerAgentID: caller,
		Tags:          []string{"scheduled_agent.wake"},
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("MessageRPCAdapter.MessageSend: marshal: %w", err)
	}
	result, err := a.handler.HandleSend(ctx, raw)
	if err != nil {
		return "", fmt.Errorf("MessageRPCAdapter.MessageSend(%q): %w", target, err)
	}
	// HandleSend returns a SendResponse{MessageID string} — capture
	// the id for journal Atomicity at Stage 2 (the wake_message_id
	// fact recorded alongside the StateRunning transition).
	resp, ok := result.(rpc.SendResponse)
	if !ok {
		if respPtr, okPtr := result.(*rpc.SendResponse); okPtr {
			return respPtr.MessageID, nil
		}
		return "", fmt.Errorf("MessageRPCAdapter.MessageSend: unexpected response type %T", result)
	}
	return resp.MessageID, nil
}

// Compile-time satisfaction check.
var _ MessageRPC = (*MessageRPCAdapter)(nil)

// --- WorktreeManager adapter ---

// worktreeMgrAdapter wraps the package-level worktree functions
// into the agentdispatch.WorktreeManager interface. The package
// functions already take/return the canonical opts + result
// types, so the adapter is pure delegation — kept as an explicit
// struct rather than direct function references so the adapter
// surface stays uniform with the other Deps fields.
type worktreeMgrAdapter struct{}

// NewWorktreeMgrAdapter returns a zero-state adapter.
func NewWorktreeMgrAdapter() WorktreeManager {
	return &worktreeMgrAdapter{}
}

func (a *worktreeMgrAdapter) Create(ctx context.Context, opts worktree.CreateOpts) (*worktree.CreateResult, error) {
	return worktree.Create(ctx, opts)
}

func (a *worktreeMgrAdapter) Destroy(ctx context.Context, opts worktree.DestroyOpts) (*worktree.DestroyResult, error) {
	return worktree.Destroy(ctx, opts)
}

// Compile-time satisfaction check.
var _ WorktreeManager = (*worktreeMgrAdapter)(nil)

// --- EscalationRouter adapter ---

// escalationRouterAdapter wraps the package-level RouteEscalation
// function with the configured Deps. RouteEscalation handles the
// email-vs-supervisor channel decision internally; the adapter
// just supplies the deps + delegates.
type escalationRouterAdapter struct {
	deps escalation.Deps
}

// NewEscalationRouterAdapter wraps the escalation Deps.
func NewEscalationRouterAdapter(deps escalation.Deps) EscalationRouter {
	return &escalationRouterAdapter{deps: deps}
}

func (a *escalationRouterAdapter) Route(ctx context.Context, alert escalation.Alert, subject, body string) error {
	return escalation.RouteEscalation(ctx, alert, subject, body, a.deps)
}

// Compile-time satisfaction check.
var _ EscalationRouter = (*escalationRouterAdapter)(nil)

// --- Reconciler stub ---

// reconcilerStub satisfies the Reconciler interface with a
// conservative "lost track" return so non-terminal rows found at
// daemon boot are marked failed rather than dangling. The real
// reconciliation logic (worktree-intact resume, terminal-failed
// classification, lost-track) lands at E6.9 (thrum-6qmf.4.65);
// until then the stub keeps the substrate-level wiring honest.
type reconcilerStub struct{}

// NewReconcilerStub returns the E6.9-pending placeholder.
func NewReconcilerStub() Reconciler {
	return &reconcilerStub{}
}

func (r *reconcilerStub) ReconcileRun(_ context.Context, _ scheduler.JobSpec, runID string, _ scheduler.State) (scheduler.State, error) {
	return scheduler.StateFailed, fmt.Errorf("reconcile run %q: E6.9 reconciliation not yet wired", runID)
}

// Compile-time satisfaction check.
var _ Reconciler = (*reconcilerStub)(nil)

// --- Restarter adapter (thrum-6qmf.4.88) ---

// restarterAdapter wraps the daemon's *rpc.TmuxHandler so B-B1
// agentdispatch.Respawner can call RestartSession via the Restarter
// interface. Closes spec §9.8.4 PARTIAL → FULL PASS by replacing
// thrum-fvhs's placeholderRestarter (which returned wrapped
// ErrHandlerWiringPending) with the real restart path.
//
// agentName is mapped 1:1 to the tmux session name — the canonical
// thrum convention (`thrum tmux create <agentName>` creates session
// <agentName>). When a future agent uses a non-default session
// naming scheme, the resolution moves here (registry.Lookup →
// session name), keeping rpc.TmuxHandler.RestartSession's interface
// stable.
//
// Forward-flag F1 sentinel-handling preserved: real Tmux restart
// errors (kill failure, create failure, send-keys failure) are
// distinct from agentdispatch.ErrHandlerWiringPending — Respawner's
// errors.Is(err, ErrHandlerWiringPending) check no longer matches
// the production path, so OnPaneGone treats real errors as actual
// restart failures (audit trail captures, loop guard accumulates).
type restarterAdapter struct {
	handler *rpc.TmuxHandler
}

// NewRestarterAdapter wraps an *rpc.TmuxHandler as a Restarter.
// Production callers thread the daemon's wired TmuxHandler instance
// through; tests inject fakes via Respawner.Restarter directly
// without going through this adapter.
func NewRestarterAdapter(h *rpc.TmuxHandler) Restarter {
	return &restarterAdapter{handler: h}
}

func (a *restarterAdapter) Restart(ctx context.Context, agentName string) error {
	if a.handler == nil {
		return fmt.Errorf("restart %q: nil TmuxHandler (wiring bug)", agentName)
	}
	// Zero-valued RestartSessionOpts: Force=false → graceful flow
	// (send /thrum:restart message, poll for snapshot); Runtime="" →
	// inherit from identity file. These defaults match the
	// canonical thrum tmux restart behavior — auto-respawn fires
	// the same restart flow the operator would invoke manually.
	_, err := a.handler.RestartSession(ctx, agentName, rpc.RestartSessionOpts{})
	return err
}

// Compile-time satisfaction check.
var _ Restarter = (*restarterAdapter)(nil)
