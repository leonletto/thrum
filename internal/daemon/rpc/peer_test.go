package rpc_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/rpc"
)

// xir.29 B1: PeerListEntry serializes ReconcileStatus so the CLI can
// render the drift marker. Verifies the JSON tag + omitempty behavior.
func TestPeerListEntry_ReconcileStatusRoundTrip(t *testing.T) {
	entries := []rpc.PeerListEntry{
		{
			DaemonID:        "01D1",
			Name:            "alpha",
			Address:         "1.2.3.4:7731",
			LastSync:        "1s ago",
			LastSeq:         42,
			ReconcileStatus: "drift_reconcile_failed",
		},
		{
			DaemonID: "01D2",
			Name:     "bravo",
			Address:  "5.6.7.8:7731",
			LastSync: "2s ago",
			LastSeq:  100,
			// ReconcileStatus deliberately empty — omitempty should
			// skip the field on the wire.
		},
	}

	h := rpc.NewPeerListHandler(func() []rpc.PeerListEntry { return entries })
	out, err := h.Handle(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	wire, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Decode back through the same struct shape a CLI client would use.
	var got []rpc.PeerListEntry
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].ReconcileStatus != "drift_reconcile_failed" {
		t.Errorf("alpha ReconcileStatus = %q, want drift_reconcile_failed", got[0].ReconcileStatus)
	}
	if got[1].ReconcileStatus != "" {
		t.Errorf("bravo ReconcileStatus = %q, want empty", got[1].ReconcileStatus)
	}

	// Verify omitempty actually elided the empty field on the wire.
	if containsAll(string(wire), `"reconcile_status":"drift_reconcile_failed"`) &&
		!containsAll(string(wire), `"bravo".*reconcile_status`) {
		// OK: alpha has the field, bravo does not (regex-free check).
		// If both were present, the test regresses to "field always
		// emitted" — we would catch that via the omitempty assertion
		// below.
	}
	// Quick substring check: there must be at most ONE occurrence of
	// "reconcile_status" in the wire payload (alpha only).
	if countOccurrences(string(wire), `"reconcile_status"`) != 1 {
		t.Errorf("expected exactly one reconcile_status on the wire; got payload: %s", wire)
	}
}

func countOccurrences(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
			i += len(sub) - 1
		}
	}
	return n
}
