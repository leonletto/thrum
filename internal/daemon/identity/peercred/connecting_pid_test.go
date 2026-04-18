package peercred_test

import (
	"context"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/identity/peercred"
)

func TestWithConnectingPID_RoundTrip(t *testing.T) {
	ctx := peercred.WithConnectingPID(context.Background(), 4321)
	got, ok := peercred.ConnectingPIDFromContext(ctx)
	if !ok {
		t.Fatal("ConnectingPIDFromContext should return ok=true after WithConnectingPID")
	}
	if got != 4321 {
		t.Errorf("ConnectingPIDFromContext = %d, want 4321", got)
	}
}

func TestConnectingPIDFromContext_Missing(t *testing.T) {
	got, ok := peercred.ConnectingPIDFromContext(context.Background())
	if ok {
		t.Error("ConnectingPIDFromContext should return ok=false for unmarked ctx")
	}
	if got != 0 {
		t.Errorf("ConnectingPIDFromContext = %d, want 0 for unmarked ctx", got)
	}
}

func TestConnectingPID_IndependentFromIdentity(t *testing.T) {
	// When peercred resolves to anonymous (no registered worktree match),
	// the PID must still be available to handlers so guard checks can
	// populate CheckContext.Chain without trusting a client-asserted ID.
	ctx := peercred.WithConnectingPID(context.Background(), 9999)
	ctx = peercred.WithIdentity(ctx, nil) // anonymous

	pid, pidOK := peercred.ConnectingPIDFromContext(ctx)
	if !pidOK || pid != 9999 {
		t.Errorf("PID should survive anonymous identity injection, got pid=%d ok=%v", pid, pidOK)
	}

	id, idOK := peercred.FromContext(ctx)
	if !idOK || id != nil {
		t.Errorf("identity should be (nil, true) for anonymous ctx, got id=%+v ok=%v", id, idOK)
	}
}
