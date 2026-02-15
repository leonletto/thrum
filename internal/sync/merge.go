package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/jsonl"
)

// Merger handles fetching and merging JSONL files from the sync branch.
type Merger struct {
	repoPath  string
	syncDir   string // Worktree directory containing JSONL files
	localOnly bool   // when true, skip git fetch operations
}

// NewMerger creates a new Merger for the given repository path.
// When localOnly is true, Fetch() is a no-op.
func NewMerger(repoPath string, syncDir string, localOnly bool) *Merger {
	return &Merger{
		repoPath:  repoPath,
		syncDir:   syncDir,
		localOnly: localOnly,
	}
}

// MergeResult contains statistics about the merge operation.
type MergeResult struct {
	NewEvents       int               // Number of new events from remote
	LocalEvents     int               // Number of local-only events
	Duplicates      int               // Events that existed in both
	EventIDs        []string          // IDs of new events (for notifications)
	NewParsedEvents []json.RawMessage // Parsed new events (Phase 5 optimization)
}

// Event represents a generic event with common fields.
type Event struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	ID        string          `json:"-"` // Extracted ID (not in JSON)
	Raw       json.RawMessage `json:"-"` // Original raw JSON
}

// Fetch fetches the remote a-sync branch.
func (m *Merger) Fetch(ctx context.Context) error {
	if m.localOnly {
		return nil
	}

	// Check if remote exists
	output, err := safecmd.Git(ctx, m.syncDir, "remote")
	if err != nil {
		return fmt.Errorf("checking for remotes: %w", err)
	}

	remotes := strings.TrimSpace(string(output))
	if remotes == "" {
		// No remote configured, nothing to fetch
		return nil
	}

	// Fetch the a-sync branch from origin (network operation — use GitLong for 10s timeout)
	if _, err := safecmd.GitLong(ctx, m.syncDir, "fetch", "origin", SyncBranchName); err != nil {
		// Fetch failed - might be offline or branch doesn't exist on remote yet
		return nil //nolint:nilerr // intentionally ignore error for offline support
	}

	return nil
}

// MergeAll performs multi-file merge for the sharded JSONL layout.
// Merges events.jsonl and all messages/*.jsonl files.
//
// Uses git archive to batch-extract remote files when possible,
// falling back to per-file git show if git archive fails.
func (m *Merger) MergeAll(ctx context.Context) (*MergeResult, error) {
	totalStats := &MergeResult{}
	messagesDir := filepath.Join(m.syncDir, "messages")

	// Try batch extraction via git archive
	remoteTmpDir, archiveErr := m.extractRemoteFiles(ctx)
	if archiveErr == nil {
		defer func() {
			_ = os.RemoveAll(remoteTmpDir)
		}()
	}

	// 1. Merge events.jsonl
	eventsPath := filepath.Join(m.syncDir, "events.jsonl")
	var eventsStats *MergeResult
	var err error
	if archiveErr == nil {
		eventsStats, err = m.mergeFileFromDir(eventsPath, filepath.Join(remoteTmpDir, "events.jsonl"))
	} else {
		eventsStats, err = m.mergeFile(ctx, eventsPath, "events.jsonl")
	}
	if err != nil {
		return nil, fmt.Errorf("merge events.jsonl: %w", err)
	}
	m.accumulateStats(totalStats, eventsStats)

	// 2. List local message files
	localFiles, err := m.listLocalMessageFiles(messagesDir)
	if err != nil {
		return nil, fmt.Errorf("list local message files: %w", err)
	}

	// 3. List remote message files
	var remoteFiles map[string]bool
	if archiveErr == nil {
		// List from extracted directory
		remoteMsgDir := filepath.Join(remoteTmpDir, "messages")
		remoteFiles, err = m.listLocalMessageFiles(remoteMsgDir)
		if err != nil {
			remoteFiles = make(map[string]bool)
		}
	} else {
		remoteFiles, err = m.listRemoteMessageFiles(ctx)
		if err != nil {
			remoteFiles = make(map[string]bool)
		}
	}

	// 4. Merge files that exist in both local and remote
	for localFile := range localFiles {
		if remoteFiles[localFile] {
			localPath := filepath.Join(messagesDir, localFile)
			var stats *MergeResult
			if archiveErr == nil {
				remotePath := filepath.Join(remoteTmpDir, "messages", localFile)
				stats, err = m.mergeFileFromDir(localPath, remotePath)
			} else {
				remotePath := "messages/" + localFile
				stats, err = m.mergeFile(ctx, localPath, remotePath)
			}
			if err != nil {
				return nil, fmt.Errorf("merge %s: %w", localFile, err)
			}
			m.accumulateStats(totalStats, stats)
		}
	}

	// 5. Copy remote-only files to local
	for remoteFile := range remoteFiles {
		if !localFiles[remoteFile] {
			localPath := filepath.Join(messagesDir, remoteFile)
			if archiveErr == nil {
				// Copy from extracted temp directory
				remotePath := filepath.Join(remoteTmpDir, "messages", remoteFile)
				data, readErr := os.ReadFile(remotePath) //nolint:gosec // G304 - path from internal temp directory
				if readErr != nil {
					return nil, fmt.Errorf("read extracted remote file %s: %w", remoteFile, readErr)
				}
				if mkErr := os.MkdirAll(filepath.Dir(localPath), 0750); mkErr != nil {
					return nil, fmt.Errorf("create messages dir: %w", mkErr)
				}
				if writeErr := os.WriteFile(localPath, data, 0600); writeErr != nil {
					return nil, fmt.Errorf("write local file %s: %w", remoteFile, writeErr)
				}
			} else {
				remotePath := "messages/" + remoteFile
				if cpErr := m.copyRemoteFile(ctx, localPath, remotePath); cpErr != nil {
					return nil, fmt.Errorf("copy remote file %s: %w", remoteFile, cpErr)
				}
			}
			// Count events in copied file
			events, readErr := m.readEventsFromFile(localPath)
			if readErr == nil {
				totalStats.NewEvents += len(events)
				for _, event := range events {
					totalStats.EventIDs = append(totalStats.EventIDs, event.ID)
					totalStats.NewParsedEvents = append(totalStats.NewParsedEvents, event.Raw)
				}
			}
		}
	}

	// 6. Local-only files are kept as-is (will be pushed)

	return totalStats, nil
}

// extractRemoteFiles batch-extracts remote files from the sync branch
// using git archive + tar. Returns the temp directory path containing the
// extracted files. The caller must clean up the temp directory.
func (m *Merger) extractRemoteFiles(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "thrum-merge-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}

	// Use git archive to extract all remote thrum files in a single command.
	// This is significantly faster than multiple git show calls.
	// NOTE: Files are at root level on a-sync branch (no .thrum/ prefix)
	//nolint:gosec // arguments are not user-controlled
	gitCmd := exec.CommandContext(ctx, "git", "archive", "origin/"+SyncBranchName, "--", "messages/", "events.jsonl")
	gitCmd.Dir = m.syncDir

	tarCmd := exec.CommandContext(ctx, "tar", "-xf", "-", "-C", tmpDir) //nolint:gosec // tmpDir from os.MkdirTemp
	tarCmd.Stdin, err = gitCmd.StdoutPipe()
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("create pipe: %w", err)
	}

	if err := tarCmd.Start(); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("start tar: %w", err)
	}

	if err := gitCmd.Run(); err != nil {
		// Kill tar since git failed
		_ = tarCmd.Process.Kill()
		_ = tarCmd.Wait()
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("git archive: %w", err)
	}

	if err := tarCmd.Wait(); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("tar extract: %w", err)
	}

	return tmpDir, nil
}

// mergeFileFromDir merges a single JSONL file using a local remote file
// (extracted from git archive) instead of git show.
func (m *Merger) mergeFileFromDir(localPath, remoteFilePath string) (*MergeResult, error) {
	// Read local events
	localEvents, err := m.readEventsFromFile(localPath)
	if err != nil {
		localEvents = []*Event{}
	}

	// Read remote events from extracted file
	remoteEvents, err := m.readEventsFromFile(remoteFilePath)
	if err != nil {
		remoteEvents = []*Event{}
	}

	if len(localEvents) == 0 && len(remoteEvents) == 0 {
		return &MergeResult{}, nil
	}

	merged, stats := m.mergeEvents(localEvents, remoteEvents)

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Timestamp < merged[j].Timestamp
	})

	if err := m.writeEventsToFile(localPath, merged); err != nil {
		return nil, fmt.Errorf("write merged events: %w", err)
	}

	return stats, nil
}

// accumulateStats adds stats from a single file merge into the total.
func (m *Merger) accumulateStats(total, stats *MergeResult) {
	total.NewEvents += stats.NewEvents
	total.LocalEvents += stats.LocalEvents
	total.Duplicates += stats.Duplicates
	total.EventIDs = append(total.EventIDs, stats.EventIDs...)
	total.NewParsedEvents = append(total.NewParsedEvents, stats.NewParsedEvents...)
}

// mergeFile merges a single JSONL file (local vs remote).
func (m *Merger) mergeFile(ctx context.Context, localPath, remotePath string) (*MergeResult, error) {
	// Read local events
	localEvents, err := m.readEventsFromFile(localPath)
	if err != nil {
		// File might not exist locally yet
		localEvents = []*Event{}
	}

	// Read remote events
	remoteEvents, err := m.readRemoteFile(ctx, remotePath)
	if err != nil {
		// File might not exist on remote yet
		remoteEvents = []*Event{}
	}

	// If both are empty, nothing to do
	if len(localEvents) == 0 && len(remoteEvents) == 0 {
		return &MergeResult{}, nil
	}

	// Perform union merge by event ID
	merged, stats := m.mergeEvents(localEvents, remoteEvents)

	// Sort by timestamp
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Timestamp < merged[j].Timestamp
	})

	// Write merged result
	if err := m.writeEventsToFile(localPath, merged); err != nil {
		return nil, fmt.Errorf("write merged events: %w", err)
	}

	return stats, nil
}

// listLocalMessageFiles lists all .jsonl files in the messages directory.
func (m *Merger) listLocalMessageFiles(messagesDir string) (map[string]bool, error) {
	files := make(map[string]bool)

	// Check if directory exists
	if _, err := os.Stat(messagesDir); os.IsNotExist(err) {
		// Directory doesn't exist yet
		return files, nil
	}

	entries, err := os.ReadDir(messagesDir)
	if err != nil {
		return nil, fmt.Errorf("read messages directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			files[entry.Name()] = true
		}
	}

	return files, nil
}

// listRemoteMessageFiles lists all .jsonl files in the remote messages directory.
func (m *Merger) listRemoteMessageFiles(ctx context.Context) (map[string]bool, error) {
	files := make(map[string]bool)

	// Use git ls-tree to list remote files
	output, err := safecmd.Git(ctx, m.syncDir, "ls-tree", "--name-only", "origin/"+SyncBranchName, "messages/")
	if err != nil {
		// Remote branch or directory doesn't exist yet
		return files, fmt.Errorf("list remote files: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Extract filename from path (messages/foo.jsonl -> foo.jsonl)
		filename := filepath.Base(line)
		if strings.HasSuffix(filename, ".jsonl") {
			files[filename] = true
		}
	}

	return files, nil
}

// copyRemoteFile copies a file from the remote a-sync branch to local.
func (m *Merger) copyRemoteFile(ctx context.Context, localPath, remotePath string) error {
	// Read remote file content
	output, err := safecmd.Git(ctx, m.syncDir, "show", "origin/"+SyncBranchName+":"+remotePath)
	if err != nil {
		return fmt.Errorf("read remote file: %w", err)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(localPath), 0750); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}

	// Write to local file
	if err := os.WriteFile(localPath, output, 0600); err != nil {
		return fmt.Errorf("write local file: %w", err)
	}

	return nil
}

// readEventsFromFile reads events from a local JSONL file.
func (m *Merger) readEventsFromFile(path string) ([]*Event, error) {
	reader, err := jsonl.NewReader(path)
	if err != nil {
		return nil, err
	}

	messages, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	return m.parseEvents(messages)
}

// readRemoteFile reads events from a remote JSONL file.
func (m *Merger) readRemoteFile(ctx context.Context, remotePath string) ([]*Event, error) {
	output, err := safecmd.Git(ctx, m.syncDir, "show", "origin/"+SyncBranchName+":"+remotePath)
	if err != nil {
		return nil, fmt.Errorf("read remote file: %w", err)
	}

	// Parse JSONL from output
	lines := strings.Split(string(output), "\n")
	var messages []json.RawMessage
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		messages = append(messages, json.RawMessage(line))
	}

	return m.parseEvents(messages)
}

// writeEventsToFile writes events to a local JSONL file.
func (m *Merger) writeEventsToFile(path string, events []*Event) error {
	// Create temporary file for atomic write
	tmpPath := path + ".merge.tmp"
	f, err := os.Create(tmpPath) //nolint:gosec // G304 - path from internal JSONL config
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		_ = os.Remove(tmpPath) // Best-effort cleanup on error
	}()

	// Write all events as JSONL
	encoder := json.NewEncoder(f)
	for _, event := range events {
		var obj map[string]any
		if err := json.Unmarshal(event.Raw, &obj); err != nil {
			_ = f.Close()
			return fmt.Errorf("unmarshal event %s: %w", event.ID, err)
		}

		if err := encoder.Encode(obj); err != nil {
			_ = f.Close()
			return fmt.Errorf("write event %s: %w", event.ID, err)
		}
	}

	// Sync to disk before rename
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync file: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename file: %w", err)
	}

	return nil
}

// parseEvents parses raw JSON messages into Event structs.
func (m *Merger) parseEvents(messages []json.RawMessage) ([]*Event, error) {
	events := make([]*Event, 0, len(messages))

	for _, msg := range messages {
		var base struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
		}

		if err := json.Unmarshal(msg, &base); err != nil {
			// Skip malformed events
			continue
		}

		// Extract event ID based on type
		id, err := extractEventID(msg, base.Type)
		if err != nil {
			// Skip events without IDs
			continue
		}

		events = append(events, &Event{
			Type:      base.Type,
			Timestamp: base.Timestamp,
			ID:        id,
			Raw:       msg,
		})
	}

	return events, nil
}

// extractEventID extracts the unique ID from an event.
// All events now have an event_id field (ULID) for deduplication.
// This fixes the bug where message.create, message.edit, and message.delete
// with the same message_id would collide in the dedup map.
func extractEventID(data json.RawMessage, eventType string) (string, error) {
	// Parse event to get event_id field
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return "", err
	}

	// Extract event_id (universal dedup key)
	eventID, ok := obj["event_id"].(string)
	if !ok || eventID == "" {
		return "", fmt.Errorf("missing or invalid event_id field for %s", eventType)
	}

	return eventID, nil
}

// mergeEvents performs a union merge of local and remote events by ID.
func (m *Merger) mergeEvents(local, remote []*Event) ([]*Event, *MergeResult) {
	// Build map of all events by ID
	eventMap := make(map[string]*Event)
	localIDs := make(map[string]bool)

	// Add local events
	for _, event := range local {
		eventMap[event.ID] = event
		localIDs[event.ID] = true
	}

	stats := &MergeResult{}
	newEventIDs := []string{}
	newParsedEvents := []json.RawMessage{}

	// Merge remote events
	for _, event := range remote {
		if _, exists := eventMap[event.ID]; exists {
			// Event exists in both local and remote
			stats.Duplicates++
		} else {
			// New event from remote
			eventMap[event.ID] = event
			stats.NewEvents++
			newEventIDs = append(newEventIDs, event.ID)
			newParsedEvents = append(newParsedEvents, event.Raw)
		}
	}

	// Count local-only events
	for id := range eventMap {
		if localIDs[id] && !contains(remote, id) {
			stats.LocalEvents++
		}
	}

	stats.EventIDs = newEventIDs
	stats.NewParsedEvents = newParsedEvents

	// Convert map back to slice
	merged := make([]*Event, 0, len(eventMap))
	for _, event := range eventMap {
		merged = append(merged, event)
	}

	return merged, stats
}

// contains checks if the event slice contains an event with the given ID.
func contains(events []*Event, id string) bool {
	for _, event := range events {
		if event.ID == id {
			return true
		}
	}
	return false
}
