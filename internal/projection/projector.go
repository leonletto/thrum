package projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/jsonl"
	"github.com/leonletto/thrum/internal/sync/pending"
	"github.com/leonletto/thrum/internal/types"
)

// Projector replays JSONL events into SQLite.
type Projector struct {
	db              *safedb.DB
	syncDir         string           // set via SetPendingPool; empty disables pending-pool logic
	pendingPool     *pending.Pool    // nil when sync is not configured
	pendingResolver pending.Resolver // nil when sync is not configured
}

// NewProjector creates a new projector for the given database.
func NewProjector(db *safedb.DB) *Projector {
	return &Projector{db: db}
}

// SetPendingPool wires the pending-pool and the sync worktree directory into
// the projector. Once set, applyMessageCreate checks whether referenced state
// files are present on disk; missing references cause the message row to be
// inserted with pending_route_resolution=1 and the message to be added to the
// pool. Callers without sync (legacy code paths, tests that don't need
// pending-pool behaviour) can safely omit this call — the projector nil-checks
// both fields on every ingest.
func (p *Projector) SetPendingPool(syncDir string, pool *pending.Pool) {
	p.syncDir = syncDir
	p.pendingPool = pool
}

// SetPendingResolver wires the Resolver implementation. Must be called after
// SetPendingPool. The resolver is invoked by ResolveOnStateLand calls that are
// triggered from applyAgentRegister and applyGroupCreate handlers.
func (p *Projector) SetPendingResolver(resolver pending.Resolver) {
	p.pendingResolver = resolver
}

// ProjectionResolver implements pending.Resolver by checking whether all
// BlockedBy state files are now present on disk, then clearing
// pending_route_resolution on the message row.
type ProjectionResolver struct {
	projector *Projector
}

// NewProjectionResolver returns a ProjectionResolver backed by p. The resolver
// is constructed by the caller (typically cmd/thrum/main.go after SetPendingPool)
// and passed back via SetPendingResolver, keeping the coupling as a value not a
// circular reference.
func NewProjectionResolver(p *Projector) *ProjectionResolver {
	return &ProjectionResolver{projector: p}
}

// Resolve checks whether all msg.BlockedBy IDs are now present on disk as
// state/agents/<id>.json or state/bridge-groups/<id>.json. If yes, it clears
// pending_route_resolution on the messages row and returns (true, nil).
// If any are still missing, it returns (false, nil) leaving the orphan in the
// pool. A non-nil error alongside true is never returned per spec §5.4.
func (r *ProjectionResolver) Resolve(ctx context.Context, msg pending.OrphanedMessage) (bool, error) {
	p := r.projector
	if p.syncDir == "" {
		// No sync dir configured — resolve unconditionally to unblock pool.
		return true, nil
	}

	// Check all BlockedBy IDs are now present on disk.
	for _, id := range msg.BlockedBy {
		if !stateFileExists(p.syncDir, id) {
			return false, nil
		}
	}

	// All prerequisites are satisfied — clear the pending flag.
	_, err := p.db.ExecContext(ctx,
		`UPDATE messages SET pending_route_resolution = 0 WHERE message_id = ?`,
		msg.MessageID,
	)
	if err != nil {
		return false, fmt.Errorf("projection resolver: clear pending flag for %s: %w", msg.MessageID, err)
	}
	return true, nil
}

// Apply applies a single event to the database.
func (p *Projector) Apply(ctx context.Context, event json.RawMessage) error {
	// Parse base event to get type
	var base types.BaseEvent
	if err := json.Unmarshal(event, &base); err != nil {
		return fmt.Errorf("unmarshal base event: %w", err)
	}

	switch base.Type {
	case "message.create":
		return p.applyMessageCreate(ctx, event)
	case "message.edit":
		return p.applyMessageEdit(ctx, event)
	case "message.delete":
		return p.applyMessageDelete(ctx, event)
	case "message.receipt":
		return p.applyMessageReceipt(ctx, event)
	case "agent.register":
		return p.applyAgentRegister(ctx, event)
	case "agent.session.start":
		return p.applySessionStart(ctx, event)
	case "agent.session.end":
		return p.applySessionEnd(ctx, event)
	case "agent.update":
		return p.applyAgentUpdate(ctx, event)
	case "agent.cleanup":
		return p.applyAgentCleanup(ctx, event)
	case "purge.executed":
		return p.applyPurgeExecuted(ctx, event)
	case "group.create":
		return p.applyGroupCreate(ctx, event)
	case "group.member.add":
		return p.applyGroupMemberAdd(ctx, event)
	case "group.member.remove":
		return p.applyGroupMemberRemove(ctx, event)
	case "group.update":
		return p.applyGroupUpdate(ctx, event)
	case "group.delete":
		return p.applyGroupDelete(ctx, event)
	default:
		// Unknown event types are ignored (forward compatibility)
		return nil
	}
}

// eventWithOrder holds an event and its sorting keys.
type eventWithOrder struct {
	event     json.RawMessage
	timestamp string
	eventID   string
}

// Rebuild rebuilds the database by replaying all events from multiple JSONL files.
// It reads from events.jsonl and messages/*.jsonl in the sync worktree directory,
// sorts all events globally by (timestamp, event_id), and applies them in order.
func (p *Projector) Rebuild(ctx context.Context, syncDir string) error {
	var allEvents []eventWithOrder

	// Read events.jsonl (core events: agent lifecycle, threads)
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	if _, err := os.Stat(eventsPath); err == nil {
		events, err := readEventsFromFile(eventsPath)
		if err != nil {
			return fmt.Errorf("read events.jsonl: %w", err)
		}
		allEvents = append(allEvents, events...)
	}

	// Read messages/*.jsonl (per-agent message files)
	messagesPattern := filepath.Join(syncDir, "messages", "*.jsonl")
	messageFiles, err := filepath.Glob(messagesPattern)
	if err != nil {
		return fmt.Errorf("glob message files: %w", err)
	}

	for _, filePath := range messageFiles {
		events, err := readEventsFromFile(filePath)
		if err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(filePath), err)
		}
		allEvents = append(allEvents, events...)
	}

	// Sort by (timestamp, event_id) for deterministic ordering
	// ULIDs in event_id make this globally consistent across files
	sort.Slice(allEvents, func(i, j int) bool {
		if allEvents[i].timestamp != allEvents[j].timestamp {
			return allEvents[i].timestamp < allEvents[j].timestamp
		}
		return allEvents[i].eventID < allEvents[j].eventID
	})

	// Apply events in sorted order
	for _, e := range allEvents {
		if err := p.Apply(ctx, e.event); err != nil {
			return fmt.Errorf("apply event: %w", err)
		}
	}

	return nil
}

// readEventsFromFile reads all events from a JSONL file and returns them with sorting keys.
func readEventsFromFile(path string) ([]eventWithOrder, error) {
	reader, err := jsonl.NewReader(path)
	if err != nil {
		return nil, err
	}

	messages, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	events := make([]eventWithOrder, 0, len(messages))
	for _, msg := range messages {
		// Parse timestamp and event_id for sorting
		var base types.BaseEvent
		if err := json.Unmarshal(msg, &base); err != nil {
			// Skip unparseable events (forward compatibility)
			continue
		}

		events = append(events, eventWithOrder{
			event:     msg,
			timestamp: base.Timestamp,
			eventID:   base.EventID,
		})
	}

	return events, nil
}

func (p *Projector) applyMessageCreate(ctx context.Context, data json.RawMessage) error {
	var event types.MessageCreateEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal message.create: %w", err)
	}

	// Determine whether any referenced state files are missing on disk.
	// This check is only active when SetPendingPool has been called (syncDir != "").
	// When the projector is used without sync (tests, legacy paths), skip entirely.
	pendingFlag := 0
	var missingIDs []string
	if p.syncDir != "" && p.pendingPool != nil {
		// Collect all IDs that need state-file presence: author + recipients.
		// We only check IDs that the message explicitly references; broadcast
		// messages have no specific recipient files to check and are not flagged.
		candidates := make([]string, 0, 1+len(event.Recipients))
		candidates = append(candidates, event.AgentID)
		candidates = append(candidates, event.Recipients...)
		// Deduplicate
		seen := make(map[string]bool, len(candidates))
		for _, id := range candidates {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if !stateFileExists(p.syncDir, id) {
				missingIDs = append(missingIDs, id)
			}
		}
		if len(missingIDs) > 0 {
			pendingFlag = 1
		}
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Insert message. OR IGNORE + the rows-affected dup-no-op below make this
	// idempotent (thrum-lv9x): cross-host-relayed history carries the same
	// message_id under DIFFERENT event_ids (the i057-class dup — 0.11 prevents
	// the mint via rpcrouter, absent on this line, and the dups are already in
	// shared history), so a plain INSERT aborted the whole sync-apply batch
	// with a messages.message_id UNIQUE error, permanently pinning the inbound
	// checkpoint and feeding the notify-retry storm. Every other projector
	// write is already idempotent (upsert / OR IGNORE); this was the last one.
	res, err := tx.Exec(`
		INSERT OR IGNORE INTO messages (
			message_id, thread_id, agent_id, session_id, created_at,
			body_format, body_content, body_structured, authored_by, disclosed,
			pending_route_resolution
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		event.MessageID,
		sqlNullString(event.ThreadID),
		event.AgentID,
		event.SessionID,
		event.Timestamp,
		event.Body.Format,
		event.Body.Content,
		sqlNullString(event.Body.Structured),
		sqlNullString(event.AuthoredBy),
		boolToInt(event.Disclosed),
		pendingFlag,
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	if n, raErr := res.RowsAffected(); raErr == nil && n == 0 {
		// Duplicate message_id: first write wins, this create is a no-op.
		// Skip scopes/refs/deliveries/self-row AND the post-commit pending-pool
		// registration — the apply that landed the row already did all of it.
		// Returning nil (not an error) is the load-bearing part: the sync apply
		// batch continues and the checkpoint advances past the dup.
		return tx.Commit()
	}

	// Insert scopes
	for _, scope := range event.Scopes {
		_, err = tx.Exec(`
			INSERT INTO message_scopes (message_id, scope_type, scope_value)
			VALUES (?, ?, ?)
		`, event.MessageID, scope.Type, scope.Value)
		if err != nil {
			return fmt.Errorf("insert scope: %w", err)
		}
	}

	// Insert refs
	for _, ref := range event.Refs {
		_, err = tx.Exec(`
			INSERT INTO message_refs (message_id, ref_type, ref_value)
			VALUES (?, ?, ?)
		`, event.MessageID, ref.Type, ref.Value)
		if err != nil {
			return fmt.Errorf("insert ref: %w", err)
		}
	}

	// Insert durable recipient snapshot. When the author appears in their own
	// recipient list, the delivery row represents a deliberate self-mention
	// reached via HandleSend's direct-targeting paths (--to @self, role
	// mention, group expansion). The author has already "seen" their own
	// send, so stamp read_at + seen_at = delivered_at on insert. The message
	// remains visible via --all and message.get but naturally drops out of
	// --unread queries without a markRead round-trip. Broadcasts strip self
	// at HandleSend, so this branch does not fire for echo cases.
	for _, recipientAgentID := range event.Recipients {
		if recipientAgentID == event.AgentID {
			_, err = tx.Exec(`
				INSERT OR IGNORE INTO message_deliveries (
					message_id, recipient_agent_id, delivered_at, seen_at, read_at
				) VALUES (?, ?, ?, ?, ?)
			`, event.MessageID, recipientAgentID, event.Timestamp, event.Timestamp, event.Timestamp)
		} else {
			_, err = tx.Exec(`
				INSERT OR IGNORE INTO message_deliveries (
					message_id, recipient_agent_id, delivered_at
				) VALUES (?, ?, ?)
			`, event.MessageID, recipientAgentID, event.Timestamp)
		}
		if err != nil {
			return fmt.Errorf("insert message delivery: %w", err)
		}
	}

	// thrum-b6qw (port of tcqw Option C): always create a read-stamped
	// self-delivery row for the author, even when they are not in their own
	// Recipients (the broadcast/legacy case — HandleSend strips self from
	// broadcast recipients, so the in-loop self-mention branch above never
	// fires for those). The author has already "seen" their own send, so it
	// must never count as unread; the row drops out of --unread without a
	// markRead round-trip. Idempotent via OR IGNORE: a no-op when the loop
	// already inserted the author's row (author-in-Recipients). This stops the
	// self-authored no-delivery-row class from accumulating going forward;
	// T2 (receipt gate arm) + the v40 backfill clear the historical rows.
	if event.AgentID != "" {
		if _, err = tx.Exec(`
			INSERT OR IGNORE INTO message_deliveries (
				message_id, recipient_agent_id, delivered_at, seen_at, read_at
			) VALUES (?, ?, ?, ?, ?)
		`, event.MessageID, event.AgentID, event.Timestamp, event.Timestamp, event.Timestamp); err != nil {
			return fmt.Errorf("insert author self-delivery: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// After successful commit: if the message referenced missing state files,
	// register it with the pending pool so ResolveOnStateLand can retry once
	// the missing files arrive. This is synchronous and safe on the
	// event-ingest goroutine (per anti-pattern §7: no goroutine spawning).
	if pendingFlag == 1 && p.pendingPool != nil {
		p.pendingPool.Add(pending.OrphanedMessage{
			MessageID:  event.MessageID,
			AuthorID:   event.AgentID,
			Recipients: event.Recipients,
			BlockedBy:  missingIDs,
			LandedAt:   time.Now().UTC(),
		})
	}

	return nil
}

func (p *Projector) applyMessageEdit(ctx context.Context, data json.RawMessage) error {
	var event types.MessageEditEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal message.edit: %w", err)
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Query current content and session_id before updating.
	//
	// If the original message hasn't been synced yet (out-of-order delivery
	// across peers), skip edit history and let the UPDATE become a safe no-op.
	// This prevents a missing message from poisoning the sync apply loop.
	//
	// KNOWN LIMITATION: when an edit applies before its create, the edit is
	// permanently lost from the live projection — the UPDATE matches zero rows
	// and when the create eventually arrives it inserts only the original
	// content. A full Rebuild from JSONL would apply both events in timestamp
	// order and produce the correct result, so data is preserved in the log.
	// This is acceptable per the graceful-degradation design: never block sync.
	var oldContent string
	var oldStructured sql.NullString
	var sessionID string
	query := `SELECT body_content, body_structured, session_id FROM messages WHERE message_id = ?`
	err = tx.QueryRow(query, event.MessageID).Scan(&oldContent, &oldStructured, &sessionID)
	switch {
	case err == nil:
		// Original message exists — record edit history
		_, err = tx.Exec(`
			INSERT INTO message_edits (
				message_id, edited_at, edited_by,
				old_content, new_content,
				old_structured, new_structured
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`,
			event.MessageID,
			event.Timestamp,
			sessionID,
			sqlNullString(oldContent),
			sqlNullString(event.Body.Content),
			oldStructured,
			sqlNullString(event.Body.Structured),
		)
		if err != nil {
			return fmt.Errorf("insert edit history: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		// Message not in local DB yet — skip edit history, still attempt update
	default:
		return fmt.Errorf("query message: %w", err)
	}

	// Update message content (no-op if message doesn't exist locally)
	_, err = tx.Exec(`
		UPDATE messages
		SET body_content = ?, body_structured = ?, updated_at = ?
		WHERE message_id = ?
	`,
		event.Body.Content,
		sqlNullString(event.Body.Structured),
		event.Timestamp,
		event.MessageID,
	)
	if err != nil {
		return fmt.Errorf("update message: %w", err)
	}

	return tx.Commit()
}

func (p *Projector) applyMessageDelete(ctx context.Context, data json.RawMessage) error {
	var event types.MessageDeleteEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal message.delete: %w", err)
	}

	_, err := p.db.ExecContext(ctx, `
		UPDATE messages
		SET deleted = 1, deleted_at = ?, delete_reason = ?
		WHERE message_id = ?
	`,
		event.Timestamp,
		sqlNullString(event.Reason),
		event.MessageID,
	)
	if err != nil {
		return fmt.Errorf("delete message: %w", err)
	}

	return nil
}

func (p *Projector) applyMessageReceipt(ctx context.Context, data json.RawMessage) error {
	var event types.MessageReceiptEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal message.receipt: %w", err)
	}

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Check if the referenced message exists locally. If it doesn't (out-of-order
	// sync from a peer), skip the delivery/receipt projection entirely. The event
	// is still stored in JSONL and the events table — it just won't create delivery
	// rows until the message.create arrives. This prevents FK constraint failures
	// from poisoning the sync apply loop.
	//
	// Same design trade-off as applyMessageEdit: receipts that arrive before the
	// message are permanently lost from the live projection but preserved in JSONL.
	var exists int
	err = tx.QueryRow(`SELECT 1 FROM messages WHERE message_id = ?`, event.MessageID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		// Message not synced yet — skip delivery projection, commit empty tx
		return tx.Commit()
	}
	if err != nil {
		return fmt.Errorf("check message exists: %w", err)
	}

	// thrum-qb62: gate the INSERT on recipient legitimacy to prevent phantom
	// delivery rows. Previously any receipt event unconditionally inserted a
	// delivery row, which let `thrum message read --all` fabricate rows for
	// messages the agent was never targeted for — making send targeting look
	// like it had fanned out. The row is now only created when the agent is a
	// legitimate recipient: mentioned by agent_id or role, in a targeted
	// group, or on a broadcast-scoped message. Pre-v14 messages with legitimate
	// recipients still get their row created the first time they read.
	//
	// Note: if the delivery row already exists (the normal post-v14 path), the
	// INSERT is a no-op via OR IGNORE and the subsequent UPDATE sets seen_at /
	// read_at as usual. If no row exists and the agent is not a legitimate
	// recipient, no row is created and the UPDATE below is also a no-op —
	// the receipt event is still stored in JSONL + events for auditability.
	_, err = tx.Exec(`
		INSERT OR IGNORE INTO message_deliveries (message_id, recipient_agent_id, delivered_at)
		SELECT ?, ?, ?
		WHERE EXISTS (
			SELECT 1 FROM message_refs mr
			WHERE mr.message_id = ?
			  AND mr.ref_type = 'mention'
			  AND (
			    mr.ref_value = ?
			    OR mr.ref_value = (SELECT role FROM agents WHERE agent_id = ? LIMIT 1)
			  )
		) OR EXISTS (
			SELECT 1 FROM message_scopes ms
			WHERE ms.message_id = ?
			  AND ms.scope_type = 'broadcast'
		) OR EXISTS (
			SELECT 1 FROM message_scopes ms
			JOIN groups g ON g.name = ms.scope_value
			JOIN group_members gm ON g.group_id = gm.group_id
			WHERE ms.message_id = ?
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
				WHERE mr_lb.message_id = ?
				  AND mr_lb.ref_type IN ('mention', 'group', 'broadcast')
			)
			AND NOT EXISTS (
				SELECT 1 FROM message_scopes ms_lb
				WHERE ms_lb.message_id = ?
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
			WHERE m_self.message_id = ?
			  AND m_self.agent_id IN (?, 'user:' || ?)
		)
	`,
		event.MessageID, event.AgentID, event.Timestamp,
		event.MessageID, event.AgentID, event.AgentID,
		event.MessageID,
		event.MessageID, event.AgentID, event.AgentID,
		event.MessageID, event.MessageID,
		event.MessageID, event.AgentID, event.AgentID, // authored-self arm
	)
	if err != nil {
		return fmt.Errorf("ensure message delivery: %w", err)
	}

	switch event.ReceiptType {
	case "seen":
		_, err = tx.Exec(`
			UPDATE message_deliveries
			SET seen_at = COALESCE(seen_at, ?)
			WHERE message_id = ? AND recipient_agent_id = ?
		`, event.Timestamp, event.MessageID, event.AgentID)
	case "read":
		_, err = tx.Exec(`
			UPDATE message_deliveries
			SET seen_at = COALESCE(seen_at, ?),
			    read_at = COALESCE(read_at, ?)
			WHERE message_id = ? AND recipient_agent_id = ?
		`, event.Timestamp, event.Timestamp, event.MessageID, event.AgentID)
	default:
		return fmt.Errorf("unknown receipt_type %q", event.ReceiptType)
	}
	if err != nil {
		return fmt.Errorf("update message delivery receipt: %w", err)
	}

	return tx.Commit()
}

func (p *Projector) applyAgentRegister(ctx context.Context, data json.RawMessage) error {
	var event types.AgentRegisterEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal agent.register: %w", err)
	}

	// Coalesce AgentPID and ClaudePID for backward compat with old JSONL events
	pid := event.AgentPID
	if pid == 0 {
		pid = event.ClaudePID
	}

	// origin_daemon is carried through from the event so HandleRegister's
	// role+module conflict check can distinguish local agents from synced
	// ones (thrum-mm3l). The event's origin_daemon is set by State.WriteEvent
	// on the emitting daemon (to its own daemon ID) and preserved verbatim
	// when events propagate across daemons.
	//
	// thrum-ufv5.6 review #1: use ON CONFLICT DO UPDATE (not INSERT OR REPLACE)
	// so registered_at on an existing row is NOT overwritten by the re-register
	// event's timestamp. INSERT OR REPLACE deletes-and-reinserts, which loses
	// the original first-registration time — harmless pre-ufv5.6 (only the
	// PID-drift branch hit this path, rare) but now exposed on every --force
	// quickstart. agents.registered_at is read by `agent.list ORDER BY
	// registered_at DESC`, so preserving the original value keeps sort
	// stability across re-registers.
	//
	// display and hostname pass through as raw strings (not sqlNullString)
	// because schema.go declares them `TEXT NOT NULL DEFAULT ''`. SQLite's
	// INSERT OR REPLACE special-cases NULL on NOT-NULL-with-DEFAULT columns
	// by using the DEFAULT, but ON CONFLICT DO UPDATE SET col=excluded.col
	// with NULL writes NULL verbatim and trips the constraint. Passing ""
	// directly keeps both the INSERT and the UPDATE paths valid and matches
	// the pre-fix observable behavior (stored as empty string in both cases).
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO agents (agent_id, kind, role, module, display, hostname, agent_pid, registered_at, origin_daemon)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			kind          = excluded.kind,
			role          = excluded.role,
			module        = excluded.module,
			display       = excluded.display,
			hostname      = excluded.hostname,
			agent_pid     = excluded.agent_pid,
			origin_daemon = excluded.origin_daemon
	`,
		event.AgentID,
		event.Kind,
		event.Role,
		event.Module,
		event.Display,
		event.Hostname,
		pid,
		event.Timestamp,
		event.OriginDaemon,
	)
	if err != nil {
		return fmt.Errorf("insert agent: %w", err)
	}

	// When a new agent lands on disk its state/agents/<id>.json may already
	// exist (written by the writer before the event arrived here), or the
	// register event itself is the signal that the agent is now known. Either
	// way, attempt to resolve any orphans blocked on this agent_id.
	if p.pendingPool != nil && p.pendingResolver != nil {
		p.pendingPool.ResolveOnStateLand(ctx, []string{event.AgentID}, p.pendingResolver)
	}

	return nil
}

func (p *Projector) applySessionStart(ctx context.Context, data json.RawMessage) error {
	var event types.AgentSessionStartEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal agent.session.start: %w", err)
	}

	// INSERT OR IGNORE: a peer-replicated agent.session.start for a session
	// that already exists locally (same session_id arriving via sync after the
	// originating daemon wrote it) is a no-op. session_id is by ULID
	// construction unique to one logical session, and started_at is immutable
	// for that session — there is nothing to update. We deliberately do NOT
	// UPSERT last_seen_at from the event timestamp because other apply paths
	// touch last_seen_at independently; a late-arriving peer copy of the start
	// event could regress freshness if it overwrote a newer value.
	_, err := p.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO sessions (session_id, agent_id, started_at, last_seen_at)
		VALUES (?, ?, ?, ?)
	`,
		event.SessionID,
		event.AgentID,
		event.Timestamp,
		event.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}

	return nil
}

func (p *Projector) applySessionEnd(ctx context.Context, data json.RawMessage) error {
	var event types.AgentSessionEndEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal agent.session.end: %w", err)
	}

	_, err := p.db.ExecContext(ctx, `
		UPDATE sessions
		SET ended_at = ?, end_reason = ?
		WHERE session_id = ?
	`,
		event.Timestamp,
		sqlNullString(event.Reason),
		event.SessionID,
	)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}

	return nil
}

func (p *Projector) applyAgentUpdate(ctx context.Context, data json.RawMessage) error {
	var event types.AgentUpdateEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal agent.update: %w", err)
	}

	// Get existing work contexts for this agent
	existing, err := p.getWorkContexts(ctx, event.AgentID)
	if err != nil {
		return fmt.Errorf("get existing contexts: %w", err)
	}

	// Merge incoming with existing
	merged := mergeWorkContexts(existing, event.WorkContexts)

	// Update database with merged result
	if err := p.setWorkContexts(ctx, event.AgentID, merged); err != nil {
		return fmt.Errorf("set work contexts: %w", err)
	}

	return nil
}

// getWorkContexts retrieves all work contexts for an agent from the database.
func (p *Projector) getWorkContexts(ctx context.Context, agentID string) ([]types.SessionWorkContext, error) {
	query := `SELECT session_id, branch, worktree_path,
	                 unmerged_commits, uncommitted_files, changed_files, git_updated_at,
	                 current_task, task_updated_at, intent, intent_updated_at
	          FROM agent_work_contexts
	          WHERE agent_id = ?`

	rows, err := p.db.QueryContext(ctx, query, agentID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var contexts []types.SessionWorkContext

	for rows.Next() {
		var ctx types.SessionWorkContext
		var branch, worktreePath, unmergedCommitsJSON, uncommittedFilesJSON, changedFilesJSON, gitUpdatedAt sql.NullString
		var currentTask, taskUpdatedAt, intent, intentUpdatedAt sql.NullString

		err := rows.Scan(
			&ctx.SessionID,
			&branch,
			&worktreePath,
			&unmergedCommitsJSON,
			&uncommittedFilesJSON,
			&changedFilesJSON,
			&gitUpdatedAt,
			&currentTask,
			&taskUpdatedAt,
			&intent,
			&intentUpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		// Set optional fields
		if branch.Valid {
			ctx.Branch = branch.String
		}
		if worktreePath.Valid {
			ctx.WorktreePath = worktreePath.String
		}
		if gitUpdatedAt.Valid {
			ctx.GitUpdatedAt = gitUpdatedAt.String
		}
		if currentTask.Valid {
			ctx.CurrentTask = currentTask.String
		}
		if taskUpdatedAt.Valid {
			ctx.TaskUpdatedAt = taskUpdatedAt.String
		}
		if intent.Valid {
			ctx.Intent = intent.String
		}
		if intentUpdatedAt.Valid {
			ctx.IntentUpdatedAt = intentUpdatedAt.String
		}

		// Unmarshal JSON fields
		if unmergedCommitsJSON.Valid && unmergedCommitsJSON.String != "" {
			var commits []types.CommitSummary
			if err := json.Unmarshal([]byte(unmergedCommitsJSON.String), &commits); err == nil {
				ctx.UnmergedCommits = commits
			}
		}

		if uncommittedFilesJSON.Valid && uncommittedFilesJSON.String != "" {
			var files []string
			if err := json.Unmarshal([]byte(uncommittedFilesJSON.String), &files); err == nil {
				ctx.UncommittedFiles = files
			}
		}

		if changedFilesJSON.Valid && changedFilesJSON.String != "" {
			var files []string
			if err := json.Unmarshal([]byte(changedFilesJSON.String), &files); err == nil {
				ctx.ChangedFiles = files
			}
		}

		contexts = append(contexts, ctx)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return contexts, nil
}

// setWorkContexts replaces all work contexts for an agent in the database.
// Contexts whose session_id doesn't exist locally are skipped to avoid FK
// constraint failures during out-of-order peer sync (see applyMessageEdit for
// the general design trade-off).
func (p *Projector) setWorkContexts(ctx context.Context, agentID string, contexts []types.SessionWorkContext) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Delete all existing contexts for this agent
	_, err = tx.Exec("DELETE FROM agent_work_contexts WHERE agent_id = ?", agentID)
	if err != nil {
		return fmt.Errorf("delete existing contexts: %w", err)
	}

	// Insert new contexts
	for _, wc := range contexts {
		// Check that the referenced session exists locally. If it doesn't
		// (out-of-order sync from a peer — the agent.session.start hasn't
		// arrived yet), skip this context rather than failing the FK constraint.
		var exists int
		err = tx.QueryRow(`SELECT 1 FROM sessions WHERE session_id = ?`, wc.SessionID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			continue // Session not synced yet — skip this context
		}
		if err != nil {
			return fmt.Errorf("check session exists: %w", err)
		}

		// Marshal JSON fields
		unmergedCommitsJSON, _ := json.Marshal(wc.UnmergedCommits)
		uncommittedFilesJSON, _ := json.Marshal(wc.UncommittedFiles)
		changedFilesJSON, _ := json.Marshal(wc.ChangedFiles)

		_, err = tx.Exec(`
			INSERT INTO agent_work_contexts (
				session_id, agent_id, branch, worktree_path,
				unmerged_commits, uncommitted_files, changed_files, git_updated_at,
				current_task, task_updated_at, intent, intent_updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			wc.SessionID,
			agentID,
			sqlNullString(wc.Branch),
			sqlNullString(wc.WorktreePath),
			string(unmergedCommitsJSON),
			string(uncommittedFilesJSON),
			string(changedFilesJSON),
			sqlNullString(wc.GitUpdatedAt),
			sqlNullString(wc.CurrentTask),
			sqlNullString(wc.TaskUpdatedAt),
			sqlNullString(wc.Intent),
			sqlNullString(wc.IntentUpdatedAt),
		)
		if err != nil {
			return fmt.Errorf("insert context: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// mergeWorkContexts merges two slices of work contexts by session_id.
// For contexts with the same session_id, keeps the one with newer git_updated_at.
func mergeWorkContexts(a, b []types.SessionWorkContext) []types.SessionWorkContext {
	bySession := make(map[string]types.SessionWorkContext)

	// Add all from a
	for _, ctx := range a {
		bySession[ctx.SessionID] = ctx
	}

	// Merge in from b
	for _, ctx := range b {
		if existing, ok := bySession[ctx.SessionID]; ok {
			// Both have this session - keep newer by git_updated_at
			if ctx.GitUpdatedAt > existing.GitUpdatedAt {
				bySession[ctx.SessionID] = ctx
			}
			// else keep existing (it's newer or same)
		} else {
			// Only in b, add it
			bySession[ctx.SessionID] = ctx
		}
	}

	// Convert map to slice
	result := make([]types.SessionWorkContext, 0, len(bySession))
	for _, ctx := range bySession {
		result = append(result, ctx)
	}

	return result
}

func (p *Projector) applyAgentCleanup(ctx context.Context, data json.RawMessage) error {
	var event types.AgentCleanupEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal agent.cleanup: %w", err)
	}

	agentID := event.AgentID

	// Delete message child tables
	for _, table := range []string{"message_edits", "message_reads", "message_deliveries", "message_refs", "message_scopes"} {
		//nolint:gosec // table name is a hardcoded constant, not user input
		q := `DELETE FROM ` + table + ` WHERE message_id IN (SELECT message_id FROM messages WHERE agent_id = ?)`
		if _, err := p.db.ExecContext(ctx, q, agentID); err != nil {
			return fmt.Errorf("delete %s for agent: %w", table, err)
		}
	}

	// Delete messages
	if _, err := p.db.ExecContext(ctx, `DELETE FROM messages WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("delete messages for agent: %w", err)
	}

	// Delete session child tables
	for _, table := range []string{"session_refs", "session_scopes"} {
		//nolint:gosec // table name is a hardcoded constant, not user input
		q := `DELETE FROM ` + table + ` WHERE session_id IN (SELECT session_id FROM sessions WHERE agent_id = ?)`
		if _, err := p.db.ExecContext(ctx, q, agentID); err != nil {
			return fmt.Errorf("delete %s for agent: %w", table, err)
		}
	}

	// Delete sessions
	if _, err := p.db.ExecContext(ctx, `DELETE FROM sessions WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("delete sessions for agent: %w", err)
	}

	// Delete events referencing this agent (but not the cleanup event itself).
	// Note: LIKE pattern shares a known limitation with HandleDelete in agent.go —
	// agent IDs that are prefixes of other IDs could cause false positives.
	// Acceptable since agent names are generated with sufficient entropy.
	if _, err := p.db.ExecContext(ctx,
		`DELETE FROM events WHERE event_json LIKE ? AND type != 'agent.cleanup'`,
		`%"agent_id":"`+agentID+`"%`); err != nil {
		return fmt.Errorf("delete events for agent: %w", err)
	}

	// Delete agent row
	if _, err := p.db.ExecContext(ctx, `DELETE FROM agents WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}

	return nil
}

func (p *Projector) applyPurgeExecuted(ctx context.Context, data json.RawMessage) error {
	var event types.PurgeExecutedEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal purge.executed: %w", err)
	}

	cutoff := event.Cutoff

	// Store/update purge cutoff (only advance, never regress)
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO purge_metadata (key, value) VALUES ('purge_cutoff', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value
		 WHERE excluded.value > purge_metadata.value`,
		cutoff)
	if err != nil {
		return fmt.Errorf("store purge cutoff: %w", err)
	}

	// Delete old messages (child tables first)
	for _, table := range []string{"message_edits", "message_reads", "message_deliveries", "message_refs", "message_scopes"} {
		//nolint:gosec // table name is a hardcoded constant, not user input
		q := `DELETE FROM ` + table + ` WHERE message_id IN (SELECT message_id FROM messages WHERE created_at < ?)`
		if _, err := p.db.ExecContext(ctx, q, cutoff); err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}
	if _, err := p.db.ExecContext(ctx, `DELETE FROM messages WHERE created_at < ?`, cutoff); err != nil {
		return fmt.Errorf("delete messages: %w", err)
	}

	// Delete old sessions (child tables first)
	for _, table := range []string{"session_refs", "session_scopes"} {
		//nolint:gosec // table name is a hardcoded constant, not user input
		q := `DELETE FROM ` + table + ` WHERE session_id IN (SELECT session_id FROM sessions WHERE started_at < ?)`
		if _, err := p.db.ExecContext(ctx, q, cutoff); err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}
	if _, err := p.db.ExecContext(ctx, `DELETE FROM sessions WHERE started_at < ?`, cutoff); err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}

	// Delete old events (but not purge.executed events — they must survive for replay)
	if _, err := p.db.ExecContext(ctx,
		`DELETE FROM events WHERE timestamp < ? AND type != 'purge.executed'`, cutoff); err != nil {
		return fmt.Errorf("delete events: %w", err)
	}

	return nil
}

func (p *Projector) applyGroupCreate(ctx context.Context, data json.RawMessage) error {
	var event types.GroupCreateEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal group.create: %w", err)
	}

	_, err := p.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO groups (group_id, name, description, created_at, created_by)
		VALUES (?, ?, ?, ?, ?)
	`, event.GroupID, event.Name, sqlNullString(event.Description), event.Timestamp, event.CreatedBy)
	if err != nil {
		return fmt.Errorf("insert group: %w", err)
	}

	// A new group landing may unblock orphans whose bridge-group state file
	// is now present (or will arrive shortly). Attempt resolution synchronously.
	if p.pendingPool != nil && p.pendingResolver != nil {
		p.pendingPool.ResolveOnStateLand(ctx, []string{event.GroupID}, p.pendingResolver)
	}

	return nil
}

func (p *Projector) applyGroupMemberAdd(ctx context.Context, data json.RawMessage) error {
	var event types.GroupMemberAddEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal group.member.add: %w", err)
	}

	// Check if the referenced group exists locally. If not (out-of-order sync),
	// skip the member insert. The event is still stored in JSONL and the events
	// table. This prevents FK constraint failures from poisoning the sync loop.
	//
	// Same design trade-off as applyMessageEdit: member-adds that arrive before
	// the group.create are permanently lost from the projection but preserved
	// in JSONL. A full Rebuild would apply them correctly.
	var exists int
	err := p.db.QueryRowContext(ctx, `SELECT 1 FROM groups WHERE group_id = ?`, event.GroupID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // Group not synced yet — skip
	}
	if err != nil {
		return fmt.Errorf("check group exists: %w", err)
	}

	_, err = p.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO group_members (group_id, member_type, member_value, added_at, added_by)
		VALUES (?, ?, ?, ?, ?)
	`, event.GroupID, event.MemberType, event.MemberValue, event.Timestamp, sqlNullString(event.AddedBy))
	if err != nil {
		return fmt.Errorf("insert group member: %w", err)
	}

	return nil
}

func (p *Projector) applyGroupMemberRemove(ctx context.Context, data json.RawMessage) error {
	var event types.GroupMemberRemoveEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal group.member.remove: %w", err)
	}

	_, err := p.db.ExecContext(ctx, `
		DELETE FROM group_members
		WHERE group_id = ? AND member_type = ? AND member_value = ?
	`, event.GroupID, event.MemberType, event.MemberValue)
	if err != nil {
		return fmt.Errorf("delete group member: %w", err)
	}

	return nil
}

func (p *Projector) applyGroupUpdate(ctx context.Context, data json.RawMessage) error {
	var event types.GroupUpdateEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal group.update: %w", err)
	}

	if desc, ok := event.Fields["description"]; ok {
		_, err := p.db.ExecContext(ctx, `
			UPDATE groups SET description = ?, updated_at = ? WHERE group_id = ?
		`, sqlNullString(desc), event.Timestamp, event.GroupID)
		if err != nil {
			return fmt.Errorf("update group description: %w", err)
		}
	}

	if name, ok := event.Fields["name"]; ok {
		_, err := p.db.ExecContext(ctx, `
			UPDATE groups SET name = ?, updated_at = ? WHERE group_id = ?
		`, name, event.Timestamp, event.GroupID)
		if err != nil {
			return fmt.Errorf("update group name: %w", err)
		}
	}

	return nil
}

func (p *Projector) applyGroupDelete(ctx context.Context, data json.RawMessage) error {
	var event types.GroupDeleteEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("unmarshal group.delete: %w", err)
	}

	// CASCADE will delete group_members too
	_, err := p.db.ExecContext(ctx, `DELETE FROM groups WHERE group_id = ?`, event.GroupID)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}

	return nil
}

// stateFileExists reports whether a state file for the given ID is present on
// disk in the sync worktree. It checks both state/agents/<id>.json and
// state/bridge-groups/<id>.json so it handles both agent and bridge-group IDs
// without requiring the caller to know which kind the ID refers to.
func stateFileExists(syncDir, id string) bool {
	agentPath := filepath.Join(syncDir, "state", "agents", id+".json")
	if _, err := os.Stat(agentPath); err == nil {
		return true
	}
	bgPath := filepath.Join(syncDir, "state", "bridge-groups", id+".json")
	if _, err := os.Stat(bgPath); err == nil {
		return true
	}
	return false
}

// sqlNullString returns a sql.NullString for optional string fields.
func sqlNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// boolToInt converts a boolean to an integer (0 or 1) for SQLite.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
