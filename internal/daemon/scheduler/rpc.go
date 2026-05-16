package scheduler

import (
	"context"
	"errors"
	"fmt"
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
