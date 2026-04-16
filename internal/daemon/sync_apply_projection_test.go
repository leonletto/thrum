package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/eventlog"
)

// TestSyncApplier_AgentRegisterLandsInAgentsTable — thrum-mm3l regression probe.
// Verifies that a remote agent.register event, after going through the sync
// apply path, actually upserts a row in the `agents` table (not just the
// `events` table). This is the gap observed on ubuntu: 91 events but 0 agents.
func TestSyncApplier_AgentRegisterLandsInAgentsTable(t *testing.T) {
	st := createTestStateForSync(t)
	applier := NewSyncApplier(st)

	// Exact JSON payload from ubuntu's events table for coordinator_main
	// (captured live; empty display field, all other fields populated).
	eventJSON := `{"agent_id":"coordinator_main","agent_pid":55765,"hostname":"leonsmacm1pro","kind":"agent","module":"main","name":"coordinator_main","origin_daemon":"d_ees9pkfgax8p","role":"coordinator","sequence":6022,"timestamp":"2026-04-15T19:34:18.350028Z","type":"agent.register","v":1,"worktree":"thrum"}`

	events := []eventlog.Event{
		{
			EventID:      "evt_01KP9A68ZE1688YHWPHXW4C9X2",
			Sequence:     6022,
			Type:         "agent.register",
			Timestamp:    "2026-04-15T19:34:18.350028Z",
			OriginDaemon: "d_ees9pkfgax8p",
			EventJSON:    json.RawMessage(eventJSON),
		},
	}

	applied, skipped, err := applier.ApplyRemoteEvents(context.Background(), events)
	if err != nil {
		t.Fatalf("ApplyRemoteEvents: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied = %d, want 1 (skipped=%d)", applied, skipped)
	}

	// The critical assertion: did the agent row land in the agents table?
	var count int
	err = st.RawDB().QueryRow(`SELECT COUNT(*) FROM agents WHERE agent_id = ?`, "coordinator_main").Scan(&count)
	if err != nil {
		t.Fatalf("query agents table: %v", err)
	}
	if count != 1 {
		t.Errorf("agents table row count for coordinator_main = %d, want 1 (projection did not land)", count)
	}

	// Also verify key fields are populated correctly.
	var agentID, kind, role, module, hostname string
	err = st.RawDB().QueryRow(`
		SELECT agent_id, kind, role, module, hostname FROM agents WHERE agent_id = ?
	`, "coordinator_main").Scan(&agentID, &kind, &role, &module, &hostname)
	if err != nil {
		t.Fatalf("query agent row: %v", err)
	}
	if kind != "agent" || role != "coordinator" || module != "main" || hostname != "leonsmacm1pro" {
		t.Errorf("agent fields wrong: kind=%q role=%q module=%q hostname=%q",
			kind, role, module, hostname)
	}
}
