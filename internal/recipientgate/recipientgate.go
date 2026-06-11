// Package recipientgate holds the single canonical SQL predicate (thrum-qb62)
// that decides whether an agent is a *legitimate recipient* of a message —
// mentioned by id/role, in a targeted group, broadcast-scoped, an unaddressed
// legacy broadcast, or the message's own author.
//
// It exists so the two places that must agree on "may this agent hold a read
// receipt for this message" share ONE predicate and can never drift:
//
//   - internal/projection/projector.go applyMessageReceipt — gates the
//     message_deliveries INSERT (the durable row).
//   - internal/daemon/rpc/message.go HandleMarkRead — gates receipt-event
//     EMISSION (thrum-1846: an emitted event is broadcast to every mesh peer,
//     so emitting for a non-recipient is the storm vector even when the
//     projector later refuses the row).
//
// If these predicates diverged, `thrum message read --all` could again emit
// cross-agent receipts that the projector silently drops — invisible until the
// next storm. Keeping the text here, used verbatim by both sites, makes the
// invariant structural.
package recipientgate

// Predicate is a SQL boolean expression, correlated to a `messages m` row via
// `m.message_id`, that evaluates true when the agent identified by the bind
// args is a legitimate recipient of that message. It carries NO outer parens —
// callers wrap as needed (`WHERE `+Predicate in an INSERT...SELECT, or
// `SELECT (`+Predicate+`)` for a direct boolean).
//
// Bind args MUST be supplied via Args(agentID) — there are six `?`
// placeholders, all the agent id, in source order. The message is bound
// positionally through the correlated `m.message_id`, so it is NOT among the
// args.
//
// This mirrors, byte-for-byte in its OR-arm logic, the qb62 INSERT-gate that
// previously lived inline in projector.applyMessageReceipt. Do not confuse it
// with the tcqw read-state backfill predicate — this is receipt legitimacy.
const Predicate = `(
		EXISTS (
			SELECT 1 FROM message_refs mr
			WHERE mr.message_id = m.message_id
			  AND mr.ref_type = 'mention'
			  AND (
			    mr.ref_value = ?
			    OR mr.ref_value = (SELECT role FROM agents WHERE agent_id = ? LIMIT 1)
			  )
		) OR EXISTS (
			SELECT 1 FROM message_scopes ms
			WHERE ms.message_id = m.message_id
			  AND ms.scope_type = 'broadcast'
		) OR EXISTS (
			SELECT 1 FROM message_scopes ms
			JOIN groups g ON g.name = ms.scope_value
			JOIN group_members gm ON g.group_id = gm.group_id
			WHERE ms.message_id = m.message_id
			  AND ms.scope_type = 'group'
			  AND (
			    (gm.member_type = 'agent' AND gm.member_value = ?)
			    OR (gm.member_type = 'role' AND gm.member_value = (SELECT role FROM agents WHERE agent_id = ? LIMIT 1))
			  )
		) OR (
			-- Legacy-broadcast: the message has no targeting whatsoever
			-- (no mention refs, no broadcast/group scopes). Any agent can
			-- mark it read. Mirrors the legacy-broadcast branch in
			-- buildForAgentClause (message.go) so inbox visibility and
			-- delivery-gate semantics stay aligned.
			NOT EXISTS (
				SELECT 1 FROM message_refs mr_lb
				WHERE mr_lb.message_id = m.message_id
				  AND mr_lb.ref_type IN ('mention', 'group', 'broadcast')
			)
			AND NOT EXISTS (
				SELECT 1 FROM message_scopes ms_lb
				WHERE ms_lb.message_id = m.message_id
				  AND ms_lb.scope_type IN ('group', 'broadcast')
			)
		) OR EXISTS (
			-- thrum-b6qw authored-self (port of tcqw): the agent's own sent
			-- messages belong in their inbox. Marking one read creates a
			-- read-stamped self-delivery row so the self-authored
			-- no-delivery-row phantom-unread class converges — the class the
			-- legacy-broadcast arm above does NOT cover, since an authored
			-- message is typically targeted (carries a mention/scope). Matches
			-- both the bare agent_id and the "user:"-prefixed form (a message is
			-- authored by an agent_id; user inboxes mark via the "user:" id).
			SELECT 1 FROM messages m_self
			WHERE m_self.message_id = m.message_id
			  AND m_self.agent_id IN (?, 'user:' || ?)
		)
	)`

// Args returns the bind arguments for Predicate, in source order. All six
// placeholders bind the same agent id; the message is bound by correlation on
// m.message_id and is therefore not included here.
func Args(agentID string) []any {
	return []any{
		agentID, agentID, // mention arm: ref_value, role subquery
		agentID, agentID, // group arm: member_value, role subquery
		agentID, agentID, // authored-self arm: agent_id IN (?, 'user:'||?)
	}
}
