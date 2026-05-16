package scheduler

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/daemon/scheduler/schedule"
)

// reactorState is the in-reactor view of the world: a min-heap of pending
// fires plus the per-job parsed Schedule (state-bearing for @at / @once).
// Only the reactor goroutine reads or writes this; no synchronization.
type reactorState struct {
	heap      *fireHeap
	schedules map[string]schedule.Schedule
}

// runReactor is the single reactor goroutine. Maintains a min-heap of
// (next_fire_time, job_id). Wakes on soonest, dispatches via per-run
// goroutine, recomputes next-fire, requeues. Also wakes early on
// registration changes via reactorWake.
func (s *Scheduler) runReactor(ctx context.Context) {
	state := &reactorState{
		heap:      &fireHeap{},
		schedules: map[string]schedule.Schedule{},
	}
	heap.Init(state.heap)

	s.seedHeap(state)

	// Bogus initial duration; overwritten before the first select.
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	for {
		var waitDur time.Duration
		if state.heap.Len() == 0 {
			// No jobs; long sleep — reactorWake will fire on registration.
			waitDur = time.Hour
		} else {
			top := state.heap.peek()
			waitDur = time.Until(top.fireAt)
			if waitDur < 0 {
				waitDur = 0
			}
		}
		if !timer.Stop() {
			// Drain the channel if the timer already fired but its value is
			// still queued. Go 1.23+ Reset semantics make this defensive
			// rather than load-bearing, but cheap insurance.
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(waitDur)

		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-s.reactorWake:
			s.seedHeap(state)
		case <-timer.C:
			now := time.Now()
			for state.heap.Len() > 0 && !state.heap.peek().fireAt.After(now) {
				//nolint:forcetypeassert // fireHeap only ever contains *heapItem; failure means programmer error
				top := heap.Pop(state.heap).(*heapItem)
				s.dispatchOne(ctx, state, top)
			}
		}
	}
}

// seedHeap walks the spec map and adds any not-yet-tracked enabled jobs to
// the heap. Honors RunAtStart by pinning the first fire to now. Idempotent
// — re-running on reactorWake only adds newly-registered jobs.
func (s *Scheduler) seedHeap(state *reactorState) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for jobID, spec := range s.specs {
		if !spec.Enabled {
			continue
		}
		if _, seen := state.schedules[jobID]; seen {
			continue
		}

		loc := s.resolveLocation(spec)
		sched, err := schedule.Parse(spec.Schedule, schedule.ParseOpts{
			Location:   loc,
			JitterSeed: jobID + s.cfg.DaemonID,
		})
		if err != nil {
			log.Printf("scheduler: skip %s (parse error): %v", jobID, err)
			continue
		}
		state.schedules[jobID] = sched

		var fireAt time.Time
		if spec.RunAtStart {
			fireAt = time.Now()
		} else {
			fireAt = sched.Next(time.Now())
			if fireAt.IsZero() {
				// One-shot with no future fire (already-fired @at in the past).
				continue
			}
		}
		// Jitter is reactor-applied. For one-shot schedules the period
		// collapses to 0 (DeterministicJitter returns 0); for recurring
		// the per-job Jitter override controls bounds (0 = default ±3%).
		period := time.Until(fireAt)
		if period > 0 {
			jit := schedule.DeterministicJitter(jobID, s.cfg.DaemonID, period, spec.Jitter)
			fireAt = fireAt.Add(jit)
		}
		heap.Push(state.heap, &heapItem{fireAt: fireAt, jobID: jobID})
	}
}

// dispatchOne fires one job: overlap-skip check, mint run_id, write
// dispatched state + event, launch handler goroutine, requeue if recurring.
func (s *Scheduler) dispatchOne(ctx context.Context, state *reactorState, item *heapItem) {
	spec, ok := s.JobSpec(item.jobID)
	if !ok {
		// Deregistered between schedule and fire.
		return
	}

	s.mu.RLock()
	handler := s.resolveHandler(spec)
	s.mu.RUnlock()
	if handler == nil {
		log.Printf("scheduler: no handler for job %q (type=%q)", spec.ID, spec.Type)
		return
	}

	// Overlap-skip: if the prior run is still in `running`, append an
	// overlapping_skipped event and decline to enter the run path.
	existing, err := s.state.GetState(ctx, spec.ID)
	if err != nil && !errors.Is(err, ErrJobNotFound) {
		log.Printf("scheduler: GetState %s: %v", spec.ID, err)
		// Soldier on — treat as no-prior-row.
		existing = nil
	}
	if existing != nil && existing.CurrentState == StateRunning {
		_ = s.state.AppendEvent(ctx, &Event{
			JobID:     spec.ID,
			RunID:     existing.LastRunID,
			EventTime: time.Now(),
			FromState: StateRunning,
			ToState:   StateOverlappingSkipped,
			Reason:    "prior run still active",
		})
		// Don't increment total_runs; just schedule the next fire.
		s.scheduleNext(state, spec.ID, time.Now())
		return
	}

	// Mint run_id per Q4.2: <job_id>-g<generation>-<fire_unix>
	generation := 1
	if existing != nil && existing.JobID != "" {
		generation = existing.Generation
	}
	runID := fmt.Sprintf("%s-g%d-%d", spec.ID, generation, item.fireAt.Unix())

	now := time.Now()
	nextScheduled := s.computeNextFor(state, spec.ID, now)
	err = s.state.UpsertState(ctx, &StateRow{
		JobID:           spec.ID,
		Generation:      generation,
		CurrentState:    StateDispatched,
		LastRunID:       runID,
		LastFiredAt:     &now,
		NextScheduledAt: nextScheduled,
		TotalRuns:       incrementRuns(existing),
		CreatedAt:       createdAtOrNow(existing, now),
		UpdatedAt:       now,
	})
	if err != nil {
		log.Printf("scheduler: UpsertState(dispatched) %s: %v", spec.ID, err)
		return
	}
	_ = s.state.AppendEvent(ctx, &Event{
		JobID:     spec.ID,
		RunID:     runID,
		EventTime: now,
		FromState: "",
		ToState:   StateDispatched,
		Reason:    "tick fired",
	})

	s.launchRun(ctx, spec, runID, handler)

	if nextScheduled != nil {
		heap.Push(state.heap, &heapItem{fireAt: *nextScheduled, jobID: spec.ID})
	}
}

// resolveHandler returns the handler for a spec. Internal jobs use the
// per-job handlers map; user jobs route by Type to typeHandlers. Caller
// holds s.mu (read or write).
func (s *Scheduler) resolveHandler(spec JobSpec) Handler {
	if spec.Type == "internal" {
		return s.handlers[spec.ID]
	}
	return s.typeHandlers[spec.Type]
}

// resolveLocation returns the *time.Location for parsing a job's schedule
// per spec §8.2.5's 4-level cascade:
//  1. per-job  jobs.<id>.schedule_tz (JobSpec.ScheduleTZ)
//  2. daemon   daemon.schedule_tz (Config.Location)
//  3. operator-local (time.Local)
//  4. UTC fallback
//
// Unparseable per-job TZ falls through to the daemon default with a log
// line so config errors are visible to the operator.
func (s *Scheduler) resolveLocation(spec JobSpec) *time.Location {
	if spec.ScheduleTZ != "" {
		if loc, err := time.LoadLocation(spec.ScheduleTZ); err == nil {
			return loc
		}
		log.Printf("scheduler: job %s: invalid schedule_tz %q; falling back to daemon default", spec.ID, spec.ScheduleTZ)
	}
	if s.cfg.Location != nil {
		return s.cfg.Location
	}
	if time.Local != nil {
		return time.Local
	}
	return time.UTC
}

// scheduleNext recomputes the next fire for a job and requeues. No-op if
// the schedule has no more fires (one-shot done).
func (s *Scheduler) scheduleNext(state *reactorState, jobID string, now time.Time) {
	next := s.computeNextFor(state, jobID, now)
	if next == nil {
		return
	}
	heap.Push(state.heap, &heapItem{fireAt: *next, jobID: jobID})
}

// computeNextFor consults the per-job Schedule + applies jitter. Returns
// nil when the schedule signals one-shot-done (Next() returns zero time).
func (s *Scheduler) computeNextFor(state *reactorState, jobID string, now time.Time) *time.Time {
	sched, ok := state.schedules[jobID]
	if !ok {
		return nil
	}
	next := sched.Next(now)
	if next.IsZero() {
		return nil
	}
	spec, _ := s.JobSpec(jobID)
	period := time.Until(next)
	if period > 0 {
		jit := schedule.DeterministicJitter(jobID, s.cfg.DaemonID, period, spec.Jitter)
		next = next.Add(jit)
	}
	return &next
}

// incrementRuns returns prior total_runs + 1, or 1 for first-fire rows.
func incrementRuns(existing *StateRow) int {
	if existing == nil {
		return 1
	}
	return existing.TotalRuns + 1
}

// createdAtOrNow preserves the original CreatedAt across updates; first-fire
// rows get `now`.
func createdAtOrNow(existing *StateRow, now time.Time) time.Time {
	if existing == nil || existing.CreatedAt.IsZero() {
		return now
	}
	return existing.CreatedAt
}

// launchRun starts a per-run goroutine wrapped in `defer recover()`. On
// panic, writes a StateFailed transition with `handler panic: <msg>` plus
// the stack trace in event details — the daemon stays up.
//
// Registers cancel-func + signal-channel on entry; deregisters on exit
// (whether terminal-transition completion or post-panic cleanup).
//
// Substrate-owned (command, thrum_command) and B-B1's scheduled_agent
// handler are responsible for the dispatched → running transition via
// reporter.Transition(StateRunning, ...); the `nudge` handler skips
// `running` per Q5.2. If the handler returns nil without a terminal
// transition, treat as completed. If the handler returns an error
// without a terminal transition, treat as failed.
func (s *Scheduler) launchRun(ctx context.Context, spec JobSpec, runID string, h Handler) {
	runCtx, cancel := context.WithCancel(ctx)
	signals := s.runReg.register(runID, cancel)
	reporter := &stateReporter{
		store: s.state,
		jobID: spec.ID,
		runID: runID,
	}

	go func() {
		// Always deregister at goroutine exit so the registry doesn't leak
		// even if the handler panics or returns without a terminal
		// transition.
		defer s.runReg.deregister(runID)
		defer func() {
			if r := recover(); r != nil {
				_ = reporter.Transition(StateFailed,
					fmt.Sprintf("handler panic: %v", r),
					map[string]any{"stack": string(debug.Stack())})
				log.Printf("scheduler: handler panic for run %s: %v", runID, r)
			}
		}()

		err := h.Dispatch(runCtx, spec, runID, reporter, signals)
		if err != nil {
			row, _ := s.state.GetState(context.Background(), spec.ID)
			if row != nil && !isTerminal(row.CurrentState) {
				_ = reporter.Transition(StateFailed,
					fmt.Sprintf("handler returned err: %v", err), nil)
			}
			return
		}
		// Handler returned nil; if no terminal transition was emitted,
		// treat as completed.
		row, _ := s.state.GetState(context.Background(), spec.ID)
		if row != nil && !isTerminal(row.CurrentState) {
			_ = reporter.Transition(StateCompleted, "handler returned without explicit completion", nil)
		}
	}()
}

// stateReporter is the substrate-side StateReporter implementation — one
// per run. Writes both the latest-state row and the event-log row for
// every transition; the two writes are not yet wrapped in a single SQLite
// transaction (E1.3 may refactor).
//
// E1.3 will move stateReporter alongside the canonical StateReporter
// interface in handler.go; it lives here in E1.1 to keep the reactor's
// panic-recover wrapper self-contained.
type stateReporter struct {
	store *StateStore
	jobID string
	runID string
}

// Transition records a state change. Idempotent w.r.t. terminal states:
// the reactor.launchRun deferred path may call this after the handler has
// already emitted a terminal transition, in which case we no-op rather
// than over-write.
func (r *stateReporter) Transition(to State, reason string, details map[string]any) error {
	ctx := context.Background()
	existing, err := r.store.GetState(ctx, r.jobID)
	if err != nil && !errors.Is(err, ErrJobNotFound) {
		return err
	}
	if existing == nil {
		// Shouldn't happen — dispatchOne writes 'dispatched' before
		// invoking the handler. Defensive: surface the anomaly rather
		// than panic on the nil deref.
		return fmt.Errorf("scheduler: no state row for %q at Transition(%q)", r.jobID, to)
	}

	now := time.Now()
	fromState := existing.CurrentState
	newRow := *existing
	newRow.CurrentState = to
	newRow.UpdatedAt = now

	switch to {
	case StateRunning:
		// No special bookkeeping; stage/timing recorded via Stage().
	case StateCompleted, StateFailed, StateCancelled, StateOverBudget:
		newRow.LastCompletedAt = &now
		newRow.LastCompletionState = to
		switch to {
		case StateCompleted:
			newRow.ConsecutiveFailures = 0
			newRow.EscalationSent = false
			newRow.LastError = ""
		case StateFailed:
			newRow.ConsecutiveFailures = existing.ConsecutiveFailures + 1
			if reason != "" {
				newRow.LastError = reason
			}
			// Canonical §6.3 marker-readback: if the failure carries an
			// escalation_emitted_by marker matching `b-b1.*`, suppress
			// A-B1's own escalation emit (E1.3 owns the emit side).
			if details != nil {
				if marker, ok := details["escalation_emitted_by"].(string); ok && strings.HasPrefix(marker, "b-b1.") {
					newRow.EscalationSent = true
				}
			}
		}
	}

	if err := r.store.UpsertState(ctx, &newRow); err != nil {
		return err
	}
	return r.store.AppendEvent(ctx, &Event{
		JobID:     r.jobID,
		RunID:     r.runID,
		EventTime: now,
		FromState: fromState,
		ToState:   to,
		Reason:    reason,
		Details:   details,
	})
}

// Stage records entry into a named stage (e.g. "executing"). Empty name
// clears the stage marker. State remains whatever it was; the event log
// captures the stage entry for job.show / job.history.
func (r *stateReporter) Stage(name string) error {
	ctx := context.Background()
	existing, err := r.store.GetState(ctx, r.jobID)
	if err != nil {
		return err
	}
	now := time.Now()
	newRow := *existing
	newRow.CurrentStage = name
	if name == "" {
		newRow.StageEnteredAt = nil
	} else {
		newRow.StageEnteredAt = &now
	}
	newRow.UpdatedAt = now
	if err := r.store.UpsertState(ctx, &newRow); err != nil {
		return err
	}
	return r.store.AppendEvent(ctx, &Event{
		JobID:     r.jobID,
		RunID:     r.runID,
		EventTime: now,
		FromState: existing.CurrentState,
		ToState:   existing.CurrentState,
		Reason:    "stage: " + name,
	})
}

// isTerminal reports whether `s` is a terminal state — one that closes a
// run. Includes StateOverlappingSkipped because overlap-skip never enters
// the run path in the first place.
func isTerminal(s State) bool {
	switch s {
	case StateCompleted, StateFailed, StateCancelled, StateOverBudget, StateOverlappingSkipped:
		return true
	}
	return false
}

// heapItem is one entry in the reactor's min-heap.
type heapItem struct {
	fireAt time.Time
	jobID  string
}

// fireHeap implements container/heap.Interface, ordered by fireAt ascending.
type fireHeap []*heapItem

func (h fireHeap) Len() int           { return len(h) }
func (h fireHeap) Less(i, j int) bool { return h[i].fireAt.Before(h[j].fireAt) }
func (h fireHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

// Push satisfies container/heap.Interface. Callers must only pass *heapItem
// per the fireHeap contract; a wrong-type push is a programmer error.
func (h *fireHeap) Push(x any) {
	//nolint:forcetypeassert // fireHeap contract: only *heapItem is pushed
	*h = append(*h, x.(*heapItem))
}

func (h *fireHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
func (h *fireHeap) peek() *heapItem { return (*h)[0] }
