package agentdispatch

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/leonletto/thrum/internal/worktree"
)

// JournalReader is the read-only surface E6.9's boot reconciler needs
// over scheduler_job_events + scheduler_job_state. The production
// implementation lives in internal/daemon/scheduler (StateStore.EventsForRun
// + StateStore.NonTerminalWorktrees); journalAdapter below threads it
// through as the JournalReader interface so this package stays free of
// direct SQL access.
type JournalReader interface {
	// EventsForRun returns every scheduler_job_events row for the
	// given run_id, oldest first. Empty result on unknown run_id (the
	// caller treats that as the "no journal" row class — spec §7.7
	// row 1).
	EventsForRun(ctx context.Context, runID string) ([]scheduler.Event, error)

	// NonTerminalWorktrees returns the set of worktree paths journaled
	// under non-terminal scheduler_job_state rows. Consumed by E6.9's
	// orphan sweep (B2); declared on the interface so the same adapter
	// services both consumers.
	NonTerminalWorktrees(ctx context.Context) (map[string]bool, error)
}

// BootReconciler is the production implementation of the package's
// Reconciler interface (declared in scheduled_agent.go). At daemon
// boot, A-B1's per-row walker (scheduler/reconcile.go) routes each
// non-terminal scheduled_agent row here via Handler.Reconcile; the
// BootReconciler reads the per-stage journal and matches against the
// spec §7.7 row table to decide whether the run resumes, rolls back
// to scheduled, or fails.
//
// Composition (interface-injected so tests can swap fakes):
//   - tmux probes pane health via CheckPane.
//   - worktree.Destroy() cleans up orphan filesystem + branches.
//   - journal.EventsForRun() replays the per-stage transitions.
//   - lifecycleStore.Append() records crash_detected events when the
//     pane is gone (spec §7.7 row 5 + §6.2 mapping).
//
// All per-run state lives in stack scope; the BootReconciler struct is
// safe for concurrent ReconcileRun calls (A-B1 walks rows
// sequentially in v0.11, but the contract is concurrency-safe).
type BootReconciler struct {
	repoPath       string
	tmux           TmuxRPC
	worktree       WorktreeManager
	journal        JournalReader
	lifecycleStore state.AgentLifecycleStore

	// pathExists is os.Stat-based by default; tests override it to
	// fake "worktree gone from disk" (spec §7.7 row 2) without
	// touching the filesystem.
	pathExists func(string) bool

	// nowFn supplies AgentLifecycleEvent.EventTime; tests pin a
	// deterministic value via NewBootReconciler... overrides.
	nowFn func() time.Time
}

// NewBootReconciler constructs the production BootReconciler. pathExists
// defaults to an os.Stat-based check; nowFn defaults to time.Now. Tests
// override those fields directly (they're package-private) — no
// dedicated test-only constructor.
func NewBootReconciler(
	repoPath string,
	tmux TmuxRPC,
	wt WorktreeManager,
	journal JournalReader,
	lifecycleStore state.AgentLifecycleStore,
) *BootReconciler {
	return &BootReconciler{
		repoPath:       repoPath,
		tmux:           tmux,
		worktree:       wt,
		journal:        journal,
		lifecycleStore: lifecycleStore,
		pathExists:     defaultPathExists,
		nowFn:          time.Now,
	}
}

// defaultPathExists returns true iff os.Stat succeeds. Symlinks
// follow (Stat, not Lstat) so a dangling symlink reports gone —
// matches the operator intuition that "the worktree directory is
// usable" implies the underlying target resolves.
func defaultPathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// journalState carries the latest non-empty values for the three
// reconciliation-relevant fields recorded during the dispatch
// protocol's stage 3+4 transitions:
//   - stage 3 complete → worktree_path + branch_name
//   - stage 4 complete → tmux_session_name
//
// Rollback rows record *_destroyed / tmux_session_killed under
// different keys, so extractJournalState ignores them: we only
// walk worktree_path / branch_name / tmux_session_name.
type journalState struct {
	WorktreePath    string
	BranchName      string
	TmuxSessionName string
}

// extractJournalState replays events in ASC order, overwriting each
// field as later transitions record fresh values. ASC order means
// the last non-empty value wins, matching what's actually present
// on disk + in tmux at crash time.
func extractJournalState(events []scheduler.Event) journalState {
	var s journalState
	for _, e := range events {
		if wp, ok := e.Details["worktree_path"].(string); ok && wp != "" {
			s.WorktreePath = wp
		}
		if bn, ok := e.Details["branch_name"].(string); ok && bn != "" {
			s.BranchName = bn
		}
		if ts, ok := e.Details["tmux_session_name"].(string); ok && ts != "" {
			s.TmuxSessionName = ts
		}
	}
	return s
}

// ReconcileRun is the scheduler.Handler.Reconcile body for the
// scheduled_agent type. Routes the non-terminal row through spec
// §7.7's 5-row classification and returns the resolved state.
//
// Returns (newState, nil) on every classification — including the
// pane-gone failure path. The companion error wrapping in
// scheduler/reconcile.go drops non-ErrLostTrack errors (they're
// logged but no transition fires), so the only way to surface a
// definitive StateFailed transition is err == nil. The
// operator-facing failure reason lives in the agent_lifecycle_events
// row written via lifecycleStore.Append, not the scheduler_job_events
// reason (which becomes the canonical "reconciled at boot" string
// per scheduler/reconcile.go).
func (r *BootReconciler) ReconcileRun(
	ctx context.Context,
	job scheduler.JobSpec,
	runID string,
	lastState scheduler.State,
) (scheduler.State, error) {
	events, err := r.journal.EventsForRun(ctx, runID)
	if err != nil {
		return lastState, fmt.Errorf("reconcile %s: read journal: %w", runID, err)
	}
	jstate := extractJournalState(events)

	// Row 1: no worktree_path journaled — nothing on disk to clean up.
	if jstate.WorktreePath == "" {
		return scheduler.StateScheduled, nil
	}

	// Row 2: worktree_path journaled, worktree gone from disk. The
	// row's transition back to StateScheduled (written by
	// scheduler/reconcile.go's reconcileOne after we return) IS the
	// journal-of-discrepancy that spec §7.7 row 2 calls for; no
	// separate lifecycle event is appended because operator
	// hand-cleanup is the dominant cause and a crash_detected row
	// for that case would misclassify operator action as a crash.
	// Skip Destroy — double-destroy against an already-gone path
	// surfaces spurious git errors.
	if !r.pathExists(jstate.WorktreePath) {
		return scheduler.StateScheduled, nil
	}

	// Row 3: worktree exists but no tmux_session_name was journaled
	// (daemon died between stage 3 and stage 4). Destroy the orphan
	// + branch, then roll back. Destroy errors are intentionally
	// swallowed at this layer — boot reconciliation is best-effort
	// and the next sweep cycle (B2's SweepOrphans) catches anything
	// still on disk via the basepath scan.
	if jstate.TmuxSessionName == "" {
		_, _ = r.worktree.Destroy(ctx, worktree.DestroyOpts{
			RepoPath:     r.repoPath,
			WorktreePath: jstate.WorktreePath,
			Branch:       jstate.BranchName,
			Force:        true,
		})
		return scheduler.StateScheduled, nil
	}

	// Row 4: pane alive — the run survived the daemon restart. Resume
	// polling by holding state at running; A-B1's stage-7 re-entry
	// (spec §7.3) picks up the idle-nudge loop on the next tick.
	//
	// CheckPane errors are NOT silently treated as "pane gone": a
	// transient tmux-RPC failure (daemon not yet up, mux socket
	// flaking) would otherwise misclassify a live run into row 5
	// and irreversibly destroy its worktree. Surface the err so
	// scheduler/reconcile.go logs it without transitioning the row;
	// the next boot-walker pass (or a subsequent A-B1 reconciliation
	// tick if one exists) re-attempts cleanly.
	alive, err := r.tmux.CheckPane(ctx, jstate.TmuxSessionName)
	if err != nil {
		return lastState, fmt.Errorf("reconcile %s: check pane %q: %w", runID, jstate.TmuxSessionName, err)
	}
	if alive {
		return scheduler.StateRunning, nil
	}

	// Row 5: pane gone after the daemon-restart window. Destroy
	// orphan worktree + branch, append crash_detected lifecycle
	// event (operator-facing reason lives here), return StateFailed
	// so scheduler/reconcile.go increments consecutive_failures via
	// the StateFailed transition. We only journal the lifecycle
	// event when ScheduledAgent is non-nil — the field carries the
	// agent name that's the key on agent_lifecycle_events.
	//
	// Deviation from plan v2.5 §3304 (returns errors.New("pane
	// terminated…")) — see ReconcileRun doc-comment above; nil is
	// the only path that fires the StateFailed transition through
	// scheduler/reconcile.go.
	_, _ = r.worktree.Destroy(ctx, worktree.DestroyOpts{
		RepoPath:     r.repoPath,
		WorktreePath: jstate.WorktreePath,
		Branch:       jstate.BranchName,
		Force:        true,
	})
	if job.ScheduledAgent != nil {
		if _, appendErr := r.lifecycleStore.Append(ctx, state.AgentLifecycleEvent{
			AgentName:       job.ScheduledAgent.Target,
			EventKind:       state.EventCrashDetected,
			EventTime:       r.nowFn(),
			DetectionMethod: state.DetectionRestartReconciliation,
			Reason:          "pane terminated during daemon restart",
		}); appendErr != nil {
			// Operator-facing visibility: the crash_detected row is
			// the canonical audit trail for boot-time
			// restart-reconciliation crashes. Losing it silently
			// makes the surface row in `thrum team --journal`
			// disappear without explanation; surface via slog so
			// the daemon log still records what happened.
			slog.Warn("reconcile: lifecycle append failed",
				"agent", job.ScheduledAgent.Target,
				"event_kind", state.EventCrashDetected,
				"run_id", runID,
				"err", appendErr,
			)
		}
	}
	return scheduler.StateFailed, nil
}

// Compile-time check that *BootReconciler satisfies the consumer-side
// Reconciler interface declared in scheduled_agent.go.
// wireScheduledAgentHandlers (E6.9 B3) will swap NewReconcilerStub() →
// NewBootReconciler(...).
var _ Reconciler = (*BootReconciler)(nil)

// Compile-time check that *scheduler.StateStore directly satisfies
// JournalReader — its EventsForRun + NonTerminalWorktrees methods
// match the interface signatures exactly, so B3 can pass the
// daemon-boot StateStore through without a wrapping adapter. If a
// future StateStore signature drift breaks this, the build fails
// here rather than at the wireScheduledAgentHandlers call site
// where the chain of indirection makes the diagnostic less obvious.
var _ JournalReader = (*scheduler.StateStore)(nil)
