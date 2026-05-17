package agent_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/schema"
)

// newRegistryDB opens an in-memory SQLite DB migrated to head and
// wrapped with *safedb.DB. Mirrors the pattern used by
// internal/daemon/state/agent_lifecycle_test.go.
func newRegistryDB(t *testing.T) *safedb.DB {
	t.Helper()
	db, err := schema.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return safedb.New(db)
}

// seedAgent inserts a baseline v0.11-shaped agents row directly so
// tests don't depend on the agent.register RPC handler (which lives
// in internal/daemon/rpc and would create an import cycle).
func seedAgent(t *testing.T, db *safedb.DB, id, mode, identity string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO agents
			(agent_id, kind, role, module, registered_at,
			 mode, identity, auto_respawn_enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "agent", "implementer", "test",
		"2026-05-17T00:00:00Z", mode, identity, 0,
	)
	if err != nil {
		t.Fatalf("seed agent %q: %v", id, err)
	}
}

func TestSQLiteRegistry_LookupReturnsErrAgentNotFound(t *testing.T) {
	reg := agent.NewSQLiteRegistry(newRegistryDB(t))
	_, err := reg.Lookup(context.Background(), "ghost")
	if !errors.Is(err, agent.ErrAgentNotFound) {
		t.Fatalf("got err = %v; want wraps ErrAgentNotFound", err)
	}
}

// TestSQLiteRegistry_LookupRoundTrip pins per-column persistence: every
// field present in the agents row surfaces on the Agent struct. Catches
// drift between the Lookup SQL column order and the Scan target list.
func TestSQLiteRegistry_LookupRoundTrip(t *testing.T) {
	db := newRegistryDB(t)
	seedAgent(t, db, "docs_bot", agent.ModePersistent, agent.IdentityLongLived)

	reg := agent.NewSQLiteRegistry(db)
	got, err := reg.Lookup(context.Background(), "docs_bot")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}

	cases := []struct {
		name, want, got string
	}{
		{"AgentID", "docs_bot", got.AgentID},
		{"Kind", "agent", got.Kind},
		{"Role", "implementer", got.Role},
		{"Module", "test", got.Module},
		{"RegisteredAt", "2026-05-17T00:00:00Z", got.RegisteredAt},
		{"Mode", agent.ModePersistent, got.Mode},
		{"Identity", agent.IdentityLongLived, got.Identity},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q; want %q", c.name, c.got, c.want)
		}
	}
	if got.AutoRespawnEnabled {
		t.Errorf("AutoRespawnEnabled = true; want false (seed sets 0)")
	}
	// All four nullable timestamps unset on a freshly-seeded row.
	if got.AutoRespawnDisabledAt != nil {
		t.Errorf("AutoRespawnDisabledAt = %v; want nil", got.AutoRespawnDisabledAt)
	}
	if got.StateMdParseFailedAt != nil {
		t.Errorf("StateMdParseFailedAt = %v; want nil", got.StateMdParseFailedAt)
	}
	if got.LastPaneAliveAt != nil {
		t.Errorf("LastPaneAliveAt = %v; want nil", got.LastPaneAliveAt)
	}
}

// TestSQLiteRegistry_SetAndClearAutoRespawnDisabledAt pins the
// loop-guard trip-and-ack flow: Set arms the marker; subsequent
// Lookup observes it; Clear resets to nil.
func TestSQLiteRegistry_SetAndClearAutoRespawnDisabledAt(t *testing.T) {
	db := newRegistryDB(t)
	seedAgent(t, db, "flaky", agent.ModePersistent, agent.IdentityLongLived)
	reg := agent.NewSQLiteRegistry(db)
	ctx := context.Background()

	trip := time.Now().Add(-30 * time.Second).Truncate(time.Second).UTC()
	if err := reg.SetAutoRespawnDisabledAt(ctx, "flaky", trip); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := reg.Lookup(ctx, "flaky")
	if err != nil {
		t.Fatalf("Lookup after Set: %v", err)
	}
	if got.AutoRespawnDisabledAt == nil || !got.AutoRespawnDisabledAt.Equal(trip) {
		t.Errorf("AutoRespawnDisabledAt = %v; want %v", got.AutoRespawnDisabledAt, trip)
	}

	if err := reg.ClearAutoRespawnDisabledAt(ctx, "flaky"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, err = reg.Lookup(ctx, "flaky")
	if err != nil {
		t.Fatalf("Lookup after Clear: %v", err)
	}
	if got.AutoRespawnDisabledAt != nil {
		t.Errorf("AutoRespawnDisabledAt = %v; want nil after Clear", got.AutoRespawnDisabledAt)
	}
}

// TestSQLiteRegistry_SetAndClearStateMdParseFailedAt pins the state.md
// banner trip-and-ack flow: same shape as the auto-respawn flow above.
func TestSQLiteRegistry_SetAndClearStateMdParseFailedAt(t *testing.T) {
	db := newRegistryDB(t)
	seedAgent(t, db, "lost_state", agent.ModePersistent, agent.IdentityLongLived)
	reg := agent.NewSQLiteRegistry(db)
	ctx := context.Background()

	trip := time.Now().Add(-15 * time.Second).Truncate(time.Second).UTC()
	if err := reg.SetStateMdParseFailedAt(ctx, "lost_state", trip); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := reg.Lookup(ctx, "lost_state")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.StateMdParseFailedAt == nil || !got.StateMdParseFailedAt.Equal(trip) {
		t.Errorf("StateMdParseFailedAt = %v; want %v", got.StateMdParseFailedAt, trip)
	}

	if err := reg.ClearStateMdParseFailedAt(ctx, "lost_state"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, err = reg.Lookup(ctx, "lost_state")
	if err != nil {
		t.Fatalf("Lookup after Clear: %v", err)
	}
	if got.StateMdParseFailedAt != nil {
		t.Errorf("StateMdParseFailedAt = %v; want nil after Clear", got.StateMdParseFailedAt)
	}
}

// TestSQLiteRegistry_SettersReturnErrAgentNotFound pins the "no row
// updated" surface: each setter distinguishes "agent unknown" from
// other DB errors so callers can take corrective action (refuse the
// op vs retry).
func TestSQLiteRegistry_SettersReturnErrAgentNotFound(t *testing.T) {
	reg := agent.NewSQLiteRegistry(newRegistryDB(t))
	ctx := context.Background()
	now := time.Now()

	cases := []struct {
		name string
		call func() error
	}{
		{"SetAutoRespawnDisabledAt", func() error { return reg.SetAutoRespawnDisabledAt(ctx, "ghost", now) }},
		{"ClearAutoRespawnDisabledAt", func() error { return reg.ClearAutoRespawnDisabledAt(ctx, "ghost") }},
		{"SetStateMdParseFailedAt", func() error { return reg.SetStateMdParseFailedAt(ctx, "ghost", now) }},
		{"ClearStateMdParseFailedAt", func() error { return reg.ClearStateMdParseFailedAt(ctx, "ghost") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.call()
			if !errors.Is(err, agent.ErrAgentNotFound) {
				t.Errorf("err = %v; want wraps ErrAgentNotFound", err)
			}
		})
	}
}

// TestSQLiteRegistry_ConcurrentWrites confirms the implementation is
// safe under -race when multiple goroutines Set + Clear the same
// agent's markers concurrently. SQLite serializes writes per
// connection; safedb provides the connection — this test pins that no
// data race fires at the Go level (e.g. shared slice/map mutation).
func TestSQLiteRegistry_ConcurrentWrites(t *testing.T) {
	db := newRegistryDB(t)
	seedAgent(t, db, "contended", agent.ModePersistent, agent.IdentityLongLived)
	reg := agent.NewSQLiteRegistry(db)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			_ = reg.SetAutoRespawnDisabledAt(ctx, "contended",
				time.Now().Add(-time.Duration(n)*time.Second))
		}(i)
		go func() {
			defer wg.Done()
			_ = reg.ClearAutoRespawnDisabledAt(ctx, "contended")
		}()
	}
	wg.Wait()

	// Final state is non-deterministic; we only assert the registry
	// still returns the agent (no DB corruption) and the field is
	// either nil or a valid time.
	got, err := reg.Lookup(ctx, "contended")
	if err != nil {
		t.Fatalf("Lookup after concurrent writes: %v", err)
	}
	if got.AgentID != "contended" {
		t.Errorf("AgentID lost after concurrent writes: %q", got.AgentID)
	}
}
