package agentdispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/escalation"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// Test fixtures for the Respawner. Lives in package agentdispatch
// (internal) so the test can reach across to package-private
// helpers when needed; the production type is exported so package-
// external tests would also work, but this file's fakes (especially
// the lifecycle-store stub) benefit from package proximity.

// stubRespawnRegistry implements agent.AgentRegistry for respawn
// tests. The set of mutator methods we exercise is narrow:
// SetAutoRespawnDisabledAt on the loop-guard trip path. Other
// methods are no-ops to satisfy the interface.
type stubRespawnRegistry struct {
	agentRow  agent.Agent
	lookupErr error

	lookupCalls         []string
	setDisabledAtCalls  []time.Time
	setDisabledAtAgent  string
	setDisabledAtErr    error
}

func (s *stubRespawnRegistry) Lookup(_ context.Context, name string) (agent.Agent, error) {
	s.lookupCalls = append(s.lookupCalls, name)
	return s.agentRow, s.lookupErr
}
func (s *stubRespawnRegistry) ListAutoRespawnEnabled(_ context.Context) ([]agent.Agent, error) {
	return nil, nil
}
func (s *stubRespawnRegistry) SetAutoRespawnDisabledAt(_ context.Context, name string, at time.Time) error {
	s.setDisabledAtAgent = name
	s.setDisabledAtCalls = append(s.setDisabledAtCalls, at)
	return s.setDisabledAtErr
}
func (s *stubRespawnRegistry) ClearAutoRespawnDisabledAt(_ context.Context, _ string) error {
	return nil
}
func (s *stubRespawnRegistry) SetStateMdParseFailedAt(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (s *stubRespawnRegistry) ClearStateMdParseFailedAt(_ context.Context, _ string) error {
	return nil
}

// stubLifecycleStore is a minimal AgentLifecycleStore for respawn
// tests. Records every Append + answers LoopGuardCount from an
// operator-set counter (testCount). Tests assert on the recorded
// events to verify the canonical append-order invariant
// (crash_detected always; respawn_fired only on success;
// respawn_skipped_loopguard on guard trip).
type stubLifecycleStore struct {
	appended []state.AgentLifecycleEvent
	appendErr error

	loopGuardCount    int
	loopGuardErr      error
	loopGuardKind     state.AgentLifecycleEventKind
	loopGuardAgent    string
	loopGuardWindow   int
	loopGuardCallSeen bool
}

func (s *stubLifecycleStore) Append(_ context.Context, e state.AgentLifecycleEvent) (int64, error) {
	if s.appendErr != nil {
		return 0, s.appendErr
	}
	s.appended = append(s.appended, e)
	return int64(len(s.appended)), nil
}
func (s *stubLifecycleStore) ListByAgent(_ context.Context, _ string, _ int) ([]state.AgentLifecycleEvent, error) {
	return nil, nil
}
func (s *stubLifecycleStore) ListByAgents(_ context.Context, _ []string, _ int) (map[string][]state.AgentLifecycleEvent, error) {
	return map[string][]state.AgentLifecycleEvent{}, nil
}
func (s *stubLifecycleStore) LoopGuardCount(_ context.Context, agentName string, kind state.AgentLifecycleEventKind, windowSeconds int) (int, error) {
	s.loopGuardAgent = agentName
	s.loopGuardKind = kind
	s.loopGuardWindow = windowSeconds
	s.loopGuardCallSeen = true
	return s.loopGuardCount, s.loopGuardErr
}
func (s *stubLifecycleStore) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// stubRestarter records Restart invocations + returns canned
// errors. Used to drive every restart-outcome test path
// (happy, ErrHandlerWiringPending, generic error).
type stubRestarter struct {
	calls     []string
	returnErr error
}

func (s *stubRestarter) Restart(_ context.Context, agentName string) error {
	s.calls = append(s.calls, agentName)
	return s.returnErr
}

// stubEscalationRouter implementation — local to respawn_test.go
// (the one in rollback_test.go is also package-internal but Go
// would flag a duplicate; this one is renamed).
type respawnStubEscalation struct {
	calls []respawnEscalationCall
}

type respawnEscalationCall struct {
	alert   escalation.Alert
	subject string
	body    string
}

func (s *respawnStubEscalation) Route(_ context.Context, alert escalation.Alert, subject, body string) error {
	s.calls = append(s.calls, respawnEscalationCall{alert: alert, subject: subject, body: body})
	return nil
}

// newTestRespawner returns a Respawner wired with happy-path stubs.
// Tests override individual fields after the call to exercise
// specific scenarios.
func newTestRespawner() (*Respawner, *stubRespawnRegistry, *stubLifecycleStore, *stubRestarter, *respawnStubEscalation) {
	reg := &stubRespawnRegistry{
		agentRow: agent.Agent{
			AgentID:            "docs_bot",
			Mode:               agent.ModePersistent,
			Identity:           agent.IdentityLongLived,
			AutoRespawnEnabled: true,
		},
	}
	store := &stubLifecycleStore{}
	restarter := &stubRestarter{}
	router := &respawnStubEscalation{}
	r := &Respawner{
		Registry:       reg,
		LifecycleStore: store,
		Restarter:      restarter,
		Escalation:     router,
	}
	return r, reg, store, restarter, router
}

// TestRespawn_HappyPath_AppendsCrashDetectedThenFiresThenAppendsRespawnFired
// pins the canonical 3-event append sequence on the success path:
// crash_detected ALWAYS (before any gate evaluation), then if the
// gate passes + restart succeeds, respawn_fired is appended. The
// order matters for the audit trail — operators reading
// agent_lifecycle_events should see crash → fire pairs.
func TestRespawn_HappyPath_AppendsCrashDetectedThenFiresThenAppendsRespawnFired(t *testing.T) {
	r, _, store, restarter, _ := newTestRespawner()

	err := r.OnPaneGone(context.Background(), "docs_bot", state.DetectionHealthCheckTick)
	if err != nil {
		t.Fatalf("OnPaneGone err = %v; want nil", err)
	}

	if len(store.appended) != 2 {
		t.Fatalf("appended events = %d; want 2 (crash_detected + respawn_fired)", len(store.appended))
	}
	if store.appended[0].EventKind != state.EventCrashDetected {
		t.Errorf("appended[0].EventKind = %q; want crash_detected", store.appended[0].EventKind)
	}
	if store.appended[0].DetectionMethod != state.DetectionHealthCheckTick {
		t.Errorf("appended[0].DetectionMethod = %q; want health_check_tick",
			store.appended[0].DetectionMethod)
	}
	if store.appended[1].EventKind != state.EventRespawnFired {
		t.Errorf("appended[1].EventKind = %q; want respawn_fired", store.appended[1].EventKind)
	}
	if len(restarter.calls) != 1 || restarter.calls[0] != "docs_bot" {
		t.Errorf("Restart calls = %v; want [docs_bot]", restarter.calls)
	}
}

// TestRespawn_NoAutoRespawn_SkipsButCrashEventStillAppended pins
// the gate-failure path for AutoRespawnEnabled=false: crash_detected
// is appended (observability is unconditional), but no Restart fires
// and no respawn_fired follows. Operator can audit-trail crashes
// against agents that opted out of auto-respawn.
func TestRespawn_NoAutoRespawn_SkipsButCrashEventStillAppended(t *testing.T) {
	r, reg, store, restarter, _ := newTestRespawner()
	reg.agentRow.AutoRespawnEnabled = false

	err := r.OnPaneGone(context.Background(), "docs_bot", state.DetectionHealthCheckTick)
	if err != nil {
		t.Fatalf("OnPaneGone err = %v; want nil (gate skip is not an error)", err)
	}

	if len(store.appended) != 1 || store.appended[0].EventKind != state.EventCrashDetected {
		t.Errorf("appended = %v; want [crash_detected] only", store.appended)
	}
	if len(restarter.calls) != 0 {
		t.Errorf("Restart calls = %d; want 0 (auto_respawn disabled)", len(restarter.calls))
	}
}

// TestRespawn_DisabledAtSet_SkipsButCrashEventStillAppended pins
// the second gate predicate: AutoRespawnDisabledAt non-nil means
// a previous loop-guard trip is in effect. Skip respawn (operator
// must ack-clear before respawns resume), but the new crash IS
// recorded so the audit shows continued instability.
func TestRespawn_DisabledAtSet_SkipsButCrashEventStillAppended(t *testing.T) {
	r, reg, store, restarter, _ := newTestRespawner()
	disabled := time.Now().Add(-time.Hour)
	reg.agentRow.AutoRespawnDisabledAt = &disabled

	if err := r.OnPaneGone(context.Background(), "docs_bot", state.DetectionHealthCheckTick); err != nil {
		t.Fatalf("OnPaneGone err = %v; want nil", err)
	}
	if len(store.appended) != 1 || store.appended[0].EventKind != state.EventCrashDetected {
		t.Errorf("appended = %v; want [crash_detected] only", store.appended)
	}
	if len(restarter.calls) != 0 {
		t.Errorf("Restart calls = %d; want 0 (loop guard previously tripped)", len(restarter.calls))
	}
}

// TestRespawn_StateMdParseFailedAt_SkipsButCrashEventStillAppended
// pins the third gate: state.md parse failure pending operator ack.
// Respawning into a broken state.md would just crash again; the
// operator must clear the banner first.
func TestRespawn_StateMdParseFailedAt_SkipsButCrashEventStillAppended(t *testing.T) {
	r, reg, store, restarter, _ := newTestRespawner()
	failed := time.Now().Add(-30 * time.Minute)
	reg.agentRow.StateMdParseFailedAt = &failed

	if err := r.OnPaneGone(context.Background(), "docs_bot", state.DetectionHealthCheckTick); err != nil {
		t.Fatalf("OnPaneGone err = %v; want nil", err)
	}
	if len(store.appended) != 1 {
		t.Errorf("appended = %v; want [crash_detected] only", store.appended)
	}
	if len(restarter.calls) != 0 {
		t.Errorf("Restart calls = %d; want 0", len(restarter.calls))
	}
}

// TestRespawn_LoopGuardTrips_EscalatesAndDisables pins AC 9.8.5:
// when LoopGuardCount returns count >= escalate_after, the
// canonical trip sequence fires: SetAutoRespawnDisabledAt +
// append respawn_skipped_loopguard event + Route operator
// escalation. No Restart fires.
func TestRespawn_LoopGuardTrips_EscalatesAndDisables(t *testing.T) {
	r, reg, store, restarter, router := newTestRespawner()
	store.loopGuardCount = 3 // hits default escalateAfter

	if err := r.OnPaneGone(context.Background(), "docs_bot", state.DetectionHealthCheckTick); err != nil {
		t.Fatalf("OnPaneGone err = %v; want nil (guard trip is not an error)", err)
	}

	// Loop-guard query was made with the right shape.
	if !store.loopGuardCallSeen {
		t.Error("LoopGuardCount was never called")
	}
	if store.loopGuardKind != state.EventRespawnFired {
		t.Errorf("LoopGuardCount kind = %q; want respawn_fired", store.loopGuardKind)
	}
	if store.loopGuardAgent != "docs_bot" {
		t.Errorf("LoopGuardCount agent = %q; want docs_bot", store.loopGuardAgent)
	}
	if store.loopGuardWindow != defaultRespawnWindowSeconds {
		t.Errorf("LoopGuardCount window = %d; want %d default",
			store.loopGuardWindow, defaultRespawnWindowSeconds)
	}

	// Trip sequence: SetAutoRespawnDisabledAt called once.
	if len(reg.setDisabledAtCalls) != 1 {
		t.Errorf("SetAutoRespawnDisabledAt calls = %d; want 1", len(reg.setDisabledAtCalls))
	}
	if reg.setDisabledAtAgent != "docs_bot" {
		t.Errorf("SetAutoRespawnDisabledAt agent = %q; want docs_bot", reg.setDisabledAtAgent)
	}

	// Two events appended: crash_detected + respawn_skipped_loopguard.
	// NO respawn_fired (Restart never called).
	if len(store.appended) != 2 {
		t.Fatalf("appended = %d events; want 2", len(store.appended))
	}
	if store.appended[1].EventKind != state.EventRespawnSkippedLoopguard {
		t.Errorf("appended[1] = %q; want respawn_skipped_loopguard", store.appended[1].EventKind)
	}
	if !strings.Contains(store.appended[1].Reason, "3 respawns in") {
		t.Errorf("appended[1].Reason = %q; want canonical 'N respawns in Ks' phrasing",
			store.appended[1].Reason)
	}

	// Operator escalation routed.
	if len(router.calls) != 1 {
		t.Fatalf("Route calls = %d; want 1", len(router.calls))
	}
	if router.calls[0].alert.Source != "b-b1.auto_respawn_loop_guard" {
		t.Errorf("Route alert.Source = %q; want b-b1.auto_respawn_loop_guard",
			router.calls[0].alert.Source)
	}
	if !strings.Contains(router.calls[0].body, "docs_bot") {
		t.Errorf("Route body missing agent name: %q", router.calls[0].body)
	}

	// Critical: Restart NEVER called on guard trip.
	if len(restarter.calls) != 0 {
		t.Errorf("Restart calls = %d on guard trip; want 0", len(restarter.calls))
	}
}

// TestRespawn_LoopGuardTrips_NilEscalation_StillDisables pins the
// F2 nil-guard pattern: when Escalation isn't wired (partial-config
// daemon), the trip still records bookkeeping (SetAutoRespawnDisabledAt
// + respawn_skipped_loopguard event) — only the operator alert is
// skipped. Same shape as idleNudgeLoop's Layer-D nil-guard.
func TestRespawn_LoopGuardTrips_NilEscalation_StillDisables(t *testing.T) {
	r, reg, store, _, _ := newTestRespawner()
	r.Escalation = nil // partial-config
	store.loopGuardCount = 3

	if err := r.OnPaneGone(context.Background(), "docs_bot", state.DetectionHealthCheckTick); err != nil {
		t.Fatalf("OnPaneGone err = %v; want nil", err)
	}

	if len(reg.setDisabledAtCalls) != 1 {
		t.Errorf("SetAutoRespawnDisabledAt calls = %d; want 1 (bookkeeping unconditional)",
			len(reg.setDisabledAtCalls))
	}
	if len(store.appended) != 2 || store.appended[1].EventKind != state.EventRespawnSkippedLoopguard {
		t.Errorf("appended = %v; want crash_detected + respawn_skipped_loopguard", store.appended)
	}
}

// TestRespawn_HandlerWiringPending_LogsAndContinues pins the F1
// forward-flag invariant: a Restart that wraps ErrHandlerWiringPending
// (E6.5 Task 42b not yet shipped, or any future placeholder path)
// is benign — log + return nil. Critically: NO respawn_fired is
// appended, so a placeholder-induced miss doesn't pollute the loop
// guard count. The agent is NOT marked as crash-looped.
func TestRespawn_HandlerWiringPending_LogsAndContinues(t *testing.T) {
	r, _, store, restarter, _ := newTestRespawner()
	restarter.returnErr = fmt.Errorf("dispatch %q: %w", "scheduled_agent", ErrHandlerWiringPending)

	err := r.OnPaneGone(context.Background(), "docs_bot", state.DetectionHealthCheckTick)
	if err != nil {
		t.Errorf("OnPaneGone err = %v; want nil (wiring-pending is benign skip)", err)
	}

	// crash_detected appended, but NOT respawn_fired (Restart returned an error).
	if len(store.appended) != 1 || store.appended[0].EventKind != state.EventCrashDetected {
		t.Errorf("appended = %v; want [crash_detected] only", store.appended)
	}

	// Restart was attempted.
	if len(restarter.calls) != 1 {
		t.Errorf("Restart calls = %d; want 1 (attempted once before bail)", len(restarter.calls))
	}
}

// TestRespawn_GenericRestartError_BubblesUp pins the asymmetry
// vs. the F1 wiring-pending case: a non-ErrHandlerWiringPending
// error from Restart surfaces as a wrapped error to the caller
// (the pane-health internal job decides retry policy). The
// agent is NOT marked respawn_fired because the respawn DIDN'T
// fire — the loop guard counts actual fires, not attempts.
func TestRespawn_GenericRestartError_BubblesUp(t *testing.T) {
	r, _, store, _, _ := newTestRespawner()
	restartErr := errors.New("tmux socket gone")
	r.Restarter = &stubRestarter{returnErr: restartErr}

	err := r.OnPaneGone(context.Background(), "docs_bot", state.DetectionHealthCheckTick)
	if !errors.Is(err, restartErr) {
		t.Errorf("err = %v; want wraps %v", err, restartErr)
	}

	// crash_detected appended, but NOT respawn_fired (restart failed).
	if len(store.appended) != 1 || store.appended[0].EventKind != state.EventCrashDetected {
		t.Errorf("appended = %v; want [crash_detected] only on restart failure",
			store.appended)
	}
}

// TestRespawn_CustomLoopGuardKnobs_HonorsAgentConfig pins the
// per-agent configurability of EscalateAfter + WindowSeconds: a
// canonical agent may override defaults (e.g. critical agent with
// EscalateAfter=5, WindowSeconds=300 for a tighter window). The
// Respawner reads these off Agent.AutoRespawn and applies them
// instead of the canonical defaults.
func TestRespawn_CustomLoopGuardKnobs_HonorsAgentConfig(t *testing.T) {
	r, reg, store, _, _ := newTestRespawner()
	reg.agentRow.AutoRespawn = agent.AutoRespawnConfig{
		EscalateAfter: 5,
		WindowSeconds: 300,
	}
	// 4 respawns in window: BELOW the custom 5-threshold → no trip yet.
	store.loopGuardCount = 4

	if err := r.OnPaneGone(context.Background(), "docs_bot", state.DetectionHealthCheckTick); err != nil {
		t.Fatalf("OnPaneGone err = %v; want nil", err)
	}
	if store.loopGuardWindow != 300 {
		t.Errorf("LoopGuardCount window = %d; want 300 (custom override)", store.loopGuardWindow)
	}
	// Below custom threshold → respawn fires.
	if len(store.appended) != 2 || store.appended[1].EventKind != state.EventRespawnFired {
		t.Errorf("appended = %v; want [crash_detected, respawn_fired] (count=4 < 5)",
			store.appended)
	}
}

// TestRespawn_NoRestarterWired_ReturnsError pins the defensive
// no-restarter path: a partial-config daemon where Restarter is
// nil cannot fire respawns, so surface a clear error rather than
// silently dropping respawn cycles. Distinct from ErrHandlerWiringPending
// which is a known-transient state; nil Restarter is a configuration
// bug worth surfacing.
func TestRespawn_NoRestarterWired_ReturnsError(t *testing.T) {
	r, _, _, _, _ := newTestRespawner()
	r.Restarter = nil

	err := r.OnPaneGone(context.Background(), "docs_bot", state.DetectionHealthCheckTick)
	if err == nil {
		t.Fatal("expected error when Restarter is nil; got nil")
	}
	if !strings.Contains(err.Error(), "no Restarter") {
		t.Errorf("err = %q; want substring 'no Restarter'", err.Error())
	}
}

// TestRespawn_RegistryLookupError_BubblesUp pins the lookup-failure
// path: a registry error other than "agent not found" surfaces as
// a wrapped error so callers can choose retry policy. crash_detected
// IS still appended (it ran before the lookup).
func TestRespawn_RegistryLookupError_BubblesUp(t *testing.T) {
	r, reg, store, _, _ := newTestRespawner()
	lookupErr := errors.New("sqlite busy")
	reg.lookupErr = lookupErr

	err := r.OnPaneGone(context.Background(), "docs_bot", state.DetectionHealthCheckTick)
	if !errors.Is(err, lookupErr) {
		t.Errorf("err = %v; want wraps %v", err, lookupErr)
	}
	if len(store.appended) != 1 {
		t.Errorf("appended = %d; want 1 (crash_detected appended before lookup)",
			len(store.appended))
	}
}

// TestRespawn_CrashDetectedAppendError_BubblesUp pins the
// early-failure path in step 1 of OnPaneGone: when LifecycleStore.
// Append fails on the crash_detected write (e.g., DB locked,
// disk full), OnPaneGone returns the wrapped error WITHOUT
// proceeding to the registry lookup or downstream steps. This is
// the only error path where the canonical "crash_detected
// always" invariant is broken — by design, because nothing else
// can run if the audit-trail write itself failed.
func TestRespawn_CrashDetectedAppendError_BubblesUp(t *testing.T) {
	r, reg, store, restarter, _ := newTestRespawner()
	appendErr := errors.New("disk full")
	store.appendErr = appendErr

	err := r.OnPaneGone(context.Background(), "docs_bot", state.DetectionHealthCheckTick)
	if !errors.Is(err, appendErr) {
		t.Errorf("err = %v; want wraps %v", err, appendErr)
	}
	// Registry lookup must NOT fire — early return is the canonical
	// fail-fast on audit-trail write failure.
	if len(reg.lookupCalls) != 0 {
		t.Errorf("Registry.Lookup calls = %d; want 0 (early-return on append fail)", len(reg.lookupCalls))
	}
	// Restart must NOT fire either.
	if len(restarter.calls) != 0 {
		t.Errorf("Restart calls = %d; want 0", len(restarter.calls))
	}
}
