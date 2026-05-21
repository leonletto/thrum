package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noopSender is a MessageSender that discards all messages.
type noopSender struct{}

func (n *noopSender) HandleSend(_ context.Context, _ json.RawMessage) (any, error) {
	return nil, nil
}

func newTestSupervisor(t *testing.T) (*MonitorSupervisor, *MonitorStore) {
	t.Helper()
	store, _ := newTestStore(t)
	delivery := NewDelivery(&noopSender{})
	sup := NewMonitorSupervisor(store, delivery)
	return sup, store
}

// makeSpec returns a valid SubmitSpec for a short-lived child command.
func makeSpec(name string) SubmitSpec {
	return SubmitSpec{
		Name:            name,
		Argv:            []string{"sh", "-c", "while true; do echo hi; sleep 0.05; done"},
		MatchPattern:    "hi",
		Target:          "@test",
		Cwd:             "/tmp",
		Env:             map[string]string{},
		DebounceSeconds: 30,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestSupervisor_NewIsEmpty: NewMonitorSupervisor starts with zero runners.
// ─────────────────────────────────────────────────────────────────────────────

func TestSupervisor_NewIsEmpty(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	sup.mu.Lock()
	count := len(sup.runners)
	sup.mu.Unlock()
	assert.Equal(t, 0, count)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestSupervisor_StartAddsJob: Add while supervisor is running registers the
// runner in the map and persists the job in the DB.
// ─────────────────────────────────────────────────────────────────────────────

func TestSupervisor_StartAddsJob(t *testing.T) {
	sup, store := newTestSupervisor(t)

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	go func() {
		close(started)
		sup.Start(ctx)
	}()
	<-started
	// Small delay to ensure Start's reload path has completed.
	time.Sleep(20 * time.Millisecond)

	id, err := sup.Add(ctx, makeSpec("add-test"))
	require.NoError(t, err)
	require.NotEmpty(t, id)

	// Runner is in the map.
	sup.mu.Lock()
	_, inMap := sup.runners[id]
	sup.mu.Unlock()
	assert.True(t, inMap, "runner must be in the active map after Add")

	// Row is in the DB with status=running.
	job, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, job.Status)

	cancel()
	// Give the supervisor time to shut down.
	time.Sleep(200 * time.Millisecond)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestSupervisor_ReloadFromDBOnStart: pre-populate the DB with two running
// monitors; Start should relaunch both.
// ─────────────────────────────────────────────────────────────────────────────

func TestSupervisor_ReloadFromDBOnStart(t *testing.T) {
	sup, store := newTestSupervisor(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Insert two "running" jobs directly into the DB (bypassing Add validation).
	job1 := makeMonitorJobFromSpec("mon_RELOAD1", "reload-a", makeSpec("reload-a"))
	job2 := makeMonitorJobFromSpec("mon_RELOAD2", "reload-b", makeSpec("reload-b"))
	require.NoError(t, store.Insert(ctx, job1))
	require.NoError(t, store.Insert(ctx, job2))

	done := make(chan struct{})
	go func() {
		sup.Start(ctx)
		close(done)
	}()

	// Give Start enough time to complete its reload loop.
	deadline := time.After(2 * time.Second)
	for {
		sup.mu.Lock()
		cnt := len(sup.runners)
		sup.mu.Unlock()
		if cnt >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for 2 runners to be launched from DB reload")
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	sup.mu.Lock()
	_, has1 := sup.runners["mon_RELOAD1"]
	_, has2 := sup.runners["mon_RELOAD2"]
	sup.mu.Unlock()
	assert.True(t, has1, "reload-a must be running")
	assert.True(t, has2, "reload-b must be running")

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after ctx cancellation")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestSupervisor_StopMarksStoppedAndRemovesRunner: stop a running monitor and
// verify the runner is gone from the map and the DB row has status=stopped.
// ─────────────────────────────────────────────────────────────────────────────

func TestSupervisor_StopRemovesFromMapAndDB(t *testing.T) {
	sup, store := newTestSupervisor(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	go func() {
		close(started)
		sup.Start(ctx)
	}()
	<-started
	time.Sleep(20 * time.Millisecond)

	id, err := sup.Add(ctx, makeSpec("stop-test"))
	require.NoError(t, err)

	// Stop it.
	require.NoError(t, sup.Stop(ctx, id))

	// Runner is gone from the map.
	sup.mu.Lock()
	_, inMap := sup.runners[id]
	sup.mu.Unlock()
	assert.False(t, inMap, "runner must be removed from the map after Stop")

	// Row exists with status=stopped (not deleted).
	job, err := store.GetByID(ctx, id)
	require.NoError(t, err, "DB row must be retained after Stop")
	assert.Equal(t, StatusStopped, job.Status, "status must be stopped after Stop")

	// Restart should succeed — the stopped row is still in the DB.
	require.NoError(t, sup.Restart(ctx, id), "Restart after Stop must succeed")
	job2, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, job2.Status, "status must be running after Restart")
}

// ─────────────────────────────────────────────────────────────────────────────
// TestSupervisor_StopUnknownID: stopping a non-existent ID returns ErrNotFound.
// ─────────────────────────────────────────────────────────────────────────────

func TestSupervisor_StopUnknownID(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	err := sup.Stop(context.Background(), "mon_DOES_NOT_EXIST")
	assert.ErrorIs(t, err, ErrNotFound)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestSupervisor_StopReturnsPromptlyEvenIfRunnerHangs: thrum-puhr.9.2.
// Inject a runnerHandle whose done channel never closes (simulating a stuck
// reader goroutine, e.g. grandchildren holding the stdout/stderr pipe open
// after the direct child died). Stop must mark the DB row stopped and return
// within stopSyncWait + a small slack — NOT block the RPC critical path on
// runner exit. Without the fix, the previous code waited 10s and returned an
// error without marking the row stopped, leaving the row in running state
// forever.
// ─────────────────────────────────────────────────────────────────────────────

func TestSupervisor_StopReturnsPromptlyEvenIfRunnerHangs(t *testing.T) {
	sup, store := newTestSupervisor(t)
	ctx := context.Background()

	// Shrink the synchronous wait so the test exercises the timeout path
	// quickly. Restore at end so subsequent tests see the production value.
	// Mutating this package-level var means this test (and any sibling
	// that overrides stopSyncWait) MUST NOT call t.Parallel(); doing so
	// would race the override and restore.
	prevWait := stopSyncWait
	stopSyncWait = 100 * time.Millisecond
	defer func() { stopSyncWait = prevWait }()

	// Persist a job row in running state directly, bypassing Add (which
	// would spawn a real subprocess). The injected runnerHandle below is
	// the artificial substitute for that real runner.
	now := time.Now().UTC()
	job := &MonitorJob{
		ID:              "mon_TEST_STOP_HANG",
		Name:            "stop-hang",
		Argv:            []string{"sleep", "60"},
		MatchPattern:    ".*",
		Target:          "@t",
		Cwd:             "/tmp",
		Env:             map[string]string{},
		DebounceSeconds: 60,
		CreatedAt:       now,
		UpdatedAt:       now,
		Status:          StatusRunning,
	}
	require.NoError(t, store.Insert(ctx, job))

	// Inject a runnerHandle whose done channel never closes.
	neverDone := make(chan struct{})
	defer close(neverDone) // cleanup so leaked watchers (if any) exit
	_, cancel := context.WithCancel(ctx)
	defer cancel()
	sup.mu.Lock()
	sup.runners[job.ID] = &runnerHandle{
		job:    job,
		cancel: cancel,
		done:   neverDone,
	}
	sup.mu.Unlock()

	start := time.Now()
	err := sup.Stop(ctx, job.ID)
	elapsed := time.Since(start)

	require.NoError(t, err, "Stop must succeed even when the runner is slow to exit")
	// Allow up to 2x stopSyncWait for slack on slow CI.
	assert.Less(t, elapsed, 2*stopSyncWait+250*time.Millisecond,
		"Stop must not block the RPC critical path on runner exit; got %s", elapsed)

	// Row must be marked stopped synchronously, regardless of runner state.
	persisted, err := store.GetByID(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusStopped, persisted.Status,
		"DB row must reflect stopped status even if runner goroutine is slow to exit")

	// Runner must be removed from the map.
	sup.mu.Lock()
	_, inMap := sup.runners[job.ID]
	sup.mu.Unlock()
	assert.False(t, inMap, "runner must be removed from the map after Stop")
}

// ─────────────────────────────────────────────────────────────────────────────
// TestSupervisor_ConcurrentAddsRespectCap: submit MaxConcurrentMonitors jobs,
// then verify the (MaxConcurrentMonitors+1)th returns ErrCapExceeded.
//
// We use quick-exit commands so all runners stay "running" until we cancel ctx.
// ─────────────────────────────────────────────────────────────────────────────

func TestSupervisor_ConcurrentAddsRespectCap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cap test in short mode (spawns 100 children)")
	}

	sup, _ := newTestSupervisor(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	go func() {
		close(started)
		sup.Start(ctx)
	}()
	<-started
	time.Sleep(20 * time.Millisecond)

	// Submit exactly MaxConcurrentMonitors jobs.
	for i := 0; i < MaxConcurrentMonitors; i++ {
		spec := makeSpec(fmt.Sprintf("cap-test-%03d", i))
		_, err := sup.Add(ctx, spec)
		require.NoError(t, err, "job %d of %d must succeed", i+1, MaxConcurrentMonitors)
	}

	// The next submission must be rejected.
	_, err := sup.Add(ctx, makeSpec("cap-overflow"))
	assert.ErrorIs(t, err, ErrCapExceeded)
}

// TestSupervisor_ConcurrentAddsRespectCapRace proves the fix for review
// finding 2 (TOCTOU cap race) in combination with review finding 8
// (crypto/rand ULID entropy). It spawns MaxConcurrentMonitors+1 concurrent
// Add calls and asserts exactly ONE returns ErrCapExceeded.
//
// Before the pending-counter fix, two concurrent Adds could both pass the
// cap check at 99 runners and end up launching 101. Before the crypto/rand
// fix, concurrent Adds within the same nanosecond would collide on the
// ULID-from-math/rand and all fail with "UNIQUE constraint failed" — a
// different bug that masked the cap race.
func TestSupervisor_ConcurrentAddsRespectCapRace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cap race test in short mode (spawns 101 children)")
	}

	sup, _ := newTestSupervisor(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	go func() {
		close(started)
		sup.Start(ctx)
	}()
	<-started
	time.Sleep(20 * time.Millisecond)

	const n = MaxConcurrentMonitors + 1
	release := make(chan struct{}) // unblock all goroutines simultaneously
	results := make(chan error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-release
			_, err := sup.Add(ctx, makeSpec(fmt.Sprintf("race-%03d", i)))
			results <- err
		}()
	}
	close(release)
	wg.Wait()
	close(results)

	var rejected, accepted int
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrCapExceeded):
			rejected++
		default:
			t.Fatalf("unexpected error from Add: %v", err)
		}
	}

	assert.Equal(t, MaxConcurrentMonitors, accepted,
		"exactly MaxConcurrentMonitors Adds should have succeeded")
	assert.Equal(t, 1, rejected,
		"exactly one Add should have been rejected with ErrCapExceeded")
}

// ─────────────────────────────────────────────────────────────────────────────
// TestSupervisor_ShutdownOnContextCancel: cancel ctx while runners are active;
// Start must return and runners must have exited.
// ─────────────────────────────────────────────────────────────────────────────

func TestSupervisor_ShutdownOnContextCancel(t *testing.T) {
	sup, _ := newTestSupervisor(t)

	ctx, cancel := context.WithCancel(context.Background())

	startDone := make(chan struct{})
	go func() {
		sup.Start(ctx)
		close(startDone)
	}()

	// Give Start time to enter its select loop.
	time.Sleep(30 * time.Millisecond)

	// Add two jobs.
	id1, err := sup.Add(ctx, makeSpec("shutdown-a"))
	require.NoError(t, err)
	id2, err := sup.Add(ctx, makeSpec("shutdown-b"))
	require.NoError(t, err)

	// Confirm runners are live.
	sup.mu.Lock()
	_, live1 := sup.runners[id1]
	_, live2 := sup.runners[id2]
	sup.mu.Unlock()
	require.True(t, live1)
	require.True(t, live2)

	// Cancel the context — this should trigger the SIGTERM/SIGKILL chain.
	cancel()

	select {
	case <-startDone:
		// Good — Start returned.
	case <-time.After(15 * time.Second):
		t.Fatal("Start did not return within 15s after ctx cancellation")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestSupervisor_AddValidation: various invalid specs are rejected.
// ─────────────────────────────────────────────────────────────────────────────

func TestSupervisor_AddValidation(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		spec    SubmitSpec
		wantErr error
	}{
		{
			// Review finding 10: name must be explicitly validated. An
			// empty name would otherwise insert an unlabeled row and
			// silently take the unique slot for name=''.
			name: "empty name",
			spec: SubmitSpec{
				Name: "", Argv: []string{"true"}, MatchPattern: ".",
				Target: "@t", Cwd: "/tmp", Env: map[string]string{},
				DebounceSeconds: 30,
			},
			wantErr: nil, // checked by require.Error below
		},
		{
			name: "debounce too short",
			spec: SubmitSpec{
				Name: "x", Argv: []string{"true"}, MatchPattern: ".",
				Target: "@t", Cwd: "/tmp", Env: map[string]string{},
				DebounceSeconds: 10,
			},
			wantErr: ErrDebounceTooShort,
		},
		{
			name: "invalid regex",
			spec: SubmitSpec{
				Name: "x", Argv: []string{"true"}, MatchPattern: "[invalid",
				Target: "@t", Cwd: "/tmp", Env: map[string]string{},
				DebounceSeconds: 30,
			},
			wantErr: ErrInvalidRegex,
		},
		{
			name: "empty argv",
			spec: SubmitSpec{
				Name: "x", Argv: nil, MatchPattern: ".",
				Target: "@t", Cwd: "/tmp", Env: map[string]string{},
				DebounceSeconds: 30,
			},
			wantErr: nil, // checked by require.Error below
		},
		{
			name: "empty cwd",
			spec: SubmitSpec{
				Name: "x", Argv: []string{"true"}, MatchPattern: ".",
				Target: "@t", Cwd: "", Env: map[string]string{},
				DebounceSeconds: 30,
			},
			wantErr: nil,
		},
		{
			name: "empty target",
			spec: SubmitSpec{
				Name: "x", Argv: []string{"true"}, MatchPattern: ".",
				Target: "", Cwd: "/tmp", Env: map[string]string{},
				DebounceSeconds: 30,
			},
			wantErr: nil,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := sup.Add(ctx, tc.spec)
			require.Error(t, err)
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestSupervisor_DuplicateNameRejected: adding two monitors with the same name
// fails on the second insert (DB UNIQUE constraint).
// ─────────────────────────────────────────────────────────────────────────────

func TestSupervisor_DuplicateNameRejected(t *testing.T) {
	sup, _ := newTestSupervisor(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	go func() {
		close(started)
		sup.Start(ctx)
	}()
	<-started
	time.Sleep(20 * time.Millisecond)

	_, err := sup.Add(ctx, makeSpec("dup-name"))
	require.NoError(t, err)

	_, err = sup.Add(ctx, makeSpec("dup-name"))
	require.Error(t, err, "second Add with same name must fail")
	// Review finding 3: the sqlite UNIQUE constraint must be translated into
	// the typed ErrNameTaken sentinel so Epic B RPC handlers can use
	// errors.Is to return a user-friendly error.
	assert.ErrorIs(t, err, ErrNameTaken,
		"duplicate-name Add must return ErrNameTaken, not a raw sqlite error")
}

// TestSupervisor_AddRunnerSurvivesCallerCtxCancel proves that a monitor
// submitted via Add() keeps running even after the caller's context is
// canceled — the runner's lifetime is tied to the supervisor's base
// context (captured by Start), not to the short-lived RPC request context.
//
// Regression test for the bug caught by dd1.6 Smoke 1: RPC-submitted
// monitors were dying the moment the RPC handler returned because
// launch() used the caller's ctx as the runner's parent.
func TestSupervisor_AddRunnerSurvivesCallerCtxCancel(t *testing.T) {
	sup, _ := newTestSupervisor(t)

	supCtx, supCancel := context.WithCancel(context.Background())
	defer supCancel()

	started := make(chan struct{})
	go func() {
		close(started)
		sup.Start(supCtx)
	}()
	<-started
	time.Sleep(20 * time.Millisecond)

	// Submit via Add with a SHORT-LIVED caller ctx that mimics an RPC
	// request.
	callerCtx, callerCancel := context.WithCancel(context.Background())
	spec := makeSpec("rpc-sim")
	// Use a long-running child so we can observe whether the runner
	// survives after the caller ctx is canceled.
	spec.Argv = []string{"sh", "-c", "while true; do echo hi; sleep 0.05; done"}

	id, err := sup.Add(callerCtx, spec)
	require.NoError(t, err)
	require.NotEmpty(t, id)

	// Mimic RPC request completion.
	callerCancel()

	// Give the runner ample time to notice a canceled ctx and exit (if
	// the bug were still present).
	time.Sleep(300 * time.Millisecond)

	// The runner MUST still be in the supervisor's map after caller ctx
	// cancellation. If the old bug were present, the runner would have
	// exited via its natural exit path and removed itself from the map.
	sup.mu.Lock()
	_, stillRunning := sup.runners[id]
	sup.mu.Unlock()
	assert.True(t, stillRunning,
		"monitor %s must still be registered after caller ctx cancel; "+
			"runner lifetime must be tied to supervisor base ctx, not RPC request ctx", id)
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// makeMonitorJobFromSpec builds a *MonitorJob from a SubmitSpec with a fixed ID,
// used to pre-populate the DB in reload tests.
func makeMonitorJobFromSpec(id, name string, spec SubmitSpec) *MonitorJob {
	now := time.Now().UTC()
	env := spec.Env
	if env == nil {
		env = make(map[string]string)
	}
	return &MonitorJob{
		ID:              id,
		Name:            name,
		Argv:            spec.Argv,
		MatchPattern:    spec.MatchPattern,
		Target:          spec.Target,
		Cwd:             spec.Cwd,
		Env:             env,
		DebounceSeconds: spec.DebounceSeconds,
		CreatedAt:       now,
		UpdatedAt:       now,
		Status:          StatusRunning,
	}
}
