package sweep

import (
	"context"
	"fmt"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// JobSpecAccessor is the minimal interface this adapter needs from
// A-B1's *scheduler.Scheduler. Defined here as a narrow seam so
// tests can stub without spinning up a real Scheduler.
//
// Real impl is satisfied directly by *scheduler.Scheduler (which
// exposes JobSpec(id) (JobSpec, bool)).
type JobSpecAccessor interface {
	JobSpec(id string) (scheduler.JobSpec, bool)
}

// schedulerStateAdapter satisfies the sweep.SchedulerState interface
// by joining scheduler_job_state (job_id by stage label) with the
// in-memory JobSpec registry to map job_ids to their target agents.
//
// The DDL of scheduler_job_state (canonical §3.2) has no agent_name
// column — target lives on the JobSpec (jobs.<id>.scheduled_agent.
// target). This adapter is the cross-package join that translates
// from one to the other.
type schedulerStateAdapter struct {
	db   *safedb.DB
	jobs JobSpecAccessor
}

// NewSchedulerState wires the SQL-side DB access + the in-memory
// JobSpec accessor. Returns a sweep.SchedulerState ready to inject
// into Handler.
//
// Production wiring (thrum-6qmf.3.16): pass the daemon's *safedb.DB
// and the live *scheduler.Scheduler instance.
func NewSchedulerState(db *safedb.DB, jobs JobSpecAccessor) SchedulerState {
	return &schedulerStateAdapter{db: db, jobs: jobs}
}

// AgentsInBB1ManagedStages returns the set of agent names whose
// currently active scheduled-agent run is in a B-B1-managed stage
// (running_work, idle_nudge_*). A-B4 sweep reads this to compute its
// skip-set: those agents already have B-B1's idle-nudge engaged, so
// sweeping them would duplicate the nudge sequence.
//
// Excluded stage labels — pre-first-output stages like
// awaiting_first_output, launching_runtime, creating_worktree —
// stay sweep-eligible because B-B1's idle-nudge only engages AFTER
// first output. The Q4 high-stakes case (agent wedges at boot with
// zero output) depends on this exclusion.
//
// Implementation: single SQL pulls job_ids matching the stage labels,
// then Go-side iterates each id through JobSpecAccessor to resolve
// the agent target. Jobs that were de-registered mid-flight (JobSpec
// returns ok=false) are silently skipped — they can't be in B-B1's
// management anymore.
func (s *schedulerStateAdapter) AgentsInBB1ManagedStages(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT job_id
		FROM scheduler_job_state
		WHERE current_stage = 'running_work'
		   OR current_stage LIKE 'idle_nudge_%'
	`)
	if err != nil {
		return nil, fmt.Errorf("query managed-stage job_ids: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]bool{}
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			return nil, fmt.Errorf("scan job_id: %w", err)
		}
		spec, ok := s.jobs.JobSpec(jobID)
		if !ok {
			// Job de-registered mid-flight (config reload removed it,
			// or the row exists from a prior run no longer in the
			// active spec set). Skip — no agent to add to the skip-set.
			continue
		}
		if spec.ScheduledAgent == nil || spec.ScheduledAgent.Target == "" {
			// Non-scheduled_agent job (command / thrum_command /
			// nudge / internal). These have no target agent so they
			// can't appear in the sweep skip set. Skip.
			continue
		}
		out[spec.ScheduledAgent.Target] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job_id rows: %w", err)
	}
	return out, nil
}

// Compile-time satisfaction check for sweep.SchedulerState.
var _ SchedulerState = (*schedulerStateAdapter)(nil)
