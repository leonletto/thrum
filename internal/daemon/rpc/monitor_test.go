package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon/monitor"
	"github.com/leonletto/thrum/internal/daemon/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----- test helpers -----

// newMonitorTestSetup boots a state DB + MonitorSupervisor backed by a no-op
// delivery.  The supervisor is NOT started (no background goroutine), which
// keeps tests synchronous and race-free.
func newMonitorTestSetup(t *testing.T) (*MonitorHandler, *monitor.MonitorStore, *state.State) {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	require.NoError(t, os.MkdirAll(thrumDir, 0750))

	st, err := state.NewState(thrumDir, thrumDir, "test-repo", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	store := monitor.NewMonitorStore(st.DB())
	delivery := monitor.NewDelivery(&noopSender{})
	sup := monitor.NewMonitorSupervisor(store, delivery)

	h := NewMonitorHandler(sup, store, st)
	return h, store, st
}

// noopSender satisfies monitor.MessageSender without touching the DB.
type noopSender struct{}

func (n *noopSender) HandleSend(_ context.Context, _ json.RawMessage) (any, error) {
	return nil, nil
}

// minimalStartParams returns a minimal valid monitorStartParams JSON encoded
// for use in HandleStart calls.  The argv uses "true" (always exits 0, never
// outputs anything) so tests that don't care about runner output complete fast.
func minimalStartParams(name string) json.RawMessage {
	p := monitorStartParams{
		Name:            name,
		Argv:            []string{"true"},
		Match:           ".*",
		Target:          "@test",
		Cwd:             os.TempDir(),
		DebounceSeconds: 60,
	}
	b, _ := json.Marshal(p)
	return b
}

// ----- HandleStart tests -----

func TestMonitorStart_Success(t *testing.T) {
	h, _, _ := newMonitorTestSetup(t)
	resp, err := h.HandleStart(context.Background(), minimalStartParams("success-test"))
	require.NoError(t, err)
	require.NotNil(t, resp)
	r, ok := resp.(monitorStartResponse)
	require.True(t, ok, "expected monitorStartResponse, got %T", resp)
	assert.NotEmpty(t, r.ID, "monitor ID must be non-empty")
}

func TestMonitorStart_RejectsDebounceBelow30s(t *testing.T) {
	h, _, _ := newMonitorTestSetup(t)
	p := monitorStartParams{
		Name: "fast-debounce", Argv: []string{"true"}, Match: ".*",
		Target: "@test", Cwd: os.TempDir(), DebounceSeconds: 10,
	}
	b, _ := json.Marshal(p)
	_, err := h.HandleStart(context.Background(), b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "debounce must be at least")
}

func TestMonitorStart_RejectsInvalidRegex(t *testing.T) {
	h, _, _ := newMonitorTestSetup(t)
	p := monitorStartParams{
		Name: "bad-regex", Argv: []string{"true"}, Match: "[invalid((",
		Target: "@test", Cwd: os.TempDir(), DebounceSeconds: 60,
	}
	b, _ := json.Marshal(p)
	_, err := h.HandleStart(context.Background(), b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid match pattern")
}

func TestMonitorStart_RejectsEmptyArgv(t *testing.T) {
	h, _, _ := newMonitorTestSetup(t)
	p := monitorStartParams{
		Name: "no-argv", Argv: []string{}, Match: ".*",
		Target: "@test", Cwd: os.TempDir(), DebounceSeconds: 60,
	}
	b, _ := json.Marshal(p)
	_, err := h.HandleStart(context.Background(), b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "argv")
}

func TestMonitorStart_RejectsMissingCwd(t *testing.T) {
	h, _, _ := newMonitorTestSetup(t)
	p := monitorStartParams{
		Name: "no-cwd", Argv: []string{"true"}, Match: ".*",
		Target: "@test", Cwd: "", DebounceSeconds: 60,
	}
	b, _ := json.Marshal(p)
	_, err := h.HandleStart(context.Background(), b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cwd")
}

func TestMonitorStart_RejectsCapExceeded(t *testing.T) {
	// We can't actually launch 100 real processes in a unit test, so we test
	// the error translation path directly by confirming ErrCapExceeded is
	// translated correctly.
	translated := translateMonitorError(monitor.ErrCapExceeded)
	require.Error(t, translated)
	assert.Contains(t, translated.Error(), "maximum concurrent monitors reached")
	assert.Contains(t, translated.Error(), "100")
}

// ----- HandleList tests -----

func TestMonitorList_ReturnsAllRunning(t *testing.T) {
	h, store, _ := newMonitorTestSetup(t)

	// Insert two jobs directly via the store (bypasses the runner goroutine).
	now := time.Now().UTC().Truncate(time.Second)
	for _, name := range []string{"alpha", "beta"} {
		job := &monitor.MonitorJob{
			ID: "mon_" + name, Name: name,
			Argv: []string{"true"}, MatchPattern: ".*", Target: "@t",
			Cwd: os.TempDir(), Env: map[string]string{"SECRET_" + name: "raw_value"},
			DebounceSeconds: 60, CreatedAt: now, UpdatedAt: now,
			Status: monitor.StatusRunning,
		}
		require.NoError(t, store.Insert(context.Background(), job))
	}

	resp, err := h.HandleList(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	views, ok := resp.([]monitorJobView)
	require.True(t, ok, "expected []monitorJobView, got %T", resp)
	assert.Len(t, views, 2)
}

// TestMonitorList_FiltersByStatus verifies that default HandleList returns
// only running monitors, and include_all=true also returns dead/stopped
// monitors younger than 1 week. Review finding R2.3.
func TestMonitorList_FiltersByStatus(t *testing.T) {
	h, store, _ := newMonitorTestSetup(t)

	now := time.Now().UTC().Truncate(time.Second)
	weekAgo := now.Add(-8 * 24 * time.Hour)

	cases := []struct {
		id, name string
		status   monitor.Status
		updated  time.Time
	}{
		{"mon_r", "running-one", monitor.StatusRunning, now},
		{"mon_d_fresh", "dead-recent", monitor.StatusDead, now.Add(-1 * time.Hour)},
		{"mon_s_fresh", "stopped-recent", monitor.StatusStopped, now.Add(-12 * time.Hour)},
		{"mon_d_stale", "dead-old", monitor.StatusDead, weekAgo},
	}
	for _, tc := range cases {
		job := &monitor.MonitorJob{
			ID: tc.id, Name: tc.name,
			Argv: []string{"true"}, MatchPattern: ".", Target: "@t",
			Cwd: os.TempDir(), Env: map[string]string{},
			DebounceSeconds: 60, CreatedAt: now, UpdatedAt: tc.updated,
			Status: tc.status,
		}
		require.NoError(t, store.Insert(context.Background(), job))
	}

	// Default: running only.
	resp, err := h.HandleList(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)
	views, _ := resp.([]monitorJobView)
	require.Len(t, views, 1, "default list should only return running monitors")
	assert.Equal(t, "mon_r", views[0].ID)

	// --all: running + dead/stopped younger than 1 week (stale dead excluded).
	resp, err = h.HandleList(context.Background(), json.RawMessage(`{"include_all":true}`))
	require.NoError(t, err)
	views, _ = resp.([]monitorJobView)
	require.Len(t, views, 3, "--all should include fresh dead + fresh stopped + running, "+
		"but exclude week-old dead row")
	gotIDs := map[string]bool{}
	for _, v := range views {
		gotIDs[v.ID] = true
	}
	assert.True(t, gotIDs["mon_r"])
	assert.True(t, gotIDs["mon_d_fresh"])
	assert.True(t, gotIDs["mon_s_fresh"])
	assert.False(t, gotIDs["mon_d_stale"], "week-old dead monitor must be hidden even with --all")
}

func TestMonitorHandler_ListRedactsEnvValues(t *testing.T) {
	h, store, _ := newMonitorTestSetup(t)

	now := time.Now().UTC().Truncate(time.Second)
	job := &monitor.MonitorJob{
		ID: "mon_list_redact", Name: "list-redact-test",
		Argv: []string{"true"}, MatchPattern: ".*", Target: "@t",
		Cwd: os.TempDir(),
		Env: map[string]string{
			"API_KEY":     "supersecretvalue",
			"DB_PASSWORD": "hunter2",
		},
		DebounceSeconds: 60, CreatedAt: now, UpdatedAt: now,
		Status: monitor.StatusRunning,
	}
	require.NoError(t, store.Insert(context.Background(), job))

	resp, err := h.HandleList(context.Background(), json.RawMessage(`{}`))
	require.NoError(t, err)

	views, ok := resp.([]monitorJobView)
	require.True(t, ok, "expected []monitorJobView, got %T", resp)
	require.Len(t, views, 1)

	// Also verify via JSON serialization so we catch any unexpected leaks.
	jsonBody, err := json.Marshal(resp)
	require.NoError(t, err)
	jsonStr := string(jsonBody)

	// Security assertions: raw secret values MUST NOT appear in the response.
	assert.False(t, strings.Contains(jsonStr, "supersecretvalue"),
		"raw env value 'supersecretvalue' must not appear in list response")
	assert.False(t, strings.Contains(jsonStr, "hunter2"),
		"raw env value 'hunter2' must not appear in list response")

	// Keys MUST be visible.
	assert.True(t, strings.Contains(jsonStr, `"API_KEY"`),
		"env key 'API_KEY' must be visible in list response")
	assert.True(t, strings.Contains(jsonStr, `"DB_PASSWORD"`),
		"env key 'DB_PASSWORD' must be visible in list response")

	// Values in the typed struct must be the redaction sentinel.
	envView := views[0].Env
	assert.Equal(t, "<redacted>", envView["API_KEY"],
		"API_KEY value must be '<redacted>' in list response")
	assert.Equal(t, "<redacted>", envView["DB_PASSWORD"],
		"DB_PASSWORD value must be '<redacted>' in list response")
}

// ----- HandleShow tests -----

func TestMonitorHandler_ShowRedactsEnvValues(t *testing.T) {
	h, store, _ := newMonitorTestSetup(t)

	now := time.Now().UTC().Truncate(time.Second)
	job := &monitor.MonitorJob{
		ID: "mon_show_redact", Name: "show-redact-test",
		Argv: []string{"true"}, MatchPattern: ".*", Target: "@t",
		Cwd: os.TempDir(),
		Env: map[string]string{
			"API_KEY":     "supersecretvalue",
			"DB_PASSWORD": "hunter2",
		},
		DebounceSeconds: 60, CreatedAt: now, UpdatedAt: now,
		Status: monitor.StatusRunning,
	}
	require.NoError(t, store.Insert(context.Background(), job))

	params, _ := json.Marshal(monitorIDParams{ID: "mon_show_redact"})
	resp, err := h.HandleShow(context.Background(), params)
	require.NoError(t, err)

	view, ok := resp.(monitorJobView)
	require.True(t, ok, "expected monitorJobView, got %T", resp)

	// 1. Typed struct: raw secret values MUST NOT appear in the env map.
	assert.NotEqual(t, "supersecretvalue", view.Env["API_KEY"],
		"raw env value 'supersecretvalue' must not appear in show response")
	assert.NotEqual(t, "hunter2", view.Env["DB_PASSWORD"],
		"raw env value 'hunter2' must not appear in show response")

	// 2. Keys MUST remain visible so users know which env vars are set.
	_, hasAPIKey := view.Env["API_KEY"]
	assert.True(t, hasAPIKey, "env key 'API_KEY' must be visible in show response")
	_, hasDBPass := view.Env["DB_PASSWORD"]
	assert.True(t, hasDBPass, "env key 'DB_PASSWORD' must be visible in show response")

	// 3. Values must be the redaction sentinel.
	assert.Equal(t, "<redacted>", view.Env["API_KEY"],
		"API_KEY value must be '<redacted>' in show response")
	assert.Equal(t, "<redacted>", view.Env["DB_PASSWORD"],
		"DB_PASSWORD value must be '<redacted>' in show response")

	// 4. Also verify via JSON serialization (catches any accidental double-encoding).
	jsonBody, err := json.Marshal(resp)
	require.NoError(t, err)
	jsonStr := string(jsonBody)
	assert.False(t, strings.Contains(jsonStr, "supersecretvalue"),
		"raw env value 'supersecretvalue' must not appear in serialized show response")
	assert.False(t, strings.Contains(jsonStr, "hunter2"),
		"raw env value 'hunter2' must not appear in serialized show response")
	assert.True(t, strings.Contains(jsonStr, `"API_KEY"`),
		"env key 'API_KEY' must be visible in serialized show response")
}

func TestMonitorShow_ReturnsNotFoundForUnknownID(t *testing.T) {
	h, _, _ := newMonitorTestSetup(t)
	params, _ := json.Marshal(monitorIDParams{ID: "mon_DOESNOTEXIST"})
	_, err := h.HandleShow(context.Background(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monitor not found")
}

// ----- HandleStop tests -----

func TestMonitorStop_ReturnsNotFoundForUnknownID(t *testing.T) {
	h, _, _ := newMonitorTestSetup(t)
	params, _ := json.Marshal(monitorIDParams{ID: "mon_GHOST"})
	_, err := h.HandleStop(context.Background(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monitor not found")
}

func TestMonitorStop_MissingIDReturnsError(t *testing.T) {
	h, _, _ := newMonitorTestSetup(t)
	_, err := h.HandleStop(context.Background(), json.RawMessage(`{}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id is required")
}

// ----- HandleRestart tests -----

func TestMonitorRestart_PreservesID(t *testing.T) {
	// Review finding R2.1: HandleRestart must preserve the monitor ID per
	// the design doc. This test asserts the returned ID equals the input
	// ID and that the same row remains in the store (not a fresh one).
	h, store, _ := newMonitorTestSetup(t)

	now := time.Now().UTC().Truncate(time.Second)
	const stableID = "mon_restart_test"
	job := &monitor.MonitorJob{
		ID: stableID, Name: "restart-test",
		Argv: []string{"true"}, MatchPattern: ".*", Target: "@t",
		Cwd: os.TempDir(), Env: map[string]string{},
		DebounceSeconds: 60, CreatedAt: now, UpdatedAt: now,
		Status: monitor.StatusStopped,
	}
	require.NoError(t, store.Insert(context.Background(), job))

	params, _ := json.Marshal(monitorIDParams{ID: stableID})
	resp, err := h.HandleRestart(context.Background(), params)
	require.NoError(t, err)
	require.NotNil(t, resp)
	r, ok := resp.(monitorStartResponse)
	require.True(t, ok, "expected monitorStartResponse, got %T", resp)
	assert.Equal(t, stableID, r.ID,
		"restart MUST preserve the monitor ID (design doc §'thrum monitor restart')")

	// The original row must still exist in the DB with the same ID.
	after, err := store.GetByID(context.Background(), stableID)
	require.NoError(t, err, "restarted monitor row must still exist with the same ID")
	assert.Equal(t, stableID, after.ID)
	assert.Equal(t, monitor.StatusRunning, after.Status,
		"restart should transition status back to running")
}

func TestMonitorRestart_ReturnsNotFoundForUnknownID(t *testing.T) {
	h, _, _ := newMonitorTestSetup(t)
	params, _ := json.Marshal(monitorIDParams{ID: "mon_GHOST"})
	_, err := h.HandleRestart(context.Background(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monitor not found")
}

// ----- HandleLogs tests -----

// TestMonitorLogs_ReturnsRecentMatches verifies HandleLogs queries the
// messages table and returns the last N synthetic messages with
// agent_id = "monitor:<name>" (the caller ID used by Delivery).
// Review finding R2.2.
func TestMonitorLogs_ReturnsRecentMatches(t *testing.T) {
	h, store, st := newMonitorTestSetup(t)

	// Pre-insert a monitor row so HandleLogs can resolve id → name.
	now := time.Now().UTC().Truncate(time.Second)
	const monID = "mon_logs_test"
	const monName = "logs-watch"
	job := &monitor.MonitorJob{
		ID: monID, Name: monName,
		Argv: []string{"true"}, MatchPattern: ".", Target: "@t",
		Cwd: os.TempDir(), Env: map[string]string{},
		DebounceSeconds: 60, CreatedAt: now, UpdatedAt: now,
		Status: monitor.StatusRunning,
	}
	require.NoError(t, store.Insert(context.Background(), job))

	// Pre-seed a synthetic agent+session for "monitor:logs-watch" so
	// MessageHandler.HandleSend can resolve the caller when Delivery
	// submits the synthetic message.
	callerID := "monitor:" + monName
	nowStr := now.Format(time.RFC3339)
	_, err := st.DB().ExecContext(context.Background(), `
		INSERT INTO agents (agent_id, kind, role, module, display, hostname, agent_pid, registered_at, last_seen_at)
		VALUES (?, 'monitor', 'monitor', 'monitor', ?, '', 0, ?, ?)
	`, callerID, monName, nowStr, nowStr)
	require.NoError(t, err)
	sessionID := fmt.Sprintf("ses_logs_test_%d", time.Now().UnixNano())
	_, err = st.DB().ExecContext(context.Background(), `
		INSERT INTO sessions (session_id, agent_id, started_at, last_seen_at)
		VALUES (?, ?, ?, ?)
	`, sessionID, callerID, nowStr, nowStr)
	require.NoError(t, err)

	// Insert 3 matches via the real Delivery pipeline so the messages
	// table gets rows with agent_id = "monitor:logs-watch".
	msgHandler := NewMessageHandler(st)
	delivery := monitor.NewDelivery(msgHandler)
	for i := 1; i <= 3; i++ {
		content := fmt.Sprintf("ERROR: match %d", i)
		require.NoError(t, delivery.Deliver(context.Background(), monName, "", content))
		time.Sleep(10 * time.Millisecond) // ensure distinct created_at
	}

	// Query via HandleLogs.
	params, _ := json.Marshal(monitorLogsParams{ID: monID, Limit: 10})
	resp, err := h.HandleLogs(context.Background(), params)
	require.NoError(t, err)
	entries, ok := resp.([]monitorLogEntry)
	require.True(t, ok, "expected []monitorLogEntry, got %T", resp)
	require.Len(t, entries, 3, "expected all 3 inserted matches")

	// Verify contents and ordering (DESC so most recent first).
	for _, e := range entries {
		assert.Contains(t, e.Content, "ERROR: match",
			"content should include the inserted match body")
		assert.NotEmpty(t, e.MessageID)
	}
}

func TestMonitorLogs_RejectsMissingID(t *testing.T) {
	h, _, _ := newMonitorTestSetup(t)
	_, err := h.HandleLogs(context.Background(), json.RawMessage(`{}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id is required")
}

func TestMonitorLogs_ReturnsNotFoundForUnknownID(t *testing.T) {
	h, _, _ := newMonitorTestSetup(t)
	params, _ := json.Marshal(monitorLogsParams{ID: "mon_GHOST"})
	_, err := h.HandleLogs(context.Background(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monitor not found")
}

// ----- error translation tests -----

func TestTranslateMonitorError_AllSentinels(t *testing.T) {
	cases := []struct {
		in      error
		wantMsg string
	}{
		{monitor.ErrCapExceeded, "maximum concurrent monitors reached"},
		{monitor.ErrNameTaken, "monitor name already in use"},
		{monitor.ErrDebounceTooShort, "debounce must be at least"},
		{monitor.ErrNotFound, "monitor not found"},
		{errors.New("random internal failure"), "internal error:"},
	}
	for _, tc := range cases {
		got := translateMonitorError(tc.in)
		require.Error(t, got)
		assert.Contains(t, got.Error(), tc.wantMsg,
			"translateMonitorError(%q) expected to contain %q", tc.in, tc.wantMsg)
	}
}

func TestTranslateMonitorError_InvalidRegex(t *testing.T) {
	// Simulate how supervisor wraps ErrInvalidRegex.
	wrapped := errors.Join(monitor.ErrInvalidRegex, errors.New("missing closing paren"))
	// supervisor uses fmt.Errorf("%w: ...", ErrInvalidRegex) so let's test that shape.
	wrapped2 := fmt.Errorf("%w: %v", monitor.ErrInvalidRegex, "missing closing paren")
	for _, err := range []error{wrapped, wrapped2} {
		got := translateMonitorError(err)
		require.Error(t, got)
		assert.Contains(t, got.Error(), "invalid match pattern",
			"expected 'invalid match pattern' in: %v", got)
	}
}

// ----- redactEnv unit test -----

func TestRedactEnv_KeysVisibleValuesHidden(t *testing.T) {
	src := map[string]string{
		"API_KEY":     "supersecretvalue",
		"DB_PASSWORD": "hunter2",
		"LOG_LEVEL":   "info",
	}
	got := redactEnv(src)
	assert.Len(t, got, len(src), "output must have same number of keys as input")
	for k, v := range got {
		assert.Equal(t, "<redacted>", v, "value for key %q must be '<redacted>'", k)
		_, exists := src[k]
		assert.True(t, exists, "key %q in output must exist in source", k)
	}
}

func TestRedactEnv_EmptyMapReturnsEmptyMap(t *testing.T) {
	got := redactEnv(map[string]string{})
	assert.NotNil(t, got)
	assert.Empty(t, got)
}
