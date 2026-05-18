package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/daemon/agentdispatch"
	"github.com/leonletto/thrum/internal/daemon/agenthealth"
	"github.com/leonletto/thrum/internal/daemon/escalation"
	"github.com/leonletto/thrum/internal/daemon/rpc"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/skills/mirror"
	"github.com/leonletto/thrum/internal/tmux"
)

// paneHealthCheckJobID is the canonical scheduler internal-job ID
// for the periodic pane-health monitor. Operators see it in
// `thrum cron list` + `thrum cron history` output; drift in the
// literal here would break those audit-trail queries.
const paneHealthCheckJobID = "internal.pane_health_check"

// paneHealthCheckSchedule is the canonical cadence per the
// thrum-fvhs dispatch ("Cadence: every 30s (configurable later)").
// Coarse enough not to flood tmux with display-message probes;
// tight enough that a crashed agent is detected within the same
// minute. Operator override via config lands as a separate
// follow-up bd if needed.
const paneHealthCheckSchedule = "@every 30s"

// listFilesProbeMethod is the RPC method daemon-boot probes to decide
// whether agentdispatch's stage-8 drain should run or short-circuit.
// MB-1.S2 (file-streaming epic) ships the handler; pre-MB-1.S2 daemons
// flip the tracker into skip-drain mode so teardown never polls for
// RPCs that can't exist.
const listFilesProbeMethod = "agent.listFiles"

// wireAgentDispatch performs the daemon-boot feature-detect step
// described in B-B1 plan Task 63 (and pinned by spec §9.7.4).
//
// Specifically:
//  1. Constructs the in-flight tracker that future agent.listFiles /
//     agent.getFile RPC handlers Begin/End through.
//  2. Probes the JSON-RPC server for the agent.listFiles handler.
//     If it isn't registered (the common v0.11 case — MB-1.S2 hasn't
//     shipped), flips the tracker into skip-drain mode so stage-8
//     drain returns immediately rather than polling a tracker that
//     would never see Begin calls.
//  3. Builds the Drainer that satisfies agentdispatch.RPCDrainer and
//     gets injected into ScheduledAgentHandler.Deps when the wider
//     B-B1 dispatch wiring lands.
//
// Returns (drainer, tracker) so the downstream wiring task can
// inject Drainer into Deps and the agent-side RPC adapter (lands
// with MB-1.S2) into the tracker's Begin/End surface.
//
// The tracker's concrete type is package-private to agentdispatch
// so the boot-time SetSkipDrain mutation is gated through this
// helper; callers receive only the InflightTracker interface
// (Begin/End/Count) so they can't flip skip-drain mid-flight.
//
// PANICS only if server is nil — that's a wiring bug, not a runtime
// failure mode.
func wireAgentDispatch(server *daemon.Server) (*agentdispatch.Drainer, agentdispatch.InflightTracker) {
	if server == nil {
		panic("wireAgentDispatch: nil server (wiring bug)")
	}

	tracker := agentdispatch.NewInflightTracker()
	if !server.HasHandler(listFilesProbeMethod) {
		tracker.SetSkipDrain(true)
		// Debug-level: this is the expected v0.11 steady state, not
		// an operational event. Operators investigating fast stage-8
		// teardowns can flip the log level to see the probe outcome.
		slog.Debug("agent.listFiles RPC not registered; stage-8 drain short-circuit active",
			"component", "agentdispatch",
			"probe_method", listFilesProbeMethod,
		)
	}
	drainer := agentdispatch.NewDrainer(tracker)
	return drainer, tracker
}

// userJobTypes lists the user-facing scheduler job types B-B1 E6.5
// owns. Kept here (not in agentdispatch) because cmd/thrum is the
// composition root that decides which types are "real for v0.11";
// agentdispatch can add new handler types over time without
// implicitly registering them.
var userJobTypes = []string{"scheduled_agent", "nudge"}

// Compile-time check that *mirror.Worker satisfies
// agentdispatch.MirrorWorker. The mirror package can't declare
// this directly (it doesn't import agentdispatch), and agentdispatch
// can't declare it (it doesn't import mirror — that would risk an
// import cycle through cmd/thrum). The composition root is the
// natural home for the check; catches signature drift at build time
// rather than at wireScheduledAgentHandlers' first dispatch.
var _ agentdispatch.MirrorWorker = (*mirror.Worker)(nil)

// registerPlaceholderHandlers registers a PlaceholderHandler for
// each user-facing job type. Originally shipped at E6.5 Task 42a
// as the production registration path; 42b superseded it with
// wireScheduledAgentHandlers (real handler instances + Deps
// adapters). The helper is retained as a fixture/test utility so
// scheduler-level tests can exercise the type-taxonomy registry
// without needing the full adapter chain.
//
// Idempotency note: scheduler.RegisterTypeHandler rejects
// duplicates, so calling registerPlaceholderHandlers a second
// time will fail on the first type.
func registerPlaceholderHandlers(sched *scheduler.Scheduler) error {
	if sched == nil {
		return fmt.Errorf("registerPlaceholderHandlers: nil scheduler")
	}
	for _, jobType := range userJobTypes {
		if err := sched.RegisterTypeHandler(jobType,
			agentdispatch.NewPlaceholderHandler(jobType)); err != nil {
			return fmt.Errorf("register %s type handler: %w", jobType, err)
		}
	}
	return nil
}

// scheduledAgentDeps groups the wiring inputs wireScheduledAgentHandlers
// needs from daemon-boot context. Each field maps to one Deps slot on
// ScheduledAgentHandler / NudgeHandler; keeping the grouping struct
// here (rather than inline params) makes the call site at main.go
// readable and centralizes the "what 42b consumes" inventory.
type scheduledAgentDeps struct {
	// RepoPath is the absolute path to the daemon-managed repository
	// passed through to worktree.Create as the parent path.
	RepoPath string

	// TmuxHandler is the daemon's existing rpc.TmuxHandler. The
	// adapter wraps it for the TmuxRPC interface.
	TmuxHandler *rpc.TmuxHandler

	// MessageHandler is the daemon's existing rpc.MessageHandler.
	// The adapter wraps it for the MessageRPC interface; CallerAgentID
	// is the supervisor identity used for daemon-source enqueues.
	MessageHandler *rpc.MessageHandler

	// CallerAgentID is the synthetic supervisor agent id (same value
	// reminders_wire.go uses for daemon-source sends).
	CallerAgentID string

	// AgentRegistry satisfies agent.AgentRegistry directly — passed
	// through to both ScheduledAgentHandler.Deps.Registry and
	// NudgeHandler.NudgeDeps.Registry without an adapter layer.
	AgentRegistry agent.AgentRegistry

	// MirrorWorker satisfies agentdispatch.MirrorWorker directly via
	// its EnsureMirrored method.
	MirrorWorker *mirror.Worker

	// EscalationDeps is the escalation package's Deps struct (Email,
	// Message, Config). The router adapter wraps it.
	EscalationDeps escalation.Deps

	// Drainer is the agentdispatch.Drainer constructed by
	// wireAgentDispatch. Plumbs through to
	// ScheduledAgentHandler.Deps.Drainer so stage-8 teardown's
	// real drain path is wired (replaces the parked _ = drainer
	// from 42a).
	Drainer agentdispatch.RPCDrainer

	// DaemonState owns the safedb handle threaded through both the
	// BootReconciler's JournalReader (a *scheduler.StateStore over
	// the same DB) and its AgentLifecycleStore. E6.9 B3: required
	// to construct the production BootReconciler that replaces the
	// NewReconcilerStub from E6.5 42b.
	DaemonState *state.State
}

// wireScheduledAgentHandlers performs E6.5 Task 42b + E6.9 Task 68:
// replace placeholder registrations with real ScheduledAgentHandler
// + NudgeHandler instances, each constructed with concrete Deps
// adapters. E6.9 Task 68 swaps the NewReconcilerStub Reconciler slot
// for a real *agentdispatch.BootReconciler so the per-handler
// reconcile walk inside RegisterTypeHandler routes rows through
// spec §7.7's classification.
//
// Returns the constructed BootReconciler so main.go can invoke
// SweepOrphans + BootPass against it after RegisterTypeHandler
// completes the per-handler reconcile walk (Option B sequencing
// per plan §3408-3429 — synchronous main.go ordering replaces
// the unimplemented Scheduler.ReconciliationComplete channel).
//
// Closes AC §9.6.4 (real dispatch) + AC §9.10.1-§9.10.3 (Reconcile
// row-class coverage now backed by real recovery logic) +
// §9.10.4-§9.10.6 (when callers invoke SweepOrphans post-wire).
//
// Ordering invariant: MUST be called AFTER sched.Start so the
// per-handler reconcile loop walks any non-terminal rows under
// these types and routes them through the real Reconcile path.
//
// PANICS only via the underlying scheduler error wrap when
// RegisterTypeHandler rejects a duplicate — would indicate
// registerPlaceholderHandlers was called first (a wiring bug since
// they're mutually exclusive production paths).
func wireScheduledAgentHandlers(sched *scheduler.Scheduler, deps scheduledAgentDeps) (*agentdispatch.BootReconciler, error) {
	if sched == nil {
		return nil, fmt.Errorf("wireScheduledAgentHandlers: nil scheduler")
	}
	if deps.TmuxHandler == nil {
		return nil, fmt.Errorf("wireScheduledAgentHandlers: nil TmuxHandler")
	}
	if deps.MessageHandler == nil {
		return nil, fmt.Errorf("wireScheduledAgentHandlers: nil MessageHandler")
	}
	if deps.CallerAgentID == "" {
		return nil, fmt.Errorf("wireScheduledAgentHandlers: empty CallerAgentID (need supervisor identity for daemon-source sends)")
	}
	if deps.MirrorWorker == nil {
		// Mirror is consumed by Stage 3b (EnsureMirrored). A nil
		// *mirror.Worker satisfies the MirrorWorker interface as a
		// non-nil interface holding a nil pointer — calls would
		// nil-deref deep in Stage 3b at first dispatch. Asymmetry
		// with TmuxHandler/MessageHandler nil guards becomes a trap
		// for fixtures + future callers; surface here as a boot-time
		// wiring error instead.
		return nil, fmt.Errorf("wireScheduledAgentHandlers: nil MirrorWorker (Stage 3b would nil-deref)")
	}
	if deps.DaemonState == nil {
		return nil, fmt.Errorf("wireScheduledAgentHandlers: nil DaemonState (BootReconciler needs DB handle)")
	}

	tmuxAdapter := agentdispatch.NewTmuxRPCAdapter(deps.TmuxHandler)
	messageAdapter := agentdispatch.NewMessageRPCAdapter(deps.MessageHandler, deps.CallerAgentID)
	worktreeAdapter := agentdispatch.NewWorktreeMgrAdapter()
	escalationAdapter := agentdispatch.NewEscalationRouterAdapter(deps.EscalationDeps)

	// E6.9 B3: real BootReconciler replaces NewReconcilerStub. The
	// JournalReader interface is satisfied directly by
	// *scheduler.StateStore (compile-time-checked in reconcile.go);
	// constructing a fresh StateStore over the same safedb handle
	// hits the same SQLite tables the scheduler itself writes to.
	journalStore := scheduler.NewStateStore(deps.DaemonState.DB())
	lifecycleStore := state.NewAgentLifecycleStore(deps.DaemonState.DB())
	bootReconciler := agentdispatch.NewBootReconciler(
		deps.RepoPath,
		tmuxAdapter,
		worktreeAdapter,
		journalStore,
		lifecycleStore,
	)

	scheduledHandler := agentdispatch.NewScheduledAgentHandler(agentdispatch.Deps{
		RepoPath:   deps.RepoPath,
		Tmux:       tmuxAdapter,
		Message:    messageAdapter,
		Worktree:   worktreeAdapter,
		Registry:   deps.AgentRegistry,
		Mirror:     deps.MirrorWorker,
		Escalation: escalationAdapter,
		Reconciler: bootReconciler,
		Drainer:    deps.Drainer,
	})
	nudgeHandler := agentdispatch.NewNudgeHandler(agentdispatch.NudgeDeps{
		Tmux:     tmuxAdapter,
		Message:  messageAdapter,
		Registry: deps.AgentRegistry,
	})

	if err := sched.RegisterTypeHandler("scheduled_agent", scheduledHandler); err != nil {
		return nil, fmt.Errorf("register scheduled_agent type handler: %w", err)
	}
	if err := sched.RegisterTypeHandler("nudge", nudgeHandler); err != nil {
		return nil, fmt.Errorf("register nudge type handler: %w", err)
	}
	return bootReconciler, nil
}

// tmuxPaneProber satisfies agenthealth.PaneProber by wrapping the
// canonical internal/tmux.HasSession primitive — the same probe
// internal/daemon/sweep/panes.go and internal/daemon/nudge/nudge.go
// use to decide "is this agent's tmux session still up?" Returns
// (alive, nil) on a successful probe; tmux errors surface as
// (false, err) so the loop's per-agent error path activates
// rather than silently treating an unreachable tmux as "pane gone".
type tmuxPaneProber struct{}

func (tmuxPaneProber) CheckPane(_ context.Context, target string) (bool, error) {
	// tmux.HasSession synchronously shells out via safecmd; no
	// context propagation needed (the probe is sub-millisecond
	// against a live tmux server). When tmux itself is down the
	// call returns false, which is the right answer for the
	// pane-health check anyway.
	return tmux.HasSession(target), nil
}

// buildPaneHealthRespawner constructs an *agentdispatch.Respawner
// with the canonical production deps for E6.7 pane-health respawn:
//   - Registry: the supplied agent.AgentRegistry (caller-built
//     via agent.NewSQLiteRegistry(state.DB)).
//   - LifecycleStore: state.NewAgentLifecycleStore over the same DB.
//   - Restarter: the real RestarterAdapter (thrum-6qmf.4.88)
//     wrapping rpc.TmuxHandler.RestartSession. Closes spec §9.8.4
//     PARTIAL → FULL PASS: when OnPaneGone fires + gate predicate
//     passes, the system now actually re-creates the tmux session
//   - relaunches the runtime instead of returning
//     ErrHandlerWiringPending.
//   - Escalation: nil for now; the loop-guard trip path's F2
//     nil-guard handles this. When the daemon-side escalation
//     router is wired, pass through here.
//
// Lives next to wirePaneHealthCheck so main.go's composition
// root threads the same DB + TmuxHandler to all consumers —
// single source of truth.
func buildPaneHealthRespawner(registry agent.AgentRegistry, db *state.State, tmuxHandler *rpc.TmuxHandler) *agentdispatch.Respawner {
	return &agentdispatch.Respawner{
		Registry:       registry,
		LifecycleStore: state.NewAgentLifecycleStore(db.DB()),
		Restarter:      agentdispatch.NewRestarterAdapter(tmuxHandler),
		// Escalation: nil — wire when daemon-side escalation router lands.
	}
}

// wirePaneHealthCheck registers the periodic agenthealth.CheckHandler
// per thrum-fvhs / E6.7 AC 9.8.4. The handler iterates every
// auto-respawn-eligible agent each tick, probes its tmux pane via
// tmux.HasSession, and routes pane-gone events to
// Respawner.OnPaneGone (which appends crash_detected, runs the
// gate predicate + loop guard, and fires respawn or escalation).
//
// scheduler.RegisterInternal panics on duplicate id; this function
// must be called exactly once per daemon boot. The id literal
// matches paneHealthCheckJobID — operators see it in `thrum cron`
// output.
//
// Deps are interface-injected so the production wiring at main.go's
// composition root threads the daemon state + registry; tests
// substitute fakes via the agenthealth.New constructor.
func wirePaneHealthCheck(
	sched *scheduler.Scheduler,
	registry agent.AgentRegistry,
	daemonState *state.State,
	tmuxHandler *rpc.TmuxHandler,
) error {
	if sched == nil {
		return fmt.Errorf("wirePaneHealthCheck: nil scheduler")
	}
	if registry == nil {
		return fmt.Errorf("wirePaneHealthCheck: nil registry")
	}
	if daemonState == nil {
		return fmt.Errorf("wirePaneHealthCheck: nil state")
	}
	if tmuxHandler == nil {
		return fmt.Errorf("wirePaneHealthCheck: nil tmux handler")
	}
	respawner := buildPaneHealthRespawner(registry, daemonState, tmuxHandler)
	handler := agenthealth.New(
		registry,
		tmuxPaneProber{},
		agenthealth.WrapAgentdispatchRespawner(respawner),
		nil, // use slog.Default()
	)
	// RunAtStart=false: the first tick fires after the cadence
	// window elapses. Boot-time non-terminal agents are handled
	// separately by Reconciler (E6.9). RunAtStart=true would
	// race the registry projection (agents may not yet be loaded).
	sched.RegisterInternal(paneHealthCheckJobID, paneHealthCheckSchedule,
		scheduler.InternalOpts{CatchUp: "skip"}, handler)
	return nil
}
