package agentdispatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/leonletto/thrum/internal/daemon/scheduler"
)

// ErrHandlerWiringPending is the canonical sentinel returned when an
// agentdispatch handler is constructed without its full Deps surface
// (or stub-registered as a PlaceholderHandler). Surfaces the wiring
// gap as a clean error rather than a nil-deref panic — the
// `thrum cron history` audit trail records a clear "wiring pending"
// failure so operators can correlate against the substrate's rollout
// state.
//
// Consumers (errors.Is callers): PlaceholderHandler.Dispatch (this
// package); E6.7 respawn.go (benign-skip guard for placeholder
// returns). Stable error identity — message text may evolve over
// time but errors.Is comparisons remain stable.
var ErrHandlerWiringPending = errors.New("agentdispatch: handler wiring deferred to lifecycle setup")

// PlaceholderHandler satisfies scheduler.Handler with no-op
// implementations that return ErrHandlerWiringPending from Dispatch
// and behave conservatively for Reconcile (marks any non-terminal
// row as failed since there's no real handler to recover the run).
//
// Lifecycle: registered at daemon boot by E6.5 Task 42a as the
// type-taxonomy entry for "scheduled_agent" and "nudge". E6.5 Task
// 42b replaces these registrations with the real
// ScheduledAgentHandler + NudgeHandler once the adapter glue (TmuxRPC,
// MessageRPC, WorktreeManager, etc.) is built out. Until then,
// having the type-names registered means the validator + reactor
// recognize the type even though dispatch isn't yet possible.
//
// Why ship the placeholder at all (vs. leaving the types
// unregistered until 42b)?
//   - The job-type taxonomy IS the substrate-level contract that
//     A-B1's validator, B-B2's CLI, and A-B4's stalled-sweep all
//     depend on. Registration order matters for the reconcile loop
//     (spec §8.4.4); registering as soon as the types are agreed
//     keeps the substrate honest about the "valid types are
//     scheduled_agent + nudge" promise.
//   - A scheduler reload that picks up a `type: scheduled_agent`
//     job specification while the type isn't registered would
//     surface as a different error class than "wiring pending" —
//     operators reading logs would see "unknown type" and conclude
//     the substrate is broken. The placeholder gives the right
//     error vocabulary for the right gap.
type PlaceholderHandler struct {
	jobType string
}

// NewPlaceholderHandler returns a stub Handler tagged with the
// jobType it stands in for. The jobType is reflected in the
// Dispatch error so operators can disambiguate
// "scheduled_agent wiring pending" from "nudge wiring pending"
// when reading `thrum cron history`.
func NewPlaceholderHandler(jobType string) *PlaceholderHandler {
	return &PlaceholderHandler{jobType: jobType}
}

// Dispatch satisfies scheduler.Handler. Returns
// ErrHandlerWiringPending wrapped with the job type so the error
// chain stays informative under errors.Is.
//
// The reporter sees no state transitions — the substrate's caller
// records the dispatch failure as StateFailed with the returned
// error verbatim, which is what we want operators to see.
func (h *PlaceholderHandler) Dispatch(_ context.Context, _ scheduler.JobSpec, _ string, _ scheduler.StateReporter, _ <-chan *scheduler.Completion) error {
	return fmt.Errorf("dispatch %q: %w", h.jobType, ErrHandlerWiringPending)
}

// Reconcile satisfies scheduler.Handler. A non-terminal row found
// at boot under a placeholder-handled type can't be resumed
// (there's no real handler to query). Mark it failed so the
// substrate doesn't leave it dangling across daemon restarts.
//
// Returning StateFailed is the same conservative default
// NudgeHandler.Reconcile uses for unrecoverable nudges.
func (h *PlaceholderHandler) Reconcile(_ context.Context, _ scheduler.JobSpec, _ string, _ scheduler.State) (scheduler.State, error) {
	return scheduler.StateFailed, fmt.Errorf("reconcile %q: %w", h.jobType, ErrHandlerWiringPending)
}

// Stages satisfies scheduler.Handler with a single placeholder
// stage so the A-B4 stalled-sweep has a finite dwell budget
// rather than a missing-entry panic. The 10s ceiling is
// generous given a Dispatch that returns immediately.
func (h *PlaceholderHandler) Stages() map[string]time.Duration {
	return map[string]time.Duration{
		"placeholder": 10 * time.Second,
	}
}

// Compile-time check that *PlaceholderHandler satisfies
// scheduler.Handler — catches signature drift if the interface
// changes.
var _ scheduler.Handler = (*PlaceholderHandler)(nil)
