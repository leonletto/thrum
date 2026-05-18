package agenthealth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/agenthealth"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// TestBootPass_RespawnsCrashedPersistentAgents pins the canonical
// happy path: a persistent auto-respawn-enabled agent whose pane is
// gone at boot fires OnPaneGone with detection=restart_reconciliation.
// A second persistent agent whose pane is alive is left untouched.
// Plan §3368-3376 + spec §7.7.
func TestBootPass_RespawnsCrashedPersistentAgents(t *testing.T) {
	registry := &stubRegistry{
		agents: []agent.Agent{
			{AgentID: "docs_bot", Mode: agent.ModePersistent, AutoRespawnEnabled: true},
			{AgentID: "alive_bot", Mode: agent.ModePersistent, AutoRespawnEnabled: true},
		},
	}
	prober := &stubProber{alive: map[string]bool{"docs_bot": false, "alive_bot": true}}
	respawner := &stubRespawner{}

	if err := agenthealth.BootPass(context.Background(), registry, prober, respawner, nil); err != nil {
		t.Fatalf("BootPass: %v", err)
	}
	if len(prober.calls) != 2 {
		t.Errorf("Prober called %d times, want 2 (both persistent agents probed)", len(prober.calls))
	}
	if len(respawner.calls) != 1 {
		t.Fatalf("Respawner called %d times, want 1 (only crashed agent gets respawn evaluation)", len(respawner.calls))
	}
	got := respawner.calls[0]
	if got.agent != "docs_bot" {
		t.Errorf("Respawn called for %q, want docs_bot", got.agent)
	}
	if got.detection != state.DetectionRestartReconciliation {
		t.Errorf("Detection = %q, want %q (boot pass must NOT use HealthCheckTick)",
			got.detection, state.DetectionRestartReconciliation)
	}
}

// TestBootPass_SkipsEphemeralModeAgents pins the defensive mode
// filter: a row with auto_respawn=true + mode=ephemeral (canonical
// §3.3 violation; validation should reject it but a malformed
// pre-v0.11 row could slip through) gets skipped without firing a
// respawn. The skipped agent is logged at warn level — operators
// see the malformed row in daemon logs.
func TestBootPass_SkipsEphemeralModeAgents(t *testing.T) {
	registry := &stubRegistry{
		agents: []agent.Agent{
			{AgentID: "bad_ephemeral", Mode: agent.ModeEphemeral, AutoRespawnEnabled: true},
		},
	}
	// Pane reports gone — without the mode filter this would respawn.
	prober := &stubProber{alive: map[string]bool{"bad_ephemeral": false}}
	respawner := &stubRespawner{}

	if err := agenthealth.BootPass(context.Background(), registry, prober, respawner, nil); err != nil {
		t.Fatalf("BootPass: %v", err)
	}
	if len(prober.calls) != 0 {
		t.Errorf("Prober called %d times, want 0 (mode filter must short-circuit before probe)", len(prober.calls))
	}
	if len(respawner.calls) != 0 {
		t.Errorf("Respawner called %d times, want 0 (canonical §3.3 violation must not respawn)", len(respawner.calls))
	}
}

// TestBootPass_RegistryListError_Surfaces verifies the only hard-
// error path: a DB lookup failure aborts the pass so the caller
// (daemon boot) surfaces it. Without this, a flaky DB would silently
// skip the boot pane-health scan entirely.
func TestBootPass_RegistryListError_Surfaces(t *testing.T) {
	registry := &stubRegistry{err: errors.New("db unreachable")}
	prober := &stubProber{}
	respawner := &stubRespawner{}

	err := agenthealth.BootPass(context.Background(), registry, prober, respawner, nil)
	if err == nil {
		t.Fatalf("expected wrapped registry list error, got nil")
	}
	if len(prober.calls) != 0 {
		t.Errorf("Prober called after registry failure; should not have iterated")
	}
}

// TestBootPass_ProbeError_LogsAndContinues pins the per-agent
// probe-failure isolation: a tmux blip on agent A must not block
// agent B from being checked. Both agents stay un-respawned (probe
// errors aren't proof of crash) but B's path is exercised.
func TestBootPass_ProbeError_LogsAndContinues(t *testing.T) {
	registry := &stubRegistry{
		agents: []agent.Agent{
			{AgentID: "flaky", Mode: agent.ModePersistent, AutoRespawnEnabled: true},
			{AgentID: "healthy", Mode: agent.ModePersistent, AutoRespawnEnabled: true},
		},
	}
	prober := &stubProber{
		alive: map[string]bool{"healthy": true},
		err:   map[string]error{"flaky": errors.New("tmux rpc timeout")},
	}
	respawner := &stubRespawner{}

	if err := agenthealth.BootPass(context.Background(), registry, prober, respawner, nil); err != nil {
		t.Fatalf("BootPass: %v", err)
	}
	if len(prober.calls) != 2 {
		t.Errorf("Prober calls = %d, want 2 (both agents probed)", len(prober.calls))
	}
	if len(respawner.calls) != 0 {
		t.Errorf("Respawner called %d times, want 0 (probe failure must not fire respawn)", len(respawner.calls))
	}
}

// TestBootPass_RespawnError_LogsAndContinues verifies that a
// per-agent OnPaneGone failure (e.g. lifecycle store transient
// error) doesn't block subsequent agents from being evaluated.
func TestBootPass_RespawnError_LogsAndContinues(t *testing.T) {
	registry := &stubRegistry{
		agents: []agent.Agent{
			{AgentID: "first_crash", Mode: agent.ModePersistent, AutoRespawnEnabled: true},
			{AgentID: "second_crash", Mode: agent.ModePersistent, AutoRespawnEnabled: true},
		},
	}
	prober := &stubProber{alive: map[string]bool{"first_crash": false, "second_crash": false}}
	respawner := &stubRespawner{
		err: map[string]error{"first_crash": errors.New("lifecycle append failed")},
	}

	if err := agenthealth.BootPass(context.Background(), registry, prober, respawner, nil); err != nil {
		t.Fatalf("BootPass: %v", err)
	}
	if len(respawner.calls) != 2 {
		t.Errorf("Respawner calls = %d, want 2 (continue past first failure)", len(respawner.calls))
	}
}

// TestBootPass_ContextCancelled_StopsScan verifies the loop honors
// ctx.Done — a daemon shutdown signal mid-pass must abort gracefully
// rather than scan the rest of the agents.
func TestBootPass_ContextCancelled_StopsScan(t *testing.T) {
	registry := &stubRegistry{
		agents: []agent.Agent{
			{AgentID: "a1", Mode: agent.ModePersistent, AutoRespawnEnabled: true},
			{AgentID: "a2", Mode: agent.ModePersistent, AutoRespawnEnabled: true},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before BootPass starts

	prober := &stubProber{}
	respawner := &stubRespawner{}

	if err := agenthealth.BootPass(ctx, registry, prober, respawner, nil); err != nil {
		t.Fatalf("BootPass: %v (cancel must be graceful, not error)", err)
	}
	if len(prober.calls) != 0 {
		t.Errorf("Prober called %d times after ctx cancel; want 0", len(prober.calls))
	}
}

// TestBootPass_EmptyAgentList_IsNoop verifies the safe steady-state
// path: a freshly-deployed daemon with no agents registered yet
// completes the pass without error and without invoking any deps.
func TestBootPass_EmptyAgentList_IsNoop(t *testing.T) {
	registry := &stubRegistry{} // agents: nil
	prober := &stubProber{}
	respawner := &stubRespawner{}

	if err := agenthealth.BootPass(context.Background(), registry, prober, respawner, nil); err != nil {
		t.Fatalf("BootPass: %v", err)
	}
	if registry.calls != 1 {
		t.Errorf("Registry called %d times, want 1", registry.calls)
	}
	if len(prober.calls) != 0 || len(respawner.calls) != 0 {
		t.Errorf("Empty list must not invoke prober or respawner")
	}
}
