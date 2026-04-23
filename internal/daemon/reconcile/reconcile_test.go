package reconcile

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
)

func mkRegistry(t *testing.T) *daemon.PeerRegistry {
	t.Helper()
	r, err := daemon.NewPeerRegistry(filepath.Join(t.TempDir(), "peers.json"))
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return r
}

func TestReconcileOne_SuccessUpdatesDaemonIDAndClearsStatus(t *testing.T) {
	r := mkRegistry(t)
	if err := r.AddPeer(&daemon.PeerInfo{
		Name:            "alpha",
		DaemonID:        "01OLDDID",
		Address:         "1.2.3.4:7731",
		Token:           "tok",
		Transport:       "network",
		ReconcileStatus: StatusDriftReconcileFailed,
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	fake := func(ctx context.Context, addr, tok string, local DialerIdentity) (RepairResponse, error) {
		return RepairResponse{DaemonID: "01NEWDID", Name: "alpha"}, nil
	}
	mgr := NewManager(r, fake, DialerIdentity{DaemonID: "01SELF"})
	res, err := mgr.ReconcileOne(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}
	if !res.OK {
		t.Errorf("expected OK=true, got %+v", res)
	}
	if res.OldDaemonID != "01OLDDID" || res.NewDaemonID != "01NEWDID" {
		t.Errorf("daemon_id transition = %q → %q", res.OldDaemonID, res.NewDaemonID)
	}
	if got := r.FindPeerByToken("tok"); got == nil || got.DaemonID != "01NEWDID" {
		t.Errorf("registry not re-keyed after success: %+v", got)
	}
	if got := r.FindPeerByToken("tok"); got.ReconcileStatus != StatusHealthy {
		t.Errorf("status not cleared on success: %q", got.ReconcileStatus)
	}
}

func TestReconcileOne_SameDaemonID_ClearsDriftFlag(t *testing.T) {
	r := mkRegistry(t)
	if err := r.AddPeer(&daemon.PeerInfo{
		Name:            "a",
		DaemonID:        "01D",
		Address:         "x:1",
		Token:           "t",
		ReconcileStatus: StatusDriftReconcileFailed,
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	fake := func(ctx context.Context, addr, tok string, local DialerIdentity) (RepairResponse, error) {
		return RepairResponse{DaemonID: "01D", Name: "a"}, nil
	}
	mgr := NewManager(r, fake, DialerIdentity{DaemonID: "self"})
	res, _ := mgr.ReconcileOne(context.Background(), "a")
	if !res.OK {
		t.Fatalf("expected OK=true, got %+v", res)
	}
	if got := r.FindPeerByToken("t").ReconcileStatus; got != StatusHealthy {
		t.Errorf("status not cleared for same-daemon-id success: %q", got)
	}
}

func TestReconcileOne_UnreachableMarksDriftFailed(t *testing.T) {
	r := mkRegistry(t)
	_ = r.AddPeer(&daemon.PeerInfo{Name: "a", DaemonID: "01D", Address: "x:1", Token: "t"})
	fake := func(ctx context.Context, addr, tok string, local DialerIdentity) (RepairResponse, error) {
		return RepairResponse{}, ErrUnreachable
	}
	mgr := NewManager(r, fake, DialerIdentity{DaemonID: "self"})
	res, _ := mgr.ReconcileOne(context.Background(), "a")
	if res.OK {
		t.Errorf("expected OK=false")
	}
	if res.Category != CatUnreachable {
		t.Errorf("Category = %v", res.Category)
	}
	if got := r.FindPeerByToken("t").ReconcileStatus; got != StatusDriftReconcileFailed {
		t.Errorf("status = %q, want drift_reconcile_failed", got)
	}
}

func TestReconcileOne_TokenRejectedMarksDriftFailed(t *testing.T) {
	r := mkRegistry(t)
	_ = r.AddPeer(&daemon.PeerInfo{Name: "a", DaemonID: "01D", Address: "x:1", Token: "t"})
	fake := func(ctx context.Context, addr, tok string, local DialerIdentity) (RepairResponse, error) {
		return RepairResponse{}, ErrTokenRejected
	}
	mgr := NewManager(r, fake, DialerIdentity{DaemonID: "self"})
	res, _ := mgr.ReconcileOne(context.Background(), "a")
	if res.OK {
		t.Errorf("expected OK=false")
	}
	if res.Category != CatTokenRejected {
		t.Errorf("Category = %v", res.Category)
	}
	if r.FindPeerByToken("t").ReconcileStatus != StatusDriftReconcileFailed {
		t.Errorf("status not marked drift_reconcile_failed")
	}
}

func TestReconcileOne_TransientErrorDoesNotMark(t *testing.T) {
	r := mkRegistry(t)
	_ = r.AddPeer(&daemon.PeerInfo{Name: "a", DaemonID: "01D", Address: "x:1", Token: "t"})
	fake := func(ctx context.Context, addr, tok string, local DialerIdentity) (RepairResponse, error) {
		return RepairResponse{}, errors.New("some transient bull")
	}
	mgr := NewManager(r, fake, DialerIdentity{DaemonID: "self"})
	res, _ := mgr.ReconcileOne(context.Background(), "a")
	if res.OK {
		t.Errorf("expected OK=false")
	}
	if res.Category != CatOther {
		t.Errorf("Category = %v", res.Category)
	}
	// Transient errors must NOT flip status to drift_reconcile_failed —
	// only unambiguous terminal failures (unreachable, token-rejected)
	// do; otherwise a single network blip would kick users into manual
	// repair.
	if r.FindPeerByToken("t").ReconcileStatus == StatusDriftReconcileFailed {
		t.Errorf("transient error wrongly flipped status")
	}
}

func TestReconcileOne_UnknownPeerReturnsError(t *testing.T) {
	r := mkRegistry(t)
	mgr := NewManager(r, nil, DialerIdentity{})
	_, err := mgr.ReconcileOne(context.Background(), "ghost")
	if err == nil {
		t.Errorf("expected error for unknown peer")
	}
}

// I8 fix regression test: ReconcileAll dispatches for different peers in
// parallel. Uses a DialFunc that blocks 200ms; 4 peers with 4-worker
// parallelism should finish well under the 800ms serial time.
func TestReconcileAll_ParallelDispatchForDifferentPeers(t *testing.T) {
	r := mkRegistry(t)
	const n = 4
	for i := 0; i < n; i++ {
		name := []byte{byte('a' + i)}
		if err := r.AddPeer(&daemon.PeerInfo{
			Name:     string(name),
			DaemonID: "01D" + string(name),
			Address:  "x:1",
			Token:    "t" + string(name),
		}); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	var inflight int32
	var peak int32
	fake := func(ctx context.Context, addr, tok string, local DialerIdentity) (RepairResponse, error) {
		cur := atomic.AddInt32(&inflight, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if cur <= old || atomic.CompareAndSwapInt32(&peak, old, cur) {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
		atomic.AddInt32(&inflight, -1)
		return RepairResponse{DaemonID: "same"}, nil
	}
	mgr := NewManager(r, fake, DialerIdentity{})
	start := time.Now()
	results := mgr.ReconcileAll(context.Background())
	elapsed := time.Since(start)
	if len(results) != n {
		t.Errorf("got %d results, want %d", len(results), n)
	}
	if atomic.LoadInt32(&peak) < 2 {
		t.Errorf("peak inflight = %d; expected >= 2 for parallel dispatch", peak)
	}
	if elapsed > 700*time.Millisecond {
		t.Errorf("wall clock %v suggests serial execution (want < 700ms)", elapsed)
	}
}

// I5 (plan-reviewer) Option A: unit-style replacement for the two-daemon
// resilience scenario. Verifies the boot-time ReconcileAll success path
// end-to-end through the real PeerRegistry (save/load) without needing
// a second in-process daemon. The two-daemon scenario is deferred to
// tests/resilience/ (Task 7.1/7.2) where the full wire path is
// exercised.
func TestReconcileAll_BootTimeDaemonIDRotationHealsRegistry(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "peers.json")
	r, err := daemon.NewPeerRegistry(tmp)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	// Pre-rotation state: we believe B's daemon_id is 01OLD.
	if err := r.AddPeer(&daemon.PeerInfo{
		Name:      "B",
		DaemonID:  "01OLD",
		Address:   "1.2.3.4:7731",
		Token:     "tok-B",
		Transport: "network",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Fake B returns a rotated daemon_id on peer.repair — simulating B
	// having been restarted with a fresh identity but the same address.
	fake := func(ctx context.Context, addr, tok string, local DialerIdentity) (RepairResponse, error) {
		if tok != "tok-B" {
			return RepairResponse{}, ErrTokenRejected
		}
		return RepairResponse{DaemonID: "01NEW", Name: "B"}, nil
	}
	mgr := NewManager(r, fake, DialerIdentity{DaemonID: "01SELF"})
	results := mgr.ReconcileAll(context.Background())

	if len(results) != 1 || !results[0].OK {
		t.Fatalf("ReconcileAll results: %+v", results)
	}

	// The registry has been re-keyed under the new daemon_id.
	got := r.FindPeerByToken("tok-B")
	if got == nil {
		t.Fatalf("peer lookup by token failed after reconcile")
	}
	if got.DaemonID != "01NEW" {
		t.Errorf("DaemonID = %q, want 01NEW", got.DaemonID)
	}
	if got.ReconcileStatus != "" {
		t.Errorf("ReconcileStatus not cleared after successful reconcile: %q", got.ReconcileStatus)
	}

	// Verify re-key persists through a registry reload — simulates a
	// daemon restart picking up the reconciled state.
	r2, err := daemon.NewPeerRegistry(tmp)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got2 := r2.FindPeerByToken("tok-B"); got2 == nil || got2.DaemonID != "01NEW" {
		t.Errorf("re-keyed state did not persist across registry reload: %+v", got2)
	}
}

// B1 (dual-review): an empty daemon_id in the peer.repair response
// previously slipped past the re-key guard, RemovePeer dropped the
// old entry, AddPeer rejected the empty key, and the caller saw
// OK=true with a ghost registry. This verifies the explicit guard
// refuses the response and preserves the old entry.
func TestReconcileOne_EmptyDaemonIDInResponseDoesNotCorruptRegistry(t *testing.T) {
	r := mkRegistry(t)
	if err := r.AddPeer(&daemon.PeerInfo{
		Name:     "alpha",
		DaemonID: "01OLD",
		Address:  "1.2.3.4:7731",
		Token:    "tok",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	fake := func(ctx context.Context, addr, tok string, local DialerIdentity) (RepairResponse, error) {
		return RepairResponse{DaemonID: "", Name: "alpha"}, nil
	}
	mgr := NewManager(r, fake, DialerIdentity{DaemonID: "self"})
	res, err := mgr.ReconcileOne(context.Background(), "alpha")
	if err == nil {
		t.Fatalf("expected error for empty daemon_id response")
	}
	if res.OK {
		t.Errorf("res.OK wrongly true after empty daemon_id response")
	}
	// Old entry must still exist — the re-key must not have fired.
	if got := r.FindPeerByToken("tok"); got == nil || got.DaemonID != "01OLD" {
		t.Errorf("registry corrupted after empty-response rejection: got=%+v", got)
	}
}

// I6 (dual-review): Transport="local" peers skip the dial entirely;
// same-host is strictly stronger than same-subnet. Reconcile should
// short-circuit to OK=true without invoking the DialFunc.
func TestReconcileOne_LocalTransportSkipsDial(t *testing.T) {
	r := mkRegistry(t)
	if err := r.AddPeer(&daemon.PeerInfo{
		Name:            "local-twin",
		DaemonID:        "01LOCAL",
		Transport:       "local",
		RepoPath:        "/tmp/local",
		Token:           "t",
		ReconcileStatus: StatusDriftReconcileFailed, // stale marker
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	dialCalled := false
	fake := func(ctx context.Context, addr, tok string, local DialerIdentity) (RepairResponse, error) {
		dialCalled = true
		return RepairResponse{}, nil
	}
	mgr := NewManager(r, fake, DialerIdentity{})
	res, err := mgr.ReconcileOne(context.Background(), "local-twin")
	if err != nil {
		t.Fatalf("ReconcileOne: %v", err)
	}
	if !res.OK {
		t.Errorf("local-transport peer should report OK=true; got %+v", res)
	}
	if dialCalled {
		t.Errorf("DialFunc invoked for local-transport peer (should short-circuit)")
	}
	// Stale drift marker must be cleared on the local-short-circuit
	// path so `thrum peer list` doesn't falsely flag a healthy peer.
	if got := r.FindPeerByToken("t").ReconcileStatus; got != StatusHealthy {
		t.Errorf("stale drift marker not cleared for local peer: %q", got)
	}
}
