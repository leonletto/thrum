package agentdispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/leonletto/thrum/internal/daemon/escalation"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// ErrIdleNudgeExhausted fires when the multi-fire idle-nudge loop
// hits maxNudges without a Completion or activity. The loop has
// already transitioned the run to StateFailed with the canonical
// escalation_emitted_by marker by the time this error reaches the
// Dispatch caller — A-B1's evaluator sees the marker and
// suppresses its own consecutive-failure escalation per spec §5.2.
var ErrIdleNudgeExhausted = errors.New("idle nudge exhausted")

// Probe + settle constants. The 2s settle + the 3-error probe
// budget match the values internal/daemon/rpc/tmux.go uses for
// the post-launch single-fire watchdog (silenceThreshold = 5s,
// watchdogMaxConsecutiveErrors = 3, paneSettleAfterReady = 2s).
// Re-declared here rather than imported so this loop is
// dependency-clean from the rpc package; if the rpc values ever
// shift, this comment is the cross-reference point.
const (
	// idleNudgeSettleSeconds is added to the per-window timer
	// after observing activity, so the runtime has 2s to settle
	// (finish painting) before the next silence check fires. Per
	// feedback_byte_equality_pane_detection memory + Task 40.
	idleNudgeSettleSeconds = 2

	// idleNudgeMaxProbeErrors is the consecutive-error budget for
	// PaneActivity calls. Past this threshold, the loop assumes
	// the tmux session is gone and surfaces a StateFailed
	// transition with the probe-error count for diagnostics.
	idleNudgeMaxProbeErrors = 3
)

// idleNudgeLoop carries the per-dispatch state the stage-7 select
// loop needs to coordinate the multi-fire idle-nudge protocol per
// spec §7.3 + canonical §2.2 + spec §6.2 (Layer-D marker write).
//
// Lives in stack/parameter scope per IMPORTANT #7 dual-review:
// ScheduledAgentHandler is shared across concurrent dispatches
// (AC 9.2.10 race-detector clean for 5 simultaneous), so per-run
// state must NOT live on the receiver. The loop holds direct
// references to deps it needs (Tmux, Escalation) so onTimerFire
// is testable without reaching across the package.
type idleNudgeLoop struct {
	target           string
	runID            string
	idleSeconds      int
	maxNudges        int
	lastPaneActivity time.Time

	// timer is the per-window scheduler. defer loop.timer.Stop() in
	// Dispatch ensures the timer is GC'd even on early return paths
	// (signals / ctx-cancel before the timer fires for the first time).
	timer *time.Timer

	// nudgesFired tracks how many idle-nudge fires have happened in
	// this stage 7 entry. M4 forward-compat (brainstormer-third):
	// renamed from nudgeCount for consistency with plan §Task 36
	// vocabulary + the canonical "nudges_fired" details key in
	// Layer-D escalation events.
	nudgesFired int

	// consecutiveProbeErrors counts consecutive PaneActivity
	// failures. Past idleNudgeMaxProbeErrors the loop concludes
	// the tmux session is gone and fails the dispatch with the
	// probe-error count in details (operator diagnostic).
	consecutiveProbeErrors int

	// tmux + escalation are the deps the loop actively uses on
	// fire. Held directly (not via *ScheduledAgentHandler) so
	// onTimerFire is unit-testable without constructing the full
	// handler — the canonical test-seam pattern from E6.1.
	tmux       TmuxRPC
	escalation EscalationRouter

	// probe is the test-injection seam for PaneActivity. Production
	// callers leave it nil — onTimerFire falls through to the
	// package-level PaneActivity function. Tests inject a stub so
	// the loop can be exercised without spawning a real tmux
	// subprocess. Matches the seam pattern in internal/daemon/rpc/
	// tmux.go (tmuxLastActivityFn) but kept loop-local so the
	// override has function-call lifetime rather than package
	// lifetime — concurrent test execution is race-clean.
	probe func(ctx context.Context, target string) (time.Time, error)
}

// onTimerFire runs one tick of the multi-fire idle-nudge protocol
// per spec §7.3. Called from the stage-7 select-loop's timer arm
// at every idleSeconds window boundary.
//
// Algorithm:
//  1. Probe PaneActivity. Probe errors are absorbed up to
//     idleNudgeMaxProbeErrors consecutive failures, then
//     StateFailed (the runtime is gone).
//  2. If activity has advanced since lastPaneActivity, the pane
//     was emitting/receiving during the window — update
//     lastPaneActivity, re-arm with idleSeconds + 2s settle (per
//     Task 40 + feedback_byte_equality_pane_detection memory),
//     return nil.
//  3. Pane was silent — increment nudgesFired, emit the
//     idle_nudge_NofM stage marker.
//  4. If nudgesFired >= maxNudges: Layer-D escalation. Transition
//     to StateFailed with escalation_emitted_by="b-b1.idle_nudge"
//     so A-B1's evaluator suppresses its own escalation per spec
//     §5.2/§6.2. Route to operator via the canonical escalation
//     helper (nil-safe — I3 forward-compat). Return
//     ErrIdleNudgeExhausted so Dispatch closes.
//  5. Else: inject the nudge prompt into the pane (operator-
//     visible message asking the runtime to either run
//     `thrum job done` if work is complete or ignore + continue
//     if blocked on external work), re-arm with the normal
//     idleSeconds window (no settle — we're already in
//     not-silent-yet state), return nil.
func (loop *idleNudgeLoop) onTimerFire(ctx context.Context, reporter scheduler.StateReporter) error {
	probeFn := loop.probe
	if probeFn == nil {
		probeFn = PaneActivity
	}
	activity, err := probeFn(ctx, loop.target)
	if err != nil {
		loop.consecutiveProbeErrors++
		if loop.consecutiveProbeErrors >= idleNudgeMaxProbeErrors {
			_ = reporter.Transition(scheduler.StateFailed,
				"idle nudge: pane-activity probe failed consecutively",
				map[string]any{"probe_errors": loop.consecutiveProbeErrors})
			return fmt.Errorf("idle nudge probe error budget exhausted: %w", err)
		}
		// Single transient error — re-arm and retry on next fire.
		loop.timer.Reset(time.Duration(loop.idleSeconds) * time.Second)
		slog.Debug("idle nudge probe transient error; will retry",
			"target", loop.target,
			"consecutive_errors", loop.consecutiveProbeErrors,
			"err", err,
		)
		return nil
	}
	// Probe succeeded — reset the consecutive-error counter so a
	// stale probe error doesn't poison future ticks.
	loop.consecutiveProbeErrors = 0

	if activity.After(loop.lastPaneActivity) {
		// Pane was active during the window. Per Task 40 +
		// feedback_byte_equality_pane_detection memory: add a 2s
		// settle on top of the idle window so the runtime has
		// time to finish painting before the next silence check.
		loop.lastPaneActivity = activity
		settled := time.Duration(loop.idleSeconds+idleNudgeSettleSeconds) * time.Second
		loop.timer.Reset(settled)
		return nil
	}

	// Pane has been silent for the full idle window. Fire nudge.
	loop.nudgesFired++
	_ = reporter.Stage(IdleNudgeStageFmt(loop.nudgesFired, loop.maxNudges))

	if loop.nudgesFired >= loop.maxNudges {
		// Layer-D escalation per canonical §6.2 + spec §5.2.
		_ = reporter.Transition(scheduler.StateFailed,
			"idle nudge exhausted",
			map[string]any{
				"escalation_emitted_by": "b-b1.idle_nudge",
				"nudges_fired":          loop.nudgesFired,
			})
		// Route the operator-facing alert. nil-safe via routeEscalation
		// pattern — when Escalation isn't wired, we still transition
		// to StateFailed + return ErrIdleNudgeExhausted so the
		// substrate's bookkeeping is correct; only the operator
		// notification is skipped.
		if loop.escalation != nil {
			_ = loop.escalation.Route(ctx,
				escalation.Alert{
					Source:    "b-b1.idle_nudge",
					AgentName: loop.target,
					RunID:     loop.runID,
				},
				"Scheduled agent idle-nudge exhausted",
				buildLayerDBody(loop),
			)
		}
		return ErrIdleNudgeExhausted
	}

	// Inject the operator-visible nudge prompt and re-arm.
	// PaneInjectPrompt errors are absorbed — best-effort by
	// design; if injection fails, the agent simply doesn't see
	// the nudge and the loop retries on the next fire.
	if err := loop.tmux.PaneInjectPrompt(ctx, loop.target, idleNudgePrompt(loop.nudgesFired, loop.maxNudges)); err != nil {
		slog.Warn("idle nudge prompt injection failed; will retry on next fire",
			"target", loop.target,
			"nudge", fmt.Sprintf("%d/%d", loop.nudgesFired, loop.maxNudges),
			"err", err,
		)
	}
	loop.timer.Reset(time.Duration(loop.idleSeconds) * time.Second)
	return nil
}

// idleNudgePrompt formats the operator-visible nudge body per spec
// §9.5.7 ("distinguishable markers"). The N-of-M counter gives the
// agent context for how close it is to Layer-D escalation; the
// "thrum job done" reference is the canonical CLI surface to close
// the run cleanly.
func idleNudgePrompt(n, m int) string {
	return fmt.Sprintf(
		"\n[Idle detection — finished? If yes, run: thrum job done. "+
			"If waiting on something external (a peer reply, long compute, etc.), "+
			"ignore this and continue. Nudge %d of %d.]\n",
		n, m,
	)
}

// buildLayerDBody composes the operator-facing escalation body
// sent through escalation.Route when nudgesFired reaches
// maxNudges. Includes the agent name, run ID, and final nudge
// count so the operator can drill into `thrum cron history` for
// the full event chain.
func buildLayerDBody(loop *idleNudgeLoop) string {
	return fmt.Sprintf(
		"Scheduled agent %q (run %s) exhausted its idle-nudge budget "+
			"(%d of %d fires without activity or completion). The substrate "+
			"has marked the run failed. Investigate via: thrum cron history %s",
		loop.target, loop.runID, loop.nudgesFired, loop.maxNudges, loop.runID,
	)
}
