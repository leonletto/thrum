package guard

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestDaemonResolve_PeercredTrustedNoClaim(t *testing.T) {
	cfg := DefaultConfig()
	got, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{
		PeercredAgentID: "impl_alpha",
		PeercredRan:     true,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.AgentID != "impl_alpha" {
		t.Errorf("AgentID = %q, want impl_alpha", got.AgentID)
	}
}

func TestDaemonResolve_PeercredTrustedMatchingClaim(t *testing.T) {
	cfg := DefaultConfig()
	got, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{
		CallerAgentID:   "impl_alpha",
		PeercredAgentID: "impl_alpha",
		PeercredRan:     true,
	}, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.AgentID != "impl_alpha" {
		t.Errorf("AgentID = %q, want impl_alpha", got.AgentID)
	}
}

func TestDaemonResolve_PeercredMismatchRejected(t *testing.T) {
	cfg := DefaultConfig()
	_, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{
		CallerAgentID:   "impl_beta", // forged
		PeercredAgentID: "impl_alpha",
		PeercredRan:     true,
	}, nil)
	if err == nil {
		t.Fatal("want error on forged CallerAgentID")
	}
	var gErr *Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *guard.Error, got %T: %v", err, err)
	}
	if gErr.Reason != "identity_mismatch" {
		t.Errorf("reason = %q, want identity_mismatch", gErr.Reason)
	}
}

func TestDaemonResolve_PeercredAnonymousRejected(t *testing.T) {
	cfg := DefaultConfig()
	_, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{
		CallerAgentID: "whatever_claim",
		PeercredRan:   true,
		// PeercredAgentID empty → anonymous
	}, nil)
	if err == nil {
		t.Fatal("want error on anonymous caller")
	}
	var gErr *Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *guard.Error, got %T: %v", err, err)
	}
	if gErr.Reason != "anonymous_mutating_rpc" {
		t.Errorf("reason = %q, want anonymous_mutating_rpc", gErr.Reason)
	}
}

func TestDaemonResolve_NoPeercredStrictEmptyClaim(t *testing.T) {
	cfg := DefaultConfig() // UnauthenticatedRPC = strict
	_, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{
		// CallerAgentID empty, PeercredRan false
	}, nil)
	if err == nil {
		t.Fatal("want G3 error on empty CallerAgentID in strict mode")
	}
	var gErr *Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *guard.Error, got %T: %v", err, err)
	}
	if gErr.Guard != "unauthenticated_rpc" {
		t.Errorf("guard = %q, want unauthenticated_rpc", gErr.Guard)
	}
}

func TestDaemonResolve_NoPeercredWarnEmptyClaim(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UnauthenticatedRPC = ModeWarn
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	got, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{}, logger)
	if err != nil {
		t.Fatalf("warn mode should not error, got %v", err)
	}
	if got.AgentID != "" {
		t.Errorf("AgentID = %q, want empty in warn-mode fall-through", got.AgentID)
	}
	if !strings.Contains(buf.String(), "unauthenticated_rpc") {
		t.Errorf("warn mode should emit guard slog event, got %q", buf.String())
	}
}

func TestDaemonResolve_NoPeercredAcceptsClaim(t *testing.T) {
	cfg := DefaultConfig()
	got, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{
		CallerAgentID: "impl_legacy",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.AgentID != "impl_legacy" {
		t.Errorf("AgentID = %q, want impl_legacy", got.AgentID)
	}
}

// TestDaemonResolve_SharedWorktreeTrustsClaim — thrum-0pos. When
// peercred picked one agent but the caller claims a DIFFERENT agent
// AND the claimed agent is also registered in the peercred-resolved
// worktree, DaemonResolve trusts the claim. This disambiguates
// multi-agent worktrees where peercred's CWD → worktree match is
// ambiguous (e.g. E2E harnesses, peer-bridge proxies).
func TestDaemonResolve_SharedWorktreeTrustsClaim(t *testing.T) {
	cfg := DefaultConfig()
	registered := map[string]map[string]bool{
		"/tmp/shared-worktree": {
			"agent_a": true,
			"agent_b": true,
		},
	}
	checker := func(agentID, worktree string) bool {
		return registered[worktree][agentID]
	}
	got, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{ //nolint:gosec // G101 false positive: "agent_a"/"agent_b" are test-only identities
		CallerAgentID:     "agent_b", // CLI claims agent_b
		PeercredAgentID:   "agent_a", // peercred arbitrarily picked agent_a
		PeercredRan:       true,
		PeercredWorktree:  "/tmp/shared-worktree",
		IsAgentInWorktree: checker,
	}, nil)
	if err != nil {
		t.Fatalf("shared-worktree claim must be trusted, got err: %v", err)
	}
	if got.AgentID != "agent_b" {
		t.Errorf("AgentID = %q, want agent_b (the CLI claim)", got.AgentID)
	}
}

// TestDaemonResolve_CrossWorktreeForgeryRejected — thrum-0pos guard
// still denies when the claimed agent is NOT registered in the
// peercred-resolved worktree. This preserves the forgery defense:
// a process in worktree X cannot claim to be an agent from worktree
// Y just by supplying a CallerAgentID hint.
func TestDaemonResolve_CrossWorktreeForgeryRejected(t *testing.T) {
	cfg := DefaultConfig()
	checker := func(agentID, worktree string) bool {
		// agent_b is NOT registered in /tmp/peercred-worktree.
		return agentID == "agent_a" && worktree == "/tmp/peercred-worktree"
	}
	_, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{ //nolint:gosec // G101 false positive: "agent_a"/"agent_b" are test-only identities
		CallerAgentID:     "agent_b", // cross-worktree claim
		PeercredAgentID:   "agent_a",
		PeercredRan:       true,
		PeercredWorktree:  "/tmp/peercred-worktree",
		IsAgentInWorktree: checker,
	}, nil)
	if err == nil {
		t.Fatal("want error — claim for agent not registered in peercred worktree must be rejected")
	}
	var gErr *Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *guard.Error, got %T: %v", err, err)
	}
	if gErr.Reason != "identity_mismatch" {
		t.Errorf("reason = %q, want identity_mismatch", gErr.Reason)
	}
}

// TestDaemonResolve_SharedWorktreeNilCheckerStrict — when the
// checker is not wired (tests, non-unix transports), mismatch falls
// through to the strict deny path. This preserves backward
// compatibility with all existing DaemonResolve callers.
func TestDaemonResolve_SharedWorktreeNilCheckerStrict(t *testing.T) {
	cfg := DefaultConfig()
	_, err := DaemonResolve(context.Background(), cfg, DaemonResolveRequest{ //nolint:gosec // G101 false positive: "agent_a"/"agent_b" are test-only identities
		CallerAgentID:    "agent_b",
		PeercredAgentID:  "agent_a",
		PeercredRan:      true,
		PeercredWorktree: "/tmp/shared",
		// IsAgentInWorktree: nil
	}, nil)
	if err == nil {
		t.Fatal("want error — nil checker must preserve strict deny")
	}
}
