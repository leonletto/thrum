package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
)

// ─── Frozen v40 inbox-visibility predicate ──────────────────────────────────
//
// backfillForAgentValues and backfillForAgentClause are a FROZEN COPY of the
// release-line inline inbox predicate — rpc/message.go buildForAgentValues
// (~:2001) and buildForAgentClause (~:1903) — as of the v40 crossing
// (thrum-b6qw). INTENTIONALLY NOT SHARED with the live rpc code: a one-time
// migration must reflect inbox visibility AS OF ITS CROSSING VERSION, frozen in
// time. If the live rpc predicate changes in a later version, the already-run
// v40 backfill must NOT retroactively mean something different — migrations pin
// application logic at their crossing version. Do not "helpfully" deduplicate
// these into a shared package; the duplication is the design.
// (state cannot import rpc anyway — rpc imports state — and the inboxfilter
// package extraction is a 0.11 refactor deliberately not backported.)
//
// TestBackfillPredicate_ParityWithInboxPredicate (internal/daemon/rpc) pins
// this copy byte-equivalent in behavior to the rpc predicate at v40: the same
// seeded fixture must yield identical visible sets through HandleList and
// through this clause. If that test fails after editing rpc/message.go, the
// live predicate has drifted PAST v40 — that is expected and fine; do NOT
// update this frozen copy to match. (It failing at v40 itself means a
// transcription error in the copy — fix that.)

// backfillForAgentValues mirrors rpc buildForAgentValues at v40: the unique set
// of values to match against mention refs for the for-agent inbox filter.
func backfillForAgentValues(forAgent, forAgentRole string) []string {
	if forAgent == "" {
		return nil
	}
	values := []string{forAgent}
	if !strings.HasPrefix(forAgent, "user:") {
		values = append(values, "user:"+forAgent)
	}
	if forAgentRole != "" && forAgentRole != forAgent {
		values = append(values, forAgentRole)
	}
	return values
}

// backfillForAgentClause mirrors rpc buildForAgentClause at v40 — four parts
// OR-combined: (1) direct mention refs, (2) group membership, (3) legacy
// broadcast (no targeting whatsoever), (4) broadcast scope backed by a delivery
// row. (The release line has NO Part-5 authored-self visibility; the backfill
// therefore never creates rows for the authored-self class — deliberate, see
// the b6qw Part-5 fork decision.)
func backfillForAgentClause(forAgentValues []string, forAgent, forAgentRole string) (string, []any) {
	if len(forAgentValues) == 0 {
		return "", nil
	}

	var args []any

	mentionPlaceholders := make([]string, len(forAgentValues))
	for i := range forAgentValues {
		mentionPlaceholders[i] = "?"
	}
	mentionSubquery := "m.message_id IN (SELECT mr_fa2.message_id FROM message_refs mr_fa2 WHERE mr_fa2.ref_type = 'mention' AND mr_fa2.ref_value IN (" +
		strings.Join(mentionPlaceholders, ",") + "))"
	for _, v := range forAgentValues {
		args = append(args, v)
	}

	agentVal := forAgent
	if agentVal == "" {
		agentVal = forAgentRole
	}
	roleVal := forAgentRole
	if roleVal == "" {
		roleVal = forAgent
	}

	var roleCondition string
	if forAgentRole != "" {
		roleCondition = "(gm.member_type = 'role' AND (gm.member_value = ? OR gm.member_value = '*'))"
	} else {
		roleCondition = "(gm.member_type = 'role' AND gm.member_value = ?)"
	}
	groupSubquery := `m.message_id IN (
		SELECT ms_g.message_id FROM message_scopes ms_g
		WHERE ms_g.scope_type = 'group'
		AND ms_g.scope_value IN (
			SELECT g.name FROM groups g
			JOIN group_members gm ON g.group_id = gm.group_id
			WHERE (gm.member_type = 'agent' AND gm.member_value = ?)
			   OR ` + roleCondition + `
		)
	)`
	args = append(args, agentVal, roleVal)

	legacyBroadcastSubquery := `(
		NOT EXISTS (SELECT 1 FROM message_refs mr_bc WHERE mr_bc.message_id = m.message_id AND mr_bc.ref_type IN ('mention', 'group', 'broadcast'))
		AND NOT EXISTS (SELECT 1 FROM message_scopes ms_bc WHERE ms_bc.message_id = m.message_id AND ms_bc.scope_type IN ('group', 'broadcast'))
	)`

	broadcastDeliverySubquery := `(
		EXISTS (
			SELECT 1 FROM message_deliveries md_bc
			WHERE md_bc.message_id = m.message_id
			AND md_bc.recipient_agent_id = ?
		)
		AND EXISTS (
			SELECT 1 FROM message_scopes ms_bc
			WHERE ms_bc.message_id = m.message_id
			AND ms_bc.scope_type = 'broadcast'
		)
	)`
	args = append(args, forAgent)

	clause := " AND (" + mentionSubquery + " OR " + groupSubquery + " OR " + legacyBroadcastSubquery + " OR " + broadcastDeliverySubquery + ")"
	return clause, args
}

// BackfillReadState clears historical stuck unread (thrum-b6qw, backport of
// thrum-tcqw), once, at the v40 read-state crossing. Local-only + leak-guarded:
// it acts ONLY on agents owned by this daemon — including prior incarnations of
// this host (LocalDaemonIDs, hostname-anchored) — never on synced peer-agent
// rows (thrum-edhn, structural). Runs in a single transaction.
//
// The local scope is derived via LocalDaemonIDs/LocalAgentScopeClause rather than
// the current daemon id alone: an agent registered under an OLD daemon id (a
// prior incarnation of this host) is genuinely local but carries a stale
// origin_daemon. thrum-agents' first (v42) backfill keyed on the current id only
// and so skipped user:leon-letto (stale id), leaving 234 stuck-unread — this
// single v40 marker ships the corrected, hostname-anchored scope from the start.
//
// No-cutoff (Leon S100): Pass 1 blanket-clears ALL local unread delivery rows,
// including recently-delivered ones — a clean slate is the intent. Pass 2 then
// creates read-stamped rows for the inbox-visible no-delivery-row class (legacy
// broadcasts) per local agent, using the FROZEN v40 visibility predicate above
// so the backfill's meaning never changes after the fact.
//
// Pass 1 MUST precede Pass 2: Pass 1 stamps every existing local unread delivery
// row read; Pass 2's NOT EXISTS then only CREATEs rows for messages that still
// have no delivery row for the agent.
//
// Source-coverage note: the e23c3a4a0c rescope also pointed thrum-agents'
// rpc queryLocalCoordinator at the shared hostname-anchored scope. That
// function does not exist on this release line, so that portion of the rescope
// is N/A here — not missed.
func BackfillReadState(ctx context.Context, db *safedb.DB, daemonID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Derive this host's local-agent scope (current + prior-incarnation daemon
	// ids, hostname-anchored) BEFORE the tx — it is a read and the agent set is
	// stable for a one-time startup backfill.
	localIDs, err := LocalDaemonIDs(ctx, db, daemonID)
	if err != nil {
		return fmt.Errorf("backfill derive local daemon ids: %w", err)
	}
	scopeClause, scopeArgs := LocalAgentScopeClause(localIDs)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("backfill begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Pass 1: existing unread delivery rows for LOCAL agents -> read.
	pass1Args := append([]any{now, now}, scopeArgs...)
	if _, err := tx.ExecContext(ctx,
		`UPDATE message_deliveries SET read_at = ?, seen_at = COALESCE(seen_at, ?)
		 WHERE read_at IS NULL
		   AND recipient_agent_id IN (SELECT agent_id FROM agents WHERE `+scopeClause+`)`,
		pass1Args...); err != nil {
		return fmt.Errorf("backfill pass1 stamp existing: %w", err)
	}

	// Enumerate local agents (leak-guard: peer agents are never touched).
	rows, err := tx.QueryContext(ctx,
		`SELECT agent_id, COALESCE(role,'') FROM agents WHERE `+scopeClause, scopeArgs...)
	if err != nil {
		return fmt.Errorf("backfill list local agents: %w", err)
	}
	type ag struct{ id, role string }
	var agents []ag
	for rows.Next() {
		var a ag
		if err := rows.Scan(&a.id, &a.role); err != nil {
			_ = rows.Close()
			return fmt.Errorf("backfill scan agent: %w", err)
		}
		agents = append(agents, a)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("backfill iterate agents: %w", err)
	}

	// Pass 2: per local agent, create read-stamped rows for inbox-visible
	// no-delivery-row messages (the FROZEN v40 predicate above).
	for _, a := range agents {
		values := backfillForAgentValues(a.id, a.role)
		clause, args := backfillForAgentClause(values, a.id, a.role)
		if clause == "" {
			continue
		}
		// Placeholder order: 4 SELECT projection args (recipient, delivered,
		// seen, read) first, then the clause args, then the final NOT-EXISTS
		// recipient arg. Keep exactly.
		insert := `INSERT OR IGNORE INTO message_deliveries (message_id, recipient_agent_id, delivered_at, seen_at, read_at)
			SELECT m.message_id, ?, ?, ?, ? FROM messages m
			WHERE m.deleted = 0` + clause + `
			  AND NOT EXISTS (SELECT 1 FROM message_deliveries md WHERE md.message_id = m.message_id AND md.recipient_agent_id = ?)`
		insArgs := append([]any{a.id, now, now, now}, args...)
		insArgs = append(insArgs, a.id)
		if _, err := tx.ExecContext(ctx, insert, insArgs...); err != nil {
			return fmt.Errorf("backfill pass2 agent %s: %w", a.id, err)
		}
	}
	return tx.Commit()
}
