package permission

import (
	"context"
	"testing"

	"github.com/leonletto/thrum/internal/types"
)

// supervisorRecipients returns the @-mention recipients of every supervisor
// message authored by supervisor_thrum — i.e. who firstDetect/fireReminder
// actually relayed to.
func supervisorRecipients(t *testing.T, p *Permission) map[string]bool {
	t.Helper()
	rows, err := p.state.RawDB().Query(`
		SELECT mr.ref_value FROM messages m
		JOIN message_refs mr ON mr.message_id = m.message_id AND mr.ref_type = 'mention'
		WHERE m.agent_id = 'supervisor_thrum'`)
	if err != nil {
		t.Fatalf("query supervisor recipients: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[v] = true
	}
	return out
}

// TestScheduler_FirstDetect_ExcludesModalOwner is the thrum-x3fnh end-to-end
// owner-exclusion pin (the 09:48Z self-referential datapoint): when the
// modal-blocked agent is itself a configured coordinator, firstDetect must NOT
// relay the modal to that agent's own (blocked) inbox — it relays only to the
// OTHER coordinators.
func TestScheduler_FirstDetect_ExcludesModalOwner(t *testing.T) {
	p, _ := newSchedulerFixture(t)
	ctx := context.Background()

	// Register a second coordinator that IS the modal owner.
	for _, ev := range []any{
		types.AgentRegisterEvent{Type: "agent.register", Timestamp: "2026-04-14T00:00:02Z", AgentID: "coordinator_owner", Kind: "agent", Role: "coordinator", Module: "test"},
		types.AgentSessionStartEvent{Type: "agent.session.start", Timestamp: "2026-04-14T00:00:03Z", SessionID: "ses_coordinator_owner", AgentID: "coordinator_owner"},
	} {
		if _, err := p.state.WriteEvent(ctx, ev); err != nil {
			t.Fatalf("seed owner coordinator: %v", err)
		}
	}

	p.SetPaneCaptureForTest(func(_ string, _ int) (string, error) {
		return "Not in allowlist: test\n", nil
	})

	// The modal owner is coordinator_owner — it must be excluded from its own
	// relay audience.
	if err := p.OnDetection(ctx, "owner-sess", "claude", "owner-sess:0.0",
		"coordinator_owner", testPattern(), "pane A"); err != nil {
		t.Fatalf("OnDetection: %v", err)
	}

	// mention ref_value is stored bare (no '@').
	got := supervisorRecipients(t, p)
	if got["coordinator_owner"] {
		t.Errorf("modal owner coordinator_owner must NOT be relayed its own modal (self-referential); recipients=%v", keys(got))
	}
	if !got["coordinator_main"] {
		t.Errorf("the other coordinator must still receive the relay; recipients=%v", keys(got))
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
