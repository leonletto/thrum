package backstop

import (
	"context"
	"testing"
	"time"
)

// TestTick_ResidencyFilter is the thrum-wo2z regression: the agents table
// includes SYNCED REMOTE registrations (sync replicates them and refreshes
// last_seen_at), so the scan's alive-window does NOT imply local residency.
// Pre-fix, a delivery for a remote-resident recipient synced into the local DB
// made the backstop nudge a local session every tick, forever (the leonair
// 15-minute phantom-wake metronome: feeders impl_backend→coord_remote_
// thrum_agents and coordinator_main→researcher_memories, neither resident).
// The scan must apply the recipient-residency predicate: only dispatch for
// recipients resident on THIS daemon.
func TestTick_ResidencyFilter(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	fake := &fakeDispatcher{}

	// Both agents are alive (synced last_seen_at refresh makes remote agents
	// look alive too — exactly the production trap).
	seedAgent(t, db, "local_resident", true)
	seedAgent(t, db, "remote_only", true)

	old := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339Nano)
	seedDelivery(t, db, "msg_local", "local_resident", old, "", "")
	seedDelivery(t, db, "msg_remote", "remote_only", old, "", "")

	bs := &Backstop{
		DB:        db,
		Dispatch:  fake,
		AgeCutoff: 15 * time.Minute,
		// The residency predicate the daemon wires to nudge.HasLocalIdentity:
		// only local_resident has an identity file on this box.
		IsResident: func(agentID string) bool { return agentID == "local_resident" },
	}
	if err := bs.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("dispatch calls = %d, want exactly 1 (resident only); got %+v", len(calls), calls)
	}
	if calls[0].agentID != "local_resident" {
		t.Errorf("dispatched to %q, want local_resident (remote_only must be filtered — the phantom-wake fix)", calls[0].agentID)
	}
}

// TestTick_NilResidencyPredicate_LegacyAllowAll pins the nil-predicate
// behavior: existing constructors that don't set IsResident keep the
// pre-wo2z scan-everyone semantics (no silent behavior change for callers
// that haven't opted in).
func TestTick_NilResidencyPredicate_LegacyAllowAll(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	fake := &fakeDispatcher{}

	seedAgent(t, db, "agent_a", true)
	old := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339Nano)
	seedDelivery(t, db, "msg_a", "agent_a", old, "", "")

	bs := &Backstop{DB: db, Dispatch: fake, AgeCutoff: 15 * time.Minute}
	if err := bs.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if calls := fake.Calls(); len(calls) != 1 || calls[0].agentID != "agent_a" {
		t.Fatalf("nil predicate must keep legacy allow-all; got %+v", calls)
	}
}
