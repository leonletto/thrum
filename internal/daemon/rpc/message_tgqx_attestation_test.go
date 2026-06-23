package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// thrum-tgqx E2 — daemon-side fail-open closure for message.list (HandleList).
// These tests pin the identity-guard refusal behavior AND the legitimate
// fail-open paths that must keep working. The fail-open they close: a
// stale/mismatched identity used to slip through resolveAgentOnly (swallowed to
// "") and fall through to the untrusted caller-supplied for_agent filter,
// exposing another worktree's inbox.
//
// Release-line note: these handlers take params json.RawMessage (the legacy
// signature), so requests are marshaled via tgqxJSON before the call. The
// behavior under test is identical to the typed-request dev line.

func tgqxJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return b
}

// newTGQXHandler builds a MessageHandler over a fresh temp state and registers
// one real agent so role lookups + identity resolution have something to read.
func newTGQXHandler(t *testing.T, realAgent, realRole string) (*MessageHandler, *state.State) {
	t.Helper()
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	st, err := state.NewState(thrumDir, thrumDir, "r_tgqx", "")
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Register the real agent via the legitimate peercred-bootstrap path.
	regCtx := peercred.WithIdentity(context.Background(), &peercred.ResolvedIdentity{
		AgentID:  realAgent,
		Worktree: tmpDir,
		PID:      os.Getpid(),
	})
	if _, err := NewAgentHandler(st).HandleRegister(regCtx, tgqxJSON(t, RegisterRequest{
		Name:     realAgent,
		Role:     realRole,
		Module:   "core",
		AgentPID: os.Getpid(),
	})); err != nil {
		t.Fatalf("register %s: %v", realAgent, err)
	}
	return NewMessageHandler(st), st
}

// AC.1 — a peercred-verified caller that asserts a DIFFERENT caller_agent_id
// (cross-worktree forgery) is refused; no rows returned.
func TestHandleList_ForgedCallerAgentID_Refused(t *testing.T) {
	h, _ := newTGQXHandler(t, "agent_real", "implementer")

	// peercred resolves to agent_real, but the RPC frame claims agent_forged.
	// agent_forged is NOT bound to this worktree, so the shared-worktree
	// fallback does not rescue it → DaemonResolve returns *guard.Error.
	ctx := peercred.WithIdentity(context.Background(), &peercred.ResolvedIdentity{
		AgentID:  "agent_real",
		Worktree: t.TempDir(),
		PID:      os.Getpid(),
	})
	resp, err := h.HandleList(ctx, tgqxJSON(t, ListMessagesRequest{
		CallerAgentID: "agent_forged",
		ForAgent:      "agent_forged",
	}))
	if err == nil {
		t.Fatalf("expected identity-guard refusal for forged caller_agent_id, got nil err (resp=%+v)", resp)
	}
	if resp != nil {
		t.Errorf("expected nil response on refusal, got %+v", resp)
	}
}

// AC.3 / AC.6 — when peercred did NOT run (WebSocket / cross-host peer /
// web-UI), the for_agent attestation is skipped entirely; the request proceeds
// (fail-open preserved). This is the path web-UI inbox loads depend on.
func TestHandleList_NoPeercred_FailsOpen(t *testing.T) {
	h, _ := newTGQXHandler(t, "agent_real", "implementer")

	// No peercred.WithIdentity → FromContext returns (nil, false) → PeercredRan
	// is false → resolveAgentOnly returns ("", nil), attestation skipped.
	resp, err := h.HandleList(context.Background(), tgqxJSON(t, ListMessagesRequest{
		CallerAgentID: "whoever",
		ForAgent:      "agent_real",
	}))
	if err != nil {
		t.Fatalf("non-peercred caller must NOT be refused (fail-open preserved), got: %v", err)
	}
	if resp == nil {
		t.Fatal("expected a response for the non-peercred path")
	}
}

// AC.7 — a peercred-verified caller requesting its OWN inbox (caller_agent_id
// matches peercred AND for_agent matches) is allowed.
func TestHandleList_OwnInbox_Allowed(t *testing.T) {
	h, _ := newTGQXHandler(t, "agent_real", "implementer")

	ctx := peercred.WithIdentity(context.Background(), &peercred.ResolvedIdentity{
		AgentID:  "agent_real",
		Worktree: t.TempDir(),
		PID:      os.Getpid(),
	})
	resp, err := h.HandleList(ctx, tgqxJSON(t, ListMessagesRequest{
		CallerAgentID: "agent_real",
		ForAgent:      "agent_real",
	}))
	if err != nil {
		t.Fatalf("caller requesting its own inbox must be allowed, got: %v", err)
	}
	if resp == nil {
		t.Fatal("expected a response for the own-inbox path")
	}
}

// AC.5 — a peercred-verified caller whose identity is fine (caller_agent_id
// matches peercred) but who requests for_agent = a DIFFERENT agent is refused
// (an agent may not read another agent's inbox; only user: impersonators may).
func TestHandleList_ForAgentContradictsPeercred_Refused(t *testing.T) {
	h, _ := newTGQXHandler(t, "agent_real", "implementer")

	ctx := peercred.WithIdentity(context.Background(), &peercred.ResolvedIdentity{
		AgentID:  "agent_real",
		Worktree: t.TempDir(),
		PID:      os.Getpid(),
	})
	resp, err := h.HandleList(ctx, tgqxJSON(t, ListMessagesRequest{
		CallerAgentID: "agent_real",
		ForAgent:      "some_other_agent",
	}))
	if err == nil {
		t.Fatalf("expected refusal when peercred caller requests another agent's for_agent, got nil (resp=%+v)", resp)
	}
	if !strings.Contains(err.Error(), "identity guard") {
		t.Errorf("expected identity-guard refusal message, got: %v", err)
	}
}

// AC.8 — for_agent_role attestation: a peercred-verified caller requesting a
// role group that is NOT its own role is refused.
func TestHandleList_ForAgentRoleMismatch_Refused(t *testing.T) {
	h, _ := newTGQXHandler(t, "agent_real", "implementer")

	ctx := peercred.WithIdentity(context.Background(), &peercred.ResolvedIdentity{
		AgentID:  "agent_real",
		Worktree: t.TempDir(),
		PID:      os.Getpid(),
	})
	resp, err := h.HandleList(ctx, tgqxJSON(t, ListMessagesRequest{
		CallerAgentID: "agent_real",
		ForAgentRole:  "coordinator", // agent_real is an implementer
	}))
	if err == nil {
		t.Fatalf("expected refusal when peercred caller requests a non-matching for_agent_role, got nil (resp=%+v)", resp)
	}
	if !strings.Contains(err.Error(), "identity guard") {
		t.Errorf("expected identity-guard refusal message, got: %v", err)
	}
}

// AC.8 (positive) — a peercred-verified caller requesting its OWN role group is
// allowed.
func TestHandleList_ForAgentRoleMatchesOwn_Allowed(t *testing.T) {
	h, _ := newTGQXHandler(t, "agent_real", "implementer")

	ctx := peercred.WithIdentity(context.Background(), &peercred.ResolvedIdentity{
		AgentID:  "agent_real",
		Worktree: t.TempDir(),
		PID:      os.Getpid(),
	})
	resp, err := h.HandleList(ctx, tgqxJSON(t, ListMessagesRequest{
		CallerAgentID: "agent_real",
		ForAgentRole:  "implementer",
	}))
	if err != nil {
		t.Fatalf("caller requesting its own role group must be allowed, got: %v", err)
	}
	if resp == nil {
		t.Fatal("expected a response for the own-role path")
	}
}

// AC.10 — validateImpersonation honors the request context (Task 5 ctx fix). A
// pre-cancelled context must cancel the agent-exists DB query rather than
// succeeding via a detached context.Background(). agent_real exists, so without
// the ctx fix this would return nil; with it, the cancelled query errors.
func TestValidateImpersonation_RespectsContextCancellation(t *testing.T) {
	h, _ := newTGQXHandler(t, "agent_real", "implementer")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call so the DB query observes it

	err := h.validateImpersonation(ctx, "user:someone", "agent_real")
	if err == nil {
		t.Fatal("expected an error from the cancelled context, got nil (ctx not threaded into the query?)")
	}
}

// B1/I2 — anonymous-peercred BARE read (no for_agent / for_agent_role) must be
// ALLOWED. peercred.WithIdentity(ctx, nil) is the PRODUCTION anonymous path:
// FromContext returns (nil, true) — peercred ran but resolved to no agent.
// DaemonResolve then yields a non-identity_mismatch guard reason
// (anonymous_mutating_rpc / no_caller_agent_id), which resolveAgentOnly maps to
// ("", nil) after the B1 fix so the bare read proceeds (message.list is in
// server.go anonymousAllowedMethods). Before the fix (errors.As type-only) this
// was refused — over-closing designed anonymous bootstrap reads. This is the
// regression pin via the real WithIdentity(ctx,nil) path, NOT context.Background().
func TestHandleList_AnonymousPeercred_BareRead_Allowed(t *testing.T) {
	h, _ := newTGQXHandler(t, "agent_real", "implementer")

	// (nil, true) — peercred ran, no match. No caller_agent_id claimed (genuine
	// anonymous, not a forgery), no for_agent/for_agent_role (bare read).
	ctx := peercred.WithIdentity(context.Background(), nil)
	resp, err := h.HandleList(ctx, tgqxJSON(t, ListMessagesRequest{}))
	if err != nil {
		t.Fatalf("anonymous-peercred bare read must be ALLOWED (anonymousAllowedMethods), got: %v", err)
	}
	if resp == nil {
		t.Fatal("expected a response for the anonymous-peercred bare-read path")
	}
}

// B1 complementary gap — an anonymous-peercred caller that supplies for_agent
// must STILL be REFUSED. The bare-read relaxation above only relaxes when no
// impersonation field is present; the for_agent attestation gates on peercredRan
// ALONE (not currentAgentID != ""), so an anonymous caller (currentAgentID == "")
// requesting another agent's inbox hits validateImpersonation(ctx, "", victim),
// which refuses the empty (non-user:) caller. Without dropping the
// currentAgentID != "" gate this would leak.
func TestHandleList_AnonymousPeercred_ForAgent_Refused(t *testing.T) {
	h, _ := newTGQXHandler(t, "agent_real", "implementer")

	ctx := peercred.WithIdentity(context.Background(), nil)
	resp, err := h.HandleList(ctx, tgqxJSON(t, ListMessagesRequest{
		ForAgent: "agent_real",
	}))
	if err == nil {
		t.Fatalf("anonymous-peercred caller requesting for_agent must be REFUSED, got nil (resp=%+v)", resp)
	}
	if !strings.Contains(err.Error(), "identity guard") {
		t.Errorf("expected identity-guard refusal message, got: %v", err)
	}
}

// B1 complementary gap — an anonymous-peercred caller that supplies
// for_agent_role must STILL be REFUSED. The role lookup keys on the empty
// currentAgentID, which finds no row → roleErr → fail-closed refusal.
func TestHandleList_AnonymousPeercred_ForAgentRole_Refused(t *testing.T) {
	h, _ := newTGQXHandler(t, "agent_real", "implementer")

	ctx := peercred.WithIdentity(context.Background(), nil)
	resp, err := h.HandleList(ctx, tgqxJSON(t, ListMessagesRequest{
		ForAgentRole: "implementer",
	}))
	if err == nil {
		t.Fatalf("anonymous-peercred caller requesting for_agent_role must be REFUSED, got nil (resp=%+v)", resp)
	}
	if !strings.Contains(err.Error(), "identity guard") {
		t.Errorf("expected identity-guard refusal message, got: %v", err)
	}
}

// AC.4 — HandleOutbox propagates the identity-guard ownership violation rather
// than absorbing it to "" and falling through.
func TestHandleOutbox_ForgedCaller_Refused(t *testing.T) {
	h, _ := newTGQXHandler(t, "agent_real", "implementer")

	ctx := peercred.WithIdentity(context.Background(), &peercred.ResolvedIdentity{
		AgentID:  "agent_real",
		Worktree: t.TempDir(),
		PID:      os.Getpid(),
	})
	if _, err := h.HandleOutbox(ctx, tgqxJSON(t, OutboxRequest{CallerAgentID: "agent_forged"})); err == nil {
		t.Fatal("expected identity-guard refusal for forged caller in HandleOutbox, got nil")
	}
}

// AC.4 — HandleDeleteByAgent propagates the identity-guard ownership violation.
func TestHandleDeleteByAgent_ForgedCaller_Refused(t *testing.T) {
	h, _ := newTGQXHandler(t, "agent_real", "implementer")

	ctx := peercred.WithIdentity(context.Background(), &peercred.ResolvedIdentity{
		AgentID:  "agent_real",
		Worktree: t.TempDir(),
		PID:      os.Getpid(),
	})
	if _, err := h.HandleDeleteByAgent(ctx, tgqxJSON(t, DeleteByAgentRequest{
		AgentID:       "agent_forged",
		CallerAgentID: "agent_forged",
	})); err == nil {
		t.Fatal("expected identity-guard refusal for forged caller in HandleDeleteByAgent, got nil")
	}
}
