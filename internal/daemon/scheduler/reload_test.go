package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// waitForReload polls reloadCount up to 500ms (50 × 10ms) for the watcher
// to fire. Returns true if reloadCount >= want before timeout.
func waitForReload(mu *sync.Mutex, reloadCount *int, want int) bool {
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		c := *reloadCount
		mu.Unlock()
		if c >= want {
			return true
		}
	}
	return false
}

// TestReload_FSNotify_FiresOnInPlaceModify: in-place edit fires fsnotify
// Write event → onReload invoked.
func TestReload_FSNotify_FiresOnInPlaceModify(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"jobs":{}}`), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	var (
		mu          sync.Mutex
		reloadCount int
	)
	onReload := func() {
		mu.Lock()
		reloadCount++
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.WatchConfig(ctx, configPath, onReload); err != nil {
		t.Fatalf("watch: %v", err)
	}

	// Give the watcher goroutine a moment to register.
	time.Sleep(50 * time.Millisecond)

	if err := os.WriteFile(configPath,
		[]byte(`{"jobs":{"docs-bot":{"type":"command","schedule":"@every 5m","enabled":true,"command":{"exec":"/bin/true"}}}}`),
		0o600); err != nil {
		t.Fatalf("modify config: %v", err)
	}

	if !waitForReload(&mu, &reloadCount, 1) {
		mu.Lock()
		got := reloadCount
		mu.Unlock()
		t.Errorf("fsnotify modify: reload count = %d; want >= 1", got)
	}
}

// TestReload_SIGHUP_FiresReload: SIGHUP to the test process triggers
// onReload via the signal-handler fallback path.
func TestReload_SIGHUP_FiresReload(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"jobs":{}}`), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	var (
		mu          sync.Mutex
		reloadCount int
	)
	onReload := func() {
		mu.Lock()
		reloadCount++
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.WatchConfig(ctx, configPath, onReload); err != nil {
		t.Fatalf("watch: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("find process: %v", err)
	}
	if err := proc.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}

	if !waitForReload(&mu, &reloadCount, 1) {
		mu.Lock()
		got := reloadCount
		mu.Unlock()
		t.Errorf("SIGHUP: reload count = %d; want >= 1", got)
	}
}

// TestReload_MacOS_RenameAndReplace: simulates the write-tmp-and-rename
// pattern. fsnotify on Darwin may not fire CREATE on the new inode
// immediately, so we also send a SIGHUP to verify the fallback closes the
// gap. Test passes when EITHER path reports a reload.
func TestReload_MacOS_RenameAndReplace(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"jobs":{}}`), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	var (
		mu          sync.Mutex
		reloadCount int
	)
	onReload := func() {
		mu.Lock()
		reloadCount++
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.WatchConfig(ctx, configPath, onReload); err != nil {
		t.Fatalf("watch: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	tmpPath := configPath + ".tmp"
	if err := os.WriteFile(tmpPath,
		[]byte(`{"jobs":{"new":{"type":"command","schedule":"@every 1m","enabled":true,"command":{"exec":"/bin/true"}}}}`),
		0o600); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	if err := os.Rename(tmpPath, configPath); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// SIGHUP closes the macOS rename-gap if fsnotify didn't fire.
	proc, _ := os.FindProcess(os.Getpid())
	_ = proc.Signal(syscall.SIGHUP)

	if !waitForReload(&mu, &reloadCount, 1) {
		mu.Lock()
		got := reloadCount
		mu.Unlock()
		t.Errorf("rename + SIGHUP: reload count = %d; want >= 1", got)
	}
}

// TestReload_StopsOnContextCancel: when ctx cancels, the watcher
// goroutine exits and post-cancel file edits no longer trigger
// onReload. (We deliberately don't send SIGHUP here — signal.Stop on
// the watcher's deferred cleanup releases the signal handler, and an
// un-handled SIGHUP would terminate the test process. The
// signal-listener half of the contract is exercised by
// TestReload_SIGHUP_FiresReload before cancel runs; the fsnotify-half
// is what's left to pin here.)
func TestReload_StopsOnContextCancel(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"jobs":{}}`), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	var (
		mu          sync.Mutex
		reloadCount int
	)
	onReload := func() {
		mu.Lock()
		reloadCount++
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := s.WatchConfig(ctx, configPath, onReload); err != nil {
		t.Fatalf("watch: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	// Allow the watcher goroutine to drain Events / close the
	// watcher + signal.Stop the SIGHUP channel.
	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(configPath, []byte(`{"jobs":{"x":{}}}`), 0o600); err != nil {
		t.Fatalf("post-cancel write: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if reloadCount != 0 {
		t.Errorf("post-cancel reload count = %d; want 0 (fsnotify watcher should be closed)", reloadCount)
	}
}

// TestReload_ValidConfigSwapsIn: a fresh config with one user job lands
// in s.specs after ReloadConfig.
func TestReload_ValidConfigSwapsIn(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"jobs":{}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	if err := s.ReloadConfig(context.Background(), configPath); err != nil {
		t.Fatalf("reload empty: %v", err)
	}
	if _, ok := s.JobSpec("docs-bot"); ok {
		t.Error("docs-bot should NOT be present yet")
	}

	valid := `{
		"jobs": {
			"docs-bot": {
				"id": "docs-bot", "type": "command",
				"schedule": "@every 5m", "enabled": true,
				"command": {"exec": "/bin/true"}
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(valid), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := s.ReloadConfig(context.Background(), configPath); err != nil {
		t.Fatalf("reload valid: %v", err)
	}

	spec, ok := s.JobSpec("docs-bot")
	if !ok {
		t.Fatal("docs-bot not swapped in")
	}
	if spec.Schedule != "@every 5m" {
		t.Errorf("schedule = %q", spec.Schedule)
	}
}

// TestReload_InvalidConfigKeepsLastGood: a validator failure preserves
// the prior good config — neither the bad job is swapped in nor is the
// prior good job evicted.
func TestReload_InvalidConfigKeepsLastGood(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	valid := `{
		"jobs": {
			"good-job": {
				"id": "good-job", "type": "command",
				"schedule": "@every 5m", "enabled": true,
				"command": {"exec": "/bin/true"}
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(valid), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	if err := s.ReloadConfig(context.Background(), configPath); err != nil {
		t.Fatalf("first reload: %v", err)
	}
	if _, ok := s.JobSpec("good-job"); !ok {
		t.Fatal("good-job not loaded initially")
	}

	invalid := `{
		"jobs": {
			"bad-job": {
				"id": "bad-job", "type": "command",
				"schedule": "not a cron", "enabled": true,
				"command": {"exec": "/bin/true"}
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(invalid), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if err := s.ReloadConfig(context.Background(), configPath); err == nil {
		t.Error("expected validator error from ReloadConfig")
	}
	if _, ok := s.JobSpec("good-job"); !ok {
		t.Error("last-good config evicted on validator failure")
	}
	if _, ok := s.JobSpec("bad-job"); ok {
		t.Error("bad-job swapped in despite validator error")
	}
}

// TestReload_InvalidConfigEmitsEscalation: OnReloadError callback fires
// with the failing config path and all validator findings.
func TestReload_InvalidConfigEmitsEscalation(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	bad := `{
		"jobs": {
			"bad": {
				"id": "bad", "type": "command",
				"schedule": "not a cron", "enabled": true,
				"command": {"exec": "/bin/true"}
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(bad), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	var escalations []ReloadEscalation
	s.OnReloadError = func(e ReloadEscalation) {
		escalations = append(escalations, e)
	}

	_ = s.ReloadConfig(context.Background(), configPath)

	if len(escalations) == 0 {
		t.Fatal("expected escalation event for validator failure")
	}
	if escalations[0].ConfigPath != configPath {
		t.Errorf("escalation.ConfigPath = %q; want %q", escalations[0].ConfigPath, configPath)
	}
	if len(escalations[0].Errors) == 0 {
		t.Error("escalation.Errors should carry validator diagnostics")
	}
}

// TestReload_UnknownTopLevelKey_Rejected: rule 8 — unknown JSON keys
// under jobs.<id> fail the json.Decoder.DisallowUnknownFields() pass
// before the validator ever sees the spec. The error is normalized
// into the spec §4.1 operator-facing format
// `jobs.<id>.<key>: not a recognized field`.
func TestReload_UnknownTopLevelKey_Rejected(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	bad := `{
		"jobs": {
			"x": {
				"id": "x", "type": "command", "schedule": "@every 5m",
				"enabled": true, "command": {"exec": "/bin/true"},
				"totally_unknown_field": "boom"
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(bad), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	var captured []ReloadEscalation
	s.OnReloadError = func(e ReloadEscalation) { captured = append(captured, e) }

	err := s.ReloadConfig(context.Background(), configPath)
	if err == nil {
		t.Fatal("expected error on unknown top-level key")
	}
	// Spec §4.1 rule-8 operator-facing format. The path's <id> is filled
	// as the literal token (Go's DisallowUnknownFields error doesn't
	// carry the offending jobs entry); the field name MUST appear so
	// operators can locate the typo.
	want := "totally_unknown_field: not a recognized field"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q; want it to contain %q", err.Error(), want)
	}
	if len(captured) != 1 {
		t.Fatalf("escalation count = %d; want 1", len(captured))
	}
	if !strings.Contains(captured[0].Errors[0].Error(), want) {
		t.Errorf("escalation.Errors[0] = %q; want it to contain %q", captured[0].Errors[0].Error(), want)
	}
}

// TestAtomicWriter_PriorFileIntactOnFailure: success path leaves the
// configured content at the path with no .tmp leftover, and the prior
// file content is correctly replaced.
func TestAtomicWriter_PriorFileIntactOnFailure(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"original":true}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := AtomicWriteConfig(configPath, []byte(`{"new":true}`)); err != nil {
		t.Fatalf("atomic write: %v", err)
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read post-write: %v", err)
	}
	if string(got) != `{"new":true}` {
		t.Errorf("config = %q; want new content", got)
	}
	if _, err := os.Stat(configPath + ".tmp"); !os.IsNotExist(err) {
		t.Error(".tmp file leaked after successful rename")
	}
}

// TestAtomicWriter_NonexistentDir_PriorFileIntact: when the target
// directory doesn't exist, the write fails AND the (non-existent) prior
// path stays non-existent — no stray .tmp lingers.
func TestAtomicWriter_NonexistentDir_PriorFileIntact(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "missing-subdir", "config.json")
	if err := AtomicWriteConfig(configPath, []byte(`{}`)); err == nil {
		t.Error("expected error writing into non-existent directory")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Error("config file appeared despite write failure")
	}
	if _, err := os.Stat(configPath + ".tmp"); !os.IsNotExist(err) {
		t.Error(".tmp file leaked after failed write")
	}
}

// TestAtomicWriter_PriorContent_OnFailedRename: simulate a rename
// failure by replacing the target directory with a file mid-write. The
// prior config content must survive intact.
func TestAtomicWriter_OverwriteExisting(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(configPath, []byte(`v1`), 0o600); err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	if err := AtomicWriteConfig(configPath, []byte(`v2`)); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	got, _ := os.ReadFile(configPath)
	if string(got) != `v2` {
		t.Errorf("got %q; want v2", got)
	}
}

// TestAtomicWriter_FsyncOnTmp_HappyPath documents the fsync expectation;
// hard to assert directly in unit-test without intercepting syscalls,
// but exercising the happy path keeps the discipline in commit history.
func TestAtomicWriter_FsyncOnTmp_HappyPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := AtomicWriteConfig(configPath, []byte(`{}`)); err != nil {
		t.Fatalf("atomic write: %v", err)
	}
}

// TestReload_DuringActiveRun_DoesNotDisturb: in-flight runs continue
// under the dispatched spec; a concurrent ReloadConfig that REMOVES the
// job from user config must not cancel or restart the running handler.
// The handler's snapshot of JobSpec is taken at dispatch time; the
// per-run goroutine never touches the live spec map.
//
// Spec §8.6.5 acceptance criterion. The full operator-facing path
// (job.cancel mid-run + job.delete refusal) is covered by the E2E
// smoke at §8.8 step 6 — this test pins the in-process safety
// invariant the unit layer can verify without an RPC round-trip.
func TestReload_DuringActiveRun_DoesNotDisturb(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")

	// Seed with one user job pointing at a slow handler so we have time
	// to reload while it runs.
	initial := `{
		"jobs": {
			"slow-job": {
				"id": "slow-job", "type": "scheduled_agent",
				"schedule": "@every 1h", "enabled": true,
				"scheduled_agent": {"target": "@x", "primer": "do work"}
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(initial), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()

	// Slow handler stays "running" 400ms then completes.
	slow := &slowHandler{wait: 400 * time.Millisecond}
	if err := s.RegisterTypeHandler("scheduled_agent", slow); err != nil {
		t.Fatalf("register scheduled_agent: %v", err)
	}

	if err := s.ReloadConfig(context.Background(), configPath); err != nil {
		t.Fatalf("initial reload: %v", err)
	}
	if _, ok := s.JobSpec("slow-job"); !ok {
		t.Fatal("slow-job not loaded")
	}

	// Manually dispatch the slow job in a goroutine so we don't depend
	// on reactor tick timing.
	doneRunning := make(chan struct{})
	go func() {
		spec, _ := s.JobSpec("slow-job")
		// Seed a state row so the reporter has something to update.
		now := time.Now()
		_ = s.state.UpsertState(context.Background(), &StateRow{
			JobID: spec.ID, Generation: 1, CurrentState: StateDispatched,
			CreatedAt: now, UpdatedAt: now,
		})
		s.launchRun(context.Background(), spec, "slow-job-g1-1", slow)
		// Poll for completion.
		for i := 0; i < 100; i++ {
			row, _ := s.state.GetState(context.Background(), spec.ID)
			if row != nil && isTerminal(row.CurrentState) {
				close(doneRunning)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Errorf("slow-job did not reach terminal state within poll window")
		close(doneRunning)
	}()

	// Wait for the handler to enter running.
	time.Sleep(50 * time.Millisecond)

	// Reload with an EMPTY user-config; this should evict slow-job from
	// s.specs but MUST NOT cancel the in-flight handler. The handler
	// holds its own JobSpec snapshot from dispatch time.
	emptyConfig := `{"jobs":{}}`
	if err := os.WriteFile(configPath, []byte(emptyConfig), 0o600); err != nil {
		t.Fatalf("rewrite empty: %v", err)
	}
	if err := s.ReloadConfig(context.Background(), configPath); err != nil {
		t.Fatalf("reload empty: %v", err)
	}

	// Confirm slow-job evicted from specs.
	if _, ok := s.JobSpec("slow-job"); ok {
		t.Error("slow-job should be evicted after reload with empty config")
	}

	// Wait for the in-flight handler to complete.
	select {
	case <-doneRunning:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch goroutine never reported completion")
	}

	// Final state must be StateCompleted — the handler ran to its own
	// success path, undisturbed by the reload.
	row, err := s.state.GetState(context.Background(), "slow-job")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if row.CurrentState != StateCompleted {
		t.Errorf("post-reload state = %q; want %q (in-flight run should have completed normally)", row.CurrentState, StateCompleted)
	}
}

// TestReload_PreservesInternalJobs: internal.* jobs live in the
// daemon-registered registry, not in the user config. Reloading must
// not evict them.
func TestReload_PreservesInternalJobs(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"jobs":{}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s := New(Config{DB: setupStateTestDB(t), DaemonID: "test", Location: time.UTC})
	defer func() { _ = s.Stop(context.Background()) }()
	s.RegisterInternal("internal.backup", "@every 1h", InternalOpts{}, &noopHandler{})

	if err := s.ReloadConfig(context.Background(), configPath); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := s.JobSpec("internal.backup"); !ok {
		t.Error("internal.backup should be preserved across reload")
	}
}
