package rpc

// Parity pin for the FROZEN v40 backfill predicate (thrum-b6qw, coordinator
// requirement): the same seeded fixture must yield IDENTICAL visible sets
// through the live rpc inline inbox predicate (HandleList for-agent mode) and
// through the backfill's frozen copy (exercised end-to-end via
// state.BackfillReadState — every message the agent can see must end up with a
// read-stamped delivery row, and nothing else may). This catches transcription
// errors in the frozen copy NOW, at v40. If the live rpc predicate evolves in a
// LATER version this test may legitimately diverge — that is the freeze working
// as designed; re-pin against the new intent rather than editing the frozen
// copy (see readstate_backfill.go).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/leonletto/thrum/internal/daemon/state"
)

func parityExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("parityExec %q: %v", q, err)
	}
}

func parityInsertMessage(t *testing.T, db *sql.DB, msgID string) {
	t.Helper()
	// created_at far AFTER the test agent's registered_at: HandleList applies a
	// for-agent floor (m.created_at > registered_at) that the backfill
	// deliberately lacks (no-cutoff — it clears ALL history). Parity is
	// asserted on the post-registration window where both predicates apply.
	parityExec(t, db, `INSERT INTO messages (message_id, agent_id, session_id, created_at, body_format, body_content)
		VALUES (?, 'agent:author:OTH', 'ses_par', '2030-01-01T00:00:00Z', 'markdown', ?)`, msgID, msgID)
}

func TestBackfillPredicate_ParityWithInboxPredicate(t *testing.T) {
	handler, agentID, cleanup := setupFilterTest(t)
	defer cleanup()
	ctx := context.Background()
	db := handler.state.RawDB()

	// Fixture spanning every arm of the 4-part v40 predicate plus negatives.
	// All messages are authored by a third party (no delivery rows except where
	// stated), so Pass 2's created-row set is exactly the predicate-visible set.
	//
	// Visible:
	//   m-mention     — direct mention ref to the agent           (Part 1)
	//   m-rolemention — mention ref to the agent's role           (Part 1, role value)
	//   m-group       — scoped to a group the agent belongs to    (Part 2, agent member)
	//   m-wildrole    — scoped to a group with a role:'*' member  (Part 2, wildcard-role sub-arm)
	//   m-legacy      — no targeting whatsoever                   (Part 3)
	//   m-bcast       — broadcast scope + delivery row for agent  (Part 4)
	// Not visible:
	//   m-other       — mention ref to a different agent only
	//   m-othergroup  — scoped to a group the agent is NOT in
	for _, id := range []string{"m-mention", "m-rolemention", "m-group", "m-wildrole", "m-legacy", "m-bcast", "m-other", "m-othergroup"} {
		parityInsertMessage(t, db, id)
	}
	parityExec(t, db, `INSERT INTO message_refs (message_id, ref_type, ref_value) VALUES ('m-mention', 'mention', ?)`, agentID)
	parityExec(t, db, `INSERT INTO message_refs (message_id, ref_type, ref_value) VALUES ('m-rolemention', 'mention', 'reviewer')`)
	parityExec(t, db, `INSERT INTO groups (group_id, name, created_at, created_by) VALUES ('g1', 'reviewers-club', 't', 'x')`)
	parityExec(t, db, `INSERT INTO group_members (group_id, member_type, member_value, added_at) VALUES ('g1', 'agent', ?, 't')`, agentID)
	parityExec(t, db, `INSERT INTO message_scopes (message_id, scope_type, scope_value) VALUES ('m-group', 'group', 'reviewers-club')`)
	// Wildcard-role group (gm.member_value='*'): matches because the test agent
	// has a non-empty role — exercises the roleCondition wildcard sub-arm
	// present in BOTH predicates (a transcription error there would otherwise
	// slip past the parity pin).
	parityExec(t, db, `INSERT INTO groups (group_id, name, created_at, created_by) VALUES ('g3', 'all-roles', 't', 'x')`)
	parityExec(t, db, `INSERT INTO group_members (group_id, member_type, member_value, added_at) VALUES ('g3', 'role', '*', 't')`)
	parityExec(t, db, `INSERT INTO message_scopes (message_id, scope_type, scope_value) VALUES ('m-wildrole', 'group', 'all-roles')`)
	parityExec(t, db, `INSERT INTO message_scopes (message_id, scope_type, scope_value) VALUES ('m-bcast', 'broadcast', 'everyone')`)
	parityExec(t, db, `INSERT INTO message_deliveries (message_id, recipient_agent_id, delivered_at) VALUES ('m-bcast', ?, 't')`, agentID)
	parityExec(t, db, `INSERT INTO message_refs (message_id, ref_type, ref_value) VALUES ('m-other', 'mention', 'agent:someone:ELSE')`)
	parityExec(t, db, `INSERT INTO groups (group_id, name, created_at, created_by) VALUES ('g2', 'other-club', 't', 'x')`)
	parityExec(t, db, `INSERT INTO group_members (group_id, member_type, member_value, added_at) VALUES ('g2', 'agent', 'agent:someone:ELSE', 't')`)
	parityExec(t, db, `INSERT INTO message_scopes (message_id, scope_type, scope_value) VALUES ('m-othergroup', 'group', 'other-club')`)

	fixture := map[string]bool{
		"m-mention": true, "m-rolemention": true, "m-group": true, "m-wildrole": true,
		"m-legacy": true, "m-bcast": true, "m-other": true, "m-othergroup": true,
	}

	// Axis 1: the live rpc inline predicate via HandleList for-agent mode.
	listParams, _ := json.Marshal(ListMessagesRequest{
		ForAgent:     agentID,
		ForAgentRole: "reviewer",
		PageSize:     100,
	})
	resp, err := handler.HandleList(ctx, listParams)
	if err != nil {
		t.Fatalf("HandleList: %v", err)
	}
	listResp := resp.(*ListMessagesResponse)
	rpcVisible := map[string]bool{}
	for _, m := range listResp.Messages {
		if fixture[m.MessageID] {
			rpcVisible[m.MessageID] = true
		}
	}

	// Axis 2: the frozen backfill predicate via BackfillReadState, scoped to
	// the test state's own daemon id so its registered agents are local. Pass 2
	// creates a read-stamped row for exactly the predicate-visible
	// no-delivery-row messages; Pass 1 stamps m-bcast's pre-existing row.
	if err := state.BackfillReadState(ctx, handler.state.DB(), handler.state.DaemonID()); err != nil {
		t.Fatalf("BackfillReadState: %v", err)
	}
	backfillVisible := map[string]bool{}
	rows, err := db.Query(`SELECT message_id FROM message_deliveries WHERE recipient_agent_id = ? AND read_at IS NOT NULL`, agentID)
	if err != nil {
		t.Fatalf("query backfilled rows: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if fixture[id] {
			backfillVisible[id] = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}

	// The two axes must agree exactly — and against the expected truth.
	want := []string{"m-bcast", "m-group", "m-legacy", "m-mention", "m-rolemention", "m-wildrole"}
	if got := sortedKeys(rpcVisible); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("rpc predicate visible set = %v, want %v", got, want)
	}
	if got := sortedKeys(backfillVisible); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("frozen backfill predicate visible set = %v, want %v", got, want)
	}
	if fmt.Sprint(sortedKeys(rpcVisible)) != fmt.Sprint(sortedKeys(backfillVisible)) {
		t.Errorf("PARITY BROKEN at v40: rpc=%v backfill=%v — transcription error in the frozen copy",
			sortedKeys(rpcVisible), sortedKeys(backfillVisible))
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
