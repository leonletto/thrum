package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/leonletto/thrum/internal/identity"
	"github.com/leonletto/thrum/internal/jsonl"
	"github.com/leonletto/thrum/internal/projection"
	"github.com/leonletto/thrum/internal/schema"
	_ "modernc.org/sqlite"
)

// State manages the daemon's persistent state (JSONL log and SQLite projection).
type State struct {
	eventsWriter   *jsonl.Writer            // Writer for events.jsonl (non-message events)
	messageWriters map[string]*jsonl.Writer // Writers for messages/{agent}.jsonl (keyed by agent name)
	writersMu      sync.Mutex               // Protects messageWriters map
	db             *sql.DB
	projector      *projection.Projector
	repoID         string
	daemonID       string       // Unique identifier for this daemon instance (for sync origin tracking)
	sequence       atomic.Int64 // Monotonically increasing event sequence counter
	repoPath       string       // Path to the repository root
	thrumDir       string       // Path to .thrum directory (runtime: var/, identities/)
	syncDir        string       // Path to sync worktree (JSONL data on a-sync branch)
	mu             sync.RWMutex // Protects agent/session operations
}

// NewState creates a new state manager for the given .thrum directory.
func NewState(thrumDir string, syncDir string, repoID string) (*State, error) {
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

	// Create events writer for events.jsonl (core non-message events)
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	eventsWriter, err := jsonl.NewWriter(eventsPath)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create events writer: %w", err)
	}

	// Ensure messages directory exists for per-agent message files
	messagesDir := filepath.Join(syncDir, "messages")
	if err := os.MkdirAll(messagesDir, 0750); err != nil {
		_ = eventsWriter.Close()
		_ = db.Close()
		return nil, fmt.Errorf("create messages directory: %w", err)
	}

	// Create projector
	projector := projection.NewProjector(db)

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

	s := &State{
		eventsWriter:   eventsWriter,
		messageWriters: make(map[string]*jsonl.Writer),
		db:             db,
		projector:      projector,
		repoID:         repoID,
		daemonID:       identity.GenerateDaemonID(),
		repoPath:       repoPath,
		thrumDir:       thrumDir,
		syncDir:        syncDir,
	}
	s.sequence.Store(maxSeq)

	return s, nil
}

// Close closes the state manager and its resources.
func (s *State) Close() error {
	var errs []error

	if err := s.eventsWriter.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close events writer: %w", err))
	}

	s.writersMu.Lock()
	for agentName, writer := range s.messageWriters {
		if err := writer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close message writer for %s: %w", agentName, err))
		}
	}
	s.writersMu.Unlock()

	if err := s.db.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close database: %w", err))
	}

	return errors.Join(errs...)
}

// WriteEvent writes an event to both JSONL and SQLite.
// Automatically generates and adds event_id (ULID) and version fields.
func (s *State) WriteEvent(event any) error {
	// Marshal event to map so we can add fields
	eventBytes, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	var eventMap map[string]any
	if err := json.Unmarshal(eventBytes, &eventMap); err != nil {
		return fmt.Errorf("unmarshal to map: %w", err)
	}

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

	// Route event to appropriate JSONL file based on type
	eventType, _ := eventMap["type"].(string)
	var writer *jsonl.Writer

	switch {
	case strings.HasPrefix(eventType, "message."):
		// Message events go to per-agent message files
		agentName, err := s.resolveAgentForMessage(eventMap)
		if err != nil {
			return fmt.Errorf("resolve agent for message event: %w", err)
		}
		writer, err = s.getOrCreateMessageWriter(agentName)
		if err != nil {
			return fmt.Errorf("get message writer: %w", err)
		}
	default:
		// All other events go to events.jsonl
		writer = s.eventsWriter
	}

	// Assign next sequence number
	seq := s.sequence.Add(1)
	eventMap["sequence"] = seq

	// Append enriched event to JSONL (source of truth)
	if err := writer.Append(eventMap); err != nil {
		return fmt.Errorf("append to JSONL: %w", err)
	}

	// Marshal enriched event for projector
	eventJSON, err := json.Marshal(eventMap)
	if err != nil {
		return fmt.Errorf("marshal enriched event: %w", err)
	}

	// Insert into events table for sequence-based queries
	evtID, _ := eventMap["event_id"].(string)
	evtType, _ := eventMap["type"].(string)
	evtTimestamp, _ := eventMap["timestamp"].(string)
	evtOrigin, _ := eventMap["origin_daemon"].(string)
	_, err = s.db.Exec(
		`INSERT OR IGNORE INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json) VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, seq, evtType, evtTimestamp, evtOrigin, string(eventJSON),
	)
	if err != nil {
		return fmt.Errorf("insert into events table: %w", err)
	}

	// Apply to projector (update SQLite)
	if err := s.projector.Apply(eventJSON); err != nil {
		return fmt.Errorf("apply to projector: %w", err)
	}

	return nil
}

// resolveAgentForMessage determines which agent file a message event should be routed to.
// For message.create: extracts agent name from the event's agent_id field.
// For message.edit/delete: looks up the original message's author from SQLite.
func (s *State) resolveAgentForMessage(event map[string]any) (string, error) {
	eventType, _ := event["type"].(string)

	switch eventType {
	case "message.create":
		// For creates, agent_id is in the event
		agentID, ok := event["agent_id"].(string)
		if !ok || agentID == "" {
			return "", fmt.Errorf("message.create event missing agent_id")
		}
		return agentIDToName(agentID), nil

	case "message.edit", "message.delete":
		// For edits/deletes, look up the original author from SQLite
		messageID, ok := event["message_id"].(string)
		if !ok || messageID == "" {
			return "", fmt.Errorf("%s event missing message_id", eventType)
		}

		// Query the messages table for the original author
		var agentID string
		query := `SELECT agent_id FROM messages WHERE message_id = ?`
		err := s.db.QueryRow(query, messageID).Scan(&agentID)
		if err != nil {
			return "", fmt.Errorf("lookup original author for %s: %w", messageID, err)
		}

		return agentIDToName(agentID), nil

	default:
		return "", fmt.Errorf("unexpected message event type: %s", eventType)
	}
}

// agentIDToName extracts the agent name from an agent ID.
// Uses the centralized identity.AgentIDToName function for consistency.
func agentIDToName(agentID string) string {
	return identity.AgentIDToName(agentID)
}

// getOrCreateMessageWriter returns a JSONL writer for the given agent's message file.
// Creates a new writer if one doesn't exist. Thread-safe.
func (s *State) getOrCreateMessageWriter(agentName string) (*jsonl.Writer, error) {
	s.writersMu.Lock()
	defer s.writersMu.Unlock()

	// Check if writer already exists
	if writer, exists := s.messageWriters[agentName]; exists {
		return writer, nil
	}

	// Create new writer for this agent
	messagePath := filepath.Join(s.syncDir, "messages", agentName+".jsonl")
	writer, err := jsonl.NewWriter(messagePath)
	if err != nil {
		return nil, fmt.Errorf("create message writer for %s: %w", agentName, err)
	}

	// Cache the writer
	s.messageWriters[agentName] = writer
	return writer, nil
}

// DB returns the SQLite database connection for queries.
func (s *State) DB() *sql.DB {
	return s.db
}

// RepoID returns the repository ID.
func (s *State) RepoID() string {
	return s.repoID
}

// DaemonID returns the daemon's unique identifier for sync origin tracking.
func (s *State) DaemonID() string {
	return s.daemonID
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
