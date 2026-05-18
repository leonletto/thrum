package agentdispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/escalation"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// Default loop-guard knobs per canonical-ref §6.3: 3 respawn_fired
// events within 600 seconds (10 minutes) trips the guard.
const (
	defaultRespawnEscalateAfter = 3
	defaultRespawnWindowSeconds = 600
)

// Restarter is the tmux-restart surface Respawner needs to actually
// trigger a respawn. The daemon-side adapter at cmd/thrum/main.go
// forwards to internal/daemon/rpc.HandleRestart per IMPORTANT #10
// (audit verdict: existing HandleRestart is a complex JSON-RPC
// handler with snapshot/kill/relaunch orchestration; B-B1's
// Respawner injects this thin interface so the adapter composes
// the call without B-B1 needing to know about RPC params).
//
// Restart targets are agent names (not session names) — the
// adapter resolves the agent's session via the registry.
type Restarter interface {
	Restart(ctx context.Context, agentName string) error
}

// Respawner orchestrates the canonical auto-respawn flow per spec
// §6.4 + canonical-ref §3.4 loop-guard. Lives at the agentdispatch
// boundary so all deps (Registry, LifecycleStore, Restarter,
// Escalation) are interface-injected — tests swap fakes; production
// wires concrete implementations at daemon boot.
//
// Per IMPORTANT #7 (E6.1 dual-review): no per-event mutable state
// on the struct; OnPaneGone is the only entry point and runs
// purely against parameters + injected deps.
type Respawner struct {
	Registry       agent.AgentRegistry
	LifecycleStore state.AgentLifecycleStore
	Restarter      Restarter
	Escalation     EscalationRouter
}

// loopGuardBody composes the operator-facing escalation body sent
// through Escalation.Route when the loop guard trips. Includes the
// agent name (twice — once for the headline, once for the
// re-enable command) so an operator copying from email/inbox can
// run the right ack command without reformatting.
const loopGuardBody = `Auto-respawn for agent %q has been disabled by the loop guard.

Reason: %d respawn_fired events occurred within %d seconds — exceeded the canonical 3-in-10min threshold per substrate spec §6.4.

Investigate via:
  thrum agent show %s
  bd memories %s

Re-enable (after diagnosing the underlying crash cause):
  thrum agent ack %s --clear-auto-respawn-disabled`

// OnPaneGone runs the canonical pane-gone evaluation cycle per
// spec §6.4 + canonical §3.4 + brainstormer-third forward-flags.
// Steps:
//
//  1. Append crash_detected event (always — independent of gate
//     outcome) so observability captures every detection.
//  2. Read agent record. Lookup failure halts the cycle (no agent,
//     no respawn) and surfaces as a wrapped error so the caller
//     can choose retry policy.
//  3. Gate predicate (canonical §3.4):
//     - AutoRespawnEnabled must be true
//     - AutoRespawnDisabledAt must be nil (loop guard armed)
//     - StateMdParseFailedAt must be nil (no parse-failure banner)
//     Failing any gate → return nil (no respawn, but no error
//     either; the crash_detected event is preserved for audit).
//  4. Loop-guard check: count respawn_fired events in the rolling
//     window. If count >= escalateAfter:
//     - SetAutoRespawnDisabledAt(now)
//     - Append respawn_skipped_loopguard event
//     - Route operator escalation via Escalation.Route — nil-safe
//       per F2 forward-flag (E6.4 pattern), so a partial-config
//       daemon doesn't nil-deref but still records the bookkeeping.
//     - return nil (loop-guard trip is not an error)
//  5. Fire respawn via Restarter.Restart. On success, append
//     respawn_fired event. F1 forward-flag: ErrHandlerWiringPending
//     (from agentdispatch placeholders + future scheduler paths) is
//     a benign skip — log + return nil without marking the agent
//     as crash-looped. Other restart failures bubble up.
func (r *Respawner) OnPaneGone(ctx context.Context, agentName string, detection state.DetectionMethod) error {
	// Step 1: append crash_detected event. Per F4 forward-flag,
	// the lifecycle store uses safedb under the hood (the canonical
	// daemon-path SQL anti-pattern check); no raw db.Exec here.
	if _, err := r.LifecycleStore.Append(ctx, state.AgentLifecycleEvent{
		AgentName:       agentName,
		EventKind:       state.EventCrashDetected,
		EventTime:       time.Now(),
		DetectionMethod: detection,
	}); err != nil {
		return fmt.Errorf("append crash_detected for %q: %w", agentName, err)
	}

	// Step 2: read agent record.
	agentRow, err := r.Registry.Lookup(ctx, agentName)
	if err != nil {
		return fmt.Errorf("lookup %q: %w", agentName, err)
	}

	// Step 3: gate predicate. Each gate failure is logged at debug
	// for operator diagnostics — the canonical reason is which
	// predicate failed.
	if !agentRow.AutoRespawnEnabled {
		slog.Debug("respawn skipped: auto_respawn not enabled",
			"agent", agentName)
		return nil
	}
	if agentRow.AutoRespawnDisabledAt != nil {
		slog.Debug("respawn skipped: loop guard previously tripped",
			"agent", agentName,
			"disabled_at", *agentRow.AutoRespawnDisabledAt)
		return nil
	}
	if agentRow.StateMdParseFailedAt != nil {
		slog.Debug("respawn skipped: state.md parse failure pending operator ack",
			"agent", agentName,
			"failed_at", *agentRow.StateMdParseFailedAt)
		return nil
	}

	// Step 4: loop-guard check. Defaults per canonical §6.3 when
	// the operator hasn't configured per-agent values.
	escalateAfter := agentRow.AutoRespawn.EscalateAfter
	if escalateAfter == 0 {
		escalateAfter = defaultRespawnEscalateAfter
	}
	windowSecs := agentRow.AutoRespawn.WindowSeconds
	if windowSecs == 0 {
		windowSecs = defaultRespawnWindowSeconds
	}
	count, err := r.LifecycleStore.LoopGuardCount(ctx, agentName, state.EventRespawnFired, windowSecs)
	if err != nil {
		return fmt.Errorf("loop guard count for %q: %w", agentName, err)
	}
	if count >= escalateAfter {
		now := time.Now()
		if err := r.Registry.SetAutoRespawnDisabledAt(ctx, agentName, now); err != nil {
			return fmt.Errorf("set auto_respawn_disabled_at for %q: %w", agentName, err)
		}
		reason := fmt.Sprintf("%d respawns in %ds tripped guard", count, windowSecs)
		if _, err := r.LifecycleStore.Append(ctx, state.AgentLifecycleEvent{
			AgentName: agentName,
			EventKind: state.EventRespawnSkippedLoopguard,
			EventTime: now,
			Reason:    reason,
		}); err != nil {
			return fmt.Errorf("append respawn_skipped_loopguard for %q: %w", agentName, err)
		}
		// F2 nil-guard: bookkeeping above is unconditional, but the
		// operator-facing alert is conditional on Escalation being
		// wired. Same pattern as idleNudgeLoop's Layer-D path.
		if r.Escalation != nil {
			_ = r.Escalation.Route(ctx,
				escalation.Alert{
					Source:    "b-b1.auto_respawn_loop_guard",
					AgentName: agentName,
				},
				"Auto-respawn disabled for "+agentName,
				fmt.Sprintf(loopGuardBody, agentName, count, windowSecs, agentName, agentName, agentName),
			)
		}
		return nil
	}

	// Step 5: fire respawn.
	if r.Restarter == nil {
		return fmt.Errorf("respawn %q: no Restarter dep wired", agentName)
	}
	if err := r.Restarter.Restart(ctx, agentName); err != nil {
		// F1: ErrHandlerWiringPending is benign — placeholder
		// handlers haven't been replaced with real ones yet (E6.5
		// Task 42b pending). Log + continue pane-gone watching for
		// the next trigger. Do NOT mark as crash-looped (we never
		// appended respawn_fired, so the loop guard is unaffected).
		if errors.Is(err, ErrHandlerWiringPending) {
			slog.Info("respawn deferred: handler wiring pending; skipping respawn cycle",
				"agent", agentName, "err", err)
			return nil
		}
		// Other restart failures bubble up — the pane-health caller
		// chooses retry policy. We do NOT count failed respawns as
		// respawn_fired because the loop guard is about excessive
		// respawn attempts that ACTUALLY fired (canonical §3.4
		// wording: "respawn_fired" is the event-kind that gates,
		// not "respawn_attempted").
		return fmt.Errorf("restart %q: %w", agentName, err)
	}
	// Step 5b: respawn fired successfully — append respawn_fired
	// so the loop guard can count it on the next cycle.
	if _, err := r.LifecycleStore.Append(ctx, state.AgentLifecycleEvent{
		AgentName: agentName,
		EventKind: state.EventRespawnFired,
		EventTime: time.Now(),
	}); err != nil {
		return fmt.Errorf("append respawn_fired for %q: %w", agentName, err)
	}
	return nil
}
