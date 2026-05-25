package state

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/leonletto/thrum/internal/daemon/eventlog"
	"github.com/leonletto/thrum/internal/daemon/safedb"
	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/jsonl"
	"github.com/leonletto/thrum/internal/profile"
	"github.com/leonletto/thrum/internal/projection"
	"github.com/leonletto/thrum/internal/schema"
	_ "modernc.org/sqlite"
)

// EventWriteHook is called after a successful event write with the
// daemon ID, the assigned sequence number, and the enriched event
// payload as raw JSON. It is called synchronously but should not
// block — use goroutines for async work.
//
// The payload is the post-enrichment event (with event_id, version,
// origin_daemon, and sequence fields added) so consumers can inspect
// fields like refs[].reply_to without re-marshaling. Callers that
// only care about sequence/daemon can simply ignore the event arg.
type EventWriteHook func(daemonID string, sequence int64, event []byte)

// State manages the daemon's persistent state (JSONL log and SQLite projection).
type State struct {
	eventsWriter       *jsonl.Writer // Writer for .thrum/events.jsonl (local journal)
	db                 *safedb.DB
	projector          *projection.Projector
	repoID             string
	daemonID           string                                             // Unique identifier for this daemon instance (for sync origin tracking)
	identity           identity.Identity                                  // Full identity block when Bootstrap ran (zero-value in test paths)
	sequence           atomic.Int64                                       // Monotonically increasing event sequence counter
	repoPath           string                                             // Path to the repository root
	thrumDir           string                                             // Path to .thrum directory (runtime: var/, identities/)
	syncDir            string                                             // Path to sync worktree (JSONL data on a-sync branch)
	mu                 sync.RWMutex                                       // Protects agent/session operations
	onEventWrite       EventWriteHook                                     // Optional hook called after successful event write
	triggerSyncOnWrite func(context.Context)                              // Optional hook called after structural-event write to fire sync
	signingKey         ed25519.PrivateKey                                 // Optional Ed25519 key for signing events
	signEvent          func(event map[string]any, key ed25519.PrivateKey) // Injected signing function
	touchMu            sync.Mutex                                         // Protects touchTimes (thrum-7nuj: agent last_seen debounce)
	touchTimes         map[string]time.Time                               // Per-agent most-recent TouchAgentLastSeen timestamp
}

// NewState creates a new state manager for the given .thrum directory.
// If daemonID is non-empty, it is used verbatim (test path — no config.json
// or daemon_identity table mutation). If empty, identity.Bootstrap loads or
// creates the identity from .thrum/config.json and mirrors it into the
// daemon_identity SQLite table.
func NewState(thrumDir string, syncDir string, repoID string, daemonID string) (*State, error) {
	// Ensure var directory exists
	varDir := filepath.Join(thrumDir, "var")
	if err := os.MkdirAll(varDir, 0750); err != nil {
		return nil, fmt.Errorf("create var directory: %w", err)
	}

	// Open SQLite database with schema initialization
	dbPath := filepath.Join(thrumDir, "var", "messages.db")
	db, err := schema.OpenDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Initialize or migrate schema
	if err := schema.Migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}

	// Migrate from monolithic messages.jsonl to sharded structure (v6→v7)
	// This must run after SQL schema migration but before writers are created
	if err := schema.MigrateJSONLSharding(syncDir); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate JSONL sharding: %w", err)
	}

	// Backfill event_id for events that lack it (v6→v7 migration)
	// This must run after JSONL sharding migration
	if err := schema.BackfillEventID(syncDir); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("backfill event_id: %w", err)
	}

	// Create events writer for .thrum/events.jsonl (local journal, not synced).
	// As of v0.10.6 (thrum-s6os), all events are written locally; what peers
	// see is materialized into state/, messages-v2/, receipts/ by the snapshot
	// walker when a structural event fires triggerSyncOnWrite.
	eventsPath := filepath.Join(thrumDir, "events.jsonl")
	eventsWriter, err := jsonl.NewWriter(eventsPath)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create events writer: %w", err)
	}

	// Wrap raw DB in safedb to enforce context-aware queries at compile time
	safeDB := safedb.New(db)

	// Create projector (now uses *safedb.DB — migrated in step 8c)
	projector := projection.NewProjector(safeDB)

	// Compute repo path from thrumDir (parent of .thrum)
	repoPath := filepath.Dir(thrumDir)

	// Load the current max sequence from the events table
	var maxSeq int64
	err = db.QueryRow("SELECT COALESCE(MAX(sequence), 0) FROM events").Scan(&maxSeq)
	if err != nil {
		_ = eventsWriter.Close()
		_ = db.Close()
		return nil, fmt.Errorf("load max sequence: %w", err)
	}

	// Identity resolution:
	//   - Caller-provided daemonID (non-empty) → honor verbatim. This is the
	//     test path (sync_nudge, pid_identity, etc.). No config.json mutation.
	//   - Empty daemonID → run identity.Bootstrap against config.json. This is
	//     the production daemon path and also the common test path that
	//     passes "" as daemonID.
	var ident identity.Identity
	if daemonID == "" {
		ident, err = identity.Bootstrap(thrumDir, repoPath)
		if err != nil {
			_ = eventsWriter.Close()
			_ = db.Close()
			return nil, fmt.Errorf("bootstrap identity: %w", err)
		}
		daemonID = ident.DaemonID

		// Mirror identity into the daemon_identity table (single row).
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := safeDB.ExecContext(context.Background(), `INSERT OR REPLACE INTO daemon_identity
            (daemon_id, repo_name, hostname, repo_path, git_origin_url, init_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?)`,
			ident.DaemonID, ident.RepoName, ident.Hostname, ident.RepoPath,
			ident.GitOriginURL, ident.InitAt, now); err != nil {
			_ = eventsWriter.Close()
			_ = db.Close()
			return nil, fmt.Errorf("mirror identity to daemon_identity: %w", err)
		}
	}

	s := &State{
		eventsWriter: eventsWriter,
		db:           safeDB,
		projector:    projector,
		repoID:       repoID,
		daemonID:     daemonID,
		identity:     ident,
		repoPath:     repoPath,
		thrumDir:     thrumDir,
		syncDir:      syncDir,
	}
	s.sequence.Store(maxSeq)

	return s, nil
}

// SetOnEventWrite sets a hook that is called after each successful
// event write. The hook receives the daemon ID, the assigned
// sequence number, and the enriched event payload as raw JSON.
func (s *State) SetOnEventWrite(hook EventWriteHook) {
	s.onEventWrite = hook
}

// SetSigningKey configures Ed25519 event signing. When set, all new events are signed
// before being written to JSONL. The signFunc is called with (eventMap, privateKey)
// and should add a "signature" field to the event map.
func (s *State) SetSigningKey(key ed25519.PrivateKey, signFunc func(event map[string]any, key ed25519.PrivateKey)) {
	s.signingKey = key
	s.signEvent = signFunc
}

// SetSyncTrigger registers the hook called after a successful
// structural-event write. Per spec §3.2, sync fires only on the
// structural-event whitelist; the trigger function (provided by
// internal/sync/triggers.SyncOnWrite at daemon bootstrap) drives
// the snapshot walker and then loop.TriggerSync. The hook is
// optional; tests that don't exercise sync may leave it nil.
func (s *State) SetSyncTrigger(fn func(context.Context)) {
	s.triggerSyncOnWrite = fn
}

// isStructuralEvent reports whether an event type triggers cross-
// machine sync per spec §3.2 whitelist. Structural events drive
// state-file writes via the snapshot walker; non-structural events
// (edits, deletes, receipts, session boundaries, intents) remain
// local-only and are folded into the wire stream at the NEXT
// structural-driven walk. This deferred-folding semantic is what
// makes the T2 invariant (100 receipts → 0 commits) work.
//
// One whitelist, one helper, one source of truth. If a new
// structural event type is introduced, add it here; do NOT inline
// the check at call sites.
func isStructuralEvent(eventType string) bool {
	switch eventType {
	case "agent.register",
		"group.create", "group.delete",
		"group.member.add", "group.member.remove",
		"message.create":
		return true
	}
	return false
}

// Close closes the state manager and its resources.
func (s *State) Close() error {
	var errs []error

	if err := s.eventsWriter.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close events writer: %w", err))
	}

	if err := s.db.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close database: %w", err))
	}

	return errors.Join(errs...)
}

// WriteEvent writes an event to both JSONL and SQLite.
// Automatically generates and adds event_id (ULID) and version fields.
// The context is used for SQLite operations, ensuring the server's per-request
// timeout propagates to database queries.
//
// Returns a postCommit closure (nil if non-structural or no sync trigger
// is wired) that the caller MUST invoke to fire the snapshot walker +
// compactor + sync. Callers that hold an external lock (e.g. state.Lock())
// during WriteEvent MUST release that lock BEFORE invoking postCommit —
// the walker+compactor can run for tens of seconds and would otherwise
// starve every other goroutine waiting on the lock (thrum-bsn7).
//
// Callers that hold no external lock can invoke postCommit() inline
// immediately after the error check — semantically identical to the
// pre-bsn7 inline-trigger behavior.
func (s *State) WriteEvent(ctx context.Context, event any) (postCommit func(), err error) {
	// thrum-bpq5 substrate: per-phase WriteEvent timing. Gated by
	// THRUM_PROFILE; zero cost when off.
	weStart := time.Now()
	var marshalMs, jsonlMs, eventsInsertMs, projectorMs int64
	defer func() {
		if !profile.Enabled() {
			return
		}
		evtTypeRaw := ""
		if event != nil {
			// best-effort type extraction; cheap
			if m, ok := event.(map[string]any); ok {
				evtTypeRaw, _ = m["type"].(string)
			}
		}
		slog.Info("profile.write_event.total",
			"total_ms", time.Since(weStart).Milliseconds(),
			"marshal_ms", marshalMs,
			"jsonl_ms", jsonlMs,
			"events_insert_ms", eventsInsertMs,
			"projector_ms", projectorMs,
			"event_type", evtTypeRaw,
		)
	}()
	// Marshal event to map so we can add fields
	marshalStart := time.Now()
	eventBytes, mErr := json.Marshal(event)
	if mErr != nil {
		return nil, fmt.Errorf("marshal event: %w", mErr)
	}

	var eventMap map[string]any
	if uErr := json.Unmarshal(eventBytes, &eventMap); uErr != nil {
		return nil, fmt.Errorf("unmarshal to map: %w", uErr)
	}
	marshalMs = time.Since(marshalStart).Milliseconds()

	// Generate and add event_id if not present or empty
	eventID, _ := eventMap["event_id"].(string)
	if eventID == "" {
		eventMap["event_id"] = identity.GenerateEventID()
	}

	// Add version field if not present or zero
	version, vExists := eventMap["v"].(float64)
	if !vExists || version == 0 {
		eventMap["v"] = 1
	}

	// Add origin_daemon if not present or empty
	originDaemon, _ := eventMap["origin_daemon"].(string)
	if originDaemon == "" {
		eventMap["origin_daemon"] = s.daemonID
	}

	// Sign event if signing key is configured
	if s.signingKey != nil && s.signEvent != nil {
		s.signEvent(eventMap, s.signingKey)
	}

	// All events go to the local journal as of v0.10.6 (thrum-s6os).
	// The synced events.jsonl is gone from the write path; what peers
	// see is materialized into state/, messages-v2/, receipts/ by the
	// snapshot walker (internal/sync/snapshot.Walker) when a
	// structural event fires the returned postCommit closure below.
	writer := s.eventsWriter

	// Assign next sequence number
	seq := s.sequence.Add(1)
	eventMap["sequence"] = seq

	// Append enriched event to JSONL (source of truth)
	jsonlStart := time.Now()
	if aErr := writer.Append(eventMap); aErr != nil {
		return nil, fmt.Errorf("append to JSONL: %w", aErr)
	}
	jsonlMs = time.Since(jsonlStart).Milliseconds()

	// Marshal enriched event for projector
	eventJSON, jErr := json.Marshal(eventMap)
	if jErr != nil {
		return nil, fmt.Errorf("marshal enriched event: %w", jErr)
	}

	// Insert into events table for sequence-based queries
	evtID, _ := eventMap["event_id"].(string)
	evtType, _ := eventMap["type"].(string)
	evtTimestamp, _ := eventMap["timestamp"].(string)
	evtOrigin, _ := eventMap["origin_daemon"].(string)
	eventsInsertStart := time.Now()
	if _, iErr := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, seq, evtType, evtTimestamp, evtOrigin, string(eventJSON),
	); iErr != nil {
		return nil, fmt.Errorf("insert into events table: %w", iErr)
	}
	eventsInsertMs = time.Since(eventsInsertStart).Milliseconds()

	// Apply to projector (update SQLite)
	projectorStart := time.Now()
	if pErr := s.projector.Apply(ctx, eventJSON); pErr != nil {
		return nil, fmt.Errorf("apply to projector: %w", pErr)
	}
	projectorMs = time.Since(projectorStart).Milliseconds()

	// Notify sync hook (e.g., to broadcast sync.notify to peers).
	// Passes the enriched event JSON so downstream consumers (e.g.
	// the permission reply interceptor) can inspect refs/reply_to
	// without re-marshaling. Fires inline because it does not block
	// on walker/compactor — it just notifies peer connections.
	if s.onEventWrite != nil {
		s.onEventWrite(s.daemonID, seq, eventJSON)
	}

	// Build the deferred sync trigger for structural events. The caller
	// is responsible for invoking this AFTER releasing any external
	// lock (state.Lock()). thrum-bsn7 broke the synchronous-under-lock
	// pattern so walker(30s) + compactor(60s) ceilings no longer starve
	// concurrent goroutines waiting on the same lock. Non-structural
	// events return nil — they're folded into the wire stream at the
	// next structural-driven walk.
	if s.triggerSyncOnWrite != nil && isStructuralEvent(evtType) {
		trigger := s.triggerSyncOnWrite
		return func() { trigger(ctx) }, nil
	}
	return nil, nil
}

// DB returns the safedb wrapper that enforces context-aware queries at compile time.
func (s *State) DB() *safedb.DB {
	return s.db
}

// RawDB returns the underlying *sql.DB for schema setup and migrations ONLY.
func (s *State) RawDB() *sql.DB {
	return s.db.Raw()
}

// RepoID returns the repository ID.
func (s *State) RepoID() string {
	return s.repoID
}

// DaemonID returns the daemon's unique identifier for sync origin tracking.
func (s *State) DaemonID() string {
	return s.daemonID
}

// Identity returns the full identity block for this state.
// Zero-valued when NewState was called with a non-empty daemonID (test path).
func (s *State) Identity() identity.Identity {
	return s.identity
}

// GetEventsSince returns events with sequence > afterSeq, up to limit.
// Delegates to the eventlog package.
func (s *State) GetEventsSince(ctx context.Context, afterSeq int64, limit int) ([]eventlog.Event, int64, bool, error) {
	return eventlog.GetEventsSince(ctx, s.db, afterSeq, limit)
}

// IngestSyncedEvent applies an event that arrived via sync (already
// in the peer's JSONL, already merged into our local JSONL) to the
// SQLite projection AND fires the event-write hook. It does NOT
// write to JSONL again (avoids double-writes) and does NOT increment
// the local sequence counter — the event arrives pre-sequenced from
// the peer.
//
// This is the cross-repo correctness bridge. Internal/sync/loop.go's
// updateProjection step previously called projector.Apply directly,
// bypassing the event-write hook entirely. That meant synced
// message.create events (including replies to cross-repo nudges)
// never reached the permission package's reply interceptor, silently
// breaking cross-repo approve/deny delivery. Routing sync ingest
// through this method fixes that: the projector still runs AND the
// permission intercept fires.
//
// The hook sees sequence == 0 as a sentinel for "synced from peer,
// not locally authored". The daemon_id argument is still our own (the
// handling daemon's ID). Consumers that need to know where the event
// ORIGINATED should read event.origin_daemon from the JSON payload, not
// the hook's daemonID argument — the latter is always "this process"
// regardless of origin. thrum-xfsb uses the payload field to suppress
// peer-replicated broadcasts that would otherwise fan out to this
// daemon's local Telegram bridge.
func (s *State) IngestSyncedEvent(ctx context.Context, event []byte) error {
	// Apply to projector — same work the previous direct call did.
	if err := s.projector.Apply(ctx, event); err != nil {
		return fmt.Errorf("apply synced event: %w", err)
	}

	// Fire the hook so downstream consumers (the permission reply
	// interceptor in particular) see the event even though it didn't
	// originate here.
	if s.onEventWrite != nil {
		s.onEventWrite(s.daemonID, 0, event)
	}
	return nil
}

// RepoPath returns the path to the repository root.
func (s *State) RepoPath() string {
	return s.repoPath
}

// SyncDir returns the path to the sync worktree directory (.git/thrum-sync/a-sync).
func (s *State) SyncDir() string {
	return s.syncDir
}

// Projector returns the projector for applying events to SQLite.
func (s *State) Projector() *projection.Projector {
	return s.projector
}

// Lock acquires a write lock for agent/session operations.
func (s *State) Lock() {
	s.mu.Lock()
}

// Unlock releases the write lock.
func (s *State) Unlock() {
	s.mu.Unlock()
}

// RLock acquires a read lock for queries.
func (s *State) RLock() {
	s.mu.RLock()
}

// RUnlock releases the read lock.
func (s *State) RUnlock() {
	s.mu.RUnlock()
}
