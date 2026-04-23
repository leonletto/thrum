package guard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// seedIdentityFile drops a minimal .thrum/identities/<name>.json into
// dir carrying the given AgentPID so DaemonResolve's chain walk has
// something to match against.
func seedIdentityFile(t *testing.T, dir, name string, agentPID int) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{
		"agent":     map[string]any{"name": name},
		"agent_pid": agentPID,
	})
	if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0o600); err != nil {
		t.Fatalf("seed identity: %v", err)
	}
}

// stubRuntimeAncestorPresent forces the chain-walk's runtime-ancestor
// precondition to succeed for the duration of the test. Without this,
// tests whose process tree lacks an AI runtime ancestor (e.g. bare GH
// Actions runners) hit the §Rule #4‴ step 2 fallthrough and return
// "" before the chain walk runs. The PID value is arbitrary — only
// non-zero matters.
func stubRuntimeAncestorPresent(t *testing.T) {
	t.Helper()
	prev := closestRuntimeAncestorFn
	closestRuntimeAncestorFn = func(_ context.Context, _ int) (int, string, error) {
		return 1, "claude", nil
	}
	t.Cleanup(func() { closestRuntimeAncestorFn = prev })
}

// TestDaemonResolve_ChainWalk_FindsOwner proves that on the anonymous-
// peercred branch, if the connecting process's ancestor chain contains
// a registered agent's AgentPID AND that PID is alive, DaemonResolve
// returns that agent as the authenticated caller rather than failing
// closed. This is Rule #4‴ ancestor-chain authentication — the
// daemon-side complement to CWD-based peercred matching.
func TestDaemonResolve_ChainWalk_FindsOwner(t *testing.T) {
	stubRuntimeAncestorPresent(t)
	identitiesDir := t.TempDir()
	self := os.Getpid() // definitely alive, in its own chain
	seedIdentityFile(t, identitiesDir, "impl_self", self)

	cfg := DefaultConfig()
	got, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{
		PeercredRan:   true, // anonymous branch: peercred ran but CWD didn't match
		ConnectingPID: self,
		IdentitiesDir: identitiesDir,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.AgentID != "impl_self" {
		t.Errorf("AgentID = %q, want impl_self", got.AgentID)
	}
}

// TestDaemonResolve_ChainWalk_DeadPIDFallsClosed verifies self-heal
// cross-verify: an identity file's AgentPID appearing in the chain
// is NOT trusted on its own — liveness must pass before declaring
// the chain match authentic. Stale files from a crashed agent should
// not authenticate a fresh caller.
func TestDaemonResolve_ChainWalk_DeadPIDFallsClosed(t *testing.T) {
	identitiesDir := t.TempDir()
	seedIdentityFile(t, identitiesDir, "impl_stale", 999999) // dead PID

	cfg := DefaultConfig()
	_, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{
		PeercredRan:   true,
		ConnectingPID: os.Getpid(), // live, but chain walk won't find 999999 anyway
		IdentitiesDir: identitiesDir,
	}, nil)
	if err == nil {
		t.Fatal("want anonymous_mutating_rpc error: stale identity AgentPID not in chain")
	}
	var gErr *Error
	if !errors.As(err, &gErr) || gErr.Reason != "anonymous_mutating_rpc" {
		t.Errorf("want anonymous_mutating_rpc, got %v", err)
	}
}

// TestDaemonResolve_ChainWalk_MismatchClaimRejected proves that when
// the chain walk matches agent X but the request claims CallerAgentID=Y,
// we reject with identity_mismatch — forgery defense applies to the
// chain-authenticated path too, not just peercred.
func TestDaemonResolve_ChainWalk_MismatchClaimRejected(t *testing.T) {
	stubRuntimeAncestorPresent(t)
	identitiesDir := t.TempDir()
	self := os.Getpid()
	seedIdentityFile(t, identitiesDir, "impl_self", self)

	cfg := DefaultConfig()
	_, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{
		CallerAgentID: "impersonator", // forged
		PeercredRan:   true,
		ConnectingPID: self,
		IdentitiesDir: identitiesDir,
	}, nil)
	if err == nil {
		t.Fatal("want identity_mismatch error")
	}
	var gErr *Error
	if !errors.As(err, &gErr) || gErr.Reason != "identity_mismatch" {
		t.Errorf("want identity_mismatch, got %v", err)
	}
}

// TestDaemonResolve_ChainWalk_NoConnectingPIDFallsClosed confirms that
// when ConnectingPID is zero (tests / non-unix transports) and no CWD
// match exists, we still fail closed on mutating RPCs — the chain-
// walk is an ADDITIONAL authentication path, not a bypass.
func TestDaemonResolve_ChainWalk_NoConnectingPIDFallsClosed(t *testing.T) {
	cfg := DefaultConfig()
	_, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{
		PeercredRan:   true,
		ConnectingPID: 0,
		IdentitiesDir: t.TempDir(),
	}, nil)
	if err == nil {
		t.Fatal("want anonymous_mutating_rpc when ConnectingPID absent")
	}
}

// TestDaemonResolve_WarnModeAnonymousStillDenied pins the intentional
// tightening beyond G3 warn-mode semantics: when peercred ran AND the
// chain walk produced no live match, we hard-deny regardless of
// UnauthenticatedRPC mode. Kernel-level evidence of an unregistered
// caller is stronger than an absent CallerAgentID on a non-peercred
// transport, so the warn-mode "log-and-continue" fallback does not
// extend to this path.
func TestDaemonResolve_WarnModeAnonymousStillDenied(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UnauthenticatedRPC = ModeWarn
	_, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{
		PeercredRan:   true,
		ConnectingPID: os.Getpid(),
		IdentitiesDir: t.TempDir(), // empty dir: no identities to match
	}, nil)
	if err == nil {
		t.Fatal("want hard-deny even in warn mode when peercred anonymous + chain empty")
	}
	var gErr *Error
	if !errors.As(err, &gErr) || gErr.Reason != "anonymous_mutating_rpc" {
		t.Errorf("want anonymous_mutating_rpc in warn mode, got %v", err)
	}
}

// TestDaemonResolve_ChainWalk_NoRuntimeAncestor_DeniesEvenWithPIDMatch
// enforces spec §Rule #4‴ step 2: the runtime-ancestor precondition.
// A caller without any AI-runtime ancestor (bare shell, cron, script)
// must fall through to anonymous even if a registered agent's
// AgentPID happens to appear in its chain.
//
// Test harness: PID 1 (init/launchd) has no runtime ancestor by
// definition — it's the root of the process tree. A seeded identity
// with AgentPID=1 makes ChainContains(chain_of_1, 1) succeed AND
// process.IsRunning(1) succeed, so the PID+liveness combination
// alone would authenticate. The runtime-ancestor precondition is
// the only backstop and must fire here.
func TestDaemonResolve_ChainWalk_NoRuntimeAncestor_DeniesEvenWithPIDMatch(t *testing.T) {
	identitiesDir := t.TempDir()
	seedIdentityFile(t, identitiesDir, "impl_init", 1)

	cfg := DefaultConfig()
	_, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{
		PeercredRan:   true,
		ConnectingPID: 1, // init: no runtime ancestor possible
		IdentitiesDir: identitiesDir,
	}, nil)
	if err == nil {
		t.Fatal("want hard-deny: PID 1 has no AI-runtime ancestor")
	}
	var gErr *Error
	if !errors.As(err, &gErr) || gErr.Reason != "anonymous_mutating_rpc" {
		t.Errorf("want anonymous_mutating_rpc (runtime-ancestor precondition), got %v", err)
	}
}
