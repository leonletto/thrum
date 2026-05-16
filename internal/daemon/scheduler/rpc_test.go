package scheduler

import (
	"context"
	"testing"
	"time"
)

// TestRPC_JobList_ReturnsRegisteredJobs: both internal-registered and
// spec-loaded user jobs appear in the listing.
func TestRPC_JobList_ReturnsRegisteredJobs(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.RegisterInternal("internal.backup", "@every 1h", InternalOpts{}, &noopHandler{})
	s.mu.Lock()
	s.specs["docs-bot"] = JobSpec{
		ID: "docs-bot", Type: "scheduled_agent",
		Schedule: "0 9 * * *", Enabled: true,
	}
	s.mu.Unlock()

	resp, err := s.RPC_JobList(context.Background(), ListJobsRequest{})
	if err != nil {
		t.Fatalf("job.list: %v", err)
	}
	if len(resp.Jobs) != 2 {
		t.Errorf("got %d jobs; want 2", len(resp.Jobs))
	}
	for _, j := range resp.Jobs {
		if j.ID == "" || j.Type == "" || j.Schedule == "" {
			t.Errorf("incomplete listing entry: %+v", j)
		}
	}
}

// TestRPC_JobList_FilterByType: req.Type filters to matching specs only.
func TestRPC_JobList_FilterByType(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.RegisterInternal("internal.backup", "@every 1h", InternalOpts{}, &noopHandler{})
	s.mu.Lock()
	s.specs["docs-bot"] = JobSpec{
		ID: "docs-bot", Type: "scheduled_agent",
		Schedule: "0 9 * * *", Enabled: true,
	}
	s.mu.Unlock()

	resp, err := s.RPC_JobList(context.Background(), ListJobsRequest{Type: "scheduled_agent"})
	if err != nil {
		t.Fatalf("job.list: %v", err)
	}
	if len(resp.Jobs) != 1 || resp.Jobs[0].ID != "docs-bot" {
		t.Errorf("filter by type=scheduled_agent: got %+v", resp.Jobs)
	}
}

// TestRPC_JobList_FilterByEnabled: req.Enabled (*bool) filters by the
// enabled flag; nil ignores the filter.
func TestRPC_JobList_FilterByEnabled(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.mu.Lock()
	s.specs["on"] = JobSpec{ID: "on", Type: "command", Schedule: "@every 1h", Enabled: true}
	s.specs["off"] = JobSpec{ID: "off", Type: "command", Schedule: "@every 1h", Enabled: false}
	s.mu.Unlock()

	yes := true
	resp, _ := s.RPC_JobList(context.Background(), ListJobsRequest{Enabled: &yes})
	if len(resp.Jobs) != 1 || resp.Jobs[0].ID != "on" {
		t.Errorf("filter enabled=true: got %+v", resp.Jobs)
	}

	no := false
	resp, _ = s.RPC_JobList(context.Background(), ListJobsRequest{Enabled: &no})
	if len(resp.Jobs) != 1 || resp.Jobs[0].ID != "off" {
		t.Errorf("filter enabled=false: got %+v", resp.Jobs)
	}
}

// TestRPC_JobList_StateRowFieldsIncluded: when a state row exists, the
// listing entry includes current_stage + stage_entered_at +
// last_completed_at + next_scheduled_at per MINOR-21 fix.
func TestRPC_JobList_StateRowFieldsIncluded(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.mu.Lock()
	s.specs["docs-bot"] = JobSpec{
		ID: "docs-bot", Type: "scheduled_agent",
		Schedule: "0 9 * * *", Enabled: true,
	}
	s.mu.Unlock()
	now := time.Now()
	stageEntered := now.Add(-time.Minute)
	next := now.Add(time.Hour)
	if err := s.state.UpsertState(context.Background(), &StateRow{
		JobID: "docs-bot", Generation: 1, CurrentState: StateRunning,
		CurrentStage: "executing", StageEnteredAt: &stageEntered,
		NextScheduledAt: &next, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	resp, _ := s.RPC_JobList(context.Background(), ListJobsRequest{})
	if len(resp.Jobs) != 1 {
		t.Fatalf("got %d jobs", len(resp.Jobs))
	}
	j := resp.Jobs[0]
	if j.CurrentState != StateRunning {
		t.Errorf("CurrentState = %q", j.CurrentState)
	}
	if j.CurrentStage != "executing" {
		t.Errorf("CurrentStage = %q", j.CurrentStage)
	}
	// SQLite stores INTEGER unix-seconds; nanosecond precision is dropped
	// on round-trip. Compare at second granularity.
	if j.StageEnteredAt == nil || j.StageEnteredAt.Unix() != stageEntered.Unix() {
		t.Errorf("StageEnteredAt = %v; want unix=%d", j.StageEnteredAt, stageEntered.Unix())
	}
}

// TestRPC_JobShow_ReturnsSpecPlusStatePlusEvents: full job.show
// response carries the spec, current state row, and recent events.
func TestRPC_JobShow_ReturnsSpecPlusStatePlusEvents(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.mu.Lock()
	s.specs["docs-bot"] = JobSpec{
		ID: "docs-bot", Type: "scheduled_agent",
		Schedule: "0 9 * * *", Enabled: true,
	}
	s.mu.Unlock()
	now := time.Now()
	next := now.Add(time.Hour)
	if err := s.state.UpsertState(context.Background(), &StateRow{
		JobID: "docs-bot", Generation: 1, CurrentState: StateScheduled,
		NextScheduledAt: &next, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	if err := s.state.AppendEvent(context.Background(), &Event{
		JobID: "docs-bot", RunID: "docs-bot-g1-100",
		EventTime: now, FromState: "", ToState: StateScheduled,
		Reason: "registered",
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	show, err := s.RPC_JobShow(context.Background(), ShowJobRequest{JobID: "docs-bot"})
	if err != nil {
		t.Fatalf("job.show: %v", err)
	}
	if show.Spec.ID != "docs-bot" {
		t.Errorf("spec.ID = %q", show.Spec.ID)
	}
	if show.State == nil {
		t.Fatal("State is nil; want StateScheduled row")
	}
	if show.State.CurrentState != StateScheduled {
		t.Errorf("State.CurrentState = %q", show.State.CurrentState)
	}
	if len(show.RecentEvents) == 0 {
		t.Error("expected recent events; got none")
	}
}

// TestRPC_JobShow_UnknownJob: unknown job_id returns an error.
func TestRPC_JobShow_UnknownJob(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	if _, err := s.RPC_JobShow(context.Background(), ShowJobRequest{JobID: "nonexistent"}); err == nil {
		t.Error("expected error for unknown job_id")
	}
}

// TestRPC_JobShow_RegisteredButNoState: a freshly-registered job whose
// reactor hasn't fired yet has no state row. show returns spec + nil
// State + empty RecentEvents.
func TestRPC_JobShow_RegisteredButNoState(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.RegisterInternal("internal.fresh", "@every 1h", InternalOpts{}, &noopHandler{})
	show, err := s.RPC_JobShow(context.Background(), ShowJobRequest{JobID: "internal.fresh"})
	if err != nil {
		t.Fatalf("job.show: %v", err)
	}
	if show.Spec.ID != "internal.fresh" {
		t.Errorf("spec.ID = %q", show.Spec.ID)
	}
	if show.State != nil {
		t.Errorf("State = %+v; want nil (no row yet)", show.State)
	}
	if len(show.RecentEvents) != 0 {
		t.Errorf("RecentEvents len = %d; want 0", len(show.RecentEvents))
	}
}
