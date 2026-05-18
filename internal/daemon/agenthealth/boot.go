package agenthealth

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// BootPass runs a single pane-health scan against every auto-respawn-
// eligible persistent agent at daemon boot, routing pane-gone events
// through the Respawner with detection=restart_reconciliation. Per
// B-B1 plan §3368-3376 + E6.9 Task 67. Caller invokes it exactly
// once during the boot sequence between scheduler row-reconcile
// (via RegisterTypeHandler) and bridge starts.
//
// Differs from the periodic CheckHandler.Dispatch in two ways:
//   - One-shot: no scheduler.JobSpec / StateReporter overhead.
//   - Detection method is state.DetectionRestartReconciliation —
//     pane-gone events recorded here indicate a daemon-crash-
//     induced miss, not a routine liveness probe. Operators
//     reading agent_lifecycle_events can tell the two classes
//     of crash apart from the detection_method column.
//
// Mode filter: skips agents whose Mode != persistent. Pre-v0.11
// validation rejects (auto_respawn=true + mode=ephemeral) per
// canonical §3.3 + AC 9.8.3, so this filter is defensive — a
// stray row carrying the rejected combo won't fire an unintended
// respawn.
//
// Errors: the only hard-error path is the initial registry list.
// Per-agent probe / respawn failures log via slog.Warn and the
// loop continues — one transient tmux blip must not block the
// remainder of the batch.
func BootPass(
	ctx context.Context,
	registry agent.AgentRegistry,
	prober PaneProber,
	respawner Respawner,
	logger *slog.Logger,
) error {
	if logger == nil {
		logger = slog.Default()
	}
	agents, err := registry.ListAutoRespawnEnabled(ctx)
	if err != nil {
		return fmt.Errorf("boot pane-health: list agents: %w", err)
	}

	probedSuccessfully := 0
	probeErrors := 0
	respawnsTriggered := 0
	skippedNonPersistent := 0
	for _, a := range agents {
		if ctx.Err() != nil {
			logger.Debug("boot pane-health: ctx cancelled mid-scan",
				"probed", probedSuccessfully,
				"total", len(agents))
			break
		}
		if a.Mode != agent.ModePersistent {
			// Defensive — ListAutoRespawnEnabled doesn't filter by
			// mode; canonical §3.3 prohibits (auto_respawn=true +
			// ephemeral) so a row in this state is malformed.
			skippedNonPersistent++
			logger.Warn("boot pane-health: skipping non-persistent auto-respawn agent (canonical §3.3 violation)",
				"agent", a.AgentID,
				"mode", a.Mode)
			continue
		}

		alive, probeErr := prober.CheckPane(ctx, a.AgentID)
		if probeErr != nil {
			probeErrors++
			logger.Warn("boot pane-health: probe failed; skipping for this pass",
				"agent", a.AgentID,
				"err", probeErr)
			continue
		}
		probedSuccessfully++
		if alive {
			continue
		}

		// Pane gone at boot — fire canonical OnPaneGone with the
		// restart_reconciliation detection method.
		if err := respawner.OnPaneGone(ctx, a.AgentID, state.DetectionRestartReconciliation); err != nil {
			logger.Warn("boot pane-health: respawn evaluation failed; continuing",
				"agent", a.AgentID,
				"err", err)
			continue
		}
		respawnsTriggered++
	}

	logger.Info("boot pane-health pass complete",
		"total_eligible", len(agents),
		"probed_successfully", probedSuccessfully,
		"probe_errors", probeErrors,
		"respawns_triggered", respawnsTriggered,
		"skipped_non_persistent", skippedNonPersistent)
	return nil
}
