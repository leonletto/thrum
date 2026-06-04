package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/schema"
)

// writeStatus writes a migration status file into varDir for test simulation.
func writeStatus(t *testing.T, varDir string, st schema.MigrationStatus) {
	t.Helper()
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	path := schema.MigrationStatusPath(varDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		t.Fatalf("write status tmp: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename status: %v", err)
	}
}

func testWaitCfg(dir string) daemonStartWaitConfig {
	return daemonStartWaitConfig{
		socketPath:         filepath.Join(dir, "thrum.sock"),
		wsPortPath:         filepath.Join(dir, "ws.port"),
		varDir:             dir,
		noMigrationTimeout: 300 * time.Millisecond,
		stallTimeout:       1 * time.Second,
		pollInterval:       20 * time.Millisecond,
		spinner:            newStartWaitSpinner(nil, false), // silent
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatalf("touch %s: %v", path, err)
	}
}

// Hung daemon, no migration ever surfaces → still times out at noMigrationTimeout.
func TestWaitForDaemonReady_NoMigration_TimesOut(t *testing.T) {
	dir := t.TempDir()
	cfg := testWaitCfg(dir)
	start := time.Now()
	err := waitForDaemonReady(cfg)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout waiting for daemon to start") {
		t.Fatalf("error = %q, want 'timeout waiting for daemon to start'", err)
	}
	// Bounded: should fire near noMigrationTimeout, well before stallTimeout.
	if elapsed > cfg.noMigrationTimeout+500*time.Millisecond {
		t.Fatalf("timed out too late: %v (noMigrationTimeout=%v)", elapsed, cfg.noMigrationTimeout)
	}
}

// Socket + ws.port appear quickly → success.
func TestWaitForDaemonReady_SocketAppears_Success(t *testing.T) {
	dir := t.TempDir()
	cfg := testWaitCfg(dir)
	go func() {
		time.Sleep(60 * time.Millisecond)
		touch(t, cfg.socketPath)
		touch(t, cfg.wsPortPath)
	}()
	if err := waitForDaemonReady(cfg); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

// A migration that OUTLASTS the no-migration timeout must NOT false-timeout;
// once it finishes and the socket appears, the wait returns success. This is
// the core thrum-vh2c regression test.
func TestWaitForDaemonReady_LongMigration_NoFalseTimeout(t *testing.T) {
	dir := t.TempDir()
	cfg := testWaitCfg(dir)

	// Simulate a migration that runs ~3x the no-migration timeout, heartbeating,
	// then completes (status removed) and the daemon comes up (socket+ws.port).
	migrationDuration := 3 * cfg.noMigrationTimeout
	var wg sync.WaitGroup
	wg.Go(func() {
		st := schema.MigrationStatus{
			FromVersion: 24, ToVersion: 41, PID: os.Getpid(),
			Phase:     schema.MigrationPhaseMigrating,
			StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		deadline := time.Now().Add(migrationDuration)
		for time.Now().Before(deadline) {
			st.Heartbeat++
			st.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			writeStatus(t, dir, st)
			time.Sleep(40 * time.Millisecond)
		}
		// Migration done: remove status, then bring daemon up.
		_ = os.Remove(schema.MigrationStatusPath(dir))
		touch(t, cfg.socketPath)
		touch(t, cfg.wsPortPath)
	})

	start := time.Now()
	err := waitForDaemonReady(cfg)
	elapsed := time.Since(start)
	wg.Wait()

	if err != nil {
		t.Fatalf("long migration: expected success (no false timeout), got %v", err)
	}
	// Prove it actually waited THROUGH the old fixed deadline instead of bailing.
	if elapsed < cfg.noMigrationTimeout {
		t.Fatalf("returned too early (%v) — did not wait through migration", elapsed)
	}
}

// A migration whose heartbeat FREEZES (daemon hung mid-migration) must still
// time out within a bounded window — no infinite hang.
func TestWaitForDaemonReady_StalledMigration_TimesOut(t *testing.T) {
	dir := t.TempDir()
	cfg := testWaitCfg(dir)

	// Write a status file once with a frozen heartbeat; never advance it, never
	// bring the daemon up.
	writeStatus(t, dir, schema.MigrationStatus{
		FromVersion: 24, ToVersion: 41, PID: os.Getpid(),
		Phase:     schema.MigrationPhaseMigrating,
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Heartbeat: 7, // frozen
	})

	start := time.Now()
	err := waitForDaemonReady(cfg)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("stalled migration: expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error = %q, want a timeout error", err)
	}
	// Bounded by stallTimeout (plus slack), not infinite.
	if elapsed > cfg.stallTimeout+800*time.Millisecond {
		t.Fatalf("stalled migration timed out too late: %v (stallTimeout=%v)", elapsed, cfg.stallTimeout)
	}
}

// A migration that COMPLETES (status file removed) but whose daemon then never
// becomes ready must still time out within a bounded window — and the error
// must say the migration completed, not that it stalled (finding #2).
func TestWaitForDaemonReady_PostMigrationStall_TimesOut(t *testing.T) {
	dir := t.TempDir()
	cfg := testWaitCfg(dir)

	// Briefly show a progressing migration, then remove the status file
	// (migration finished) but NEVER bring the daemon up.
	go func() {
		st := schema.MigrationStatus{
			FromVersion: 24, ToVersion: 41, PID: os.Getpid(),
			Phase: schema.MigrationPhaseMigrating,
		}
		for range 3 {
			st.Heartbeat++
			st.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			writeStatus(t, dir, st)
			time.Sleep(30 * time.Millisecond)
		}
		_ = os.Remove(schema.MigrationStatusPath(dir))
		// daemon never comes up: no socket / ws.port
	}()

	start := time.Now()
	err := waitForDaemonReady(cfg)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("post-migration stall: expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "migration completed but daemon did not become ready") {
		t.Fatalf("error = %q, want the 'migration completed' message (not 'stalled')", err)
	}
	if strings.Contains(err.Error(), "stalled") {
		t.Fatalf("error wrongly blames migration stall after migration completed: %q", err)
	}
	// Bounded: fires within stallTimeout (measured from migration-finish) + slack.
	if elapsed > cfg.stallTimeout+1500*time.Millisecond {
		t.Fatalf("post-migration stall timed out too late: %v (stallTimeout=%v)", elapsed, cfg.stallTimeout)
	}
}

// The spinner surfaces a human-visible migration message including vN->vM.
func TestWaitForDaemonReady_SpinnerSurfacesProgress(t *testing.T) {
	dir := t.TempDir()
	cfg := testWaitCfg(dir)
	var buf bytes.Buffer
	var mu sync.Mutex
	cfg.spinner = newStartWaitSpinner(&lockedWriter{w: &buf, mu: &mu}, false)

	go func() {
		st := schema.MigrationStatus{
			FromVersion: 24, ToVersion: 41, PID: os.Getpid(),
			Phase: schema.MigrationPhaseMigrating,
		}
		for range 6 {
			st.Heartbeat++
			st.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			writeStatus(t, dir, st)
			time.Sleep(30 * time.Millisecond)
		}
		_ = os.Remove(schema.MigrationStatusPath(dir))
		touch(t, cfg.socketPath)
		touch(t, cfg.wsPortPath)
	}()

	if err := waitForDaemonReady(cfg); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	mu.Lock()
	out := buf.String()
	mu.Unlock()
	if !strings.Contains(out, "Migrating database schema v24->v41") {
		t.Fatalf("spinner output did not surface migration progress; got: %q", out)
	}
}

type lockedWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
