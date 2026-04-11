package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) (*MonitorStore, *state.State) {
	t.Helper()
	tmpDir := t.TempDir()
	st, err := state.NewState(tmpDir, tmpDir, "test-repo", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return NewMonitorStore(st.DB()), st
}

func makeJob(id, name string, status Status) *MonitorJob {
	now := time.Now().UTC().Truncate(time.Second)
	return &MonitorJob{
		ID:              id,
		Name:            name,
		Argv:            []string{"tail", "-F", "/tmp/dev.log"},
		MatchPattern:    "(?i)(error|warning)",
		Target:          "@impl_api",
		Cwd:             "/tmp",
		Env:             map[string]string{"LOG_LEVEL": "debug", "API_KEY": "supersecret"},
		DebounceSeconds: 60,
		CreatedAt:       now,
		UpdatedAt:       now,
		Status:          status,
	}
}

func TestMonitorStore_InsertAndGetByID(t *testing.T) {
	store, _ := newTestStore(t)
	job := makeJob("mon_TEST1", "dev-errors", StatusRunning)

	require.NoError(t, store.Insert(context.Background(), job))

	got, err := store.GetByID(context.Background(), "mon_TEST1")
	require.NoError(t, err)
	assert.Equal(t, job.ID, got.ID)
	assert.Equal(t, job.Name, got.Name)
	assert.Equal(t, job.Argv, got.Argv)
	assert.Equal(t, job.MatchPattern, got.MatchPattern)
	assert.Equal(t, job.Target, got.Target)
	assert.Equal(t, job.Cwd, got.Cwd)
	// Env must round-trip losslessly, including secret-looking values.
	assert.Equal(t, job.Env, got.Env)
	assert.Equal(t, "supersecret", got.Env["API_KEY"], "raw secret must survive round-trip unchanged")
	assert.Equal(t, 60, got.DebounceSeconds)
	assert.Equal(t, StatusRunning, got.Status)
	assert.Nil(t, got.LastExitCode)
	assert.Nil(t, got.LastExitAt)
	assert.Nil(t, got.PID)
	assert.Equal(t, job.CreatedAt, got.CreatedAt)
}

func TestMonitorStore_GetByID_Missing(t *testing.T) {
	store, _ := newTestStore(t)

	_, err := store.GetByID(context.Background(), "mon_MISSING")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMonitorStore_GetByName(t *testing.T) {
	store, _ := newTestStore(t)
	job := makeJob("mon_TEST2", "named-monitor", StatusRunning)
	require.NoError(t, store.Insert(context.Background(), job))

	got, err := store.GetByName(context.Background(), "named-monitor")
	require.NoError(t, err)
	assert.Equal(t, "mon_TEST2", got.ID)
}

func TestMonitorStore_GetByName_Missing(t *testing.T) {
	store, _ := newTestStore(t)

	_, err := store.GetByName(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMonitorStore_NameUniqueness(t *testing.T) {
	store, _ := newTestStore(t)
	require.NoError(t, store.Insert(context.Background(), &MonitorJob{
		ID: "mon_A", Name: "same-name", Argv: []string{"true"},
		MatchPattern: ".*", Target: "@x", Cwd: "/tmp",
		Env: map[string]string{}, DebounceSeconds: 60,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Status: StatusRunning,
	}))
	err := store.Insert(context.Background(), &MonitorJob{
		ID: "mon_B", Name: "same-name", Argv: []string{"true"},
		MatchPattern: ".*", Target: "@x", Cwd: "/tmp",
		Env: map[string]string{}, DebounceSeconds: 60,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Status: StatusRunning,
	})
	assert.Error(t, err, "second insert with same name must fail")
}

func TestMonitorStore_ListByStatus(t *testing.T) {
	store, _ := newTestStore(t)

	require.NoError(t, store.Insert(context.Background(), makeJob("mon_R1", "run1", StatusRunning)))
	require.NoError(t, store.Insert(context.Background(), makeJob("mon_R2", "run2", StatusRunning)))
	require.NoError(t, store.Insert(context.Background(), makeJob("mon_D1", "dead1", StatusDead)))
	require.NoError(t, store.Insert(context.Background(), makeJob("mon_S1", "stopped1", StatusStopped)))

	running, err := store.ListByStatus(context.Background(), StatusRunning)
	require.NoError(t, err)
	assert.Len(t, running, 2)

	dead, err := store.ListByStatus(context.Background(), StatusDead)
	require.NoError(t, err)
	assert.Len(t, dead, 1)

	stopped, err := store.ListByStatus(context.Background(), StatusStopped)
	require.NoError(t, err)
	assert.Len(t, stopped, 1)
}

func TestMonitorStore_ListAll(t *testing.T) {
	store, _ := newTestStore(t)

	require.NoError(t, store.Insert(context.Background(), makeJob("mon_1", "first", StatusRunning)))
	require.NoError(t, store.Insert(context.Background(), makeJob("mon_2", "second", StatusDead)))
	require.NoError(t, store.Insert(context.Background(), makeJob("mon_3", "third", StatusStopped)))

	all, err := store.ListAll(context.Background())
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

func TestMonitorStore_MarkDead(t *testing.T) {
	store, _ := newTestStore(t)
	job := makeJob("mon_MD", "mark-dead", StatusRunning)
	require.NoError(t, store.Insert(context.Background(), job))

	exitAt := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, store.MarkDead(context.Background(), "mon_MD", 1, exitAt))

	got, err := store.GetByID(context.Background(), "mon_MD")
	require.NoError(t, err)
	assert.Equal(t, StatusDead, got.Status)
	require.NotNil(t, got.LastExitCode)
	assert.Equal(t, 1, *got.LastExitCode)
	require.NotNil(t, got.LastExitAt)
	assert.Equal(t, exitAt, *got.LastExitAt)
	assert.Nil(t, got.PID)
}

func TestMonitorStore_Delete(t *testing.T) {
	store, _ := newTestStore(t)
	job := makeJob("mon_DEL", "deletable", StatusRunning)
	require.NoError(t, store.Insert(context.Background(), job))

	require.NoError(t, store.Delete(context.Background(), "mon_DEL"))

	_, err := store.GetByID(context.Background(), "mon_DEL")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMonitorStore_Update(t *testing.T) {
	store, _ := newTestStore(t)
	job := makeJob("mon_UPD", "updatable", StatusRunning)
	require.NoError(t, store.Insert(context.Background(), job))

	pid := 12345
	job.Status = StatusDead
	job.PID = &pid
	job.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	require.NoError(t, store.Update(context.Background(), job))

	got, err := store.GetByID(context.Background(), "mon_UPD")
	require.NoError(t, err)
	assert.Equal(t, StatusDead, got.Status)
	require.NotNil(t, got.PID)
	assert.Equal(t, 12345, *got.PID)
}

func TestMonitorStore_EnvRoundTrip_NoRedaction(t *testing.T) {
	// The store must NOT redact env — that is done in display/RPC paths only.
	store, _ := newTestStore(t)
	job := &MonitorJob{
		ID:   "mon_ENV", Name: "env-test",
		Argv: []string{"env"},
		Env: map[string]string{
			"SECRET_TOKEN": "my-super-secret-value",
			"DB_PASSWORD":  "hunter2",
		},
		MatchPattern:    ".*",
		Target:          "@x",
		Cwd:             "/tmp",
		DebounceSeconds: 60,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
		Status:          StatusRunning,
	}
	require.NoError(t, store.Insert(context.Background(), job))

	got, err := store.GetByID(context.Background(), "mon_ENV")
	require.NoError(t, err)
	assert.Equal(t, "my-super-secret-value", got.Env["SECRET_TOKEN"])
	assert.Equal(t, "hunter2", got.Env["DB_PASSWORD"])
}
