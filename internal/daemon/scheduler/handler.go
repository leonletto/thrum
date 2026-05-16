package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Handler is the per-type dispatch contract. Implemented by:
//
//   - internal/daemon/scheduler/handlers/command.go       (substrate-owned)
//   - internal/daemon/scheduler/handlers/thrum_command.go (substrate-owned)
//   - internal/daemon/scheduler/cleanup.go                (E1.7 — own cleanup; via RegisterInternal)
//   - internal/daemon/agentdispatch/scheduled_agent.go    (B-B1 / E6.1)
//   - internal/daemon/agentdispatch/nudge.go              (B-B1 / E6.3)
//   - internal/backup/handler.go                          (A-B2 / via RegisterInternal)
//   - internal/daemon/sync_handler.go                     (A-B2 / via RegisterInternal)
//   - internal/bridge/email/poll.go                       (D-B1 / via RegisterInternal)
//   - internal/daemon/sweep/sweep.go                      (A-B4 / via RegisterInternal)
//
// Cross-epic stability commitment: this interface is consumed by every
// downstream v0.11 substrate consumer. Adding a method is a breaking
// change; changing a method signature is a breaking change.
type Handler interface {
	// Dispatch runs one fire of a job. Returns when the run reaches a
	// terminal state. Handler reports state and stage transitions via the
	// provided reporter. The signals channel receives Completion values
	// from job.done RPC; scheduled_agent handler consumes, other handlers
	// ignore.
	Dispatch(ctx context.Context, job JobSpec, runID string, reporter StateReporter, signals <-chan *Completion) error

	// Reconcile is called at daemon boot for each non-terminal run found
	// in scheduler_job_state (the StateStore.NonTerminalAtBoot enumeration).
	// Returns the actual current state, or ErrLostTrack if the handler
	// can't determine — that signals the substrate to mark the run
	// failed.
	Reconcile(ctx context.Context, job JobSpec, runID string, lastState State) (State, error)

	// Stages returns the handler's declared stage vocabulary with default
	// max-dwell durations. Read at daemon startup; used by A-B4
	// stalled-sweep to know when to nudge.
	Stages() map[string]time.Duration
}

// StateReporter is provided to Handler.Dispatch; the handler reports
// state-machine transitions through it. The substrate-side implementation
// writes scheduler_job_state + scheduler_job_events atomically (single
// SQLite transaction per spec §8.4.2).
type StateReporter interface {
	Transition(to State, reason string, details map[string]any) error
	Stage(name string) error
}

// Completion is the structured payload delivered through a per-run signal
// channel from the job.done RPC (canonical-ref §6.1 Alt-A).
type Completion struct {
	Reason  string
	Summary string
}

// Sentinel errors for the Handler + RPC surfaces.
var (
	ErrUnknownRun                 = errors.New("scheduler: unknown run id")
	ErrCompletionAlreadyDelivered = errors.New("scheduler: completion already delivered")
	ErrJobActive                  = errors.New("scheduler: job has active run; cancel first")
	ErrLostTrack                  = errors.New("scheduler: handler lost track across daemon restart")
)

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

// stateReporter is the substrate-side StateReporter — one per run. Writes
// the latest-state row and the event-log row in a single SQLite
// transaction per spec §8.4.2: a daemon crash between the two writes
// would otherwise leave the state row updated but the event log empty,
// breaking the audit trail.
type stateReporter struct {
	store *StateStore
	jobID string
	runID string
}

// Transition records a state change. Atomic w.r.t. scheduler_job_state +
// scheduler_job_events writes via StateStore.UpsertStateAndEvent.
//
// Failure rules:
//   - StateFailed increments consecutive_failures and records reason in
//     last_error. If details carries `escalation_emitted_by` matching
//     `b-b1.*`, set escalation_sent=true so A-B1's own emit (E1.3) can
//     short-circuit (canonical §6.3 marker-readback).
//   - StateCompleted resets consecutive_failures + escalation_sent and
//     clears last_error.
func (r *stateReporter) Transition(to State, reason string, details map[string]any) error {
	ctx := context.Background()
	existing, err := r.store.GetState(ctx, r.jobID)
	if err != nil && !errors.Is(err, ErrJobNotFound) {
		return err
	}
	if existing == nil {
		// dispatchOne writes 'dispatched' before invoking the handler;
		// existing should never be nil here. Surface the anomaly rather
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
			if details != nil {
				if marker, ok := details["escalation_emitted_by"].(string); ok && strings.HasPrefix(marker, "b-b1.") {
					newRow.EscalationSent = true
				}
			}
		}
	}

	event := &Event{
		JobID:     r.jobID,
		RunID:     r.runID,
		EventTime: now,
		FromState: fromState,
		ToState:   to,
		Reason:    reason,
		Details:   details,
	}
	return r.store.UpsertStateAndEvent(ctx, &newRow, event)
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

	event := &Event{
		JobID:     r.jobID,
		RunID:     r.runID,
		EventTime: now,
		FromState: existing.CurrentState,
		ToState:   existing.CurrentState,
		Reason:    "stage: " + name,
	}
	return r.store.UpsertStateAndEvent(ctx, &newRow, event)
}
