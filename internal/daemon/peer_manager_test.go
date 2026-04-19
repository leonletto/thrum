package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// newTestPeerManager creates a PeerManager backed by a temp-dir registry.
func newTestPeerManager(t *testing.T) (*PeerManager, *PeerRegistry) {
	t.Helper()
	dir := t.TempDir()
	reg, err := NewPeerRegistry(filepath.Join(dir, "peers.json"))
	if err != nil {
		t.Fatalf("NewPeerRegistry: %v", err)
	}
	pm := NewPeerManager(reg, "9999", nil)
	return pm, reg
}

// TestPeerManager_BuildConfigs verifies that only dialer-role peers produce configs.
func TestPeerManager_BuildConfigs(t *testing.T) {
	pm, reg := newTestPeerManager(t)

	// Add a dialer peer.
	dialer := &PeerInfo{
		Name:     "sf-node",
		DaemonID: "daemon-sf",
		Address:  "100.64.1.2:9100",
		Token:    "tok-sf",
		Role:     "dialer",
		PairedAt: time.Now(),
		LastSync: time.Now(),
	}
	if err := reg.AddPeer(dialer); err != nil {
		t.Fatalf("AddPeer(dialer): %v", err)
	}

	// Add a listener peer — should be excluded.
	listener := &PeerInfo{
		Name:     "ny-node",
		DaemonID: "daemon-ny",
		Address:  "100.64.1.3:9100",
		Token:    "tok-ny",
		Role:     "listener",
		PairedAt: time.Now(),
		LastSync: time.Now(),
	}
	if err := reg.AddPeer(listener); err != nil {
		t.Fatalf("AddPeer(listener): %v", err)
	}

	configs := pm.BuildConfigs()

	if len(configs) != 1 {
		t.Fatalf("BuildConfigs() returned %d configs, want 1", len(configs))
	}
	cfg := configs[0]
	if cfg.PeerName != "sf-node" {
		t.Errorf("PeerName = %q, want %q", cfg.PeerName, "sf-node")
	}
	if cfg.LocalWSPort != "9999" {
		t.Errorf("LocalWSPort = %q, want %q", cfg.LocalWSPort, "9999")
	}
	if cfg.PeerAddress != "100.64.1.2:9100" {
		t.Errorf("PeerAddress = %q, want %q", cfg.PeerAddress, "100.64.1.2:9100")
	}
	if cfg.BridgeUserID != "user:peer-sf-node" {
		t.Errorf("BridgeUserID = %q, want %q", cfg.BridgeUserID, "user:peer-sf-node")
	}
}

// TestPeerManager_BuildConfigs_LocalPeer verifies local transport uses PeerRepoPath.
func TestPeerManager_BuildConfigs_LocalPeer(t *testing.T) {
	pm, reg := newTestPeerManager(t)

	local := &PeerInfo{
		Name:      "local-twin",
		DaemonID:  "daemon-twin",
		Token:     "tok-twin",
		Role:      "dialer",
		Transport: "local",
		RepoPath:  "/home/dev/other-repo",
		PairedAt:  time.Now(),
		LastSync:  time.Now(),
	}
	if err := reg.AddPeer(local); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	configs := pm.BuildConfigs()

	if len(configs) != 1 {
		t.Fatalf("BuildConfigs() returned %d configs, want 1", len(configs))
	}
	cfg := configs[0]
	if cfg.PeerRepoPath != "/home/dev/other-repo" {
		t.Errorf("PeerRepoPath = %q, want %q", cfg.PeerRepoPath, "/home/dev/other-repo")
	}
	if cfg.PeerAddress != "" {
		t.Errorf("PeerAddress should be empty for local peer, got %q", cfg.PeerAddress)
	}
}

// TestPeerManager_ActiveCount verifies the count starts at 0.
func TestPeerManager_ActiveCount(t *testing.T) {
	pm, _ := newTestPeerManager(t)
	if count := pm.ActiveCount(); count != 0 {
		t.Errorf("ActiveCount() = %d, want 0", count)
	}
}

// TestPeerManager_StopAll verifies that StopAll clears the bridge map.
func TestPeerManager_StopAll(t *testing.T) {
	pm, _ := newTestPeerManager(t)

	// Manually inject a fake bridge to test StopAll without real connections.
	ctx, cancel := context.WithCancel(context.Background())
	pm.mu.Lock()
	pm.bridges["fake-peer"] = &runningBridge{cancel: cancel}
	pm.mu.Unlock()

	if count := pm.ActiveCount(); count != 1 {
		t.Fatalf("ActiveCount() before StopAll = %d, want 1", count)
	}

	pm.StopAll()

	if count := pm.ActiveCount(); count != 0 {
		t.Errorf("ActiveCount() after StopAll = %d, want 0", count)
	}

	// Verify the context was canceled.
	select {
	case <-ctx.Done():
		// expected
	default:
		t.Error("bridge context should have been canceled by StopAll")
	}
}

// xir.29 B2: auth errors surface via the hook's category gate (I3
// review finding: pre-gate removed). When reconcile returns
// category=CatTokenRejected, the hook returns false without retry.
func TestOnDialError_AuthCategorySkipsRetry(t *testing.T) {
	pm, _ := newTestPeerManager(t)
	pm.reconcileDelayFn = func(int) time.Duration { return 0 }
	var calls int32
	hook := &fakeHook{reconcileOne: func(ctx context.Context, name string) (bool, bool, int, error) {
		calls++
		return false, false, 2, nil // CatTokenRejected
	}}
	pm.SetReconcileManager(hook)
	fn := pm.makeOnDialError(hook, "peer-x")

	if got := fn(context.Background(), "peer-x", errString("websocket: 401 Unauthorized")); got {
		t.Errorf("auth category wrongly signaled immediate retry")
	}
	if calls != 1 {
		t.Errorf("ReconcileOneHook call count = %d, want 1 (unified gate)", calls)
	}
}

// xir.29 B2: 3-attempt cap with 2s/8s/30s backoff. Uses the injected
// delay function to skip real sleeps; tests only the cap logic.
func TestOnDialError_RetryCapAtThree(t *testing.T) {
	pm, _ := newTestPeerManager(t)
	pm.reconcileDelayFn = func(int) time.Duration { return 0 }
	var calls int32
	hook := &fakeHook{reconcileOne: func(ctx context.Context, name string) (bool, bool, int, error) {
		calls++
		return false, false, 3, nil // CatOther → transient, not auth
	}}
	pm.SetReconcileManager(hook)

	// Pre-populate attempt state so we skip real 2s/8s sleeps — we're
	// testing the cap, not the timing.
	pm.attemptStates.Store("peer-x", &peerAttemptState{count: 3})
	fn := pm.makeOnDialError(hook, "peer-x")

	// Attempt #4 must exceed cap and log "cap exceeded" without firing
	// the hook.
	if got := fn(context.Background(), "peer-x", errString("transient")); got {
		t.Errorf("attempt past cap wrongly returned true")
	}
	if calls != 0 {
		t.Errorf("ReconcileOneHook called %d times at cap-exceeded; want 0", calls)
	}
}

// I5 (dual-review): exercises the actual production reset path —
// calls fn with a success-returning hook and asserts the counter is
// zeroed afterwards. Previous version poked state directly; this
// version would catch regression if the `st.count = 0` line inside
// makeOnDialError were deleted.
func TestOnDialError_CounterResetsOnSuccess(t *testing.T) {
	pm, _ := newTestPeerManager(t)
	pm.reconcileDelayFn = func(int) time.Duration { return 0 } // no sleep
	hook := &fakeHook{reconcileOne: func(ctx context.Context, name string) (bool, bool, int, error) {
		return true, true, 0, nil // ok + daemonIDChanged
	}}
	pm.SetReconcileManager(hook)

	// Pre-populate count=2 so the upcoming call becomes attempt #3
	// (still inside the cap).
	pm.attemptStates.Store("peer-x", &peerAttemptState{count: 2})
	fn := pm.makeOnDialError(hook, "peer-x")

	got := fn(context.Background(), "peer-x", errString("transient drift"))
	if !got {
		t.Errorf("success + daemonIDChanged should signal immediate retry")
	}
	if after := pm.peerAttemptCount(t, "peer-x"); after != 0 {
		t.Errorf("counter not reset on production success path: got %d", after)
	}
}

// B2 (dual-review): ctx cancellation during backoff must return false
// promptly without calling the hook. Exercises the ctx.Done arm of
// the select inside makeOnDialError's sleep.
func TestOnDialError_CtxCancelInterruptsBackoff(t *testing.T) {
	pm, _ := newTestPeerManager(t)
	pm.reconcileDelayFn = func(int) time.Duration { return 5 * time.Second }
	var called int32
	hook := &fakeHook{reconcileOne: func(ctx context.Context, name string) (bool, bool, int, error) {
		called++
		return true, true, 0, nil
	}}
	pm.SetReconcileManager(hook)
	fn := pm.makeOnDialError(hook, "peer-x")

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()

	start := time.Now()
	got := fn(ctx, "peer-x", errString("transient"))
	elapsed := time.Since(start)

	if got {
		t.Errorf("cancelled ctx wrongly signaled immediate retry")
	}
	if called != 0 {
		t.Errorf("hook wrongly invoked after ctx cancel")
	}
	if elapsed > 1*time.Second {
		t.Errorf("ctx cancel did not short-circuit 5s backoff; elapsed=%v", elapsed)
	}
}

// Test helpers.

type errString string

func (e errString) Error() string { return string(e) }

type fakeHook struct {
	reconcileOne func(ctx context.Context, name string) (bool, bool, int, error)
}

func (f *fakeHook) ReconcileOneHook(ctx context.Context, name string) (bool, bool, int, error) {
	if f.reconcileOne == nil {
		return false, false, 0, nil
	}
	return f.reconcileOne(ctx, name)
}

// peerAttemptCount is a test-only peek at the attempt counter for a
// given peer. Returns -1 if no entry is present.
func (pm *PeerManager) peerAttemptCount(t *testing.T, name string) int {
	t.Helper()
	raw, ok := pm.attemptStates.Load(name)
	if !ok {
		return -1
	}
	st := raw.(*peerAttemptState)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.count
}
