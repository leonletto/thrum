// Package agenthealth provides the periodic pane-health monitor
// that wires B-B1 E6.7's Respawner to the production scheduler.
//
// Per spec §6.4 + AC 9.8.4 + the thrum-fvhs follow-up: a periodic
// scheduler job (registered via scheduler.RegisterInternal at
// daemon boot, default cadence 30s) iterates the
// auto_respawn-enabled agents and probes each for tmux pane
// liveness. When a pane is gone, the handler calls
// Respawner.OnPaneGone — the canonical 5-step evaluation that
// appends crash_detected, runs the gate predicate + loop guard,
// and fires respawn or escalation.
//
// Why a dedicated package (not a hook on A-B4 stalled-sweep):
// pre-mature coupling. A-B4 hasn't shipped + has different
// semantics (silence-based stalled detection vs. existence-based
// pane-health). Keeping the two separate avoids repeating the
// v2.3 under-scoping anti-pattern flagged in plan v2.2.
package agenthealth

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/agentdispatch"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// StageCheckName is the canonical operator-facing stage marker for
// the pane-health-check internal job. A-B4's stalled-sweep skip-set
// keys off scheduler_job_state.current_stage; drift in the literal
// here would silently break that integration.
const StageCheckName = "pane-health-check"

// stageBudget is the dwell budget per fire. 30s is generous for a
// list-and-probe pass (well under the operator-configurable cadence
// so a wedged tick doesn't overlap the next one).
const stageBudget = 30 * time.Second

// PaneProber is the minimal tmux-liveness surface CheckHandler
// reaches for on every fire. The production wiring at
// cmd/thrum/agentdispatch_wire.go forwards to TmuxRPC.CheckPane;
// tests inject a fake. Reusing the existing TmuxRPC.CheckPane
// interface contract (returns true when pane:0.0 is alive) lets
// us share the daemon-side adapter that already exists for B-B1.
type PaneProber interface {
	CheckPane(ctx context.Context, target string) (bool, error)
}

// Respawner is the minimal surface CheckHandler needs from
// agentdispatch.Respawner. Declared as an interface so unit tests
// can drive the loop with a fake (no need to construct the full
// Respawner dep chain — Registry + LifecycleStore + Restarter +
// Escalation) just to verify the iteration logic.
type Respawner interface {
	OnPaneGone(ctx context.Context, agentName string, detection state.DetectionMethod) error
}

// CheckHandler implements scheduler.Handler for the
// internal.pane_health_check periodic job. Lists eligible agents
// once per fire, probes each pane, and routes pane-gone events
// to Respawner.OnPaneGone. Errors on either path are logged and
// the loop continues — one transient probe failure must not
// block the rest of the batch.
type CheckHandler struct {
	Registry  agent.AgentRegistry
	Prober    PaneProber
	Respawner Respawner
	Logger    *slog.Logger
}

// New constructs a CheckHandler with sensible defaults. Logger
// defaults to slog.Default() when nil so callers don't have to
// thread a logger through the daemon-boot wire if they don't need
// custom routing.
func New(registry agent.AgentRegistry, prober PaneProber, respawner Respawner, logger *slog.Logger) *CheckHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &CheckHandler{
		Registry:  registry,
		Prober:    prober,
		Respawner: respawner,
		Logger:    logger,
	}
}

// Dispatch is the per-tick entry point invoked by the scheduler.
// Lists currently-eligible agents, probes each pane, and routes
// pane-gone events to Respawner. Returns nil even on per-agent
// probe failures (logged + skipped) so the loop survives transient
// tmux issues without ending the dispatch.
//
// The only error path is registry-list failure (DB unreachable) —
// without the list, we can't iterate, so we surface the error.
func (h *CheckHandler) Dispatch(ctx context.Context, _ scheduler.JobSpec, _ string, reporter scheduler.StateReporter, _ <-chan *scheduler.Completion) error {
	if err := reporter.Stage(StageCheckName); err != nil {
		return err
	}

	agents, err := h.Registry.ListAutoRespawnEnabled(ctx)
	if err != nil {
		_ = reporter.Transition(scheduler.StateFailed,
			fmt.Sprintf("pane-health: list agents: %v", err), nil)
		return fmt.Errorf("pane-health list agents: %w", err)
	}

	scanned := 0
	respawnsTriggered := 0
	for _, a := range agents {
		// ctx-cancel mid-loop: complete the tick gracefully rather
		// than mark the whole job failed. The next tick re-scans
		// from scratch (idempotent).
		if ctx.Err() != nil {
			h.Logger.Debug("pane-health: ctx cancelled mid-scan",
				"scanned", scanned, "total", len(agents))
			break
		}

		// Probe the pane. CheckPane returns (alive, err); we treat
		// transient probe errors as "skip this agent for the tick"
		// rather than "fire respawn" — a probe error doesn't prove
		// the agent is gone.
		alive, probeErr := h.Prober.CheckPane(ctx, a.AgentID)
		scanned++
		if probeErr != nil {
			h.Logger.Warn("pane-health: probe failed; skipping for this tick",
				"agent", a.AgentID, "err", probeErr)
			continue
		}
		if alive {
			continue
		}

		// Pane is gone. Fire the canonical respawn evaluation.
		// OnPaneGone errors are logged + skipped — one agent's
		// respawn failure must not block the rest of the batch.
		if err := h.Respawner.OnPaneGone(ctx, a.AgentID, state.DetectionHealthCheckTick); err != nil {
			h.Logger.Warn("pane-health: respawn evaluation failed; continuing",
				"agent", a.AgentID, "err", err)
			continue
		}
		respawnsTriggered++
	}

	_ = reporter.Transition(scheduler.StateCompleted,
		fmt.Sprintf("pane-health: scanned %d, triggered %d respawn evaluations",
			scanned, respawnsTriggered),
		map[string]any{
			"scanned":            scanned,
			"respawns_triggered": respawnsTriggered,
		})
	return nil
}

// Reconcile satisfies scheduler.Handler. The pane-health check
// holds no in-flight state — every tick lists fresh + probes
// fresh, so a daemon crash mid-tick is harmless: the next tick
// re-scans from scratch. Boot-time non-terminal rows mark
// completed since the data they were checking is already stale.
func (h *CheckHandler) Reconcile(_ context.Context, _ scheduler.JobSpec, _ string, _ scheduler.State) (scheduler.State, error) {
	return scheduler.StateCompleted, nil
}

// Stages declares the single-stage budget. A-B4's stalled-sweep
// skip-set queries this to know when a wedged tick has exhausted
// its dwell.
func (h *CheckHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{
		StageCheckName: stageBudget,
	}
}

// Compile-time guard: drift on the scheduler.Handler interface
// surfaces here as "does not implement" rather than a runtime
// assertion in cmd/thrum/agentdispatch_wire.go's RegisterInternal call.
var _ scheduler.Handler = (*CheckHandler)(nil)

// agentdispatchRespawnerAdapter wraps *agentdispatch.Respawner to
// satisfy the local Respawner interface — keeps agenthealth's
// dependency surface to interfaces only (no concrete-type leak
// into Dispatch). cmd/thrum/agentdispatch_wire.go uses this when
// constructing the production CheckHandler.
type agentdispatchRespawnerAdapter struct {
	inner *agentdispatch.Respawner
}

// WrapAgentdispatchRespawner returns a Respawner that forwards to
// the concrete *agentdispatch.Respawner. Daemon-boot wiring passes
// the constructed Respawner through this adapter so the agenthealth
// dep slot stays interface-only.
func WrapAgentdispatchRespawner(r *agentdispatch.Respawner) Respawner {
	return &agentdispatchRespawnerAdapter{inner: r}
}

func (a *agentdispatchRespawnerAdapter) OnPaneGone(ctx context.Context, agentName string, detection state.DetectionMethod) error {
	return a.inner.OnPaneGone(ctx, agentName, detection)
}
