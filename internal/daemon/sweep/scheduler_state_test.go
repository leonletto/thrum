package sweep

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/daemon/scheduler"
	"github.com/leonletto/thrum/internal/schema"
)

// stubJobSpec implements JobSpecAccessor with a static map. nil entries
// model "job not registered" (mid-flight de-registration).
type stubJobSpec map[string]*scheduler.JobSpec

func (s stubJobSpec) JobSpec(id string) (scheduler.JobSpec, bool) {
	spec, ok := s[id]
	if !ok || spec == nil {
		return scheduler.JobSpec{}, false
	}
	return *spec, true
}

// stateRowForTest seeds scheduler_job_state with the minimum columns
// AgentsInBB1ManagedStages cares about. Uses raw SQL so we don't pull
// in scheduler.StateStore — the adapter under test should work over
// the raw table shape per canonical §3.2.
func stateRowForTest(t *testing.T, db *safedb.DB, jobID, stage string) {
	t.Helper()
	now := time.Now().UTC().Unix()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO scheduler_job_state (
			job_id, job_generation, current_state, current_stage,
			consecutive_failures, escalation_sent, total_runs,
			created_at, updated_at
		) VALUES (?, 1, 'running', ?, 0, 0, 1, ?, ?)
	`, jobID, stage, now, now)
	if err != nil {
		t.Fatalf("insert state row for %s: %v", jobID, err)
	}
}

// schedAgentSpec is a tiny constructor for *scheduler.JobSpec with a
// ScheduledAgent.Target set.
func schedAgentSpec(target string) *scheduler.JobSpec {
	return &scheduler.JobSpec{
		Type:           "scheduled_agent",
		ScheduledAgent: &scheduler.ScheduledAgentSpec{Target: target},
	}
}

// nonAgentSpec is a JobSpec with no ScheduledAgent (e.g. command type).
func nonAgentSpec(jobType string) *scheduler.JobSpec {
	return &scheduler.JobSpec{Type: jobType}
}

func TestAgentsInBB1ManagedStages_JoinsViaJobSpec(t *testing.T) {
	dbTest := schedulerStateTestDB(t)

	stateRowForTest(t, dbTest, "wake_docs_bot", "running_work")
	stateRowForTest(t, dbTest, "wake_release_dash", "idle_nudge_2of5")
	stateRowForTest(t, dbTest, "wake_billing_bot", "awaiting_first_output") // NOT managed
	stateRowForTest(t, dbTest, "internal_backup", "running_work")           // NOT scheduled_agent

	jobs := stubJobSpec{
		"wake_docs_bot":     schedAgentSpec("docs_bot"),
		"wake_release_dash": schedAgentSpec("release_dashboard"),
		"wake_billing_bot":  schedAgentSpec("billing_bot"),
		"internal_backup":   nonAgentSpec("internal"),
	}

	adapter := NewSchedulerState(dbTest, jobs)
	got, err := adapter.AgentsInBB1ManagedStages(context.Background())
	if err != nil {
		t.Fatalf("AgentsInBB1ManagedStages: %v", err)
	}
	want := map[string]bool{"docs_bot": true, "release_dashboard": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestAgentsInBB1ManagedStages_SkipsDeregisteredJobs(t *testing.T) {
	dbTest := schedulerStateTestDB(t)
	stateRowForTest(t, dbTest, "orphan_job", "running_work")
	// JobSpec returns ok=false (job removed from config mid-flight).
	jobs := stubJobSpec{}

	adapter := NewSchedulerState(dbTest, jobs)
	got, err := adapter.AgentsInBB1ManagedStages(context.Background())
	if err != nil {
		t.Fatalf("AgentsInBB1ManagedStages: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("deregistered job should be silently skipped; got %v", got)
	}
}

func TestAgentsInBB1ManagedStages_AllIdleNudgeVariants(t *testing.T) {
	dbTest := schedulerStateTestDB(t)
	// Max idle nudges is 5 per canonical §4.2.4 — sweep skip-set
	// must cover all 5.
	for i := 1; i <= 5; i++ {
		stage := "idle_nudge_" + itoa(i) + "of5"
		jobID := "wake_agent" + itoa(i)
		stateRowForTest(t, dbTest, jobID, stage)
	}
	jobs := stubJobSpec{
		"wake_agent1": schedAgentSpec("a1"),
		"wake_agent2": schedAgentSpec("a2"),
		"wake_agent3": schedAgentSpec("a3"),
		"wake_agent4": schedAgentSpec("a4"),
		"wake_agent5": schedAgentSpec("a5"),
	}
	adapter := NewSchedulerState(dbTest, jobs)
	got, err := adapter.AgentsInBB1ManagedStages(context.Background())
	if err != nil {
		t.Fatalf("AgentsInBB1ManagedStages: %v", err)
	}
	for i := 1; i <= 5; i++ {
		if !got["a"+itoa(i)] {
			t.Errorf("a%d (idle_nudge_%dof5) missing from skip-set; got %v", i, i, got)
		}
	}
}

func TestAgentsInBB1ManagedStages_EmptyState_ReturnsEmptyMap(t *testing.T) {
	dbTest := schedulerStateTestDB(t)
	adapter := NewSchedulerState(dbTest, stubJobSpec{})
	got, err := adapter.AgentsInBB1ManagedStages(context.Background())
	if err != nil {
		t.Fatalf("AgentsInBB1ManagedStages: %v", err)
	}
	if got == nil {
		t.Error("empty state should return non-nil empty map (callers do `if skip[agent]` and rely on nil-vs-empty parity)")
	}
	if len(got) != 0 {
		t.Errorf("empty state should return empty map; got %v", got)
	}
}

func TestAgentsInBB1ManagedStages_NilScheduledAgent_Skipped(t *testing.T) {
	dbTest := schedulerStateTestDB(t)
	stateRowForTest(t, dbTest, "weird_job", "running_work")
	// JobSpec exists but ScheduledAgent is nil (e.g. type:command).
	jobs := stubJobSpec{
		"weird_job": {Type: "command"}, // ScheduledAgent left nil
	}
	adapter := NewSchedulerState(dbTest, jobs)
	got, err := adapter.AgentsInBB1ManagedStages(context.Background())
	if err != nil {
		t.Fatalf("AgentsInBB1ManagedStages: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("job with nil ScheduledAgent should be skipped; got %v", got)
	}
}

// schedulerStateTestDB sets up a fresh on-disk SQLite with the schema
// migrated and wraps in safedb. Mirrors scheduler/state_test.go's
// setupStateTestDB (in-memory doesn't survive multi-connection access).
func schedulerStateTestDB(t *testing.T) *safedb.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "sweep_state_test.db")
	raw, err := schema.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if err := schema.InitDB(raw); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return safedb.New(raw)
}

// itoa is a tiny strconv.Itoa replacement so the test file doesn't
// pull in another import.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
