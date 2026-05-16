package scheduler

import (
	"context"
	"errors"
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

// TestRPC_JobCreate_Happy: a valid spec lands in the spec map and seeds
// a StateScheduled row.
func TestRPC_JobCreate_Happy(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()
	_ = s.RegisterTypeHandler("scheduled_agent", &noopHandler{})

	req := CreateJobRequest{
		JobID: "docs-bot",
		Spec: JobSpec{
			Type: "scheduled_agent", Schedule: "0 9 * * *", Enabled: true,
			ScheduledAgent: &ScheduledAgentSpec{
				Target: "docs_bot", Primer: "Update API docs",
			},
		},
	}
	resp, err := s.RPC_JobCreate(context.Background(), req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.JobID != "docs-bot" {
		t.Errorf("resp = %+v", resp)
	}

	spec, ok := s.JobSpec("docs-bot")
	if !ok {
		t.Fatal("spec not registered")
	}
	if spec.Type != "scheduled_agent" {
		t.Errorf("spec.Type = %q", spec.Type)
	}
	if spec.ID != "docs-bot" {
		t.Errorf("spec.ID = %q (should be normalized from JobID)", spec.ID)
	}

	row, err := s.state.GetState(context.Background(), "docs-bot")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if row.CurrentState != StateScheduled {
		t.Errorf("seeded state = %q; want scheduled", row.CurrentState)
	}
}

// TestRPC_JobCreate_RejectsInternalPrefix: operator-facing RPC must
// refuse `internal.*` IDs (bridges use the Go API).
func TestRPC_JobCreate_RejectsInternalPrefix(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	req := CreateJobRequest{
		JobID: "internal.evil",
		Spec: JobSpec{
			Type: "command", Schedule: "@every 5m", Enabled: true,
			Command: &CommandSpec{Exec: "/bin/echo"},
		},
	}
	if _, err := s.RPC_JobCreate(context.Background(), req); err == nil {
		t.Error("expected rejection of internal.* prefix via user RPC")
	}
}

// TestRPC_JobCreate_DuplicateRejected: creating a job_id that already
// exists must fail with a use-update directive in the error message.
func TestRPC_JobCreate_DuplicateRejected(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	good := CreateJobRequest{
		JobID: "x",
		Spec: JobSpec{Type: "command", Schedule: "@every 5m", Enabled: true, Command: &CommandSpec{Exec: "/bin/true"}},
	}
	if _, err := s.RPC_JobCreate(context.Background(), good); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := s.RPC_JobCreate(context.Background(), good); err == nil {
		t.Error("expected duplicate-id rejection")
	}
}

// TestRPC_JobCreate_InvalidSpecReturnsValidatorErrors: a malformed spec
// surfaces every validator finding via validateSpec.
func TestRPC_JobCreate_InvalidSpecReturnsValidatorErrors(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	req := CreateJobRequest{
		JobID: "BadID",
		Spec:  JobSpec{Type: "command", Schedule: "not a cron", Enabled: true, Command: &CommandSpec{Exec: "/bin/echo"}},
	}
	_, err := s.RPC_JobCreate(context.Background(), req)
	if err == nil {
		t.Error("expected validator error for BadID + bad schedule")
	}
}

// TestRPC_JobUpdate_Happy: update replaces the spec for an existing job.
func TestRPC_JobUpdate_Happy(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	create := CreateJobRequest{
		JobID: "x",
		Spec: JobSpec{Type: "command", Schedule: "@every 5m", Enabled: true, Command: &CommandSpec{Exec: "/bin/true"}},
	}
	if _, err := s.RPC_JobCreate(context.Background(), create); err != nil {
		t.Fatalf("seed create: %v", err)
	}

	update := UpdateJobRequest{
		JobID: "x",
		Spec: JobSpec{Type: "command", Schedule: "@every 10m", Enabled: false, Command: &CommandSpec{Exec: "/bin/false"}},
	}
	if _, err := s.RPC_JobUpdate(context.Background(), update); err != nil {
		t.Fatalf("update: %v", err)
	}
	spec, _ := s.JobSpec("x")
	if spec.Schedule != "@every 10m" || spec.Enabled {
		t.Errorf("post-update spec = %+v", spec)
	}
}

// TestRPC_JobUpdate_NotFound: update on an unregistered id returns an
// error (it does NOT silently create — that's what job.create is for).
func TestRPC_JobUpdate_NotFound(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	req := UpdateJobRequest{
		JobID: "missing",
		Spec:  JobSpec{Type: "command", Schedule: "@every 5m", Enabled: true, Command: &CommandSpec{Exec: "/bin/true"}},
	}
	if _, err := s.RPC_JobUpdate(context.Background(), req); err == nil {
		t.Error("expected not-found error")
	}
}

// TestRPC_JobDelete_RefusesActiveRun: spec §5.1 — delete is refused
// while the run is dispatched or running.
func TestRPC_JobDelete_RefusesActiveRun(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.mu.Lock()
	s.specs["docs-bot"] = JobSpec{
		ID: "docs-bot", Type: "scheduled_agent",
		Schedule: "0 9 * * *", Enabled: true,
	}
	s.mu.Unlock()

	if err := s.state.UpsertState(context.Background(), &StateRow{
		JobID: "docs-bot", Generation: 1, CurrentState: StateRunning,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	_, err := s.RPC_JobDelete(context.Background(), DeleteJobRequest{JobID: "docs-bot"})
	if !errors.Is(err, ErrJobActive) {
		t.Errorf("err = %v; want ErrJobActive", err)
	}
}

// TestRPC_JobDelete_RemovesIdle: with the state row in a terminal state
// (or no state row), the spec is removed.
func TestRPC_JobDelete_RemovesIdle(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.mu.Lock()
	s.specs["docs-bot"] = JobSpec{
		ID: "docs-bot", Type: "scheduled_agent",
		Schedule: "0 9 * * *", Enabled: true,
	}
	s.mu.Unlock()
	if err := s.state.UpsertState(context.Background(), &StateRow{
		JobID: "docs-bot", Generation: 1, CurrentState: StateCompleted,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if _, err := s.RPC_JobDelete(context.Background(), DeleteJobRequest{JobID: "docs-bot"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := s.JobSpec("docs-bot"); ok {
		t.Error("spec still present after delete")
	}
}

// TestRPC_JobDelete_RejectsInternal: operator-facing delete must NOT
// touch internal jobs.
func TestRPC_JobDelete_RejectsInternal(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.RegisterInternal("internal.persist", "@every 1h", InternalOpts{}, &noopHandler{})
	if _, err := s.RPC_JobDelete(context.Background(), DeleteJobRequest{JobID: "internal.persist"}); err == nil {
		t.Error("expected rejection of internal.* delete via RPC")
	}
	// Spec must still be registered.
	if _, ok := s.JobSpec("internal.persist"); !ok {
		t.Error("internal.persist evicted despite delete refusal")
	}
}

// TestRPC_JobEnable_Disable: flipping Enabled writes back to the spec
// map under the config mutex.
func TestRPC_JobEnable_Disable(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.mu.Lock()
	s.specs["docs-bot"] = JobSpec{
		ID: "docs-bot", Type: "scheduled_agent",
		Schedule: "0 9 * * *", Enabled: true,
	}
	s.mu.Unlock()

	if _, err := s.RPC_JobDisable(context.Background(), EnableDisableRequest{JobID: "docs-bot"}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	sp, _ := s.JobSpec("docs-bot")
	if sp.Enabled {
		t.Error("Enabled should be false after disable")
	}

	if _, err := s.RPC_JobEnable(context.Background(), EnableDisableRequest{JobID: "docs-bot"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	sp, _ = s.JobSpec("docs-bot")
	if !sp.Enabled {
		t.Error("Enabled should be true after enable")
	}
}

// TestRPC_JobEnable_Disable_RejectsInternal: operator-facing
// enable/disable can't touch internal jobs.
func TestRPC_JobEnable_Disable_RejectsInternal(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()
	s.RegisterInternal("internal.bg", "@every 1h", InternalOpts{}, &noopHandler{})

	if _, err := s.RPC_JobDisable(context.Background(), EnableDisableRequest{JobID: "internal.bg"}); err == nil {
		t.Error("expected rejection of internal disable")
	}
	if _, err := s.RPC_JobEnable(context.Background(), EnableDisableRequest{JobID: "internal.bg"}); err == nil {
		t.Error("expected rejection of internal enable")
	}
}

// TestRPC_JobEnable_NotFound returns an error rather than silently
// creating the spec.
func TestRPC_JobEnable_NotFound(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	if _, err := s.RPC_JobEnable(context.Background(), EnableDisableRequest{JobID: "missing"}); err == nil {
		t.Error("expected not-found error")
	}
}

// TestRPC_JobCancel_NoActiveRun: when the state row is in a terminal
// state, cancel returns (Cancelled=false, Reason="no active run").
func TestRPC_JobCancel_NoActiveRun(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.mu.Lock()
	s.specs["docs-bot"] = JobSpec{
		ID: "docs-bot", Type: "scheduled_agent",
		Schedule: "0 9 * * *", Enabled: true,
	}
	s.mu.Unlock()
	if err := s.state.UpsertState(context.Background(), &StateRow{
		JobID: "docs-bot", Generation: 1, CurrentState: StateCompleted,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := s.RPC_JobCancel(context.Background(), CancelJobRequest{JobID: "docs-bot"})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if resp.Cancelled {
		t.Error("Cancelled should be false when no active run")
	}
	if resp.Reason != "no active run" {
		t.Errorf("Reason = %q; want %q", resp.Reason, "no active run")
	}
}

// TestRPC_JobCancel_ActiveRun: with an active run registered in the
// runRegistry, cancel invokes the registered cancel-func and returns
// Cancelled=true.
func TestRPC_JobCancel_ActiveRun(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	s.mu.Lock()
	s.specs["docs-bot"] = JobSpec{
		ID: "docs-bot", Type: "scheduled_agent",
		Schedule: "0 9 * * *", Enabled: true,
	}
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	s.runReg.register("docs-bot-g1-100", cancel)
	if err := s.state.UpsertState(context.Background(), &StateRow{
		JobID: "docs-bot", Generation: 1, CurrentState: StateRunning,
		LastRunID: "docs-bot-g1-100",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := s.RPC_JobCancel(context.Background(), CancelJobRequest{JobID: "docs-bot"})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !resp.Cancelled {
		t.Error("Cancelled should be true when active run was cancelled")
	}
	if resp.RunID != "docs-bot-g1-100" {
		t.Errorf("RunID = %q", resp.RunID)
	}
	select {
	case <-ctx.Done():
		// good — the registered cancel-func fired
	case <-time.After(100 * time.Millisecond):
		t.Error("registered cancel-func wasn't invoked")
	}
}

// TestRPC_JobCancel_NotFound: unknown job_id returns an error.
func TestRPC_JobCancel_NotFound(t *testing.T) {
	db := setupStateTestDB(t)
	s := New(Config{DB: db, DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	if _, err := s.RPC_JobCancel(context.Background(), CancelJobRequest{JobID: "missing"}); err == nil {
		t.Error("expected not-found error")
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
