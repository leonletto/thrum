package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ListJobsRequest filters job.list output. Optional fields (zero value =
// no filter) keep the call site forwards-compatible — B-B2's `thrum cron
// list` adds filters by passing more fields without breaking older
// daemons.
type ListJobsRequest struct {
	Type    string `json:"type,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
}

// ListJobsResponse carries one row per registered job.
type ListJobsResponse struct {
	Jobs []JobListing `json:"jobs"`
}

// JobListing is one row of the job.list response (spec §5.1). Includes
// current_stage + stage_entered_at so B-B2's `thrum cron list` can
// render stuck-detection state without a second job.show roundtrip
// (MINOR-21 fix from the plan review).
type JobListing struct {
	ID              string     `json:"id"`
	Type            string     `json:"type"`
	Enabled         bool       `json:"enabled"`
	Schedule        string     `json:"schedule"`
	CurrentState    State      `json:"current_state,omitempty"`
	CurrentStage    string     `json:"current_stage,omitempty"`
	StageEnteredAt  *time.Time `json:"stage_entered_at,omitempty"`
	LastCompletedAt *time.Time `json:"last_completed_at,omitempty"`
	NextScheduledAt *time.Time `json:"next_scheduled_at,omitempty"`
}

// RPC_JobList implements the job.list JSON-RPC method.
//
// The wire-level registration via server.RegisterHandler (per spec §3.2)
// is a daemon-side wiring step that calls this method from a per-method
// closure adapter, mirroring the existing `message.send` pattern at
// cmd/thrum/main.go. Substrate ships the typed method; daemon ships the
// wire registration.
func (s *Scheduler) RPC_JobList(ctx context.Context, req ListJobsRequest) (ListJobsResponse, error) {
	s.mu.RLock()
	specs := make([]JobSpec, 0, len(s.specs))
	for _, sp := range s.specs {
		if req.Type != "" && sp.Type != req.Type {
			continue
		}
		if req.Enabled != nil && sp.Enabled != *req.Enabled {
			continue
		}
		specs = append(specs, sp)
	}
	s.mu.RUnlock()

	resp := ListJobsResponse{Jobs: make([]JobListing, 0, len(specs))}
	for _, sp := range specs {
		l := JobListing{
			ID: sp.ID, Type: sp.Type, Enabled: sp.Enabled, Schedule: sp.Schedule,
		}
		if row, err := s.state.GetState(ctx, sp.ID); err == nil {
			l.CurrentState = row.CurrentState
			l.CurrentStage = row.CurrentStage
			l.StageEnteredAt = row.StageEnteredAt
			l.LastCompletedAt = row.LastCompletedAt
			l.NextScheduledAt = row.NextScheduledAt
		}
		resp.Jobs = append(resp.Jobs, l)
	}
	return resp, nil
}

// ShowJobRequest carries the lookup key for job.show.
type ShowJobRequest struct {
	JobID string `json:"job_id"`
}

// ShowJobResponse carries the spec, current state row, and the most
// recent events (50) for a single job.
type ShowJobResponse struct {
	Spec         JobSpec   `json:"spec"`
	State        *StateRow `json:"state,omitempty"`
	RecentEvents []Event   `json:"recent_events"`
}

// RPC_JobShow implements the job.show JSON-RPC method.
//
// Returns an error if the job_id isn't registered. Returns no state row
// (State == nil) for a registered job that hasn't fired yet — callers
// should null-check.
func (s *Scheduler) RPC_JobShow(ctx context.Context, req ShowJobRequest) (ShowJobResponse, error) {
	spec, ok := s.JobSpec(req.JobID)
	if !ok {
		return ShowJobResponse{}, fmt.Errorf("job %q: not found", req.JobID)
	}
	state, err := s.state.GetState(ctx, req.JobID)
	if err != nil && !errors.Is(err, ErrJobNotFound) {
		return ShowJobResponse{}, fmt.Errorf("job %q load state: %w", req.JobID, err)
	}
	events, err := s.state.RecentEvents(ctx, req.JobID, 50)
	if err != nil {
		return ShowJobResponse{}, fmt.Errorf("job %q load events: %w", req.JobID, err)
	}
	return ShowJobResponse{Spec: spec, State: state, RecentEvents: events}, nil
}

// CreateJobRequest is the input to job.create. Spec.ID is normalized to
// JobID so the request key is the single source of truth.
type CreateJobRequest struct {
	JobID string  `json:"job_id"`
	Spec  JobSpec `json:"spec"`
}

// CreateJobResponse echoes the created job_id.
type CreateJobResponse struct {
	JobID string `json:"job_id"`
}

// RPC_JobCreate implements the job.create JSON-RPC method. Validates via
// the whole-config validator (E1.5 Task 30) against a single-job map so
// every operator-facing create surfaces the same diagnostics that
// ReloadConfig would produce for the equivalent disk config. Rejects
// internal.* IDs at this surface — bridges register internal jobs via
// the Go API (RegisterInternal), not RPC.
func (s *Scheduler) RPC_JobCreate(ctx context.Context, req CreateJobRequest) (CreateJobResponse, error) {
	if strings.HasPrefix(req.JobID, InternalPrefix) {
		return CreateJobResponse{}, fmt.Errorf("job.create: id %q has reserved %q prefix", req.JobID, InternalPrefix)
	}
	spec := req.Spec
	spec.ID = req.JobID
	if err := s.validateSpec(spec); err != nil {
		return CreateJobResponse{}, fmt.Errorf("job.create: %w", err)
	}

	s.mu.Lock()
	if _, exists := s.specs[req.JobID]; exists {
		s.mu.Unlock()
		return CreateJobResponse{}, fmt.Errorf("job.create: id %q already exists; use job.update", req.JobID)
	}
	s.specs[req.JobID] = spec
	s.mu.Unlock()

	now := time.Now()
	_ = s.state.UpsertState(ctx, &StateRow{
		JobID: spec.ID, Generation: 1, CurrentState: StateScheduled,
		CreatedAt: now, UpdatedAt: now,
	})
	// Persistence to disk via AtomicWriteConfig is a daemon-side wiring
	// step — the substrate exposes the in-memory mutation; the daemon
	// chooses when to flush the updated jobs map back to .thrum/config.json.
	s.wakeReactor()
	return CreateJobResponse{JobID: req.JobID}, nil
}

// UpdateJobRequest is the input to job.update.
type UpdateJobRequest struct {
	JobID string  `json:"job_id"`
	Spec  JobSpec `json:"spec"`
}

// UpdateJobResponse echoes the updated job_id.
type UpdateJobResponse struct {
	JobID string `json:"job_id"`
}

// RPC_JobUpdate replaces an existing spec. Validates via the same
// whole-config validator as create. internal.* IDs rejected.
func (s *Scheduler) RPC_JobUpdate(_ context.Context, req UpdateJobRequest) (UpdateJobResponse, error) {
	if strings.HasPrefix(req.JobID, InternalPrefix) {
		return UpdateJobResponse{}, fmt.Errorf("job.update: cannot mutate internal job %q", req.JobID)
	}
	spec := req.Spec
	spec.ID = req.JobID
	if err := s.validateSpec(spec); err != nil {
		return UpdateJobResponse{}, fmt.Errorf("job.update: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.specs[req.JobID]; !exists {
		return UpdateJobResponse{}, fmt.Errorf("job.update: id %q not found", req.JobID)
	}
	s.specs[req.JobID] = spec
	s.wakeReactor()
	return UpdateJobResponse{JobID: req.JobID}, nil
}

// DeleteJobRequest carries the lookup key for job.delete.
type DeleteJobRequest struct {
	JobID string `json:"job_id"`
}

// DeleteJobResponse echoes the deleted job_id.
type DeleteJobResponse struct {
	JobID string `json:"job_id"`
}

// RPC_JobDelete removes a user job. Refuses with ErrJobActive when the
// state row is in StateDispatched or StateRunning (per spec §5.1: a
// running job must be cancelled first). internal.* IDs rejected.
func (s *Scheduler) RPC_JobDelete(ctx context.Context, req DeleteJobRequest) (DeleteJobResponse, error) {
	if strings.HasPrefix(req.JobID, InternalPrefix) {
		return DeleteJobResponse{}, fmt.Errorf("job.delete: cannot delete internal job %q", req.JobID)
	}
	if row, err := s.state.GetState(ctx, req.JobID); err == nil {
		switch row.CurrentState {
		case StateDispatched, StateRunning:
			return DeleteJobResponse{}, ErrJobActive
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.specs[req.JobID]; !exists {
		return DeleteJobResponse{}, fmt.Errorf("job.delete: id %q not found", req.JobID)
	}
	delete(s.specs, req.JobID)
	delete(s.handlers, req.JobID) // no-op for user jobs; defensive
	s.wakeReactor()
	return DeleteJobResponse(req), nil
}

// validateSpec runs the whole-config validator (Task 30) against a
// single-job map. Used by every mutate RPC so operator-facing creates /
// updates surface the same diagnostics as a config-file reload.
//
// Returns a multi-line error when there are multiple findings, so JSON-RPC
// clients see every problem in one round-trip.
func (s *Scheduler) validateSpec(spec JobSpec) error {
	errs := s.ValidateWholeConfig(map[string]JobSpec{spec.ID: spec})
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(errs)+1)
	msgs = append(msgs, fmt.Sprintf("%d validation error(s):", len(errs)))
	for _, e := range errs {
		msgs = append(msgs, "  - "+e.Error())
	}
	return errors.New(strings.Join(msgs, "\n"))
}

// EnableDisableRequest carries the lookup key for job.enable / job.disable.
type EnableDisableRequest struct {
	JobID string `json:"job_id"`
}

// EnableDisableResponse echoes the new enabled flag.
type EnableDisableResponse struct {
	JobID   string `json:"job_id"`
	Enabled bool   `json:"enabled"`
}

// RPC_JobEnable flips the Enabled flag on. Internal jobs are rejected
// (their lifecycle is daemon-controlled).
func (s *Scheduler) RPC_JobEnable(_ context.Context, req EnableDisableRequest) (EnableDisableResponse, error) {
	return s.setEnabled(req.JobID, true)
}

// RPC_JobDisable flips the Enabled flag off. Future fires are suppressed;
// to stop a currently-running fire, the operator pairs with job.cancel.
func (s *Scheduler) RPC_JobDisable(_ context.Context, req EnableDisableRequest) (EnableDisableResponse, error) {
	return s.setEnabled(req.JobID, false)
}

// setEnabled is the shared body for the enable/disable RPCs.
func (s *Scheduler) setEnabled(jobID string, on bool) (EnableDisableResponse, error) {
	if strings.HasPrefix(jobID, InternalPrefix) {
		return EnableDisableResponse{}, fmt.Errorf("cannot enable/disable internal job %q", jobID)
	}
	s.mu.Lock()
	spec, exists := s.specs[jobID]
	if !exists {
		s.mu.Unlock()
		return EnableDisableResponse{}, fmt.Errorf("id %q not found", jobID)
	}
	spec.Enabled = on
	s.specs[jobID] = spec
	s.mu.Unlock()
	s.wakeReactor()
	return EnableDisableResponse{JobID: jobID, Enabled: on}, nil
}

// CancelJobRequest carries the lookup key for job.cancel.
type CancelJobRequest struct {
	JobID string `json:"job_id"`
}

// CancelJobResponse reports whether a cancel actually fired. Cancelled
// is false when no active run is present; Reason explains why.
type CancelJobResponse struct {
	JobID     string `json:"job_id"`
	Cancelled bool   `json:"cancelled"`
	RunID     string `json:"run_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// RPC_JobCancel invokes the registered cancel-func for the active run.
// Per Q8.2 + MINOR-19: when no active run exists, returns
// (Cancelled=false, Reason="no active run") rather than an error — the
// operator-facing surface treats cancel-of-idle as a no-op.
//
// Pair with job.disable to stop both future fires AND the current run.
func (s *Scheduler) RPC_JobCancel(ctx context.Context, req CancelJobRequest) (CancelJobResponse, error) {
	if _, ok := s.JobSpec(req.JobID); !ok {
		return CancelJobResponse{}, fmt.Errorf("id %q not found", req.JobID)
	}
	row, err := s.state.GetState(ctx, req.JobID)
	if errors.Is(err, ErrJobNotFound) {
		return CancelJobResponse{JobID: req.JobID, Reason: "no active run"}, nil
	}
	if err != nil {
		return CancelJobResponse{}, err
	}

	switch row.CurrentState {
	case StateDispatched, StateRunning:
		ok := s.runReg.cancel(row.LastRunID)
		return CancelJobResponse{
			JobID: req.JobID, Cancelled: ok, RunID: row.LastRunID,
		}, nil
	default:
		return CancelJobResponse{
			JobID: req.JobID, Reason: "no active run",
		}, nil
	}
}
