// Package snapshot implements the sync-trigger-time walker that reads local
// SQLite events since the last walk and writes the latest state to state files,
// messages-v2/, and receipts/ before Triggers.SyncOnWrite fires commit-and-push.
//
// # Invocation contract (load-bearing — do NOT change without spec re-ratification)
//
// WalkAndWrite is called ONLY by Triggers.SyncOnWrite, which is called ONLY by
// state.WriteEvent when the inbound event is structural per spec §3.2 whitelist
// (agent.register, group.create, group.delete, group.member.add,
// group.member.remove, message.create). The walker NEVER runs on its own timer
// and NEVER runs in response to a non-structural event (message.edit,
// message.delete, message.receipt, etc.).
//
// Edit / delete / receipt rows accumulate in the local journal between
// structural triggers; the walker folds them into messages-v2 / receipts at the
// NEXT structural-event-driven walk. That deferred folding is the whole reason
// T2 (100 receipts → 0 commits) works. If the walker is ever called outside
// this contract — by a startup hook, a manual debug command, or a future code
// path — receipt-only churn will start producing commits and the rearchitect's
// primary invariant (idle = silent) breaks.
package snapshot

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	gosync "sync"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/sync/state"
)

// WalkCounts holds the row/file counts from the most recent WalkAndWrite call.
// Used by loop.go to populate the sync.commit telemetry event fields.
type WalkCounts struct {
	StateFiles  int // number of agent/bridge-group state files written
	MessageRows int // number of messages-v2 rows appended
	ReceiptRows int // number of receipts rows appended
}

// Walker reads local SQLite projection + events table since last walk and
// writes the latest state to state files, messages-v2/, and receipts/.
// Called before Triggers.SyncOnWrite fires commit-and-push.
type Walker struct {
	db            *safedb.DB
	stateWriter   *state.Writer
	msgWriter     *MessageStateWriter
	receiptWriter *ReceiptStateWriter
	syncDir       string
	daemonID      string
	mu            gosync.Mutex
	lastWalkAt    time.Time
	lastCounts    WalkCounts // counts from the most recent walk; reset at each walk start
}

// NewWalker constructs a Walker.
//   - db: the daemon's SQLite database (events + agents + messages tables).
//   - sw: state-file writer for agent / bridge-group state files.
//   - msgW: message-state writer for messages-v2/ rows.
//   - recW: receipt-state writer for receipts/ rows.
//   - syncDir: absolute path to the sync worktree root.
//   - daemonID: identity of this daemon (for authoring context).
func NewWalker(
	db *safedb.DB,
	sw *state.Writer,
	msgW *MessageStateWriter,
	recW *ReceiptStateWriter,
	syncDir, daemonID string,
) *Walker {
	return &Walker{
		db:            db,
		stateWriter:   sw,
		msgWriter:     msgW,
		receiptWriter: recW,
		syncDir:       syncDir,
		daemonID:      daemonID,
	}
}

// SetLastWalkAt sets the lastWalkAt boundary (for tests and bootstrap).
func (w *Walker) SetLastWalkAt(t time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastWalkAt = t
}

// GetLastWalkAt returns the current lastWalkAt value (for tests).
func (w *Walker) GetLastWalkAt() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastWalkAt
}

// LastCounts returns a snapshot copy of the counts from the most recent
// WalkAndWrite call. Called by loop.doSync after CommitAndPush succeeds
// to populate the sync.commit telemetry event fields.
func (w *Walker) LastCounts() WalkCounts {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastCounts
}

// WalkAndWrite sweeps the events table for all events since lastWalkAt,
// derives latest state per touched entity, and writes state files + message
// rows + receipt rows before the caller fires commit-and-push.
//
// Write ordering per spec §8.1:
//  1. Agent state files (WriteAgent)
//  2. Bridge-group state files (WriteBridgeGroup / DeleteBridgeGroup)
//  3. Message rows (AppendSnapshot to messages-v2/)
//  4. Receipt rows (AppendSnapshot to receipts/)
//  5. Deletions (already handled inline in step 2 for bridge-groups)
//
// lastWalkAt advances only after ALL writes succeed. On any write error,
// the function returns immediately with the error and lastWalkAt is not
// updated — next call will re-attempt the same window.
func (w *Walker) WalkAndWrite(ctx context.Context) error {
	// Serialize concurrent callers. Triggers.SyncOnWrite serializes via the
	// trigger channel, but the mutex is cheap defense-in-depth.
	w.mu.Lock()
	defer w.mu.Unlock()

	// Reset per-walk counters so LastCounts() always reflects the most
	// recent walk, not a cumulative total.
	w.lastCounts = WalkCounts{}

	// Query all events since lastWalkAt.
	// Column names from schema: event_id, sequence, type, timestamp, origin_daemon, event_json
	const q = `SELECT event_id, type, timestamp, event_json
	           FROM events
	           WHERE timestamp > ?
	           ORDER BY sequence ASC`

	rows, err := w.db.QueryContext(ctx, q, w.lastWalkAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("snapshot: query events since lastWalkAt: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Accumulate actions in typed slices so we can apply in spec §8.1 order.
	type agentAction struct {
		agentID string
		payload map[string]any
	}
	type bridgeGroupAction struct {
		groupID string
		delete  bool
		payload map[string]any
	}
	type msgAction struct {
		agentID string
		msgID   string
	}
	type receiptAction struct {
		issuerID  string
		messageID string
	}

	var (
		agentActions    []agentAction
		bridgeGroupActs []bridgeGroupAction
		msgActions      []msgAction
		receiptActions  []receiptAction
		latestTimestamp time.Time
	)

	for rows.Next() {
		var evtID, evtType, evtTimestamp, evtJSON string
		if err := rows.Scan(&evtID, &evtType, &evtTimestamp, &evtJSON); err != nil {
			return fmt.Errorf("snapshot: scan event row: %w", err)
		}

		// Track the latest timestamp we've seen so we can advance lastWalkAt.
		if ts, parseErr := time.Parse(time.RFC3339Nano, evtTimestamp); parseErr == nil {
			if ts.After(latestTimestamp) {
				latestTimestamp = ts
			}
		}

		var raw map[string]any
		if err := json.Unmarshal([]byte(evtJSON), &raw); err != nil {
			// Skip unparseable events rather than aborting the whole walk.
			continue
		}

		switch evtType {
		case "agent.register", "agent.update":
			agentID, _ := raw["agent_id"].(string)
			if agentID != "" {
				agentActions = append(agentActions, agentAction{agentID: agentID, payload: raw})
			}

		case "group.create", "group.member.add", "group.member.remove":
			groupID, _ := raw["group_id"].(string)
			if groupID != "" {
				bridgeGroupActs = append(bridgeGroupActs, bridgeGroupAction{
					groupID: groupID,
					delete:  false,
					payload: raw,
				})
			}

		case "group.delete":
			groupID, _ := raw["group_id"].(string)
			if groupID != "" {
				bridgeGroupActs = append(bridgeGroupActs, bridgeGroupAction{
					groupID: groupID,
					delete:  true,
					payload: raw,
				})
			}

		case "message.create", "message.edit", "message.delete":
			msgID, _ := raw["message_id"].(string)
			if msgID == "" {
				continue
			}
			// Derive agent_id from the projection (latest source of truth).
			agentID, err := w.lookupMessageAuthor(ctx, msgID, raw)
			if err != nil {
				return fmt.Errorf("snapshot: lookup author for %s: %w", msgID, err)
			}
			if agentID != "" {
				msgActions = append(msgActions, msgAction{agentID: agentID, msgID: msgID})
			}

		case "message.receipt":
			issuerID, _ := raw["agent_id"].(string)
			msgID, _ := raw["message_id"].(string)
			if issuerID != "" && msgID != "" {
				receiptActions = append(receiptActions, receiptAction{
					issuerID:  issuerID,
					messageID: msgID,
				})
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("snapshot: iterate events: %w", err)
	}
	_ = rows.Close()

	// If nothing to do, advance lastWalkAt and return.
	if len(agentActions) == 0 &&
		len(bridgeGroupActs) == 0 &&
		len(msgActions) == 0 &&
		len(receiptActions) == 0 {
		if !latestTimestamp.IsZero() {
			w.lastWalkAt = latestTimestamp
		}
		return nil
	}

	// ---------------------------------------------------------------------------
	// Spec §8.1 ordering: agents → bridge-groups → messages → receipts
	// ---------------------------------------------------------------------------

	// 1. Agent state files.
	for _, a := range agentActions {
		snap, err := w.buildAgentSnapshot(ctx, a.agentID)
		if err != nil {
			return fmt.Errorf("snapshot: build agent snapshot for %s: %w", a.agentID, err)
		}
		if snap == nil {
			// Agent not in projection — may have been cleaned up; skip.
			continue
		}
		if writeErr := w.stateWriter.WriteAgent(ctx, *snap); writeErr != nil {
			// ErrNotOwner is expected when events for foreign agents are in our
			// window (cross-daemon synced events). Skip silently.
			if writeErr == state.ErrNotOwner {
				continue
			}
			return fmt.Errorf("snapshot: write agent state for %s: %w", a.agentID, writeErr)
		}
		w.lastCounts.StateFiles++
	}

	// 2. Bridge-group state files (write + delete).
	for _, bg := range bridgeGroupActs {
		if bg.delete {
			// DeleteBridgeGroup is idempotent and handles ErrNotOwner internally.
			if err := w.stateWriter.DeleteBridgeGroup(ctx, bg.groupID); err != nil {
				if err == state.ErrNotOwner {
					continue
				}
				return fmt.Errorf("snapshot: delete bridge group %s: %w", bg.groupID, err)
			}
			w.lastCounts.StateFiles++
			continue
		}
		bgSnap, err := w.buildBridgeGroupSnapshot(ctx, bg.groupID, bg.payload)
		if err != nil {
			return fmt.Errorf("snapshot: build bridge-group snapshot for %s: %w", bg.groupID, err)
		}
		if writeErr := w.stateWriter.WriteBridgeGroup(ctx, bgSnap); writeErr != nil {
			if writeErr == state.ErrNotOwner {
				continue
			}
			return fmt.Errorf("snapshot: write bridge-group state for %s: %w", bg.groupID, writeErr)
		}
		w.lastCounts.StateFiles++
	}

	// 3. Message rows — append to messages-v2/<agentID>.jsonl.
	// De-duplicate by message ID so the most recent state per message is written
	// when multiple events touch the same message (e.g. create + edit in the same
	// window). We resolve the final projection state for each unique message ID.
	seenMsgIDs := make(map[string]bool, len(msgActions))
	for _, ma := range msgActions {
		if seenMsgIDs[ma.msgID] {
			continue // already handled by an earlier action for the same message
		}
		seenMsgIDs[ma.msgID] = true

		row, err := w.buildMessageRow(ctx, ma.agentID, ma.msgID)
		if err != nil {
			return fmt.Errorf("snapshot: build message row for %s: %w", ma.msgID, err)
		}
		if row == nil {
			// Message not in projection (may have been purged).
			continue
		}
		if appendErr := w.msgWriter.AppendSnapshot(ctx, ma.agentID, *row); appendErr != nil {
			return fmt.Errorf("snapshot: append message row for %s: %w", ma.msgID, appendErr)
		}
		w.lastCounts.MessageRows++
	}

	// 4. Receipt rows — append to receipts/<issuerID>.jsonl.
	for _, ra := range receiptActions {
		row, err := w.buildReceiptRow(ctx, ra.issuerID, ra.messageID)
		if err != nil {
			return fmt.Errorf("snapshot: build receipt row (%s, %s): %w", ra.issuerID, ra.messageID, err)
		}
		if row == nil {
			continue
		}
		if appendErr := w.receiptWriter.AppendSnapshot(ctx, ra.issuerID, *row); appendErr != nil {
			return fmt.Errorf("snapshot: append receipt row (%s, %s): %w", ra.issuerID, ra.messageID, appendErr)
		}
		w.lastCounts.ReceiptRows++
	}

	// All writes succeeded — advance lastWalkAt.
	if !latestTimestamp.IsZero() {
		w.lastWalkAt = latestTimestamp
	}
	return nil
}

// ---------------------------------------------------------------------------
// Snapshot builders — derive latest state from the SQLite projection.
// ---------------------------------------------------------------------------

// buildAgentSnapshot queries the agents (and optionally agent_work_contexts)
// tables to construct the current AgentStateSnapshot for the given agentID.
// Returns (nil, nil) when the agent is absent from the projection.
func (w *Walker) buildAgentSnapshot(ctx context.Context, agentID string) (*state.AgentStateSnapshot, error) {
	const q = `SELECT kind, role, module, display, hostname, last_seen_at
	           FROM agents WHERE agent_id = ? LIMIT 1`
	row := w.db.QueryRowContext(ctx, q, agentID)

	var kind, role, module, display, hostname, lastSeenAt string
	if err := row.Scan(&kind, &role, &module, &display, &hostname, &lastSeenAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query agent %s: %w", agentID, err)
	}

	// Attempt to resolve the worktree path from agent_work_contexts.
	var worktree string
	const wq = `SELECT worktree_path FROM agent_work_contexts WHERE agent_id = ? LIMIT 1`
	_ = w.db.QueryRowContext(ctx, wq, agentID).Scan(&worktree)

	var lastSeen time.Time
	if lastSeenAt != "" {
		if t, parseErr := time.Parse(time.RFC3339Nano, lastSeenAt); parseErr == nil {
			lastSeen = t
		}
	}
	if lastSeen.IsZero() {
		lastSeen = time.Now().UTC()
	}

	return &state.AgentStateSnapshot{
		AgentID:    agentID,
		Name:       agentID, // name is not stored separately in agents table; use agentID as fallback
		Role:       role,
		Module:     module,
		Display:    display,
		Hostname:   hostname,
		Worktree:   worktree,
		Branch:     "", // resolved by state.Writer.WriteAgent via branchResolver
		Kind:       kind,
		LastSeenAt: lastSeen,
		Version:    1,
	}, nil
}

// buildBridgeGroupSnapshot constructs a BridgeGroupStateSnapshot.
// For group.create events the ownerDaemon comes from the event payload
// (origin_daemon); for member.add/remove we use the event's origin as fallback
// but the file's existing owner_daemon is authoritative.
func (w *Walker) buildBridgeGroupSnapshot(ctx context.Context, groupID string, raw map[string]any) (state.BridgeGroupStateSnapshot, error) {
	// Derive owner from origin_daemon in the event payload.
	ownerDaemon, _ := raw["origin_daemon"].(string)
	if ownerDaemon == "" {
		ownerDaemon = w.daemonID
	}

	// Query members from group_members table if available.
	var members []string
	const mq = `SELECT member_value FROM group_members WHERE group_id = ? ORDER BY added_at ASC`
	mrows, err := w.db.QueryContext(ctx, mq, groupID)
	if err == nil {
		defer func() { _ = mrows.Close() }()
		for mrows.Next() {
			var mv string
			if scanErr := mrows.Scan(&mv); scanErr == nil {
				members = append(members, mv)
			}
		}
		_ = mrows.Close()
	}

	// Determine bridge kind from group_id prefix (tg:* = telegram, peer:* = peer).
	bridgeKind := "peer"
	if len(groupID) > 3 && groupID[:3] == "tg:" {
		bridgeKind = "telegram"
	}

	return state.BridgeGroupStateSnapshot{
		GroupID:     groupID,
		Kind:        "bridge_group",
		BridgeKind:  bridgeKind,
		OwnerDaemon: ownerDaemon,
		Members:     members,
		CreatedAt:   time.Now().UTC(), // approximation; event timestamp not always present
		LastSeenAt:  time.Now().UTC(),
		Version:     1,
	}, nil
}

// buildMessageRow queries the messages table for the latest state of msgID
// and constructs a MessageStateRow. Returns (nil, nil) if the message is absent.
func (w *Walker) buildMessageRow(ctx context.Context, agentID, msgID string) (*MessageStateRow, error) {
	const q = `SELECT agent_id, body_content, created_at, deleted FROM messages WHERE message_id = ? LIMIT 1`
	row := w.db.QueryRowContext(ctx, q, msgID)

	var dbAgentID, bodyContent, createdAt string
	var deleted int
	if err := row.Scan(&dbAgentID, &bodyContent, &createdAt, &deleted); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query message %s: %w", msgID, err)
	}

	// Use the DB-sourced agentID as authoritative (caller may have passed a fallback).
	authorID := dbAgentID
	if authorID == "" {
		authorID = agentID
	}

	var createdAtTime time.Time
	if createdAt != "" {
		if t, parseErr := time.Parse(time.RFC3339Nano, createdAt); parseErr == nil {
			createdAtTime = t
		}
	}
	if createdAtTime.IsZero() {
		createdAtTime = time.Now().UTC()
	}

	return &MessageStateRow{
		MessageID: msgID,
		AuthorID:  authorID,
		Body:      bodyContent,
		CreatedAt: createdAtTime,
		Deleted:   deleted != 0,
		Version:   1,
	}, nil
}

// buildReceiptRow constructs a ReceiptStateRow for the given (issuerID, messageID) pair.
// Returns (nil, nil) when the message is absent (so we can't populate CreatedAt, but
// we still write the receipt with the current time as ReadAt).
func (w *Walker) buildReceiptRow(ctx context.Context, issuerID, messageID string) (*ReceiptStateRow, error) {
	// Look up read_at from message_deliveries if the table is available.
	var readAt time.Time
	const rdq = `SELECT read_at FROM message_deliveries WHERE message_id = ? AND recipient_agent_id = ? LIMIT 1`
	var readAtStr sql.NullString
	if err := w.db.QueryRowContext(ctx, rdq, messageID, issuerID).Scan(&readAtStr); err == nil && readAtStr.Valid {
		if t, parseErr := time.Parse(time.RFC3339Nano, readAtStr.String); parseErr == nil {
			readAt = t
		}
	}
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	}

	return &ReceiptStateRow{
		MessageID: messageID,
		AgentID:   issuerID,
		ReadAt:    readAt,
		Version:   1,
	}, nil
}

// lookupMessageAuthor returns the agentID of the message author by checking
// the messages table first, then falling back to agent_id in the event payload.
func (w *Walker) lookupMessageAuthor(ctx context.Context, msgID string, raw map[string]any) (string, error) {
	var agentID string
	const q = `SELECT agent_id FROM messages WHERE message_id = ? LIMIT 1`
	if err := w.db.QueryRowContext(ctx, q, msgID).Scan(&agentID); err == nil {
		return agentID, nil
	}
	// Fallback: agent_id from event payload (message.create carries it).
	if fallback, ok := raw["agent_id"].(string); ok && fallback != "" {
		return fallback, nil
	}
	return "", nil
}

// ---------------------------------------------------------------------------
// MessageStateWriter
// ---------------------------------------------------------------------------

// MessageStateWriter writes snapshot rows to messages-v2/<agentID>.jsonl.
// Append-only; no dedup at write time — dedup is read-side + compaction-side only.
type MessageStateWriter struct {
	syncDir  string
	daemonID string
}

// NewMessageStateWriter constructs a MessageStateWriter.
func NewMessageStateWriter(syncDir, daemonID string) *MessageStateWriter {
	return &MessageStateWriter{syncDir: syncDir, daemonID: daemonID}
}

// AppendSnapshot appends a MessageStateRow to messages-v2/<agentID>.jsonl.
// Uses O_APPEND|O_CREATE|O_WRONLY per spec §4.3 (merge=union git driver).
func (w *MessageStateWriter) AppendSnapshot(_ context.Context, agentID string, msg MessageStateRow) error {
	dir := filepath.Join(w.syncDir, "messages-v2")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("msgwriter: mkdir %s: %w", dir, err)
	}

	path := filepath.Join(dir, agentID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) // #nosec G304 -- internal sync state file
	if err != nil {
		return fmt.Errorf("msgwriter: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("msgwriter: marshal row: %w", err)
	}

	w2 := bufio.NewWriter(f)
	_, _ = w2.Write(data)
	_ = w2.WriteByte('\n')
	if err := w2.Flush(); err != nil {
		return fmt.Errorf("msgwriter: flush %s: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// ReceiptStateWriter
// ---------------------------------------------------------------------------

// ReceiptStateWriter writes snapshot rows to receipts/<receiptIssuerID>.jsonl.
// Append-only; no dedup at write time.
type ReceiptStateWriter struct {
	syncDir  string
	daemonID string
}

// NewReceiptStateWriter constructs a ReceiptStateWriter.
func NewReceiptStateWriter(syncDir, daemonID string) *ReceiptStateWriter {
	return &ReceiptStateWriter{syncDir: syncDir, daemonID: daemonID}
}

// AppendSnapshot appends a ReceiptStateRow to receipts/<receiptIssuerID>.jsonl.
// Uses O_APPEND|O_CREATE|O_WRONLY per spec §4.4 (merge=union git driver).
func (w *ReceiptStateWriter) AppendSnapshot(_ context.Context, receiptIssuerID string, rec ReceiptStateRow) error {
	dir := filepath.Join(w.syncDir, "receipts")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("recwriter: mkdir %s: %w", dir, err)
	}

	path := filepath.Join(dir, receiptIssuerID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) // #nosec G304 -- internal sync state file
	if err != nil {
		return fmt.Errorf("recwriter: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("recwriter: marshal row: %w", err)
	}

	w2 := bufio.NewWriter(f)
	_, _ = w2.Write(data)
	_ = w2.WriteByte('\n')
	if err := w2.Flush(); err != nil {
		return fmt.Errorf("recwriter: flush %s: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Row types
// ---------------------------------------------------------------------------

// MessageStateRow is one line in messages-v2/<agentID>.jsonl.
// Spec §4.3: dedup by message_id at read time (last line wins).
type MessageStateRow struct {
	MessageID string    `json:"message_id"`
	AuthorID  string    `json:"author_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	Deleted   bool      `json:"deleted"`
	Version   int       `json:"v"`
}

// ReceiptStateRow is one line in receipts/<agentID>.jsonl.
// Spec §4.4: dedup by (message_id, agent_id) at read time.
type ReceiptStateRow struct {
	MessageID string    `json:"message_id"`
	AgentID   string    `json:"agent_id"` // receipt-issuer
	ReadAt    time.Time `json:"read_at"`
	Version   int       `json:"v"`
}
