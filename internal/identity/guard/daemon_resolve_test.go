package guard

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestDaemonResolve_PeercredTrustedNoClaim(t *testing.T) {
	cfg := DefaultConfig()
	got, err := DaemonResolve(cfg, DaemonResolveRequest{
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
	got, err := DaemonResolve(cfg, DaemonResolveRequest{
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
	_, err := DaemonResolve(cfg, DaemonResolveRequest{
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
	_, err := DaemonResolve(cfg, DaemonResolveRequest{
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
	_, err := DaemonResolve(cfg, DaemonResolveRequest{
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
	got, err := DaemonResolve(cfg, DaemonResolveRequest{}, logger)
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
	got, err := DaemonResolve(cfg, DaemonResolveRequest{
		CallerAgentID: "impl_legacy",
	}, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.AgentID != "impl_legacy" {
		t.Errorf("AgentID = %q, want impl_legacy", got.AgentID)
	}
}
