package scheduler

import (
	"context"
	"sync"
)

// runRegistry holds per-run cancel-func + signal-channel registries.
//
// Both registries are keyed by run_id; entries are added at dispatch time
// (Task 13's launchRun) and removed by the caller on terminal-state
// transition. The cancel registry backs job.cancel; the signal registry
// backs job.done (canonical-ref §6.1 Alt-A).
//
// Signal channels are buffered cap 1 — there is at most one Completion per
// run; a second deliverCompletion returns ErrCompletionAlreadyDelivered
// rather than dropping or blocking.
type runRegistry struct {
	mu      sync.RWMutex
	signals map[string]chan *Completion
	cancels map[string]context.CancelFunc
}

func newRunRegistry() *runRegistry {
	return &runRegistry{
		signals: map[string]chan *Completion{},
		cancels: map[string]context.CancelFunc{},
	}
}

// register installs a run's cancel func and returns the receive-end of its
// signal channel for the handler to select on.
func (r *runRegistry) register(runID string, cancel context.CancelFunc) chan *Completion {
	r.mu.Lock()
	defer r.mu.Unlock()
	sig := make(chan *Completion, 1)
	r.signals[runID] = sig
	r.cancels[runID] = cancel
	return sig
}

// signal returns the signal channel for a run, or (nil, false) if absent.
func (r *runRegistry) signal(runID string) (chan *Completion, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sig, ok := r.signals[runID]
	return sig, ok
}

// cancel invokes the registered cancel func; returns true iff one existed.
// The cancel func itself is invoked without holding r.mu — long-running
// handler cancellation paths could otherwise deadlock against deregister.
func (r *runRegistry) cancel(runID string) bool {
	r.mu.RLock()
	cancel, ok := r.cancels[runID]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// deliverCompletion sends a Completion to the run's signal channel.
// Returns ErrUnknownRun when no run is registered, or
// ErrCompletionAlreadyDelivered if the buffered channel is full (a prior
// Completion is pending receive).
func (r *runRegistry) deliverCompletion(runID string, c *Completion) error {
	r.mu.RLock()
	sig, ok := r.signals[runID]
	r.mu.RUnlock()
	if !ok {
		return ErrUnknownRun
	}
	select {
	case sig <- c:
		return nil
	default:
		return ErrCompletionAlreadyDelivered
	}
}

// deregister removes a run from both registries. Callers invoke at
// terminal-state transition.
func (r *runRegistry) deregister(runID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.signals, runID)
	delete(r.cancels, runID)
}
