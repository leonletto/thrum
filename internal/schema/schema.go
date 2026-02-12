package schema

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// CurrentVersion is the current schema version.
const CurrentVersion = 11

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
			disclosed    INTEGER DEFAULT 0
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

		// Agents table
		`CREATE TABLE IF NOT EXISTS agents (
			agent_id   TEXT PRIMARY KEY,
			kind       TEXT NOT NULL,
			role       TEXT NOT NULL,
			module     TEXT NOT NULL,
			display    TEXT,
			hostname   TEXT,
			registered_at TEXT NOT NULL,
			last_seen_at TEXT
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

		// Message reads table (per-session read tracking, local-only, no git sync)
		`CREATE TABLE IF NOT EXISTS message_reads (
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			agent_id   TEXT NOT NULL,
			read_at    TEXT NOT NULL,
			PRIMARY KEY (message_id, session_id),
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
		"CREATE INDEX IF NOT EXISTS idx_messages_time ON messages(created_at)",
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

		// Group indexes
		"CREATE INDEX IF NOT EXISTS idx_groups_name ON groups(name)",
		"CREATE INDEX IF NOT EXISTS idx_group_members_group ON group_members(group_id)",
		"CREATE INDEX IF NOT EXISTS idx_group_members_lookup ON group_members(member_type, member_value)",

		// Events table indexes (for sync)
		"CREATE INDEX IF NOT EXISTS idx_events_sequence ON events(sequence)",
		"CREATE INDEX IF NOT EXISTS idx_events_type ON events(type)",
		"CREATE INDEX IF NOT EXISTS idx_events_origin ON events(origin_daemon)",

		// Work contexts indexes
		"CREATE INDEX IF NOT EXISTS idx_work_contexts_agent ON agent_work_contexts(agent_id)",
		"CREATE INDEX IF NOT EXISTS idx_work_contexts_branch ON agent_work_contexts(branch)",
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

	// Run migrations
	if currentVersion < CurrentVersion {
		if err := runMigrations(db, currentVersion, CurrentVersion); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
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
		// Create message_reads table
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
	file, err := os.Open(filePath) //nolint:gosec // G304 - path from internal JSONL config
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
	tmpFile, err := os.Create(tmpPath) //nolint:gosec // G304 - path from internal JSONL config
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
	case eventType == "thread.create":
		entityID, _ = event["thread_id"].(string)
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
	file, err := os.Open(oldPath) //nolint:gosec // G304 - path from internal JSONL config
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
	tmpFile, err := os.Create(tmpPath) //nolint:gosec // G304 - path from internal JSONL config
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
