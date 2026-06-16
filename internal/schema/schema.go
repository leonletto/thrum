package schema

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// CurrentVersion is the current schema version.
// CurrentVersion is 38 on release/v0.10.6, reached via the following ladder:
//
//   - v36: mirrors the thrum-agents + feature/b-b1-impl (v0.11 substrate) DDL
//     surface as dead-end DDL — none of the v33–v36 tables/columns
//     (scheduler_*, reminders, agent_lifecycle_events, email_*, memories,
//     agent_api_error_remediation, messages.pending_route_resolution) have
//     consumer code on v0.10.6. Goal: binary-supports-v36-schema-on-disk so a
//     v0.10.6 install can co-reside on a DB touched by a v0.11 substrate
//     binary. createTables/createIndexes carry full v36 parity so a fresh DB
//     created by a v0.10.6 binary stamps every v36 table.
//   - v37: dummy-tables back-port from thrum-agents j7n5 Epic 0
//     (memory_record / memory_tag / memory_edge / memory_fts /
//     memory_embeddings / memory_embed_queue + 10 indexes). DDL-only — no
//     release-line code uses these tables; ensures binary co-residence with
//     thrum-agents v37+ binaries via .thrum/redirect (without this back-port,
//     a fresh rc.3 install would create a v38 DB with NO memory tables, and
//     a later thrum-agents binary on the same DB would crash on every
//     memory.* operation). Matches the canonical CLAUDE.md dead-end-DDL
//     back-port pattern (same as v25–v36's substrate-table forward-ports).
//     See thrum-7ojv / thrum-roz1 for the rationale trail.
//   - v38: CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp)
//     (thrum-7ojv). Without this index the compactor's
//     `DELETE FROM events WHERE timestamp < ?` runs O(N) full table scan —
//     contributing to the kdyf/roz1 lock-hold ceiling. With the index the
//     DELETE is O(log N) seek + sequential range. The post-roz1 60s compactor
//     ceiling (sync/triggers.go:syncCompactorTimeout) can drop to 30s
//     symmetric with the walker (s7is.7) once this index is in place across
//     both branches.
//   - v39: ALTER TABLE monitors ADD COLUMN schedule TEXT (thrum-puhr.9).
//     Empty string / NULL means continuous mode; a non-empty 5-field cron
//     expression switches the runner to scheduled mode (one-shot per fire,
//     no auto-restart of the child between scheduled ticks). Idempotent —
//     re-running the migration on a DB that already has the column is a
//     no-op via columnSet check.
//   - v40: read-state unification marker (thrum-b6qw, backport of thrum-tcqw).
//     NO DDL — runMigrations has no v40 block; the version stamp alone marks
//     the crossing. state.NewState detects the v39→v40 crossing (pre-Migrate
//     version < SchemaVersionReadState) and runs the one-time, data-only
//     BackfillReadState: Pass 1 stamps existing local unread delivery rows
//     read; Pass 2 creates read-stamped rows for the inbox-visible
//     no-delivery-row class (legacy broadcasts). Local-only + leak-guarded
//     (hostname-anchored LocalDaemonIDs — thrum-edhn); peer-agent rows are
//     never touched. Collapses thrum-agents' v42(buggy)+v43(corrective) pair
//     into ONE clean marker running the already-fixed hostname-anchored
//     backfill.
//
// v29 is a deliberate gap (reserved for MB-1.S6 on the substrate plan);
// runMigrations handles all skipped/no-op versions cleanly.
//
//   - v41–v51: dead-end DDL forward-port from thrum-agents (thrum-399av),
//     same pattern as the v25–v36 (37e1c8682) and v37 (10bd90bf8) forward-ports.
//     Goal: a v0.10.6 binary OPENS + does basic ops on a v51 (0.11-schema) DB
//     without the one-way-migration brick — schema-on-disk parity, NOT feature
//     availability. None of the new tables/columns have consumer code on the
//     release line. The 8 real migrations are v41 (agents.agent_pid_start_time),
//     v44 (permission_nudges.prompt_fingerprint), v45 (alert_deliveries),
//     v47 (messages.visibility_class/retarget_fill_order + index swap;
//     backfill STUBBED — internal/visibility absent here, column default
//     'targeted' carries it), v48 (agents.phase / messages.priority /
//     message_deliveries.addressed_via; backfill STUBBED, safe defaults),
//     v49 (telegram_outbound_queue), v50 (graph substrate tables),
//     v51 (memory_satellite). v42/v43/v46 are no-op version markers: v42/v43
//     were the read-state backfill pair already collapsed into v40 here, and
//     v46 was the post-rebuild read-state corrective — none have a runMigrations
//     block, and the release line never references SchemaVersionReadStatePost-
//     Rebuild, so no state.NewState change is needed.
const CurrentVersion = 51

// SchemaVersionReadState is the read-state unification crossing (thrum-b6qw,
// backport of thrum-tcqw): at the first boot where the pre-migration version is
// below this and the post-migration version is at/above it, state.NewState runs
// the one-time BackfillReadState. Data-only; no DDL is attached to this version.
const SchemaVersionReadState = 40

// InitDB initializes a new database with the current schema.
func InitDB(db *sql.DB) error {
	// Begin transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Create schema_version table
	if err := createVersionTable(tx); err != nil {
		return fmt.Errorf("create version table: %w", err)
	}

	// Create all tables
	if err := createTables(tx); err != nil {
		return fmt.Errorf("create tables: %w", err)
	}

	// Create indexes
	if err := createIndexes(tx); err != nil {
		return fmt.Errorf("create indexes: %w", err)
	}

	// Set schema version
	if err := setSchemaVersion(tx, CurrentVersion); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// GetSchemaVersion returns the current schema version from the database.
func GetSchemaVersion(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("query schema version: %w", err)
	}
	return version, nil
}

// createVersionTable creates the schema_version table.
func createVersionTable(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

// setSchemaVersion sets the schema version in the database.
func setSchemaVersion(tx *sql.Tx, version int) error {
	_, err := tx.Exec("INSERT INTO schema_version (version) VALUES (?)", version)
	return err
}

// createAgentAPIErrorRemediationTable is the DDL for the per-agent
// remediation-state table (thrum-sdzk, schema v36). Defined once and used in
// BOTH createTables (fresh install) and the v36 migration block (upgrade) so
// the two can never drift — the canonical-ref §3.11 Guard-1 parity rule.
// Keyed-mutable (one upserted row per agent), modeled on scheduler_job_state;
// no FK/sync (operational local-only state). Dead-end on v0.10.6: the table is
// inert (no remediation handler ported).
const createAgentAPIErrorRemediationTable = `CREATE TABLE IF NOT EXISTS agent_api_error_remediation (
	agent_name              TEXT PRIMARY KEY,
	last_nudge_at           INTEGER,
	consecutive_nudge_count INTEGER NOT NULL DEFAULT 0,
	last_error              TEXT,
	escalation_sent         INTEGER NOT NULL DEFAULT 0,
	updated_at              INTEGER NOT NULL
)`

// agentLifecycleEventsColumns is the shared column body of
// agent_lifecycle_events, referenced by BOTH createTables (fresh install) and
// the v35 rebuild migration (thrum-6qmf.17). v35 adds the event_kind CHECK
// via a full table rebuild because SQLite cannot ALTER ... ADD CHECK; sharing
// the body makes fresh-vs-upgrade DDL parity (canonical-ref §3.11 Guard-1)
// structurally impossible to break. The 7 event_kind values are verbatim from
// state.AgentLifecycleEventKind; the detection_method CHECK is unchanged from
// the original v27 table.
const agentLifecycleEventsColumns = `
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	agent_name        TEXT    NOT NULL,
	event_kind        TEXT    NOT NULL CHECK (event_kind IN (
		'respawn_fired','respawn_skipped_loopguard','crash_detected',
		'state_md_parse_failed','state_md_ack_cleared','respawn_ack_cleared',
		'reconcile_worktree_discrepancy'
	)),
	event_time        INTEGER NOT NULL,
	detection_method  TEXT CHECK (
		detection_method IS NULL OR detection_method IN
			('health_check_tick', 'restart_reconciliation', 'rpc_observation')
	),
	reason            TEXT,
	details           TEXT
`

// createTables creates all database tables.
func createTables(tx *sql.Tx) error {
	tables := []string{
		// Messages table
		`CREATE TABLE IF NOT EXISTS messages (
			message_id   TEXT PRIMARY KEY,
			thread_id    TEXT,
			agent_id     TEXT NOT NULL,
			session_id   TEXT NOT NULL,
			created_at   TEXT NOT NULL,
			updated_at   TEXT,
			body_format  TEXT NOT NULL,
			body_content TEXT NOT NULL,
			body_structured TEXT,
			deleted      INTEGER DEFAULT 0,
			deleted_at   TEXT,
			delete_reason TEXT,
			authored_by  TEXT,
			disclosed    INTEGER DEFAULT 0,
			pending_route_resolution INTEGER NOT NULL DEFAULT 0,
			-- v47/v48 forward-port (thrum-399av): dead-end columns, no release-line reader.
			visibility_class TEXT NOT NULL DEFAULT 'targeted',
			retarget_fill_order TEXT,
			priority TEXT NOT NULL DEFAULT ''
		)`,

		// Message scopes table
		`CREATE TABLE IF NOT EXISTS message_scopes (
			message_id  TEXT NOT NULL,
			scope_type  TEXT NOT NULL,
			scope_value TEXT NOT NULL,
			PRIMARY KEY (message_id, scope_type, scope_value)
		)`,

		// Message refs table
		`CREATE TABLE IF NOT EXISTS message_refs (
			message_id TEXT NOT NULL,
			ref_type   TEXT NOT NULL,
			ref_value  TEXT NOT NULL,
			PRIMARY KEY (message_id, ref_type, ref_value)
		)`,

		// Threads table
		`CREATE TABLE IF NOT EXISTS threads (
			thread_id  TEXT PRIMARY KEY,
			title      TEXT,
			created_at TEXT NOT NULL,
			created_by TEXT NOT NULL
		)`,

		// Agents table.
		//
		// origin_daemon tracks which daemon emitted the agent.register event
		// for this row — local registrations get the local daemon_id, synced
		// registrations carry the remote origin's daemon_id. HandleRegister's
		// role+module conflict check filters by origin_daemon so cross-daemon
		// agents with overlapping (role, module) aren't treated as local
		// conflicts (thrum-mm3l). See migration 21→22 for the backfill path
		// on pre-existing databases.
		`CREATE TABLE IF NOT EXISTS agents (
			agent_id                 TEXT PRIMARY KEY,
			kind                     TEXT NOT NULL,
			role                     TEXT NOT NULL,
			module                   TEXT NOT NULL,
			display                  TEXT NOT NULL DEFAULT '',
			hostname                 TEXT NOT NULL DEFAULT '',
			agent_pid                INTEGER NOT NULL DEFAULT 0,
			registered_at            TEXT NOT NULL,
			last_seen_at             TEXT NOT NULL DEFAULT '',
			origin_daemon            TEXT NOT NULL DEFAULT '',
			mode                     TEXT NOT NULL DEFAULT 'persistent',
			identity                 TEXT NOT NULL DEFAULT 'long_lived',
			auto_respawn_enabled     INTEGER NOT NULL DEFAULT 0,
			auto_respawn_disabled_at INTEGER,
			state_md_parse_failed_at INTEGER,
			last_pane_alive_at       INTEGER,
			-- v41/v48 forward-port (thrum-399av): dead-end columns, no release-line reader.
			agent_pid_start_time     TEXT NOT NULL DEFAULT '',
			phase                    TEXT NOT NULL DEFAULT 'active'
		)`,

		// Sessions table
		`CREATE TABLE IF NOT EXISTS sessions (
			session_id   TEXT PRIMARY KEY,
			agent_id     TEXT NOT NULL,
			started_at   TEXT NOT NULL,
			ended_at     TEXT,
			end_reason   TEXT,
			last_seen_at TEXT NOT NULL
		)`,

		// Subscriptions table
		`CREATE TABLE IF NOT EXISTS subscriptions (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id   TEXT NOT NULL,
			scope_type   TEXT,
			scope_value  TEXT,
			mention_role TEXT,
			created_at   TEXT NOT NULL,
			UNIQUE(session_id, scope_type, scope_value, mention_role)
		)`,

		// Message reads table (per-session read tracking, local-only, no git sync).
		// DEPRECATED (thrum-tcqw/b6qw): read-truth unified on
		// message_deliveries.read_at; table retained for back-compat, no live
		// readers/writers as of v40 (cascade-deletes only).
		`CREATE TABLE IF NOT EXISTS message_reads (
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			agent_id   TEXT NOT NULL,
			read_at    TEXT NOT NULL,
			PRIMARY KEY (message_id, session_id),
			FOREIGN KEY (message_id) REFERENCES messages(message_id) ON DELETE CASCADE
		)`,

		// Durable message delivery/receipt state (synced via events)
		`CREATE TABLE IF NOT EXISTS message_deliveries (
			message_id          TEXT NOT NULL,
			recipient_agent_id  TEXT NOT NULL,
			delivered_at        TEXT NOT NULL,
			seen_at             TEXT,
			read_at             TEXT,
			-- v48 forward-port (thrum-399av): dead-end column, no release-line reader.
			addressed_via       TEXT NOT NULL DEFAULT 'unattributed',
			PRIMARY KEY (message_id, recipient_agent_id),
			FOREIGN KEY (message_id) REFERENCES messages(message_id) ON DELETE CASCADE
		)`,

		// Message edits table (for edit history tracking)
		`CREATE TABLE IF NOT EXISTS message_edits (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id     TEXT NOT NULL,
			edited_at      TEXT NOT NULL,
			edited_by      TEXT NOT NULL,
			old_content    TEXT,
			new_content    TEXT,
			old_structured TEXT,
			new_structured TEXT
		)`,

		// Session scopes table (for session context tracking)
		`CREATE TABLE IF NOT EXISTS session_scopes (
			session_id  TEXT NOT NULL,
			scope_type  TEXT NOT NULL,
			scope_value TEXT NOT NULL,
			added_at    TEXT NOT NULL,
			PRIMARY KEY (session_id, scope_type, scope_value)
		)`,

		// Session refs table (for session context tracking)
		`CREATE TABLE IF NOT EXISTS session_refs (
			session_id TEXT NOT NULL,
			ref_type   TEXT NOT NULL,
			ref_value  TEXT NOT NULL,
			added_at   TEXT NOT NULL,
			PRIMARY KEY (session_id, ref_type, ref_value)
		)`,

		// Groups table
		`CREATE TABLE IF NOT EXISTS groups (
			group_id    TEXT PRIMARY KEY,
			name        TEXT UNIQUE NOT NULL,
			description TEXT,
			created_at  TEXT NOT NULL,
			created_by  TEXT NOT NULL,
			updated_at  TEXT,
			metadata    TEXT
		)`,

		// Group members table
		`CREATE TABLE IF NOT EXISTS group_members (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id     TEXT NOT NULL,
			member_type  TEXT NOT NULL,
			member_value TEXT NOT NULL,
			added_at     TEXT NOT NULL,
			added_by     TEXT,
			UNIQUE(group_id, member_type, member_value),
			FOREIGN KEY (group_id) REFERENCES groups(group_id) ON DELETE CASCADE
		)`,

		// Events table (for sync: sequence-ordered, deduplicated event log)
		`CREATE TABLE IF NOT EXISTS events (
			event_id TEXT PRIMARY KEY,
			sequence INTEGER UNIQUE NOT NULL,
			type TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			origin_daemon TEXT NOT NULL,
			event_json TEXT NOT NULL
		)`,

		// Sync checkpoints table (for tracking sync progress per peer)
		`CREATE TABLE IF NOT EXISTS sync_checkpoints (
			peer_daemon_id TEXT PRIMARY KEY,
			last_synced_sequence INTEGER NOT NULL DEFAULT 0,
			last_sync_timestamp INTEGER NOT NULL,
			sync_status TEXT NOT NULL DEFAULT 'idle'
		)`,

		// Purge metadata table (for sync-aware purge coordination)
		`CREATE TABLE IF NOT EXISTS purge_metadata (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,

		// Agent work contexts table (for live git state tracking)
		`CREATE TABLE IF NOT EXISTS agent_work_contexts (
			session_id        TEXT PRIMARY KEY,
			agent_id          TEXT NOT NULL,
			branch            TEXT,
			worktree_path     TEXT,
			unmerged_commits  TEXT,
			uncommitted_files TEXT,
			changed_files     TEXT,
			file_changes      TEXT DEFAULT '[]',
			git_updated_at    TEXT,
			current_task      TEXT,
			task_updated_at   TEXT,
			intent            TEXT,
			intent_updated_at TEXT,
			FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
		)`,

		// Command queue table (for tmux session command dispatching)
		`CREATE TABLE IF NOT EXISTS command_queue (
			command_id         TEXT PRIMARY KEY,
			session_name       TEXT NOT NULL,
			requester_agent    TEXT NOT NULL,
			command_text       TEXT NOT NULL,
			state              TEXT NOT NULL DEFAULT 'queued',
			timeout_ms         INTEGER NOT NULL DEFAULT 120000,
			silence_ms         INTEGER NOT NULL DEFAULT 5000,
			notify_on_complete INTEGER NOT NULL DEFAULT 1,
			submitted_at       TEXT NOT NULL,
			sent_at            TEXT,
			completed_at       TEXT,
			captured_output    TEXT,
			position           INTEGER NOT NULL DEFAULT 0
		)`,

		// Monitors table (for thrum monitor feature — v20)
		`CREATE TABLE IF NOT EXISTS monitors (
			id                TEXT PRIMARY KEY,
			name              TEXT NOT NULL UNIQUE,
			argv              TEXT NOT NULL,       -- JSON array
			match_pattern     TEXT NOT NULL,
			target            TEXT NOT NULL,
			cwd               TEXT NOT NULL,
			env               TEXT NOT NULL,       -- JSON object
			debounce_seconds  INTEGER NOT NULL,
			created_at        TEXT NOT NULL,
			updated_at        TEXT NOT NULL,
			status            TEXT NOT NULL,       -- "running" | "dead" | "stopped"
			last_exit_code    INTEGER,
			last_exit_at      TEXT,
			pid               INTEGER,
			schedule          TEXT NOT NULL DEFAULT ''  -- 5-field cron expression; empty means continuous mode (v39)
		)`,

		// Permission nudges table (for permission-prompt detection — v21).
		// Daemon-local only: each row describes a pending nudge for a tmux
		// session on THIS host. Not synced across repos; see
		// dev-docs/specs/2026-04-14-permission-prompt-detection-design.md §3.
		`CREATE TABLE IF NOT EXISTS permission_nudges (
			message_id       TEXT PRIMARY KEY,
			session          TEXT NOT NULL,
			tmux_target      TEXT NOT NULL,
			agent_name       TEXT NOT NULL,
			pattern_key      TEXT NOT NULL,
			approve_key      TEXT NOT NULL,
			deny_key         TEXT,
			first_detected   TIMESTAMP NOT NULL,
			last_nudge_at    TIMESTAMP NOT NULL,
			nudge_count      INTEGER NOT NULL,
			last_pane_hash   BLOB NOT NULL,
			expires_at       TIMESTAMP NOT NULL,
			-- v44 forward-port (thrum-399av): dead-end column, no release-line reader.
			prompt_fingerprint TEXT NOT NULL DEFAULT ''
		)`,

		// Daemon identity table (v23). Single-row mirror of the identity block
		// in .thrum/config.json. Populated at daemon startup; not synced.
		`CREATE TABLE IF NOT EXISTS daemon_identity (
			daemon_id      TEXT PRIMARY KEY,
			repo_name      TEXT NOT NULL,
			hostname       TEXT NOT NULL,
			repo_path      TEXT NOT NULL,
			git_origin_url TEXT,
			init_at        TEXT NOT NULL,
			updated_at     TEXT NOT NULL
		)`,

		// Telegram↔Thrum message ID map (v24, thrum-48kt.2). Durable
		// backing for the in-memory LRU in internal/bridge/telegram/msgmap.go
		// so supervisor replies that arrive after a daemon restart still
		// resolve to the originating nudge. Keys are the telegram
		// "chatID:msgID" format produced by teleKey().
		`CREATE TABLE IF NOT EXISTS telegram_msg_map (
			external_key TEXT PRIMARY KEY,
			thrum_msg_id TEXT NOT NULL,
			created_at   INTEGER NOT NULL
		)`,

		// Scheduler substrate (v25, forward-ported from thrum-agents A-B1).
		// Dead-end on v0.10.5: no consumer code reads from these tables. Present
		// only so the binary can open DBs that touched v0.11 substrate work.
		`CREATE TABLE IF NOT EXISTS scheduler_job_state (
			job_id                TEXT PRIMARY KEY,
			job_generation        INTEGER NOT NULL DEFAULT 1,
			current_state         TEXT    NOT NULL,
			current_stage         TEXT,
			stage_entered_at      INTEGER,
			last_run_id           TEXT,
			last_fired_at         INTEGER,
			last_completed_at     INTEGER,
			last_completion_state TEXT,
			last_error            TEXT,
			next_scheduled_at     INTEGER,
			consecutive_failures  INTEGER NOT NULL DEFAULT 0,
			escalation_sent       INTEGER NOT NULL DEFAULT 0,
			total_runs            INTEGER NOT NULL DEFAULT 0,
			created_at            INTEGER NOT NULL,
			updated_at            INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS scheduler_job_events (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id      TEXT    NOT NULL,
			run_id      TEXT    NOT NULL,
			event_time  INTEGER NOT NULL,
			from_state  TEXT,
			to_state    TEXT    NOT NULL,
			reason      TEXT,
			details     TEXT
		)`,

		// Agent lifecycle journal (v27, forward-ported from thrum-agents B-B1;
		// event_kind CHECK added at v35 via the shared agentLifecycleEventsColumns
		// const so fresh-install and the v35 rebuild migration cannot drift).
		// Dead-end on v0.10.6.
		"CREATE TABLE IF NOT EXISTS agent_lifecycle_events (" + agentLifecycleEventsColumns + ")",

		// Unified reminder substrate (v28, forward-ported from thrum-agents A-B4).
		// Dead-end on v0.10.5.
		`CREATE TABLE IF NOT EXISTS reminders (
			id                  TEXT    PRIMARY KEY,
			source              TEXT    NOT NULL,
			source_agent        TEXT,
			trigger_kind        TEXT    NOT NULL,
			trigger_at          INTEGER,
			trigger_meta        TEXT,
			target_agent        TEXT,
			target_chain        TEXT,
			body                TEXT,
			raised_at           INTEGER NOT NULL,
			next_reminder_at    INTEGER,
			last_fired_at       INTEGER,
			state               TEXT    NOT NULL,
			pane_snapshot       TEXT,
			defer_history       TEXT    NOT NULL DEFAULT '[]',
			cleared_at          INTEGER,
			cancelled_at        INTEGER,
			created_at          INTEGER NOT NULL,
			updated_at          INTEGER NOT NULL
		)`,

		// Email substrate (v30/31/32, forward-ported from thrum-agents D-B1).
		// Dead-end on v0.10.5.
		`CREATE TABLE IF NOT EXISTS email_msg_seen (
			message_id      TEXT    PRIMARY KEY,
			from_daemon_id  TEXT,
			nonce           TEXT,
			processed_at    INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS email_outbound_queue (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			from_agent      TEXT    NOT NULL,
			to_address      TEXT    NOT NULL,
			subject         TEXT,
			body            TEXT    NOT NULL,
			headers_json    TEXT    NOT NULL DEFAULT '{}',
			attempt_count   INTEGER NOT NULL DEFAULT 0,
			next_retry_at   INTEGER NOT NULL,
			last_error      TEXT,
			status          TEXT    NOT NULL,
			enqueued_at     INTEGER NOT NULL,
			updated_at      INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS email_peer_rate_state (
			peer_key            TEXT    PRIMARY KEY,
			window_start_at     INTEGER NOT NULL,
			inbound_count       INTEGER NOT NULL DEFAULT 0,
			outbound_count      INTEGER NOT NULL DEFAULT 0,
			paused_at           INTEGER
		)`,

		// Memories table (v34, E16, forward-ported from thrum-agents).
		// Dead-end on v0.10.6 (no memory handlers ported).
		`CREATE TABLE IF NOT EXISTS memories (
			record_id   TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			description TEXT NOT NULL,
			body        TEXT NOT NULL,
			type        TEXT NOT NULL,
			author      TEXT NOT NULL,
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL,
			deleted     INTEGER NOT NULL DEFAULT 0
		)`,

		// Memory scopes join table (v34, E16). FK→memories ON DELETE CASCADE.
		// Dead-end on v0.10.6.
		`CREATE TABLE IF NOT EXISTS memory_scopes (
			record_id   TEXT NOT NULL,
			scope_type  TEXT NOT NULL,
			scope_value TEXT NOT NULL,
			PRIMARY KEY (record_id, scope_type, scope_value),
			FOREIGN KEY (record_id) REFERENCES memories(record_id) ON DELETE CASCADE
		)`,

		// API-error auto-remediation per-agent state (v36, thrum-sdzk).
		// Identical DDL to the v36 migration block via the shared const.
		// Dead-end on v0.10.6 (no remediation handler ported).
		createAgentAPIErrorRemediationTable,

		// memory_record / memory_tag / memory_edge (v37, thrum-j7n5 Epic 0).
		// Back-ported from thrum-agents per CLAUDE.md "schema migrations
		// must be back-ported to active release lines BEFORE dev branch
		// ships — DDL-only, dead-end tables on release line" (thrum-7ojv).
		// Verbatim DDL copy from thrum-agents internal/schema/schema.go
		// so cross-binary co-residence is identical: a thrum-agents
		// binary expecting v37 = these tables and a release-line binary
		// stamping v37 with the same tables agree byte-for-byte.
		// Dead-end on v0.10.6 — no release-line Go code touches
		// memory_record / memory_tag / memory_edge / memory_fts /
		// memory_embeddings / memory_embed_queue. Mirrored in the
		// v36→v37 migration block below for fresh-install parity.
		`CREATE TABLE IF NOT EXISTS memory_record (
			id                TEXT PRIMARY KEY,
			kind              TEXT NOT NULL,
			subkind           TEXT,
			title             TEXT NOT NULL,
			body_oneline      TEXT NOT NULL,
			body_short        TEXT,
			body_full         TEXT,
			agent_id          TEXT NOT NULL,
			created_at        TIMESTAMP NOT NULL,
			updated_at        TIMESTAMP NOT NULL,
			status            TEXT NOT NULL DEFAULT 'active',
			scope             TEXT NOT NULL DEFAULT 'project',
			parent_id         TEXT,
			source_session_id TEXT,
			metadata          TEXT,
			created_by        TEXT NOT NULL,
			last_edited_by    TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS memory_tag (
			memory_id  TEXT NOT NULL,
			tag        TEXT NOT NULL,
			PRIMARY KEY (memory_id, tag),
			FOREIGN KEY (memory_id) REFERENCES memory_record(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS memory_edge (
			from_id    TEXT NOT NULL,
			edge_kind  TEXT NOT NULL,
			to_id      TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			PRIMARY KEY (from_id, edge_kind, to_id),
			FOREIGN KEY (from_id) REFERENCES memory_record(id) ON DELETE CASCADE,
			FOREIGN KEY (to_id) REFERENCES memory_record(id) ON DELETE CASCADE
		)`,
		// memory_fts (v37, j7n5 Task 0.2) — FTS5 SHADOW table; no
		// `content=` clause. Projection handlers (NOT present on release
		// line) would maintain it via application-level DML. Storage
		// only; nothing reads from it on v0.10.6.
		`CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
			memory_id UNINDEXED,
			title, body_oneline, body_short, body_full
		)`,
		// memory_embeddings (v37, j7n5 Task 0.3) — LOCAL-ONLY; never
		// sync'd. Compound PK (memory_id, zoom_level, model). Storage
		// only on release line.
		`CREATE TABLE IF NOT EXISTS memory_embeddings (
			memory_id    TEXT NOT NULL,
			zoom_level   TEXT NOT NULL,
			model        TEXT NOT NULL,
			embedded_at  TIMESTAMP NOT NULL,
			embed_status TEXT NOT NULL,
			vec          BLOB,
			PRIMARY KEY (memory_id, zoom_level, model),
			FOREIGN KEY (memory_id) REFERENCES memory_record(id) ON DELETE CASCADE
		)`,
		// memory_embed_queue (v37, j7n5 Task 0.3) — LOCAL-ONLY; durable
		// across restarts. PK (memory_id, zoom_level) — model change
		// handled by re-enqueue, not by per-model row tracking.
		`CREATE TABLE IF NOT EXISTS memory_embed_queue (
			memory_id   TEXT NOT NULL,
			zoom_level  TEXT NOT NULL,
			enqueued_at TIMESTAMP NOT NULL,
			retry_count INTEGER NOT NULL DEFAULT 0,
			last_error  TEXT,
			PRIMARY KEY (memory_id, zoom_level)
		)`,

		// v41–v51 dead-end forward-port (thrum-399av). The tables below have no
		// consumer code on the release line; they exist so a fresh v0.10.6 DB
		// stamps the full v51 surface and a v51 DB opens without bricking.

		// alert_deliveries (v45): per-recipient alert dedup window.
		`CREATE TABLE IF NOT EXISTS alert_deliveries (
			recipient_agent_id       TEXT NOT NULL,
			dedup_key                TEXT NOT NULL,
			suppressed_by_message_id TEXT NOT NULL,
			expires_at               TEXT NOT NULL,
			created_at               TEXT NOT NULL,
			PRIMARY KEY (recipient_agent_id, dedup_key)
		)`,

		// telegram_outbound_queue (v49): Lane-B outbound retry queue.
		`CREATE TABLE IF NOT EXISTS telegram_outbound_queue (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id          INTEGER NOT NULL,
			content          TEXT    NOT NULL,
			reply_to_tele_id INTEGER,
			thrum_msg_id     TEXT    NOT NULL,
			attempt_count    INTEGER NOT NULL DEFAULT 0,
			next_retry_at    INTEGER NOT NULL,
			last_error       TEXT,
			status           TEXT    NOT NULL,
			enqueued_at      INTEGER NOT NULL,
			updated_at       INTEGER NOT NULL
		)`,

		// Graph substrate (v50): node/edge/label/comment/blocked.
		`CREATE TABLE IF NOT EXISTS node (
			id               TEXT PRIMARY KEY,
			kind             TEXT NOT NULL,
			title            TEXT NOT NULL,
			status           TEXT NOT NULL DEFAULT 'open',
			raw_status       TEXT NOT NULL DEFAULT 'open',
			priority         INTEGER,
			effective_labels TEXT NOT NULL DEFAULT '[]',
			metadata         TEXT,
			owner            TEXT,
			is_blocked       INTEGER NOT NULL DEFAULT 0,
			created_at       TEXT NOT NULL,
			updated_at       TEXT NOT NULL,
			created_by       TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS node_label (
			node_id  TEXT NOT NULL,
			label    TEXT NOT NULL,
			PRIMARY KEY (node_id, label),
			FOREIGN KEY (node_id) REFERENCES node(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS edge (
			from_id    TEXT NOT NULL,
			type       TEXT NOT NULL,
			to_id      TEXT NOT NULL,
			created_at TEXT NOT NULL,
			created_by TEXT NOT NULL,
			metadata   TEXT,
			PRIMARY KEY (from_id, type, to_id),
			FOREIGN KEY (from_id) REFERENCES node(id) ON DELETE CASCADE,
			FOREIGN KEY (to_id) REFERENCES node(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS node_comment (
			comment_id TEXT PRIMARY KEY,
			node_id    TEXT NOT NULL,
			author     TEXT NOT NULL,
			body       TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY (node_id) REFERENCES node(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS graph_blocked (
			node_id     TEXT PRIMARY KEY,
			blocked_by  TEXT NOT NULL DEFAULT '[]',
			computed_at TEXT NOT NULL
		)`,

		// memory_satellite (v51): graph canary memory payload.
		`CREATE TABLE IF NOT EXISTS memory_satellite (
			node_id           TEXT PRIMARY KEY REFERENCES node(id) ON DELETE CASCADE,
			body_oneline      TEXT NOT NULL DEFAULT '',
			body_short        TEXT,
			body_full         TEXT,
			scope             TEXT NOT NULL DEFAULT 'project',
			source_session_id TEXT,
			agent_id          TEXT NOT NULL DEFAULT '',
			kind              TEXT NOT NULL DEFAULT '',
			subkind           TEXT NOT NULL DEFAULT '',
			last_edited_by    TEXT NOT NULL DEFAULT ''
		)`,
	}

	for _, sql := range tables {
		if _, err := tx.Exec(sql); err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}

	return nil
}

// createIndexes creates all database indexes.
func createIndexes(tx *sql.Tx) error {
	indexes := []string{
		// Message indexes
		"CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(thread_id)",
		// v47 forward-port (thrum-399av): the composite keyset index replaces the
		// old single-column idx_messages_time (the v47 migration DROPs the latter).
		// Fresh DBs must match migrated DBs exactly; app SQL never names the index,
		// and (created_at, message_id) covers (created_at) as a prefix, so the
		// swap is transparent to release-line queries.
		"CREATE INDEX IF NOT EXISTS idx_messages_time_id ON messages(created_at, message_id)",
		"CREATE INDEX IF NOT EXISTS idx_messages_visibility ON messages(visibility_class, created_at) WHERE visibility_class != 'targeted'",
		"CREATE INDEX IF NOT EXISTS idx_messages_agent ON messages(agent_id)",
		"CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_messages_not_deleted ON messages(deleted) WHERE deleted = 0",

		// Scope and ref indexes
		"CREATE INDEX IF NOT EXISTS idx_scopes_lookup ON message_scopes(scope_type, scope_value)",
		"CREATE INDEX IF NOT EXISTS idx_refs_lookup ON message_refs(ref_type, ref_value)",

		// Session and subscription indexes
		"CREATE INDEX IF NOT EXISTS idx_sessions_agent ON sessions(agent_id, started_at)",
		"CREATE INDEX IF NOT EXISTS idx_subscriptions_session ON subscriptions(session_id)",
		"CREATE INDEX IF NOT EXISTS idx_subscriptions_scope ON subscriptions(scope_type, scope_value)",
		"CREATE INDEX IF NOT EXISTS idx_subscriptions_mention ON subscriptions(mention_role)",

		// Message edits index
		"CREATE INDEX IF NOT EXISTS idx_edits_message ON message_edits(message_id, edited_at)",

		// Session scopes and refs indexes
		"CREATE INDEX IF NOT EXISTS idx_session_scopes_lookup ON session_scopes(scope_type, scope_value)",
		"CREATE INDEX IF NOT EXISTS idx_session_refs_lookup ON session_refs(ref_type, ref_value)",

		// Message reads indexes
		"CREATE INDEX IF NOT EXISTS idx_message_reads_agent ON message_reads(agent_id, message_id)",
		"CREATE INDEX IF NOT EXISTS idx_message_reads_message ON message_reads(message_id)",
		"CREATE INDEX IF NOT EXISTS idx_message_deliveries_recipient ON message_deliveries(recipient_agent_id, message_id)",
		"CREATE INDEX IF NOT EXISTS idx_message_deliveries_read ON message_deliveries(recipient_agent_id, read_at)",

		// Group indexes
		"CREATE INDEX IF NOT EXISTS idx_groups_name ON groups(name)",
		"CREATE INDEX IF NOT EXISTS idx_group_members_group ON group_members(group_id)",
		"CREATE INDEX IF NOT EXISTS idx_group_members_lookup ON group_members(member_type, member_value)",

		// Events table indexes (for sync + compactor retention DELETE)
		"CREATE INDEX IF NOT EXISTS idx_events_sequence ON events(sequence)",
		"CREATE INDEX IF NOT EXISTS idx_events_type ON events(type)",
		"CREATE INDEX IF NOT EXISTS idx_events_origin ON events(origin_daemon)",
		// thrum-7ojv: timestamp index added in v0.10.6 v38 migration; mirrored
		// here so fresh-install DBs stamp it without needing the migration.
		"CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp)",

		// Work contexts indexes
		"CREATE INDEX IF NOT EXISTS idx_work_contexts_agent ON agent_work_contexts(agent_id)",
		"CREATE INDEX IF NOT EXISTS idx_work_contexts_branch ON agent_work_contexts(branch)",

		// Command queue indexes
		"CREATE INDEX IF NOT EXISTS idx_queue_session_state ON command_queue(session_name, state)",

		// Monitors indexes (v20)
		"CREATE INDEX IF NOT EXISTS idx_monitors_status ON monitors(status)",

		// Permission nudges indexes (v21)
		"CREATE INDEX IF NOT EXISTS idx_permission_nudges_session ON permission_nudges(session)",
		"CREATE INDEX IF NOT EXISTS idx_permission_nudges_expires ON permission_nudges(expires_at)",

		// Telegram msg map reverse-lookup index (v24, thrum-48kt.2)
		"CREATE INDEX IF NOT EXISTS idx_telegram_msg_map_thrum ON telegram_msg_map(thrum_msg_id)",

		// Scheduler indexes (v25, forward-ported from thrum-agents)
		"CREATE INDEX IF NOT EXISTS idx_scheduler_state_next ON scheduler_job_state(next_scheduled_at)",
		"CREATE INDEX IF NOT EXISTS idx_scheduler_events_job_time ON scheduler_job_events(job_id, event_time)",
		"CREATE INDEX IF NOT EXISTS idx_scheduler_events_run ON scheduler_job_events(run_id)",

		// Agent lifecycle indexes (v27)
		"CREATE INDEX IF NOT EXISTS idx_agent_lifecycle_agent_time ON agent_lifecycle_events(agent_name, event_time)",
		"CREATE INDEX IF NOT EXISTS idx_agent_lifecycle_kind ON agent_lifecycle_events(event_kind, event_time)",

		// Reminders indexes (v28)
		"CREATE INDEX IF NOT EXISTS idx_reminders_next ON reminders(next_reminder_at) WHERE state = 'open'",
		"CREATE INDEX IF NOT EXISTS idx_reminders_state ON reminders(state)",
		"CREATE INDEX IF NOT EXISTS idx_reminders_target ON reminders(target_agent) WHERE state = 'open'",
		"CREATE INDEX IF NOT EXISTS idx_reminders_source_kind ON reminders(source, trigger_kind)",

		// Email substrate indexes (v30/v31/v32)
		"CREATE INDEX IF NOT EXISTS idx_email_msg_seen_proc ON email_msg_seen(processed_at)",
		"CREATE INDEX IF NOT EXISTS idx_email_queue_next ON email_outbound_queue(next_retry_at, status)",
		"CREATE INDEX IF NOT EXISTS idx_peer_rate_paused ON email_peer_rate_state(paused_at) WHERE paused_at IS NOT NULL",

		// Memory indexes (v34, E16). idx_memories_not_deleted is partial
		// (WHERE deleted = 0); idx_memory_scopes_lookup supports the OR-match
		// scope filter.
		"CREATE INDEX IF NOT EXISTS idx_memories_name ON memories(name)",
		"CREATE INDEX IF NOT EXISTS idx_memories_author ON memories(author)",
		"CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(type)",
		"CREATE INDEX IF NOT EXISTS idx_memories_not_deleted ON memories(deleted) WHERE deleted = 0",
		"CREATE INDEX IF NOT EXISTS idx_memories_updated_at ON memories(updated_at)",
		"CREATE INDEX IF NOT EXISTS idx_memory_scopes_lookup ON memory_scopes(scope_type, scope_value)",

		// memory_record / memory_tag / memory_edge indexes (v37, j7n5 Epic 0).
		// Back-ported from thrum-agents per the thrum-7ojv DDL-only back-port
		// pattern. kind/agent/created/updated drive memory.list filters on
		// thrum-agents; status + scope drive default-assembly narrowing.
		// memory_edge has a reverse index on (to_id, edge_kind) for
		// inbound-edge lookups; kind-only index supports observability
		// filters. No release-line code uses these — index storage only.
		"CREATE INDEX IF NOT EXISTS idx_memory_kind ON memory_record(kind)",
		"CREATE INDEX IF NOT EXISTS idx_memory_agent ON memory_record(agent_id)",
		"CREATE INDEX IF NOT EXISTS idx_memory_created ON memory_record(created_at)",
		"CREATE INDEX IF NOT EXISTS idx_memory_updated ON memory_record(updated_at)",
		"CREATE INDEX IF NOT EXISTS idx_memory_status ON memory_record(status)",
		"CREATE INDEX IF NOT EXISTS idx_memory_scope ON memory_record(scope)",
		"CREATE INDEX IF NOT EXISTS idx_memory_tag_tag ON memory_tag(tag)",
		"CREATE INDEX IF NOT EXISTS idx_memory_edge_to ON memory_edge(to_id, edge_kind)",
		"CREATE INDEX IF NOT EXISTS idx_memory_edge_kind ON memory_edge(edge_kind)",
		// memory_embeddings worker-scan index (v37, j7n5 Task 0.3). Drives
		// the "next batch to embed" query the background worker runs on
		// thrum-agents. Storage only on release line.
		"CREATE INDEX IF NOT EXISTS idx_memory_embed_status ON memory_embeddings(embed_status)",

		// v45–v50 dead-end forward-port (thrum-399av): indexes for the new
		// tables/columns above. Storage only — no release-line consumer.
		"CREATE INDEX IF NOT EXISTS idx_alert_deliveries_expires ON alert_deliveries(recipient_agent_id, expires_at)",      // v45
		"CREATE INDEX IF NOT EXISTS idx_deliveries_recipient_via ON message_deliveries(recipient_agent_id, addressed_via)", // v48
		"CREATE INDEX IF NOT EXISTS idx_tg_queue_next ON telegram_outbound_queue(next_retry_at, status)",                   // v49
		"CREATE INDEX IF NOT EXISTS idx_node_ready ON node(kind, status, is_blocked)",                                      // v50
		"CREATE INDEX IF NOT EXISTS idx_node_kind ON node(kind, status)",                                                   // v50
		"CREATE INDEX IF NOT EXISTS idx_edge_to ON edge(to_id, type)",                                                      // v50
		"CREATE INDEX IF NOT EXISTS idx_node_label_label ON node_label(label, node_id)",                                    // v50
		"CREATE INDEX IF NOT EXISTS idx_node_comment_node ON node_comment(node_id, created_at)",                            // v50
	}

	for _, sql := range indexes {
		if _, err := tx.Exec(sql); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}

	return nil
}

// OpenDB opens a SQLite database connection.
func OpenDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	// Set journal mode to WAL for better concurrency
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set journal mode: %w", err)
	}

	// Set busy timeout so concurrent access retries instead of returning SQLITE_BUSY.
	// Without this, a write during heavy read activity causes immediate failures
	// that cascade into daemon deadlocks.
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}

	// NORMAL synchronous is safe in WAL mode and significantly faster than
	// the default FULL. Reduces write latency under heavy message traffic.
	if _, err := db.Exec("PRAGMA synchronous = NORMAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set synchronous mode: %w", err)
	}

	// Limit to 1 open connection. SQLite doesn't handle concurrent writers;
	// multiple connections cause checkpoint-blocking readers that let the WAL
	// grow unbounded (1.18MB observed in production).
	db.SetMaxOpenConns(1)

	return db, nil
}

// Migrate migrates the database to the current schema version.
func Migrate(db *sql.DB) error {
	// Check if schema_version table exists
	var tableName string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='schema_version'").Scan(&tableName)
	if err == sql.ErrNoRows {
		// No schema exists, initialize it
		return InitDB(db)
	}
	if err != nil {
		return fmt.Errorf("check schema_version table: %w", err)
	}

	currentVersion, err := GetSchemaVersion(db)
	if err != nil {
		return fmt.Errorf("get schema version: %w", err)
	}

	if currentVersion == 0 {
		// Version table exists but no version set, initialize
		return InitDB(db)
	}

	if currentVersion == CurrentVersion {
		// Already at current version
		return nil
	}

	// Resolve the on-disk DB path once via PRAGMA database_list. Used both
	// by the downgrade-guard error message (so operators see exactly which
	// file to remove) and by the pre-migration backup block below.
	// Empty string for in-memory test DBs; both consumers degrade
	// gracefully on the empty case.
	var dbSeq int
	var dbName, dbFile string
	_ = db.QueryRow("PRAGMA database_list").Scan(&dbSeq, &dbName, &dbFile)

	// Downgrade guard: refuse to run against a DB from a newer binary.
	// thrum-quth: include both recovery paths in the error so users on
	// multi-binary worktree machines have an immediate path forward
	// instead of having to grep the codebase for the message.
	if currentVersion > CurrentVersion {
		dbPath := dbFile
		if dbPath == "" {
			dbPath = `<unknown path; locate via "find ~ -name messages.db">`
		}
		return fmt.Errorf(`database schema is version %d, this binary supports up to %d — cannot downgrade.

Recovery options:
  1. Re-install a newer binary that supports schema v%d or above:
       cd <worktree-with-newer-branch> && make install
  2. Restore from the pre-migration backup (LOSES NOTHING, recommended):
       thrum daemon stop
       ls %s.pre-migration-*.bak    # pick the one taken before the newer-binary install
       cp <chosen>.bak %s
  3. Delete the database to start fresh (PERMANENTLY LOSES local message history, spool, scheduler state, and sync checkpoints — the daemon does NOT rebuild SQLite from JSONL on startup; only NEW events repopulate the fresh DB):
       thrum daemon stop   # release file locks first
       rm %s
       rm %s-wal %s-shm    # if present
  4. See CLAUDE.md § "Multi-binary worktree footgun" for prevention`,
			currentVersion, CurrentVersion,
			currentVersion,
			dbPath, dbPath,
			dbPath, dbPath, dbPath)
	}

	// Arm the migration-progress reporter (thrum-vh2c) BEFORE the slow backup so
	// the waiting CLI (`thrum daemon start/restart`) can show a spinner and
	// extend its start-wait while the migration is observably progressing,
	// instead of false-timing-out on a long cross-version migration of a large
	// DB. The reporter writes a heartbeating status file next to the DB; the CLI
	// tails it. Best-effort + nil-safe: a write failure returns a nil reporter
	// and migration proceeds exactly as before. The status file lives in the
	// var dir (same dir as the DB). Skipped for in-memory test DBs (dbFile == "").
	var reporter *migrationReporter
	if dbFile != "" {
		reporter = startMigrationReporter(filepath.Dir(dbFile), currentVersion, CurrentVersion)
	}
	defer reporter.Done() // nil-safe; removes the status file on every return path

	// Back up the DB file + WAL/SHM sidecars before any migration runs so the
	// operator can revert cleanly by renaming the backup back. Each migration
	// event gets its own timestamped, lexically-sortable snapshot (UTC), so
	// repeated migrations keep distinct, time-ordered recovery points (and
	// repeated test runs are never skipped by a stale backup-once snapshot).
	// Skip when file == "" (in-memory test DB). A backup failure HALTS the
	// migration: the DB is NOT rebuildable from JSONL, so we refuse to migrate
	// without a recovery snapshot. backupFileOnce is a no-op (nil) for an
	// absent source, so absent WAL/SHM sidecars stay fine; only real failures
	// (disk full, permissions) halt.
	if dbFile != "" {
		ts := time.Now().UTC().Format("20060102T150405Z")
		suffix := fmt.Sprintf(".pre-migration-v%d-%s.bak", currentVersion, ts)
		for _, src := range []string{dbFile, dbFile + "-wal", dbFile + "-shm"} {
			if err := backupFileOnce(src, src+suffix); err != nil {
				return fmt.Errorf("pre-migration DB backup failed: %w — refusing to run schema migration v%d->v%d without a recovery snapshot (the DB is NOT rebuildable from JSONL). Free disk space / fix permissions on %s and restart", err, currentVersion, CurrentVersion, dbFile)
			}
		}
	}

	// Run migrations
	if currentVersion < CurrentVersion {
		// Emit start + completion logs so operators can confirm from daemon
		// logs that a migration actually ran on post-deploy restart.
		// Pre-thrum-rchj the function logged only backup errors, so a
		// bug report of "schema stayed at vX after restart" had no log
		// evidence to distinguish "daemon didn't pick up the new binary"
		// from "migration silently failed".
		log.Printf("[schema] migrating DB from v%d to v%d", currentVersion, CurrentVersion)
		reporter.setPhase(MigrationPhaseMigrating) // nil-safe
		if err := runMigrations(db, currentVersion, CurrentVersion); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
		log.Printf("[schema] migration complete: v%d", CurrentVersion)
	}

	return nil
}

// runMigrations runs all migrations from startVersion to endVersion.
func runMigrations(db *sql.DB, startVersion, endVersion int) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Migration from version 3 to 4: Add impersonation support
	if startVersion < 4 && endVersion >= 4 {
		// Add authored_by and disclosed columns to messages table
		_, err = tx.Exec(`ALTER TABLE messages ADD COLUMN authored_by TEXT`)
		if err != nil {
			return fmt.Errorf("add authored_by column: %w", err)
		}
		_, err = tx.Exec(`ALTER TABLE messages ADD COLUMN disclosed INTEGER DEFAULT 0`)
		if err != nil {
			return fmt.Errorf("add disclosed column: %w", err)
		}
	}

	// Migration from version 4 to 5: Add message read tracking
	if startVersion < 5 && endVersion >= 5 {
		// Create message_reads table.
		// DEPRECATED (thrum-tcqw/b6qw): read-truth unified on
		// message_deliveries.read_at; table retained for back-compat, no live
		// readers/writers as of v40 (cascade-deletes only).
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS message_reads (
				message_id TEXT NOT NULL,
				session_id TEXT NOT NULL,
				agent_id   TEXT NOT NULL,
				read_at    TEXT NOT NULL,
				PRIMARY KEY (message_id, session_id),
				FOREIGN KEY (message_id) REFERENCES messages(message_id) ON DELETE CASCADE
			)
		`)
		if err != nil {
			return fmt.Errorf("create message_reads table: %w", err)
		}

		// Create indexes for message_reads
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_message_reads_agent ON message_reads(agent_id, message_id)`)
		if err != nil {
			return fmt.Errorf("create idx_message_reads_agent: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_message_reads_message ON message_reads(message_id)`)
		if err != nil {
			return fmt.Errorf("create idx_message_reads_message: %w", err)
		}
	}

	// Migration from version 5 to 6: Add agent work contexts
	if startVersion < 6 && endVersion >= 6 {
		// Create agent_work_contexts table
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS agent_work_contexts (
				session_id        TEXT PRIMARY KEY,
				agent_id          TEXT NOT NULL,
				branch            TEXT,
				worktree_path     TEXT,
				unmerged_commits  TEXT,
				uncommitted_files TEXT,
				changed_files     TEXT,
				git_updated_at    TEXT,
				current_task      TEXT,
				task_updated_at   TEXT,
				intent            TEXT,
				intent_updated_at TEXT,
				FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
			)
		`)
		if err != nil {
			return fmt.Errorf("create agent_work_contexts table: %w", err)
		}

		// Create indexes for agent_work_contexts
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_work_contexts_agent ON agent_work_contexts(agent_id)`)
		if err != nil {
			return fmt.Errorf("create idx_work_contexts_agent: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_work_contexts_branch ON agent_work_contexts(branch)`)
		if err != nil {
			return fmt.Errorf("create idx_work_contexts_branch: %w", err)
		}
	}

	// Migration from version 6 to 7: Event ID backfill
	// Note: v7 introduces event_id fields for all events in JSONL.
	// The actual JSONL backfilling is handled by BackfillEventID() function
	// which is called separately by the daemon during initialization.
	// This migration just bumps the version number.
	// (No SQL schema changes needed for v6→v7)

	// Migration from version 7 to 8: Add groups
	if startVersion < 8 && endVersion >= 8 {
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS groups (
				group_id    TEXT PRIMARY KEY,
				name        TEXT UNIQUE NOT NULL,
				description TEXT,
				created_at  TEXT NOT NULL,
				created_by  TEXT NOT NULL,
				updated_at  TEXT,
				metadata    TEXT
			)
		`)
		if err != nil {
			return fmt.Errorf("create groups table: %w", err)
		}

		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS group_members (
				id           INTEGER PRIMARY KEY AUTOINCREMENT,
				group_id     TEXT NOT NULL,
				member_type  TEXT NOT NULL,
				member_value TEXT NOT NULL,
				added_at     TEXT NOT NULL,
				added_by     TEXT,
				UNIQUE(group_id, member_type, member_value),
				FOREIGN KEY (group_id) REFERENCES groups(group_id) ON DELETE CASCADE
			)
		`)
		if err != nil {
			return fmt.Errorf("create group_members table: %w", err)
		}

		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_groups_name ON groups(name)`)
		if err != nil {
			return fmt.Errorf("create idx_groups_name: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_group_members_group ON group_members(group_id)`)
		if err != nil {
			return fmt.Errorf("create idx_group_members_group: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_group_members_lookup ON group_members(member_type, member_value)`)
		if err != nil {
			return fmt.Errorf("create idx_group_members_lookup: %w", err)
		}
	}

	// Migration from version 8 to 9: Add events table and sync_checkpoints for Tailscale sync
	if startVersion < 9 && endVersion >= 9 {
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS events (
				event_id TEXT PRIMARY KEY,
				sequence INTEGER UNIQUE NOT NULL,
				type TEXT NOT NULL,
				timestamp TEXT NOT NULL,
				origin_daemon TEXT NOT NULL,
				event_json TEXT NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("create events table: %w", err)
		}

		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_events_sequence ON events(sequence)`)
		if err != nil {
			return fmt.Errorf("create idx_events_sequence: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_events_type ON events(type)`)
		if err != nil {
			return fmt.Errorf("create idx_events_type: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_events_origin ON events(origin_daemon)`)
		if err != nil {
			return fmt.Errorf("create idx_events_origin: %w", err)
		}

		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS sync_checkpoints (
				peer_daemon_id TEXT PRIMARY KEY,
				last_synced_sequence INTEGER NOT NULL DEFAULT 0,
				last_sync_timestamp INTEGER NOT NULL,
				sync_status TEXT NOT NULL DEFAULT 'idle'
			)
		`)
		if err != nil {
			return fmt.Errorf("create sync_checkpoints table: %w", err)
		}
	}

	// Migration from version 9 to 10: Add file_changes column to agent_work_contexts
	if startVersion < 10 && endVersion >= 10 {
		_, err = tx.Exec(`ALTER TABLE agent_work_contexts ADD COLUMN file_changes TEXT DEFAULT '[]'`)
		if err != nil {
			return fmt.Errorf("add file_changes column: %w", err)
		}
	}

	// Migration from version 10 to 11: Add hostname column to agents table
	if startVersion < 11 && endVersion >= 11 {
		// Only ALTER if agents table exists (it may not in partial-schema test DBs)
		var agentsExists string
		if err := tx.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='agents'").Scan(&agentsExists); err == nil {
			_, err = tx.Exec(`ALTER TABLE agents ADD COLUMN hostname TEXT`)
			if err != nil {
				return fmt.Errorf("add hostname column: %w", err)
			}
		}
	}

	// Migration from version 11 to 12: Remove threads table and thread index
	if startVersion < 12 && endVersion >= 12 {
		_, err = tx.Exec(`DROP TABLE IF EXISTS threads`)
		if err != nil {
			return fmt.Errorf("drop threads table: %w", err)
		}
		_, err = tx.Exec(`DROP INDEX IF EXISTS idx_messages_thread`)
		if err != nil {
			return fmt.Errorf("drop idx_messages_thread: %w", err)
		}
	}

	// Migration from version 12 to 13: Backfill NULL display/hostname/last_seen_at in agents
	if startVersion < 13 && endVersion >= 13 {
		var agentsExists string
		if err := tx.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='agents'").Scan(&agentsExists); err == nil {
			_, err = tx.Exec(`UPDATE agents SET display = '' WHERE display IS NULL`)
			if err != nil {
				return fmt.Errorf("backfill agents display: %w", err)
			}
			_, err = tx.Exec(`UPDATE agents SET hostname = '' WHERE hostname IS NULL`)
			if err != nil {
				return fmt.Errorf("backfill agents hostname: %w", err)
			}
			_, err = tx.Exec(`UPDATE agents SET last_seen_at = '' WHERE last_seen_at IS NULL`)
			if err != nil {
				return fmt.Errorf("backfill agents last_seen_at: %w", err)
			}
		}
		// Note: SQLite doesn't support ALTER COLUMN to add NOT NULL or DEFAULT
		// to existing columns. New databases get the correct constraints from
		// createTables(). Existing databases have NULLs backfilled above.
	}

	// Migration from version 13 to 14: Add durable message delivery state
	if startVersion < 14 && endVersion >= 14 {
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS message_deliveries (
				message_id          TEXT NOT NULL,
				recipient_agent_id  TEXT NOT NULL,
				delivered_at        TEXT NOT NULL,
				seen_at             TEXT,
				read_at             TEXT,
				PRIMARY KEY (message_id, recipient_agent_id),
				FOREIGN KEY (message_id) REFERENCES messages(message_id) ON DELETE CASCADE
			)
		`)
		if err != nil {
			return fmt.Errorf("create message_deliveries table: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_message_deliveries_recipient ON message_deliveries(recipient_agent_id, message_id)`)
		if err != nil {
			return fmt.Errorf("create idx_message_deliveries_recipient: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_message_deliveries_read ON message_deliveries(recipient_agent_id, read_at)`)
		if err != nil {
			return fmt.Errorf("create idx_message_deliveries_read: %w", err)
		}
	}

	// Migration from version 14 to 15: Add purge_metadata table
	if startVersion < 15 && endVersion >= 15 {
		_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS purge_metadata (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`)
		if err != nil {
			return fmt.Errorf("migration 14→15: %w", err)
		}
	}

	// Migration from version 15 to 16: Add claude_pid column to agents table
	if startVersion < 16 && endVersion >= 16 {
		// Only ALTER if agents table exists (it may not in partial-schema test DBs)
		var agentsExists string
		if err := tx.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='agents'").Scan(&agentsExists); err == nil {
			_, err = tx.Exec(`ALTER TABLE agents ADD COLUMN claude_pid INTEGER NOT NULL DEFAULT 0`)
			if err != nil {
				return fmt.Errorf("migration 15→16: %w", err)
			}
		}
	}

	// Migration from version 16 to 17: Rename claude_pid to agent_pid
	if startVersion < 17 && endVersion >= 17 {
		var agentsExists string
		if err := tx.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='agents'").Scan(&agentsExists); err == nil {
			_, err = tx.Exec(`ALTER TABLE agents RENAME COLUMN claude_pid TO agent_pid`)
			if err != nil {
				return fmt.Errorf("migration 16→17: %w", err)
			}
		}
	}

	// Migration from version 17 to 18: Add command_queue table
	if startVersion < 18 && endVersion >= 18 {
		_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS command_queue (
			command_id      TEXT PRIMARY KEY,
			session_name    TEXT NOT NULL,
			requester_agent TEXT NOT NULL,
			command_text    TEXT NOT NULL,
			state           TEXT NOT NULL DEFAULT 'queued',
			timeout_ms      INTEGER NOT NULL DEFAULT 120000,
			submitted_at    TEXT NOT NULL,
			sent_at         TEXT,
			completed_at    TEXT,
			captured_output TEXT,
			position        INTEGER NOT NULL DEFAULT 0
		)`)
		if err != nil {
			return fmt.Errorf("migration 17→18: create command_queue: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_queue_session_state ON command_queue(session_name, state)`)
		if err != nil {
			return fmt.Errorf("migration 17→18: create index: %w", err)
		}
	}

	// Migration from version 18 to 19: Add silence_ms and notify_on_complete
	// columns to command_queue. SQLite does not support ADD COLUMN IF NOT
	// EXISTS, so we check PRAGMA table_info for idempotency (supports running
	// the migration against a fresh createTables() schema too).
	if startVersion < 19 && endVersion >= 19 {
		existing, err := queueColumnSet(tx)
		if err != nil {
			return fmt.Errorf("migration 18→19: inspect command_queue: %w", err)
		}
		if !existing["silence_ms"] {
			_, err = tx.Exec(`ALTER TABLE command_queue ADD COLUMN silence_ms INTEGER NOT NULL DEFAULT 5000`)
			if err != nil {
				return fmt.Errorf("migration 18→19: add silence_ms: %w", err)
			}
		}
		if !existing["notify_on_complete"] {
			_, err = tx.Exec(`ALTER TABLE command_queue ADD COLUMN notify_on_complete INTEGER NOT NULL DEFAULT 1`)
			if err != nil {
				return fmt.Errorf("migration 18→19: add notify_on_complete: %w", err)
			}
		}
	}

	// Migration from version 19 to 20: Add monitors table for the
	// thrum monitor feature. Uses CREATE TABLE IF NOT EXISTS so the
	// migration is safe to run after createTables() (which already
	// creates the table on fresh installs).
	if startVersion < 20 && endVersion >= 20 {
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS monitors (
				id                TEXT PRIMARY KEY,
				name              TEXT NOT NULL UNIQUE,
				argv              TEXT NOT NULL,
				match_pattern     TEXT NOT NULL,
				target            TEXT NOT NULL,
				cwd               TEXT NOT NULL,
				env               TEXT NOT NULL,
				debounce_seconds  INTEGER NOT NULL,
				created_at        TEXT NOT NULL,
				updated_at        TEXT NOT NULL,
				status            TEXT NOT NULL,
				last_exit_code    INTEGER,
				last_exit_at      TEXT,
				pid               INTEGER
			)
		`)
		if err != nil {
			return fmt.Errorf("migration 19→20: create monitors: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_monitors_status ON monitors(status)`)
		if err != nil {
			return fmt.Errorf("migration 19→20: idx_monitors_status: %w", err)
		}
	}

	// Migration from version 20 to 21: Add permission_nudges table for
	// the permission-prompt detection feature. Daemon-local only (not
	// synced across repos). Uses CREATE TABLE IF NOT EXISTS so the
	// migration is safe to run after createTables().
	if startVersion < 21 && endVersion >= 21 {
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS permission_nudges (
				message_id       TEXT PRIMARY KEY,
				session          TEXT NOT NULL,
				tmux_target      TEXT NOT NULL,
				agent_name       TEXT NOT NULL,
				pattern_key      TEXT NOT NULL,
				approve_key      TEXT NOT NULL,
				deny_key         TEXT,
				first_detected   TIMESTAMP NOT NULL,
				last_nudge_at    TIMESTAMP NOT NULL,
				nudge_count      INTEGER NOT NULL,
				last_pane_hash   BLOB NOT NULL,
				expires_at       TIMESTAMP NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("migration 20→21: create permission_nudges: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_permission_nudges_session ON permission_nudges(session)`)
		if err != nil {
			return fmt.Errorf("migration 20→21: idx_permission_nudges_session: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_permission_nudges_expires ON permission_nudges(expires_at)`)
		if err != nil {
			return fmt.Errorf("migration 20→21: idx_permission_nudges_expires: %w", err)
		}
	}

	// Migration from version 21 to 22: add origin_daemon column to agents
	// table and backfill from the events table. Fixes thrum-mm3l: without
	// origin tracking, HandleRegister's role+module conflict check treats
	// cross-daemon agents as local duplicates and silently deletes them on
	// force-override. The backfill reads each agent's most-recent
	// agent.register event from the events table (the authoritative source
	// of origin_daemon) and copies the value onto the agents row.
	//
	// Rows with no matching agent.register event keep the default empty
	// string — those are legacy rows from before the events table was
	// durable. HandleRegister's filter treats empty origin_daemon as
	// "unknown / assume local" to avoid false negatives on those rows.
	//
	// Idempotent: skipped entirely if the agents table doesn't exist (the
	// v5→V_partial migration tests bring up a minimal schema); skipped if
	// the column is already present (safe to re-run against a fresh
	// createTables() schema which already has it).
	if startVersion < 22 && endVersion >= 22 {
		hasAgents, err := tableExists(tx, "agents")
		if err != nil {
			return fmt.Errorf("migration 21→22: check agents table: %w", err)
		}
		if hasAgents {
			agentCols, err := columnSet(tx, "agents")
			if err != nil {
				return fmt.Errorf("migration 21→22: inspect agents: %w", err)
			}
			if !agentCols["origin_daemon"] {
				_, err = tx.Exec(`ALTER TABLE agents ADD COLUMN origin_daemon TEXT NOT NULL DEFAULT ''`)
				if err != nil {
					return fmt.Errorf("migration 21→22: add origin_daemon column: %w", err)
				}
			}
			// Backfill: for each agent, find the most-recent agent.register
			// event and copy its origin_daemon. Uses the events table (not
			// JSONL) — the table is populated by the same WriteEvent path
			// that feeds the projector, so every projected agent should have
			// at least one event. Skipped when the events table is absent
			// (minimal test schema) — the column default of '' is correct
			// in that case.
			hasEvents, err := tableExists(tx, "events")
			if err != nil {
				return fmt.Errorf("migration 21→22: check events table: %w", err)
			}
			if hasEvents {
				_, err = tx.Exec(`
					UPDATE agents
					SET origin_daemon = COALESCE((
						SELECT e.origin_daemon
						FROM events e
						WHERE e.type = 'agent.register'
						  AND json_extract(e.event_json, '$.agent_id') = agents.agent_id
						ORDER BY e.sequence DESC
						LIMIT 1
					), '')
					WHERE origin_daemon = ''
				`)
				if err != nil {
					return fmt.Errorf("migration 21→22: backfill origin_daemon: %w", err)
				}
			}
		}
	}

	// Migration from version 22 to 23: Add daemon_identity table
	// Single-row table mirroring the identity block in .thrum/config.json.
	// Populated at daemon startup by state code; not backfilled here
	// (existing databases simply get the empty table; state wiring in a
	// subsequent task fills it on first daemon start post-upgrade).
	if startVersion < 23 && endVersion >= 23 {
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS daemon_identity (
				daemon_id      TEXT PRIMARY KEY,
				repo_name      TEXT NOT NULL,
				hostname       TEXT NOT NULL,
				repo_path      TEXT NOT NULL,
				git_origin_url TEXT,
				init_at        TEXT NOT NULL,
				updated_at     TEXT NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("migration 22→23: create daemon_identity table: %w", err)
		}
	}

	// Migration from version 23 to 24: Add telegram_msg_map table for
	// durable Telegram↔Thrum message ID mapping (thrum-48kt.2).
	// Prior state: in-memory LRU only, daemon restart lost pending-nudge
	// mappings → supervisor replies after restart landed as top-level
	// DMs with no reply_to ref → TryResolve never fired.
	if startVersion < 24 && endVersion >= 24 {
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS telegram_msg_map (
				external_key TEXT PRIMARY KEY,
				thrum_msg_id TEXT NOT NULL,
				created_at   INTEGER NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("migration 23→24: create telegram_msg_map table: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_telegram_msg_map_thrum ON telegram_msg_map(thrum_msg_id)`)
		if err != nil {
			return fmt.Errorf("migration 23→24: create idx_telegram_msg_map_thrum: %w", err)
		}
	}

	// Migrations 25-32 are forward-ported from thrum-agents (v0.11 substrate).
	// All blocks add DDL only — no consumer code on release/v0.10.5. v29 is a
	// deliberate gap (reserved for MB-1.S6 on the substrate plan); runMigrations
	// handles gapped sequences naturally — the missing v29 branch no-ops.
	// thrum-quth follow-up: lets v0.10.5-rc.4 binary open DBs touched by v0.11.

	// Migration 24→25: scheduler substrate (A-B1).
	if startVersion < 25 && endVersion >= 25 {
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS scheduler_job_state (
				job_id                TEXT PRIMARY KEY,
				job_generation        INTEGER NOT NULL DEFAULT 1,
				current_state         TEXT    NOT NULL,
				current_stage         TEXT,
				stage_entered_at      INTEGER,
				last_run_id           TEXT,
				last_fired_at         INTEGER,
				last_completed_at     INTEGER,
				last_completion_state TEXT,
				last_error            TEXT,
				next_scheduled_at     INTEGER,
				consecutive_failures  INTEGER NOT NULL DEFAULT 0,
				escalation_sent       INTEGER NOT NULL DEFAULT 0,
				total_runs            INTEGER NOT NULL DEFAULT 0,
				created_at            INTEGER NOT NULL,
				updated_at            INTEGER NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("migration 24→25: create scheduler_job_state: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_scheduler_state_next ON scheduler_job_state(next_scheduled_at)`)
		if err != nil {
			return fmt.Errorf("migration 24→25: idx_scheduler_state_next: %w", err)
		}
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS scheduler_job_events (
				id          INTEGER PRIMARY KEY AUTOINCREMENT,
				job_id      TEXT    NOT NULL,
				run_id      TEXT    NOT NULL,
				event_time  INTEGER NOT NULL,
				from_state  TEXT,
				to_state    TEXT    NOT NULL,
				reason      TEXT,
				details     TEXT
			)
		`)
		if err != nil {
			return fmt.Errorf("migration 24→25: create scheduler_job_events: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_scheduler_events_job_time ON scheduler_job_events(job_id, event_time)`)
		if err != nil {
			return fmt.Errorf("migration 24→25: idx_scheduler_events_job_time: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_scheduler_events_run ON scheduler_job_events(run_id)`)
		if err != nil {
			return fmt.Errorf("migration 24→25: idx_scheduler_events_run: %w", err)
		}
	}

	// Migration 25→26: B-B1 mode + identity + 4 runtime cols on agents table.
	// Idempotent via columnSet check — safe against a fresh createTables()
	// schema (cols already present) and against partial test schemas (skipped
	// when agents table absent). Matches the established v21→v22 origin_daemon
	// pattern.
	if startVersion < 26 && endVersion >= 26 {
		hasAgents, err := tableExists(tx, "agents")
		if err != nil {
			return fmt.Errorf("migration 25→26: check agents table: %w", err)
		}
		if hasAgents {
			agentCols, err := columnSet(tx, "agents")
			if err != nil {
				return fmt.Errorf("migration 25→26: inspect agents: %w", err)
			}
			type colDef struct {
				name string
				ddl  string
			}
			for _, c := range []colDef{
				{"mode", `mode                     TEXT    NOT NULL DEFAULT 'persistent'`},
				{"identity", `identity                 TEXT    NOT NULL DEFAULT 'long_lived'`},
				{"auto_respawn_enabled", `auto_respawn_enabled     INTEGER NOT NULL DEFAULT 0`},
				{"auto_respawn_disabled_at", `auto_respawn_disabled_at INTEGER`},
				{"state_md_parse_failed_at", `state_md_parse_failed_at INTEGER`},
				{"last_pane_alive_at", `last_pane_alive_at       INTEGER`},
			} {
				if agentCols[c.name] {
					continue
				}
				if _, err := tx.Exec(`ALTER TABLE agents ADD COLUMN ` + c.ddl); err != nil {
					return fmt.Errorf("migration 25→26: add agents.%s: %w", c.name, err)
				}
			}
		}
	}

	// Migration 26→27: B-B1 agent_lifecycle_events journal.
	if startVersion < 27 && endVersion >= 27 {
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS agent_lifecycle_events (
				id                INTEGER PRIMARY KEY AUTOINCREMENT,
				agent_name        TEXT    NOT NULL,
				event_kind        TEXT    NOT NULL,
				event_time        INTEGER NOT NULL,
				detection_method  TEXT CHECK (
					detection_method IS NULL OR detection_method IN
						('health_check_tick', 'restart_reconciliation', 'rpc_observation')
				),
				reason            TEXT,
				details           TEXT
			)
		`)
		if err != nil {
			return fmt.Errorf("migration 26→27: create agent_lifecycle_events: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_agent_lifecycle_agent_time ON agent_lifecycle_events(agent_name, event_time)`)
		if err != nil {
			return fmt.Errorf("migration 26→27: idx_agent_lifecycle_agent_time: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_agent_lifecycle_kind ON agent_lifecycle_events(event_kind, event_time)`)
		if err != nil {
			return fmt.Errorf("migration 26→27: idx_agent_lifecycle_kind: %w", err)
		}
	}

	// Migration ?→28: A-B4 unified reminder substrate. Polymorphic single
	// table covering both time-triggered and condition-triggered reminders.
	// Originally landed as v25 on a side-branch, renumbered to v28 in the
	// LOCKED substrate plan to claim its slot (A-B1=25, B-B1=26/27, A-B4=28,
	// reserved=29, D-B1=30/31/32).
	if startVersion < 28 && endVersion >= 28 {
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS reminders (
				id                  TEXT    PRIMARY KEY,
				source              TEXT    NOT NULL,
				source_agent        TEXT,
				trigger_kind        TEXT    NOT NULL,
				trigger_at          INTEGER,
				trigger_meta        TEXT,
				target_agent        TEXT,
				target_chain        TEXT,
				body                TEXT,
				raised_at           INTEGER NOT NULL,
				next_reminder_at    INTEGER,
				last_fired_at       INTEGER,
				state               TEXT    NOT NULL,
				pane_snapshot       TEXT,
				defer_history       TEXT    NOT NULL DEFAULT '[]',
				cleared_at          INTEGER,
				cancelled_at        INTEGER,
				created_at          INTEGER NOT NULL,
				updated_at          INTEGER NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("migration 27→28: create reminders table: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_reminders_next ON reminders(next_reminder_at) WHERE state = 'open'`)
		if err != nil {
			return fmt.Errorf("migration 27→28: idx_reminders_next: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_reminders_state ON reminders(state)`)
		if err != nil {
			return fmt.Errorf("migration 27→28: idx_reminders_state: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_reminders_target ON reminders(target_agent) WHERE state = 'open'`)
		if err != nil {
			return fmt.Errorf("migration 27→28: idx_reminders_target: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_reminders_source_kind ON reminders(source, trigger_kind)`)
		if err != nil {
			return fmt.Errorf("migration 27→28: idx_reminders_source_kind: %w", err)
		}
	}

	// Migration 29→30: email_msg_seen (D-B1). v29 is a deliberate gap; the
	// startVersion < 30 guard covers any jump from earlier versions.
	if startVersion < 30 && endVersion >= 30 {
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS email_msg_seen (
				message_id      TEXT    PRIMARY KEY,
				from_daemon_id  TEXT,
				nonce           TEXT,
				processed_at    INTEGER NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("migration 29→30: create email_msg_seen: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_email_msg_seen_proc ON email_msg_seen(processed_at)`)
		if err != nil {
			return fmt.Errorf("migration 29→30: idx_email_msg_seen_proc: %w", err)
		}
	}

	// Migration 30→31: email_outbound_queue (D-B1).
	if startVersion < 31 && endVersion >= 31 {
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS email_outbound_queue (
				id              INTEGER PRIMARY KEY AUTOINCREMENT,
				from_agent      TEXT    NOT NULL,
				to_address      TEXT    NOT NULL,
				subject         TEXT,
				body            TEXT    NOT NULL,
				headers_json    TEXT    NOT NULL DEFAULT '{}',
				attempt_count   INTEGER NOT NULL DEFAULT 0,
				next_retry_at   INTEGER NOT NULL,
				last_error      TEXT,
				status          TEXT    NOT NULL,
				enqueued_at     INTEGER NOT NULL,
				updated_at      INTEGER NOT NULL
			)
		`)
		if err != nil {
			return fmt.Errorf("migration 30→31: create email_outbound_queue: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_email_queue_next ON email_outbound_queue(next_retry_at, status)`)
		if err != nil {
			return fmt.Errorf("migration 30→31: idx_email_queue_next: %w", err)
		}
	}

	// Migration 31→32: email_peer_rate_state (D-B1). Partial index per
	// canonical-ref §3.11 Guard 6.
	if startVersion < 32 && endVersion >= 32 {
		_, err = tx.Exec(`
			CREATE TABLE IF NOT EXISTS email_peer_rate_state (
				peer_key            TEXT    PRIMARY KEY,
				window_start_at     INTEGER NOT NULL,
				inbound_count       INTEGER NOT NULL DEFAULT 0,
				outbound_count      INTEGER NOT NULL DEFAULT 0,
				paused_at           INTEGER
			)
		`)
		if err != nil {
			return fmt.Errorf("migration 31→32: create email_peer_rate_state: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_peer_rate_paused ON email_peer_rate_state(paused_at) WHERE paused_at IS NOT NULL`)
		if err != nil {
			return fmt.Errorf("migration 31→32: idx_peer_rate_paused: %w", err)
		}
	}

	// Migrations 33-36 are forward-ported as DEAD-END DDL for v0.10.6 (v33/v34
	// from thrum-agents, v35/v36 from feature/b-b1-impl). Schema shape only —
	// no consumer code (s6os routing, memories handlers, lifecycle enforcement,
	// remediation handler) lands on the release line. Goal: a v0.10.6 binary can
	// open AND co-reside on a v36 DB touched by a v0.11 substrate binary.

	// Migration 32→33: Add pending_route_resolution column to messages table
	// (thrum-s6os E11). Flags messages whose author/scope references were
	// missing state files at ingest. Idempotent: columnSet-guarded (SQLite has
	// no ADD COLUMN IF NOT EXISTS). NOT NULL DEFAULT 0 auto-fills existing rows;
	// no backfill UPDATE. Dead-end on v0.10.6 (pending.Pool / ProjectionResolver
	// code stays out).
	if startVersion < 33 && endVersion >= 33 {
		hasMessages, err := tableExists(tx, "messages")
		if err != nil {
			return fmt.Errorf("migration 32→33: check messages table: %w", err)
		}
		if hasMessages {
			msgCols, err := columnSet(tx, "messages")
			if err != nil {
				return fmt.Errorf("migration 32→33: inspect messages: %w", err)
			}
			if !msgCols["pending_route_resolution"] {
				_, err = tx.Exec(`ALTER TABLE messages ADD COLUMN pending_route_resolution INTEGER NOT NULL DEFAULT 0`)
				if err != nil {
					return fmt.Errorf("migration 32→33: add pending_route_resolution column: %w", err)
				}
			}
		}
	}

	// Migration 33→34: E16 memories substrate. Adds memories + memory_scopes
	// tables and their six indexes. The lone FK (memory_scopes→memories ON
	// DELETE CASCADE) is self-contained (both new tables), so it creates fine
	// and is harmless empty/unused. No backfill. Dead-end on v0.10.6.
	if startVersion < 34 && endVersion >= 34 {
		stmts := []string{
			`CREATE TABLE IF NOT EXISTS memories (
				record_id   TEXT PRIMARY KEY,
				name        TEXT NOT NULL,
				description TEXT NOT NULL,
				body        TEXT NOT NULL,
				type        TEXT NOT NULL,
				author      TEXT NOT NULL,
				created_at  TEXT NOT NULL,
				updated_at  TEXT NOT NULL,
				deleted     INTEGER NOT NULL DEFAULT 0
			)`,
			`CREATE TABLE IF NOT EXISTS memory_scopes (
				record_id   TEXT NOT NULL,
				scope_type  TEXT NOT NULL,
				scope_value TEXT NOT NULL,
				PRIMARY KEY (record_id, scope_type, scope_value),
				FOREIGN KEY (record_id) REFERENCES memories(record_id) ON DELETE CASCADE
			)`,
			`CREATE INDEX IF NOT EXISTS idx_memories_name ON memories(name)`,
			`CREATE INDEX IF NOT EXISTS idx_memories_author ON memories(author)`,
			`CREATE INDEX IF NOT EXISTS idx_memories_type ON memories(type)`,
			`CREATE INDEX IF NOT EXISTS idx_memories_not_deleted ON memories(deleted) WHERE deleted = 0`,
			`CREATE INDEX IF NOT EXISTS idx_memories_updated_at ON memories(updated_at)`,
			`CREATE INDEX IF NOT EXISTS idx_memory_scopes_lookup ON memory_scopes(scope_type, scope_value)`,
		}
		for _, stmt := range stmts {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("migration 33→34: %w (stmt: %s)", err, stmt)
			}
		}
	}

	// Migration 34→35 (thrum-6qmf.17): add the event_kind CHECK to
	// agent_lifecycle_events. SQLite cannot ALTER ... ADD CHECK, so this is a
	// full table rebuild. The table has no foreign keys (in or out), so the
	// rebuild is safe inside this existing transaction. The _new table uses the
	// shared agentLifecycleEventsColumns const so it can't drift from
	// createTables; the explicit INSERT column list preserves id (PK) values.
	// On v0.10.6 the table always exists (migration 27 ≤ v32) so real upgrades
	// take the rebuild branch; the existence-guard covers bare/synthetic
	// fixtures that seed schema_version without the pre-version tables.
	if startVersion < 35 && endVersion >= 35 {
		var exists int
		if err = tx.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agent_lifecycle_events'`,
		).Scan(&exists); err != nil {
			return fmt.Errorf("migration 34→35: probe agent_lifecycle_events: %w", err)
		}
		if exists == 0 {
			if _, err = tx.Exec("CREATE TABLE IF NOT EXISTS agent_lifecycle_events (" + agentLifecycleEventsColumns + ")"); err != nil {
				return fmt.Errorf("migration 34→35: create agent_lifecycle_events: %w", err)
			}
		} else {
			if _, err = tx.Exec("CREATE TABLE agent_lifecycle_events_new (" + agentLifecycleEventsColumns + ")"); err != nil {
				return fmt.Errorf("migration 34→35: create rebuilt agent_lifecycle_events: %w", err)
			}
			if _, err = tx.Exec(`INSERT INTO agent_lifecycle_events_new
				(id, agent_name, event_kind, event_time, detection_method, reason, details)
				SELECT id, agent_name, event_kind, event_time, detection_method, reason, details
				FROM agent_lifecycle_events`); err != nil {
				return fmt.Errorf("migration 34→35: copy rows: %w", err)
			}
			if _, err = tx.Exec(`DROP TABLE agent_lifecycle_events`); err != nil {
				return fmt.Errorf("migration 34→35: drop old: %w", err)
			}
			if _, err = tx.Exec(`ALTER TABLE agent_lifecycle_events_new RENAME TO agent_lifecycle_events`); err != nil {
				return fmt.Errorf("migration 34→35: rename: %w", err)
			}
		}
		if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_agent_lifecycle_agent_time ON agent_lifecycle_events(agent_name, event_time)`); err != nil {
			return fmt.Errorf("migration 34→35: idx agent_time: %w", err)
		}
		if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_agent_lifecycle_kind ON agent_lifecycle_events(event_kind, event_time)`); err != nil {
			return fmt.Errorf("migration 34→35: idx kind: %w", err)
		}
	}

	// Migration 35→36 (thrum-sdzk): API-error auto-remediation per-agent state.
	// Additive new table — same DDL as the createTables fresh-install path via
	// the shared const (Guard-1 parity). Dead-end on v0.10.6.
	if startVersion < 36 && endVersion >= 36 {
		if _, err := tx.Exec(createAgentAPIErrorRemediationTable); err != nil {
			return fmt.Errorf("migration 35→36: %w", err)
		}
	}

	// Migration 36→37 (thrum-7ojv): back-port thrum-agents j7n5 Epic 0
	// memory-tables DDL as dead-end DDL (no consumer code on v0.10.6).
	// Verbatim copy from thrum-agents internal/schema/schema.go so
	// cross-binary co-residence works correctly: a thrum-agents binary
	// (which expects v37 = these tables) sharing a DB with a release-line
	// binary via .thrum/redirect sees identical schema content stamped
	// here. Without this back-port a fresh rc.3 install would create a
	// v38 DB with NO memory tables; a subsequently-installed thrum-agents
	// / v0.11 binary on that DB would crash on every memory.* operation.
	// With this back-port, both branches stamp v37 with byte-identical
	// DDL — no asymmetry, no crash trap. Tables are mirrored in
	// createTables / createIndexes above for fresh-install parity.
	if startVersion < 37 && endVersion >= 37 {
		stmts := []string{
			`CREATE TABLE IF NOT EXISTS memory_record (
				id                TEXT PRIMARY KEY,
				kind              TEXT NOT NULL,
				subkind           TEXT,
				title             TEXT NOT NULL,
				body_oneline      TEXT NOT NULL,
				body_short        TEXT,
				body_full         TEXT,
				agent_id          TEXT NOT NULL,
				created_at        TIMESTAMP NOT NULL,
				updated_at        TIMESTAMP NOT NULL,
				status            TEXT NOT NULL DEFAULT 'active',
				scope             TEXT NOT NULL DEFAULT 'project',
				parent_id         TEXT,
				source_session_id TEXT,
				metadata          TEXT,
				created_by        TEXT NOT NULL,
				last_edited_by    TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS memory_tag (
				memory_id  TEXT NOT NULL,
				tag        TEXT NOT NULL,
				PRIMARY KEY (memory_id, tag),
				FOREIGN KEY (memory_id) REFERENCES memory_record(id) ON DELETE CASCADE
			)`,
			`CREATE TABLE IF NOT EXISTS memory_edge (
				from_id    TEXT NOT NULL,
				edge_kind  TEXT NOT NULL,
				to_id      TEXT NOT NULL,
				created_at TIMESTAMP NOT NULL,
				PRIMARY KEY (from_id, edge_kind, to_id),
				FOREIGN KEY (from_id) REFERENCES memory_record(id) ON DELETE CASCADE,
				FOREIGN KEY (to_id) REFERENCES memory_record(id) ON DELETE CASCADE
			)`,
			`CREATE INDEX IF NOT EXISTS idx_memory_kind ON memory_record(kind)`,
			`CREATE INDEX IF NOT EXISTS idx_memory_agent ON memory_record(agent_id)`,
			`CREATE INDEX IF NOT EXISTS idx_memory_created ON memory_record(created_at)`,
			`CREATE INDEX IF NOT EXISTS idx_memory_updated ON memory_record(updated_at)`,
			`CREATE INDEX IF NOT EXISTS idx_memory_status ON memory_record(status)`,
			`CREATE INDEX IF NOT EXISTS idx_memory_scope ON memory_record(scope)`,
			`CREATE INDEX IF NOT EXISTS idx_memory_tag_tag ON memory_tag(tag)`,
			`CREATE INDEX IF NOT EXISTS idx_memory_edge_to ON memory_edge(to_id, edge_kind)`,
			`CREATE INDEX IF NOT EXISTS idx_memory_edge_kind ON memory_edge(edge_kind)`,
			// FTS5 SHADOW table (j7n5 Task 0.2). No content= clause;
			// projection handlers on thrum-agents maintain in lockstep.
			// Storage only on release line.
			`CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
				memory_id UNINDEXED,
				title, body_oneline, body_short, body_full
			)`,
			// memory_embeddings + memory_embed_queue (j7n5 Task 0.3) —
			// LOCAL-ONLY; never sync'd; populated by background embedding
			// worker on thrum-agents. See createTables comments for
			// PK/no-FK rationale. Storage only on release line.
			`CREATE TABLE IF NOT EXISTS memory_embeddings (
				memory_id    TEXT NOT NULL,
				zoom_level   TEXT NOT NULL,
				model        TEXT NOT NULL,
				embedded_at  TIMESTAMP NOT NULL,
				embed_status TEXT NOT NULL,
				vec          BLOB,
				PRIMARY KEY (memory_id, zoom_level, model),
				FOREIGN KEY (memory_id) REFERENCES memory_record(id) ON DELETE CASCADE
			)`,
			`CREATE TABLE IF NOT EXISTS memory_embed_queue (
				memory_id   TEXT NOT NULL,
				zoom_level  TEXT NOT NULL,
				enqueued_at TIMESTAMP NOT NULL,
				retry_count INTEGER NOT NULL DEFAULT 0,
				last_error  TEXT,
				PRIMARY KEY (memory_id, zoom_level)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_memory_embed_status ON memory_embeddings(embed_status)`,
		}
		for _, stmt := range stmts {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("migration 36→37: %w (stmt: %s)", err, stmt)
			}
		}
	}

	// Migration 37→38 (thrum-7ojv): add timestamp index to the events
	// table so the compactor's
	//   DELETE FROM events WHERE timestamp < ?
	// (internal/sync/compact/compact.go:101-107) runs O(log N) seek +
	// sequential range delete instead of O(N) full table scan.
	// Idempotent (CREATE INDEX IF NOT EXISTS) so a co-resident
	// thrum-agents binary that adds the same index in its own v38
	// migration won't error.
	//
	// tableExists guard mirrors the v18→v22 ALTER guards: partial-schema
	// test fixtures (those that start from very low versions like v17
	// and don't run createTables) may not have the events table yet.
	// Production always has it (events table is created in earlier
	// migrations and in createTables); the guard is purely a test
	// accommodation that keeps the production code path unchanged.
	if startVersion < 38 && endVersion >= 38 {
		hasEvents, hasErr := tableExists(tx, "events")
		if hasErr != nil {
			return fmt.Errorf("migration 37→38: check events table: %w", hasErr)
		}
		if hasEvents {
			if _, err := tx.Exec(
				`CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp)`,
			); err != nil {
				return fmt.Errorf("migration 37→38: %w", err)
			}
		}
	}

	// Migration from version 38 to 39: Add schedule column to monitors
	// (thrum-puhr.9). NULL/empty = continuous mode; cron expression =
	// scheduled mode (one-shot per fire). Idempotent: skip if column
	// already present so re-runs are safe.
	if startVersion < 39 && endVersion >= 39 {
		hasMonitors, hasErr := tableExists(tx, "monitors")
		if hasErr != nil {
			return fmt.Errorf("migration 38→39: check monitors table: %w", hasErr)
		}
		if hasMonitors {
			cols, colErr := columnSet(tx, "monitors")
			if colErr != nil {
				return fmt.Errorf("migration 38→39: read monitors columns: %w", colErr)
			}
			if !cols["schedule"] {
				if _, err := tx.Exec(
					`ALTER TABLE monitors ADD COLUMN schedule TEXT NOT NULL DEFAULT ''`,
				); err != nil {
					return fmt.Errorf("migration 38→39: %w", err)
				}
			}
		}
	}

	// ---------------------------------------------------------------------
	// v41–v51 dead-end DDL forward-port from thrum-agents (thrum-399av).
	// Strictly additive: ALTER ADD COLUMN (columnSet-guarded) + CREATE TABLE/
	// INDEX IF NOT EXISTS. No release-line code reads any new column/table.
	// v42/v43/v46 are no-op version markers with NO block (v42/v43 = read-state
	// backfill pair already collapsed into the v40 marker here; v46 = the
	// post-rebuild corrective the release line never wired). The two
	// feature-population backfills (v47 visibility_class, v48 addressed_via) are
	// STUBBED to no-ops: their source SQL lives in internal/visibility /
	// AddressedViaBackfillSQL (absent / out-of-scope here) and the column
	// defaults ('targeted' / 'unattributed') keep every row valid. Goal is
	// schema-on-disk parity, not feature behavior.
	// ---------------------------------------------------------------------

	// v41 (thrum-j9gh): agents.agent_pid_start_time — PID-reuse liveness guard.
	if startVersion < 41 && endVersion >= 41 {
		hasAgents, hasErr := tableExists(tx, "agents")
		if hasErr != nil {
			return fmt.Errorf("migration 40→41: check agents table: %w", hasErr)
		}
		if hasAgents {
			cols, colErr := columnSet(tx, "agents")
			if colErr != nil {
				return fmt.Errorf("migration 40→41: read agents columns: %w", colErr)
			}
			if !cols["agent_pid_start_time"] {
				if _, err := tx.Exec(
					`ALTER TABLE agents ADD COLUMN agent_pid_start_time TEXT NOT NULL DEFAULT ''`,
				); err != nil {
					return fmt.Errorf("migration 40→41: %w", err)
				}
			}
		}
	}

	// v44 (thrum-8zmu.2): permission_nudges.prompt_fingerprint.
	if startVersion < 44 && endVersion >= 44 {
		hasPN, hasErr := tableExists(tx, "permission_nudges")
		if hasErr != nil {
			return fmt.Errorf("migration 43→44: check permission_nudges table: %w", hasErr)
		}
		if hasPN {
			cols, colErr := columnSet(tx, "permission_nudges")
			if colErr != nil {
				return fmt.Errorf("migration 43→44: read permission_nudges columns: %w", colErr)
			}
			if !cols["prompt_fingerprint"] {
				if _, err := tx.Exec(
					`ALTER TABLE permission_nudges ADD COLUMN prompt_fingerprint TEXT NOT NULL DEFAULT ''`,
				); err != nil {
					return fmt.Errorf("migration 43→44: %w", err)
				}
			}
		}
	}

	// v45 (thrum-thdr.4): alert_deliveries dedup table + index.
	if startVersion < 45 && endVersion >= 45 {
		hasAD, hasErr := tableExists(tx, "alert_deliveries")
		if hasErr != nil {
			return fmt.Errorf("migration 44→45: check alert_deliveries table: %w", hasErr)
		}
		if !hasAD {
			if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS alert_deliveries (
				recipient_agent_id       TEXT NOT NULL,
				dedup_key                TEXT NOT NULL,
				suppressed_by_message_id TEXT NOT NULL,
				expires_at               TEXT NOT NULL,
				created_at               TEXT NOT NULL,
				PRIMARY KEY (recipient_agent_id, dedup_key)
			)`); err != nil {
				return fmt.Errorf("migration 44→45: create alert_deliveries: %w", err)
			}
			if _, err := tx.Exec(
				`CREATE INDEX IF NOT EXISTS idx_alert_deliveries_expires ON alert_deliveries(recipient_agent_id, expires_at)`,
			); err != nil {
				return fmt.Errorf("migration 44→45: create idx_alert_deliveries_expires: %w", err)
			}
		}
	}

	// v47 (thrum-01wy.6): messages.visibility_class + retarget_fill_order, plus
	// the idx_messages_time -> idx_messages_time_id keyset swap and the partial
	// visibility index. The visibility backfill (visibility.StampDeliveryRefsSQL
	// + UpdateClassSQL on thrum-agents) is STUBBED here: the internal/visibility
	// package is absent on the release line and the 'targeted' column default
	// keeps every pre-existing row valid for a binary that never reads the column.
	if startVersion < 47 && endVersion >= 47 {
		hasMessages, hasErr := tableExists(tx, "messages")
		if hasErr != nil {
			return fmt.Errorf("migration 46→47: check messages table: %w", hasErr)
		}
		if hasMessages {
			cols, colErr := columnSet(tx, "messages")
			if colErr != nil {
				return fmt.Errorf("migration 46→47: read messages columns: %w", colErr)
			}
			if !cols["visibility_class"] {
				if _, err := tx.Exec(`ALTER TABLE messages ADD COLUMN visibility_class TEXT NOT NULL DEFAULT 'targeted'`); err != nil {
					return fmt.Errorf("migration 46→47: add visibility_class: %w", err)
				}
			}
			if !cols["retarget_fill_order"] {
				if _, err := tx.Exec(`ALTER TABLE messages ADD COLUMN retarget_fill_order TEXT`); err != nil {
					return fmt.Errorf("migration 46→47: add retarget_fill_order: %w", err)
				}
			}
			// Backfill STUBBED (see block header) — defaults carry it.
			if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_visibility ON messages(visibility_class, created_at) WHERE visibility_class != 'targeted'`); err != nil {
				return fmt.Errorf("migration 46→47: create idx_messages_visibility: %w", err)
			}
			if _, err := tx.Exec(`DROP INDEX IF EXISTS idx_messages_time`); err != nil {
				return fmt.Errorf("migration 46→47: drop idx_messages_time: %w", err)
			}
			if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_time_id ON messages(created_at, message_id)`); err != nil {
				return fmt.Errorf("migration 46→47: create idx_messages_time_id: %w", err)
			}
		}
	}

	// v48 (thrum-qkgn3/momim): agents.phase, messages.priority,
	// message_deliveries.addressed_via + idx_deliveries_recipient_via. The
	// addressed_via evidence backfill (AddressedViaBackfillSQL on thrum-agents)
	// is STUBBED — the 'unattributed' default keeps rows valid for a binary that
	// never reads the column.
	if startVersion < 48 && endVersion >= 48 {
		hasAgents, aErr := tableExists(tx, "agents")
		if aErr != nil {
			return fmt.Errorf("migration 47→48: check agents table: %w", aErr)
		}
		if hasAgents {
			aCols, err2 := columnSet(tx, "agents")
			if err2 != nil {
				return fmt.Errorf("migration 47→48: read agents columns: %w", err2)
			}
			if !aCols["phase"] {
				if _, err := tx.Exec(`ALTER TABLE agents ADD COLUMN phase TEXT NOT NULL DEFAULT 'active'`); err != nil {
					return fmt.Errorf("migration 47→48: add agents.phase: %w", err)
				}
			}
		}
		hasMessages, mErr := tableExists(tx, "messages")
		if mErr != nil {
			return fmt.Errorf("migration 47→48: check messages table: %w", mErr)
		}
		if hasMessages {
			mCols, err2 := columnSet(tx, "messages")
			if err2 != nil {
				return fmt.Errorf("migration 47→48: read messages columns: %w", err2)
			}
			if !mCols["priority"] {
				if _, err := tx.Exec(`ALTER TABLE messages ADD COLUMN priority TEXT NOT NULL DEFAULT ''`); err != nil {
					return fmt.Errorf("migration 47→48: add messages.priority: %w", err)
				}
			}
		}
		hasDeliveries, dErr := tableExists(tx, "message_deliveries")
		if dErr != nil {
			return fmt.Errorf("migration 47→48: check message_deliveries table: %w", dErr)
		}
		if hasDeliveries {
			dCols, err2 := columnSet(tx, "message_deliveries")
			if err2 != nil {
				return fmt.Errorf("migration 47→48: read message_deliveries columns: %w", err2)
			}
			if !dCols["addressed_via"] {
				if _, err := tx.Exec(`ALTER TABLE message_deliveries ADD COLUMN addressed_via TEXT NOT NULL DEFAULT 'unattributed'`); err != nil {
					return fmt.Errorf("migration 47→48: add addressed_via: %w", err)
				}
			}
			// Backfill STUBBED (see block header).
			if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_deliveries_recipient_via ON message_deliveries(recipient_agent_id, addressed_via)`); err != nil {
				return fmt.Errorf("migration 47→48: create idx_deliveries_recipient_via: %w", err)
			}
		}
	}

	// v49 (Lane-B B.1): telegram_outbound_queue + index.
	if startVersion < 49 && endVersion >= 49 {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS telegram_outbound_queue (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id          INTEGER NOT NULL,
			content          TEXT    NOT NULL,
			reply_to_tele_id INTEGER,
			thrum_msg_id     TEXT    NOT NULL,
			attempt_count    INTEGER NOT NULL DEFAULT 0,
			next_retry_at    INTEGER NOT NULL,
			last_error       TEXT,
			status           TEXT    NOT NULL,
			enqueued_at      INTEGER NOT NULL,
			updated_at       INTEGER NOT NULL
		)`); err != nil {
			return fmt.Errorf("migration 48→49: create telegram_outbound_queue: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_tg_queue_next ON telegram_outbound_queue(next_retry_at, status)`); err != nil {
			return fmt.Errorf("migration 48→49: idx_tg_queue_next: %w", err)
		}
	}

	// v50 (Epic C): graph substrate tables + 5 indexes. Dead-end without graph/.
	if startVersion < 50 && endVersion >= 50 {
		stmts := []string{
			`CREATE TABLE IF NOT EXISTS node (
				id               TEXT PRIMARY KEY,
				kind             TEXT NOT NULL,
				title            TEXT NOT NULL,
				status           TEXT NOT NULL DEFAULT 'open',
				raw_status       TEXT NOT NULL DEFAULT 'open',
				priority         INTEGER,
				effective_labels TEXT NOT NULL DEFAULT '[]',
				metadata         TEXT,
				owner            TEXT,
				is_blocked       INTEGER NOT NULL DEFAULT 0,
				created_at       TEXT NOT NULL,
				updated_at       TEXT NOT NULL,
				created_by       TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS node_label (
				node_id  TEXT NOT NULL,
				label    TEXT NOT NULL,
				PRIMARY KEY (node_id, label),
				FOREIGN KEY (node_id) REFERENCES node(id) ON DELETE CASCADE
			)`,
			`CREATE TABLE IF NOT EXISTS edge (
				from_id    TEXT NOT NULL,
				type       TEXT NOT NULL,
				to_id      TEXT NOT NULL,
				created_at TEXT NOT NULL,
				created_by TEXT NOT NULL,
				metadata   TEXT,
				PRIMARY KEY (from_id, type, to_id),
				FOREIGN KEY (from_id) REFERENCES node(id) ON DELETE CASCADE,
				FOREIGN KEY (to_id) REFERENCES node(id) ON DELETE CASCADE
			)`,
			`CREATE TABLE IF NOT EXISTS node_comment (
				comment_id TEXT PRIMARY KEY,
				node_id    TEXT NOT NULL,
				author     TEXT NOT NULL,
				body       TEXT NOT NULL,
				created_at TEXT NOT NULL,
				FOREIGN KEY (node_id) REFERENCES node(id) ON DELETE CASCADE
			)`,
			`CREATE TABLE IF NOT EXISTS graph_blocked (
				node_id     TEXT PRIMARY KEY,
				blocked_by  TEXT NOT NULL DEFAULT '[]',
				computed_at TEXT NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS idx_node_ready ON node(kind, status, is_blocked)`,
			`CREATE INDEX IF NOT EXISTS idx_node_kind ON node(kind, status)`,
			`CREATE INDEX IF NOT EXISTS idx_edge_to ON edge(to_id, type)`,
			`CREATE INDEX IF NOT EXISTS idx_node_label_label ON node_label(label, node_id)`,
			`CREATE INDEX IF NOT EXISTS idx_node_comment_node ON node_comment(node_id, created_at)`,
		}
		for _, stmt := range stmts {
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("migration 49→50: %w", err)
			}
		}
	}

	// v51 (graph canary): memory_satellite. Dead-end without graph/.
	if startVersion < 51 && endVersion >= 51 {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS memory_satellite (
			node_id           TEXT PRIMARY KEY REFERENCES node(id) ON DELETE CASCADE,
			body_oneline      TEXT NOT NULL DEFAULT '',
			body_short        TEXT,
			body_full         TEXT,
			scope             TEXT NOT NULL DEFAULT 'project',
			source_session_id TEXT,
			agent_id          TEXT NOT NULL DEFAULT '',
			kind              TEXT NOT NULL DEFAULT '',
			subkind           TEXT NOT NULL DEFAULT '',
			last_edited_by    TEXT NOT NULL DEFAULT ''
		)`); err != nil {
			return fmt.Errorf("migration 50→51: create memory_satellite: %w", err)
		}
	}

	// Update schema version
	_, err = tx.Exec("UPDATE schema_version SET version = ?", endVersion)
	if err != nil {
		return fmt.Errorf("update schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// queueColumnSet returns the set of column names currently present on the
// tableExists reports whether a table of the given name exists. Used by
// migrations that ALTER or UPDATE a specific table so they stay idempotent
// when the partial test schemas don't create that table.
func tableExists(tx *sql.Tx, name string) (bool, error) {
	var dummy string
	err := tx.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
		name,
	).Scan(&dummy)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// columnSet returns the set of column names on a table via PRAGMA table_info.
// Used by migrations for idempotent ALTER TABLE ADD COLUMN since SQLite does
// not support IF NOT EXISTS on ADD COLUMN.
func columnSet(tx *sql.Tx, table string) (map[string]bool, error) {
	// PRAGMA doesn't support bind parameters; callers pass a trusted
	// table name (hardcoded at call sites), not user input.
	//nolint:gosec // trusted table name
	rows, err := tx.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// command_queue table. Used by the v18→v19 migration for idempotent ALTER TABLE
// ADD COLUMN (SQLite does not support IF NOT EXISTS on ADD COLUMN).
func queueColumnSet(tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.Query("PRAGMA table_info(command_queue)")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// backupFileOnce copies src to dst if src exists and dst does not.
// Returns nil if dst already exists or src does not exist; errors on copy failure.
// Same backup-once pattern as identity.backupConfigOnce.
func backupFileOnce(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil // backup already exists — don't overwrite pre-upgrade state
	}
	data, err := os.ReadFile(src) // #nosec G304 -- src is SQLite DB path (+ sidecars) derived from PRAGMA database_list
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // sidecar absent (e.g. no WAL) — nothing to back up
		}
		return fmt.Errorf("read for backup: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	log.Printf("[schema] backed up pre-migration file to %s", dst)
	return nil
}

// BackfillEventID backfills event_id fields for events in JSONL files that lack them.
// This is part of the v6→v7 migration. It generates deterministic event IDs based on
// the event's type, timestamp, and entity ID (message_id, thread_id, etc.).
// The deterministic generation ensures that running this migration multiple times
// produces the same event_ids. Works with both sharded and monolithic file structures.
func BackfillEventID(thrumDir string) error {
	// Collect all JSONL files to process
	var filesToProcess []string

	// Check for events.jsonl (sharded structure)
	eventsPath := filepath.Join(thrumDir, "events.jsonl")
	if _, err := os.Stat(eventsPath); err == nil {
		filesToProcess = append(filesToProcess, eventsPath)
	}

	// Check for messages/*.jsonl (sharded structure)
	messagesPattern := filepath.Join(thrumDir, "messages", "*.jsonl")
	messageFiles, err := filepath.Glob(messagesPattern)
	if err != nil {
		return fmt.Errorf("glob message files: %w", err)
	}
	filesToProcess = append(filesToProcess, messageFiles...)

	// Check for old monolithic messages.jsonl (pre-sharding)
	oldPath := filepath.Join(thrumDir, "messages.jsonl")
	if _, err := os.Stat(oldPath); err == nil && len(filesToProcess) == 0 {
		// Only process monolithic file if sharded structure doesn't exist
		filesToProcess = append(filesToProcess, oldPath)
	}

	if len(filesToProcess) == 0 {
		// No JSONL files to process
		return nil
	}

	// Process each file
	for _, filePath := range filesToProcess {
		if err := backfillEventIDInFile(filePath); err != nil {
			return fmt.Errorf("backfill %s: %w", filepath.Base(filePath), err)
		}
	}

	return nil
}

// backfillEventIDInFile backfills event_id in a single JSONL file.
func backfillEventIDInFile(filePath string) error {
	// Read all events
	file, err := os.Open(filePath) // #nosec G304 -- filePath is an internal JSONL file path from the .thrum directory
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer func() { _ = file.Close() }()

	var events []map[string]any
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB max line

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			// Skip malformed events
			continue
		}

		events = append(events, event)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}

	// Check if any events need backfilling
	needsBackfill := false
	for _, event := range events {
		eventID, _ := event["event_id"].(string)
		if eventID == "" {
			needsBackfill = true
			break
		}
	}

	if !needsBackfill {
		// All events already have event_id
		return nil
	}

	// Backfill missing event_ids
	for _, event := range events {
		eventID, _ := event["event_id"].(string)
		if eventID == "" {
			// Generate deterministic event_id
			deterministicID, err := generateDeterministicEventID(event)
			if err != nil {
				// If we can't generate ID, skip this event
				continue
			}
			event["event_id"] = deterministicID
		}

		// Add version field if missing
		if _, hasVersion := event["v"]; !hasVersion {
			event["v"] = 1
		}
	}

	// Write back to file atomically
	tmpPath := filePath + ".backfill.tmp"
	tmpFile, err := os.Create(tmpPath) // #nosec G304 -- tmpPath is filePath+".backfill.tmp", derived from internal JSONL path
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpPath) }() // Clean up on error

	encoder := json.NewEncoder(tmpFile)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			_ = tmpFile.Close()
			return fmt.Errorf("write event: %w", err)
		}
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, filePath); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}

// generateDeterministicEventID generates a deterministic event ID from an event's fields.
// Uses ULID format with a timestamp derived from the event's timestamp field and
// randomness derived from hashing the event's unique identifiers.
func generateDeterministicEventID(event map[string]any) (string, error) {
	eventType, _ := event["type"].(string)
	timestamp, _ := event["timestamp"].(string)

	if eventType == "" || timestamp == "" {
		return "", fmt.Errorf("missing type or timestamp")
	}

	// Extract entity ID based on event type
	var entityID string
	switch {
	case strings.HasPrefix(eventType, "message."):
		entityID, _ = event["message_id"].(string)
	case strings.HasPrefix(eventType, "agent."):
		if eventType == "agent.register" || eventType == "agent.update" {
			entityID, _ = event["agent_id"].(string)
		} else {
			// agent.session.start, agent.session.end
			entityID, _ = event["session_id"].(string)
		}
	default:
		// For unknown types, use timestamp as entity ID
		entityID = timestamp
	}

	// Create deterministic hash from type + timestamp + entityID
	hashInput := fmt.Sprintf("%s:%s:%s", eventType, timestamp, entityID)
	hash := sha256.Sum256([]byte(hashInput))
	hashHex := hex.EncodeToString(hash[:])

	// Parse timestamp to get ULID timestamp component
	var ts time.Time
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
		if parsed, err := time.Parse(layout, timestamp); err == nil {
			ts = parsed
			break
		}
	}
	if ts.IsZero() {
		// Fallback to current time if parsing fails
		ts = time.Now()
	}

	// Generate ULID with timestamp and deterministic randomness from hash
	entropy := strings.NewReader(hashHex)
	id, err := ulid.New(ulid.Timestamp(ts), entropy)
	if err != nil {
		return "", fmt.Errorf("generate ULID: %w", err)
	}

	return "evt_" + id.String(), nil
}

// MigrateJSONLSharding migrates from monolithic messages.jsonl to sharded structure.
// This is part of the v6→v7 migration. It splits the monolithic file into:
// - events.jsonl (non-message events: agent lifecycle, threads)
// - messages/{author}.jsonl (per-agent message files).
func MigrateJSONLSharding(thrumDir string) error {
	oldPath := filepath.Join(thrumDir, "messages.jsonl")

	// Check if old monolithic file exists
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		// No old file to migrate, we're starting fresh
		return nil
	}

	// Check if migration already done (events.jsonl or messages/ exists)
	eventsPath := filepath.Join(thrumDir, "events.jsonl")
	messagesDir := filepath.Join(thrumDir, "messages")
	if _, err := os.Stat(eventsPath); err == nil {
		// Migration already done
		return nil
	}
	if _, err := os.Stat(messagesDir); err == nil {
		// Migration already done
		return nil
	}

	// Read all events from monolithic file
	file, err := os.Open(oldPath) // #nosec G304 -- oldPath is the internal .thrum/messages.jsonl path
	if err != nil {
		return fmt.Errorf("open messages.jsonl: %w", err)
	}
	defer func() { _ = file.Close() }()

	var allEvents []map[string]any
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB max line

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			// Skip malformed events
			continue
		}

		allEvents = append(allEvents, event)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read messages.jsonl: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}

	totalEvents := len(allEvents)

	// Build a map of message_id → author for routing edit/delete events
	messageAuthors := make(map[string]string)
	for _, event := range allEvents {
		eventType, _ := event["type"].(string)
		if eventType == "message.create" {
			messageID, _ := event["message_id"].(string)
			agentID, _ := event["agent_id"].(string)
			if messageID != "" && agentID != "" {
				messageAuthors[messageID] = ExtractAgentName(agentID)
			}
		}
	}

	// Partition events
	var coreEvents []map[string]any
	perAgentMessages := make(map[string][]map[string]any)

	for _, event := range allEvents {
		eventType, _ := event["type"].(string)

		switch {
		case strings.HasPrefix(eventType, "message."):
			// Route to per-agent message file
			var agentName string

			if eventType == "message.create" {
				agentID, _ := event["agent_id"].(string)
				agentName = ExtractAgentName(agentID)
			} else {
				// message.edit or message.delete - look up original author
				messageID, _ := event["message_id"].(string)
				agentName = messageAuthors[messageID]
				if agentName == "" {
					// Fallback: if we can't find the author, skip this event
					// (shouldn't happen in practice)
					continue
				}
			}

			if agentName != "" {
				perAgentMessages[agentName] = append(perAgentMessages[agentName], event)
			}

		default:
			// Non-message events go to events.jsonl
			coreEvents = append(coreEvents, event)
		}
	}

	// Create messages/ directory
	if err := os.MkdirAll(messagesDir, 0750); err != nil {
		return fmt.Errorf("create messages directory: %w", err)
	}

	// Write events.jsonl
	if err := writeJSONLFile(eventsPath, coreEvents); err != nil {
		return fmt.Errorf("write events.jsonl: %w", err)
	}

	// Write per-agent message files
	for agentName, events := range perAgentMessages {
		agentPath := filepath.Join(messagesDir, agentName+".jsonl")
		if err := writeJSONLFile(agentPath, events); err != nil {
			return fmt.Errorf("write %s.jsonl: %w", agentName, err)
		}
	}

	// Verify event counts
	var writtenEvents int
	writtenEvents += len(coreEvents)
	for _, events := range perAgentMessages {
		writtenEvents += len(events)
	}

	if writtenEvents != totalEvents {
		return fmt.Errorf("event count mismatch: read %d, wrote %d", totalEvents, writtenEvents)
	}

	// Rename old file as backup
	backupPath := oldPath + ".v6.bak"
	if err := os.Rename(oldPath, backupPath); err != nil {
		return fmt.Errorf("backup old file: %w", err)
	}

	return nil
}

// ExtractAgentName extracts the agent name from an agent_id.
// Example: "agent:coordinator:1B9K" → "coordinator_1B9K".
func ExtractAgentName(agentID string) string {
	// Remove "agent:" prefix if present
	agentID = strings.TrimPrefix(agentID, "agent:")

	// Replace colons with underscores for filename safety
	return strings.ReplaceAll(agentID, ":", "_")
}

// writeJSONLFile writes events to a JSONL file atomically.
func writeJSONLFile(path string, events []map[string]any) error {
	tmpPath := path + ".tmp"
	tmpFile, err := os.Create(tmpPath) // #nosec G304 -- tmpPath derived from internal JSONL file path
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpPath) }() // Clean up on error

	encoder := json.NewEncoder(tmpFile)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			_ = tmpFile.Close()
			return fmt.Errorf("write event: %w", err)
		}
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}
