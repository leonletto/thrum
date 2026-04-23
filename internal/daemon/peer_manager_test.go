package daemon

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
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

// TestPeerManager_BuildConfigs_SanitizesBridgeUserID covers thrum-bew3:
// peer names derived from hostnames routinely contain dots, but the
// daemon's user.register validator rejects any character outside
// [a-zA-Z0-9_-]. buildConfigForPeer must therefore sanitize the peer
// name before folding it into BridgeUserID, otherwise the bridge
// handshake fails in a tight reconnect loop.
func TestPeerManager_BuildConfigs_SanitizesBridgeUserID(t *testing.T) {
	pm, reg := newTestPeerManager(t)

	dotted := &PeerInfo{
		Name:     "foo.bar.local",
		DaemonID: "daemon-dotted",
		Address:  "100.64.1.9:9100",
		Token:    "tok-dotted",
		Role:     "dialer",
		PairedAt: time.Now(),
		LastSync: time.Now(),
	}
	if err := reg.AddPeer(dotted); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	configs := pm.BuildConfigs()
	if len(configs) != 1 {
		t.Fatalf("BuildConfigs() = %d, want 1", len(configs))
	}
	if got := configs[0].BridgeUserID; got != "user:peer-foo-bar-local" {
		t.Errorf("BridgeUserID = %q, want %q", got, "user:peer-foo-bar-local")
	}
	// PeerName itself should remain unchanged on the config (used for
	// logging and address lookup). Only the user identifier is sanitized.
	if got := configs[0].PeerName; got != "foo.bar.local" {
		t.Errorf("PeerName = %q, want %q (raw peer name preserved)", got, "foo.bar.local")
	}
}

// TestPeerManager_BuildConfigs_NonASCIIPeerNameFallback covers the
// follow-up finding from thrum-iw42 dual-review: a peer name whose
// characters all lie outside SanitizeProxyPrefix's allowed set (e.g.
// "北京") sanitizes to the empty string. Without a fallback, the
// resulting BridgeUserID would be "user:peer-" — the same empty-suffix
// failure thrum-bew3 was fixing, just via a different code path.
//
// Guard: fall back to DaemonID's ULID body (d_ prefix stripped) and
// cap suffix at 27 chars so "peer-<suffix>" fits inside user.register's
// 32-char usernameRegex.
func TestPeerManager_BuildConfigs_NonASCIIPeerNameFallback(t *testing.T) {
	pm, reg := newTestPeerManager(t)

	// Realistic DaemonID format: "d_" + 26-char ULID = 28 chars total.
	// With the d_ prefix stripped the ULID body is 26 chars, which fits
	// inside the 27-char cap with headroom.
	const daemonID = "d_01ABCDEFGHIJKLMNPQRSTVWXYZ"
	nonASCII := &PeerInfo{ //nolint:gosec // G101 false positive: "tok-non-ascii" is a test fixture, not a real credential
		Name:     "北京",
		DaemonID: daemonID,
		Address:  "100.64.1.10:9100",
		Token:    "tok-non-ascii",
		Role:     "dialer",
		PairedAt: time.Now(),
		LastSync: time.Now(),
	}
	if err := reg.AddPeer(nonASCII); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	configs := pm.BuildConfigs()
	if len(configs) != 1 {
		t.Fatalf("BuildConfigs() = %d, want 1", len(configs))
	}
	wantUserID := "user:peer-01ABCDEFGHIJKLMNPQRSTVWXYZ"
	if got := configs[0].BridgeUserID; got != wantUserID {
		t.Errorf("BridgeUserID = %q, want %q (DaemonID-body fallback, d_ stripped)", got, wantUserID)
	}
	// Must validate against the same regex user.register enforces.
	username := strings.TrimPrefix(configs[0].BridgeUserID, "user:")
	if len(username) > 32 {
		t.Errorf("username %q is %d chars, exceeds user.register's 32-char cap", username, len(username))
	}
	userRegex := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)
	if !userRegex.MatchString(username) {
		t.Errorf("username %q does not match user.register regex %s", username, userRegex)
	}
	// Raw PeerName preserved (used for logging / peer-address lookup).
	if got := configs[0].PeerName; got != "北京" {
		t.Errorf("PeerName = %q, want raw %q", got, "北京")
	}
}

// TestPeerManager_BuildConfigs_LongDaemonIDTruncates pins the 27-char
// suffix cap that keeps the username inside user.register's 32-char
// limit even if a future DaemonID format grows past the current 26-char
// ULID body. Guards against the boundary bug the iw42 review caught.
func TestPeerManager_BuildConfigs_LongDaemonIDTruncates(t *testing.T) {
	pm, reg := newTestPeerManager(t)

	// Deliberately oversize (50-char body) to force truncation.
	longDaemonID := "d_" + strings.Repeat("a", 50)
	peer := &PeerInfo{
		Name:     "北京",
		DaemonID: longDaemonID,
		Address:  "100.64.1.11:9100",
		Token:    "tok-long",
		Role:     "dialer",
		PairedAt: time.Now(),
		LastSync: time.Now(),
	}
	if err := reg.AddPeer(peer); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	configs := pm.BuildConfigs()
	if len(configs) != 1 {
		t.Fatalf("BuildConfigs() = %d, want 1", len(configs))
	}
	username := strings.TrimPrefix(configs[0].BridgeUserID, "user:")
	if len(username) > 32 {
		t.Errorf("username %q is %d chars, exceeds 32-char cap", username, len(username))
	}
	// Expect exactly "peer-" + 27 chars of truncated body.
	wantUser := "peer-" + strings.Repeat("a", 27)
	if username != wantUser {
		t.Errorf("username = %q, want %q (truncated to 27-char suffix)", username, wantUser)
	}
}

// TestPeerManager_ConnectPeer_Dialer verifies that ConnectPeer on a dialer
// peer registers a running bridge without waiting for ConnectAll.
// Covers thrum-1f4y: peer.join must spawn a bridge immediately, not wait
// for the next daemon restart.
func TestPeerManager_ConnectPeer_Dialer(t *testing.T) {
	pm, reg := newTestPeerManager(t)

	local := &PeerInfo{
		Name:      "sibling",
		DaemonID:  "daemon-sibling",
		Token:     "tok-sibling",
		Role:      "dialer",
		Transport: "local",
		RepoPath:  t.TempDir(),
		PairedAt:  time.Now(),
		LastSync:  time.Now(),
	}
	if err := reg.AddPeer(local); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	if pm.ActiveCount() != 0 {
		t.Fatalf("pre-ConnectPeer ActiveCount = %d, want 0", pm.ActiveCount())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm.ConnectPeer(ctx, local)

	if pm.ActiveCount() != 1 {
		t.Errorf("post-ConnectPeer ActiveCount = %d, want 1", pm.ActiveCount())
	}
	pm.StopAll()
}

// TestPeerManager_ConnectPeer_Idempotent verifies that calling ConnectPeer
// twice for the same peer only registers one bridge. Required because the
// caller in peer.join RPC may race with an initial daemon-boot ConnectAll.
func TestPeerManager_ConnectPeer_Idempotent(t *testing.T) {
	pm, reg := newTestPeerManager(t)

	local := &PeerInfo{
		Name:      "sibling",
		DaemonID:  "daemon-sibling",
		Token:     "tok-sibling",
		Role:      "dialer",
		Transport: "local",
		RepoPath:  t.TempDir(),
		PairedAt:  time.Now(),
		LastSync:  time.Now(),
	}
	if err := reg.AddPeer(local); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm.ConnectPeer(ctx, local)
	pm.ConnectPeer(ctx, local)

	if got := pm.ActiveCount(); got != 1 {
		t.Errorf("ActiveCount after duplicate ConnectPeer = %d, want 1", got)
	}
	pm.StopAll()
}

// TestPeerManager_ConnectPeer_ListenerIgnored verifies that ConnectPeer
// refuses to spawn a bridge for a listener-role peer. The reverse bridge
// on the listener side is spawned reactively via AcceptPeer when the
// dialer connects, not proactively via peer.join.
func TestPeerManager_ConnectPeer_ListenerIgnored(t *testing.T) {
	pm, _ := newTestPeerManager(t)

	listener := &PeerInfo{
		Name:     "inbound",
		DaemonID: "daemon-inbound",
		Token:    "tok-inbound",
		Role:     "listener",
		Address:  "127.0.0.1:12345",
		PairedAt: time.Now(),
		LastSync: time.Now(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm.ConnectPeer(ctx, listener)

	if got := pm.ActiveCount(); got != 0 {
		t.Errorf("ActiveCount for listener ConnectPeer = %d, want 0", got)
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
