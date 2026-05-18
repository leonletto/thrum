// Package agentdispatch hosts the B-B1 scheduled-agent + nudge handler
// surface that the daemon's scheduler registers at boot. The package
// already ships cleanup_internal.go (E6.0 Task 4 — agent_lifecycle_events
// daily prune) and grows in E6.1 with the scheduled_agent 9-stage
// dispatcher.
package agentdispatch

import "fmt"

// Canonical stage vocabulary per substrate spec §7.1 + canonical-ref §2.2.
// These nine constants are the entire fixed stage set; the multi-fire
// idle-nudge stages (idle_nudge_1of5, idle_nudge_2of5, ...) are emitted
// dynamically via idleNudgeStageFmt during stage 7.
//
// Drift here breaks: A-B4's stalled-sweep skip-set (queries against
// scheduler_job_state.current_stage), B-B1's own idle-nudge stage markers,
// the operator-facing `thrum cron history` output, and the AC 9.2.2 test
// that pins the nine-stage walk in the event log.
const (
	StageNameCollisionCheck  = "name_collision_check"
	StageBudgetCheck         = "budget_check"
	StageEnqueueWakeMessage  = "enqueue_wake_message"
	StageCreatingWorktree    = "creating_worktree"
	StageCreatingTmuxSession = "creating_tmux_session"
	StageLaunchingRuntime    = "launching_runtime"
	StageWaitingForPaneReady = "waiting_for_pane_ready"
	StageRunningWork         = "running_work"
	StageTearingDown         = "tearing_down"
)

// IdleNudgeStageFmt produces the canonical §2.2 dynamic stage marker
// emitted during stage 7's multi-fire idle-nudge loop (E6.4). The format
// is intentionally compatible with both "idle_nudge_3of5" pattern
// matchers in `thrum cron history` and the A-B4 sweep observability
// query that looks for the idle_nudge_ prefix.
//
// Exported here in E6.1 Task 9 so the format is testable in isolation
// AND so E6.4's multi-fire loop body (Task 36) can call it without
// re-declaring the format string in two places.
func IdleNudgeStageFmt(n, m int) string {
	return fmt.Sprintf("idle_nudge_%dof%d", n, m)
}
