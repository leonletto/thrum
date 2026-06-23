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
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	gosync "sync"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/profile"
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
	// thrum-bpq5: per-phase instrumentation. Surfaces in daemon.log under
	// slog phase="walker.*" elapsed_ms=N. Removed once profile captured.
	walkStart := time.Now()
	w.mu.Lock()
	muAcquireMs := time.Since(walkStart).Milliseconds()
	defer w.mu.Unlock()
	defer func() {
		if !profile.Enabled() {
			return
		}
		slog.Info("profile.walker.total",
			"total_ms", time.Since(walkStart).Milliseconds(),
			"mu_acquire_ms", muAcquireMs,
			"state_files", w.lastCounts.StateFiles,
			"message_rows", w.lastCounts.MessageRows,
			"receipt_rows", w.lastCounts.ReceiptRows,
		)
	}()

	// Reset per-walk counters so LastCounts() always reflects the most
	// recent walk, not a cumulative total.
	w.lastCounts = WalkCounts{}

	// Query all events since lastWalkAt.
	// Column names from schema: event_id, sequence, type, timestamp, origin_daemon, event_json
	const q = `SELECT event_id, type, timestamp, event_json
	           FROM events
	           WHERE timestamp > ?
	           ORDER BY sequence ASC`

	selectStart := time.Now()
	rows, err := w.db.QueryContext(ctx, q, w.lastWalkAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("snapshot: query events since lastWalkAt: %w", err)
	}
	selectMs := time.Since(selectStart).Milliseconds()
	defer func() { _ = rows.Close() }()
	iterateStart := time.Now()
	var rowCount int

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
		// payload is the raw event map, used as a fallback author_id source
		// when the messages table lookup (done after rows.Close()) finds no row.
		payload map[string]any
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
		rowCount++
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
			// Defer the DB lookup until after rows is closed (see below).
			// Doing lookupMessageAuthor here while rows is open causes a
			// deadlock on MaxOpenConns=1: rows holds the single connection
			// and lookupMessageAuthor tries to acquire a second one.
			// Store (msgID, payload fallback) and resolve after rows.Close().
			msgActions = append(msgActions, msgAction{agentID: "", msgID: msgID, payload: raw})

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
	_ = rows.Close() // release DB connection before doing per-message lookups
	iterateMs := time.Since(iterateStart).Milliseconds()
	if profile.Enabled() {
		slog.Info("profile.walker.events_query",
			"select_ms", selectMs,
			"iterate_ms", iterateMs,
			"row_count", rowCount,
		)
	}
	resolveStart := time.Now()

	// Resolve message authors now that rows is closed and the DB connection
	// is free.  lookupMessageAuthor queries the messages table; doing this
	// inside the rows loop above deadlocks on MaxOpenConns=1 because rows
	// holds the single connection while the sub-query tries to acquire a
	// second one.
	for i := range msgActions {
		if msgActions[i].agentID != "" {
			continue // already resolved (shouldn't happen, but guard for safety)
		}
		msgActions[i].agentID = w.lookupMessageAuthor(ctx, msgActions[i].msgID, msgActions[i].payload)
	}
	// Filter out actions where author could not be resolved.
	filtered := msgActions[:0]
	for _, ma := range msgActions {
		if ma.agentID != "" {
			filtered = append(filtered, ma)
		}
	}
	msgActions = filtered

	resolveMs := time.Since(resolveStart).Milliseconds()
	if profile.Enabled() {
		slog.Info("profile.walker.resolve_authors",
			"resolve_ms", resolveMs,
			"agent_acts", len(agentActions),
			"bg_acts", len(bridgeGroupActs),
			"msg_acts", len(msgActions),
			"receipt_acts", len(receiptActions),
		)
	}

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
	writeStart := time.Now()

	// ---------------------------------------------------------------------------
	// Spec §8.1 ordering: agents → bridge-groups → messages → receipts
	// ---------------------------------------------------------------------------

	// 1. Agent state files.
	agentWriteStart := time.Now()
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

	agentWriteMs := time.Since(agentWriteStart).Milliseconds()

	// 2. Bridge-group state files (write + delete).
	bgWriteStart := time.Now()
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

	bgWriteMs := time.Since(bgWriteStart).Milliseconds()

	// 3. Message rows — append to messages-v2/<agentID>.jsonl.
	// De-duplicate by message ID so the most recent state per message is written
	// when multiple events touch the same message (e.g. create + edit in the same
	// window). We resolve the final projection state for each unique message ID.
	msgWriteStart := time.Now()
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

	msgWriteMs := time.Since(msgWriteStart).Milliseconds()

	// 4. Receipt rows — append to receipts/<issuerID>.jsonl.
	receiptWriteStart := time.Now()
	for _, ra := range receiptActions {
		row := w.buildReceiptRow(ctx, ra.issuerID, ra.messageID)
		if row == nil {
			continue
		}
		if appendErr := w.receiptWriter.AppendSnapshot(ctx, ra.issuerID, *row); appendErr != nil {
			return fmt.Errorf("snapshot: append receipt row (%s, %s): %w", ra.issuerID, ra.messageID, appendErr)
		}
		w.lastCounts.ReceiptRows++
	}

	receiptWriteMs := time.Since(receiptWriteStart).Milliseconds()
	if profile.Enabled() {
		slog.Info("profile.walker.writes",
			"total_write_ms", time.Since(writeStart).Milliseconds(),
			"agents_ms", agentWriteMs,
			"bridge_groups_ms", bgWriteMs,
			"messages_ms", msgWriteMs,
			"receipts_ms", receiptWriteMs,
			"agent_count", len(agentActions),
			"bridge_group_count", len(bridgeGroupActs),
			"message_count_unique", len(seenMsgIDs),
			"receipt_count", len(receiptActions),
		)
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
	// The table is optional enrichment — agents that haven't yet
	// registered a work context get an empty Worktree field (the
	// downstream branchResolver tolerates ""). The table itself is
	// also optional for callers that operate against a minimal
	// schema (unit tests with hand-rolled DDL, early-bootstrap
	// states). Tolerate both ErrNoRows AND "no such table" by
	// continuing with an empty worktree; propagate any other error
	// so the walker aborts cleanly rather than producing a
	// half-populated snapshot for a real DB problem (corrupted
	// index, ctx cancelled, lock timeout, etc.).
	var worktree string
	const wq = `SELECT worktree_path FROM agent_work_contexts WHERE agent_id = ? LIMIT 1`
	if err := w.db.QueryRowContext(ctx, wq, agentID).Scan(&worktree); err != nil {
		if err != sql.ErrNoRows && !strings.Contains(err.Error(), "no such table") {
			return nil, fmt.Errorf("query worktree for agent %s: %w", agentID, err)
		}
	}

	var lastSeen time.Time
	if lastSeenAt != "" {
		if t, parseErr := time.Parse(time.RFC3339Nano, lastSeenAt); parseErr == nil {
			lastSeen = t
		}
	}
	if lastSeen.IsZero() {
		lastSeen = time.Now().UTC()
	}

	// Name = AgentID by design. The agents projection table has no
	// separate `name` column — the wire-stream display string lives
	// in the Display field, which the inbox UI (and any other
	// consumer of AgentStateSnapshot) reads instead. Do NOT derive a
	// separate display name from this Name field; treat it as an
	// identity-only handle. If a future projection schema migration
	// adds a dedicated name column, swap the assignment here; until
	// then, agentID is the only stable identifier on hand at
	// snapshot derivation time.
	return &state.AgentStateSnapshot{
		AgentID:    agentID,
		Name:       agentID,
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
// Always returns a populated row (no error path: SQL lookup failures fall back to
// ReadAt=now). Caller must still nil-check the result for future-proofing if the
// signature ever grows an error path.
func (w *Walker) buildReceiptRow(ctx context.Context, issuerID, messageID string) *ReceiptStateRow {
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
	}
}

// lookupMessageAuthor returns the agentID of the message author by checking
// the messages table first, then falling back to agent_id in the event payload.
// Returns empty string when neither source resolves; callers filter on that.
func (w *Walker) lookupMessageAuthor(ctx context.Context, msgID string, raw map[string]any) string {
	var agentID string
	const q = `SELECT agent_id FROM messages WHERE message_id = ? LIMIT 1`
	if err := w.db.QueryRowContext(ctx, q, msgID).Scan(&agentID); err == nil {
		return agentID
	}
	// Fallback: agent_id from event payload (message.create carries it).
	if fallback, ok := raw["agent_id"].(string); ok && fallback != "" {
		return fallback
	}
	return ""
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
