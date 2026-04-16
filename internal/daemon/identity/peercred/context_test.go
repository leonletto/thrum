package peercred_test

import (
	"context"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
)

func TestWithIdentity_RoundTrip(t *testing.T) {
	id := &peercred.ResolvedIdentity{
		AgentID:  "impl_test",
		Worktree: "/tmp/worktree",
		PID:      1234,
	}
	ctx := peercred.WithIdentity(context.Background(), id)

	got, ok := peercred.FromContext(ctx)
	if !ok {
		t.Fatal("FromContext should return ok=true after WithIdentity")
	}
	if got == nil || got.AgentID != "impl_test" || got.PID != 1234 {
		t.Errorf("FromContext returned wrong identity: %+v", got)
	}
}

func TestFromContext_Missing(t *testing.T) {
	// Plain context with no injected identity should return (nil, false)
	// so callers can distinguish "never ran" from "anonymous".
	ctx := context.Background()
	got, ok := peercred.FromContext(ctx)
	if ok {
		t.Error("FromContext should return ok=false for unmarked ctx")
	}
	if got != nil {
		t.Errorf("FromContext should return nil id for unmarked ctx, got %+v", got)
	}
}

func TestWithIdentity_Anonymous(t *testing.T) {
	// Passing nil explicitly marks ctx as "peercred ran, no match" (anonymous).
	// FromContext should return (nil, true) in this case.
	ctx := peercred.WithIdentity(context.Background(), nil)
	got, ok := peercred.FromContext(ctx)
	if !ok {
		t.Error("FromContext should return ok=true even for explicitly anonymous ctx")
	}
	if got != nil {
		t.Errorf("FromContext should return nil for anonymous ctx, got %+v", got)
	}
}

func TestIdentityCtxKey_NoCollision(t *testing.T) {
	// An arbitrary string key with the same name as the package's internal
	// key MUST NOT collide with the typed key — typed keys are
	// reference-compared, so this is a sanity check that we're not
	// accidentally using string-based keys somewhere.
	ctx := context.WithValue(context.Background(), "identityCtxKey", "forged") //nolint:staticcheck,revive // intentional string key to prove no collision
	got, ok := peercred.FromContext(ctx)
	if ok {
		t.Errorf("FromContext should not find string-keyed value, got %+v", got)
	}
}
