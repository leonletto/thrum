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

	h := NewMonitorHandler(sup, store)
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

	// Also verify via JSON serialisation so we catch any unexpected leaks.
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

	// 4. Also verify via JSON serialisation (catches any accidental double-encoding).
	jsonBody, err := json.Marshal(resp)
	require.NoError(t, err)
	jsonStr := string(jsonBody)
	assert.False(t, strings.Contains(jsonStr, "supersecretvalue"),
		"raw env value 'supersecretvalue' must not appear in serialised show response")
	assert.False(t, strings.Contains(jsonStr, "hunter2"),
		"raw env value 'hunter2' must not appear in serialised show response")
	assert.True(t, strings.Contains(jsonStr, `"API_KEY"`),
		"env key 'API_KEY' must be visible in serialised show response")
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

func TestMonitorRestart_ReusesSpec(t *testing.T) {
	h, store, _ := newMonitorTestSetup(t)

	// Insert a job at stopped status so Stop's ErrNotFound path fires,
	// which then deletes and re-adds via Add.  This tests the restart code
	// path without needing a live runner process.
	now := time.Now().UTC().Truncate(time.Second)
	job := &monitor.MonitorJob{
		ID: "mon_restart_test", Name: "restart-test",
		Argv: []string{"true"}, MatchPattern: ".*", Target: "@t",
		Cwd: os.TempDir(), Env: map[string]string{},
		DebounceSeconds: 60, CreatedAt: now, UpdatedAt: now,
		Status: monitor.StatusStopped,
	}
	require.NoError(t, store.Insert(context.Background(), job))

	params, _ := json.Marshal(monitorIDParams{ID: "mon_restart_test"})
	resp, err := h.HandleRestart(context.Background(), params)
	require.NoError(t, err)
	require.NotNil(t, resp)
	r, ok := resp.(monitorStartResponse)
	require.True(t, ok, "expected monitorStartResponse, got %T", resp)
	assert.NotEmpty(t, r.ID, "restarted monitor must have an ID")
}

func TestMonitorRestart_ReturnsNotFoundForUnknownID(t *testing.T) {
	h, _, _ := newMonitorTestSetup(t)
	params, _ := json.Marshal(monitorIDParams{ID: "mon_GHOST"})
	_, err := h.HandleRestart(context.Background(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "monitor not found")
}

// ----- HandleLogs tests -----

func TestMonitorLogs_ReturnsNotImplemented(t *testing.T) {
	h, _, _ := newMonitorTestSetup(t)
	_, err := h.HandleLogs(context.Background(), json.RawMessage(`{"id":"mon_X"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet implemented")
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
