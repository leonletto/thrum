package scheduler

import (
	"context"
	"os"
	"path/filepath"
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
