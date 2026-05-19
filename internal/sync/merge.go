package sync

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/daemon/safedb"
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
// Merges the v0.10.6 paths (state/, messages-v2/, receipts/) AND
// the legacy paths (events.jsonl, messages/*.jsonl) for read-fallback
// support (spec §4.6 — legacy-read horizon is indefinite).
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

	// 1. Legacy path: merge events.jsonl (kept for read-fallback — spec §4.6).
	// New code does NOT write to this path; old peers may still write it.
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

	// 2. Legacy path: merge messages/*.jsonl files (kept for read-fallback — spec §4.6).
	// New code does NOT write to this directory; old peers may still write here.
	// List local message files
	localFiles, err := m.listLocalMessageFiles(messagesDir)
	if err != nil {
		return nil, fmt.Errorf("list local message files: %w", err)
	}

	// List remote message files
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

	// Merge files that exist in both local and remote
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

	// Copy remote-only legacy message files to local
	for remoteFile := range remoteFiles {
		if !localFiles[remoteFile] {
			localPath := filepath.Join(messagesDir, remoteFile)
			if archiveErr == nil {
				// Copy from extracted temp directory
				remotePath := filepath.Join(remoteTmpDir, "messages", remoteFile)
				data, readErr := os.ReadFile(remotePath) // #nosec G304 -- path constructed from internal temp directory during sync
				if readErr != nil {
					return nil, fmt.Errorf("read extracted remote file %s: %w", remoteFile, readErr)
				}
				if mkErr := os.MkdirAll(filepath.Dir(localPath), 0750); mkErr != nil {
					return nil, fmt.Errorf("create messages dir: %w", mkErr)
				}
				if writeErr := os.WriteFile(localPath, data, 0600); writeErr != nil { // #nosec G703 -- localPath is constructed from internal temp directory during sync; not user-controlled
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

	// 3. v0.10.6 paths: merge state/agents/*.json and state/bridge-groups/*.json.
	// Per-file last-writer-wins: each daemon owns its own files, so cross-daemon
	// writes never happen in a well-behaved cluster (spec §6.5).
	for _, stateSubDir := range []string{"state/agents", "state/bridge-groups"} {
		localStateDir := filepath.Join(m.syncDir, stateSubDir)
		if mergeErr := m.mergeJSONDir(ctx, localStateDir, stateSubDir, remoteTmpDir, archiveErr); mergeErr != nil {
			return nil, fmt.Errorf("merge %s: %w", stateSubDir, mergeErr)
		}
	}

	// 4. v0.10.6 paths: merge messages-v2/*.jsonl with union driver (spec §6.5).
	messagesV2Dir := filepath.Join(m.syncDir, "messages-v2")
	localV2Files, err := m.listLocalMessageFiles(messagesV2Dir)
	if err != nil {
		return nil, fmt.Errorf("list local messages-v2 files: %w", err)
	}
	var remoteV2Files map[string]bool
	if archiveErr == nil {
		remoteV2Dir := filepath.Join(remoteTmpDir, "messages-v2")
		remoteV2Files, err = m.listLocalMessageFiles(remoteV2Dir)
		if err != nil {
			remoteV2Files = make(map[string]bool)
		}
	} else {
		remoteV2Files, err = m.listRemoteDirFiles(ctx, "messages-v2")
		if err != nil {
			remoteV2Files = make(map[string]bool)
		}
	}
	if mergeErr := m.mergeJSONLDir(ctx, messagesV2Dir, "messages-v2", localV2Files, remoteV2Files, remoteTmpDir, archiveErr, totalStats); mergeErr != nil {
		return nil, fmt.Errorf("merge messages-v2: %w", mergeErr)
	}

	// 5. v0.10.6 paths: merge receipts/*.jsonl with union driver (spec §6.5).
	receiptsDir := filepath.Join(m.syncDir, "receipts")
	localReceiptFiles, err := m.listLocalMessageFiles(receiptsDir)
	if err != nil {
		return nil, fmt.Errorf("list local receipts files: %w", err)
	}
	var remoteReceiptFiles map[string]bool
	if archiveErr == nil {
		remoteReceiptsDir := filepath.Join(remoteTmpDir, "receipts")
		remoteReceiptFiles, err = m.listLocalMessageFiles(remoteReceiptsDir)
		if err != nil {
			remoteReceiptFiles = make(map[string]bool)
		}
	} else {
		remoteReceiptFiles, err = m.listRemoteDirFiles(ctx, "receipts")
		if err != nil {
			remoteReceiptFiles = make(map[string]bool)
		}
	}
	if mergeErr := m.mergeJSONLDir(ctx, receiptsDir, "receipts", localReceiptFiles, remoteReceiptFiles, remoteTmpDir, archiveErr, totalStats); mergeErr != nil {
		return nil, fmt.Errorf("merge receipts: %w", mergeErr)
	}

	// 6. Local-only files are kept as-is (will be pushed)

	// 7. thrum-ychn: reset the local a-sync branch pointer to origin/a-sync
	// so the next commit is fast-forward-able on push. MergeAll has already
	// written deduped content into the working tree; --mixed preserves the
	// working tree while moving HEAD (→ origin tip) and clearing the index
	// (→ next stageChanges re-stages the merged content on top of the
	// remote's history).
	//
	// Called from two paths, both intentionally:
	//   (a) doSync pipeline: Fetch → MergeAll → CommitAndPush. The reset
	//       here is the primary fix; it makes the first commit FF-able.
	//   (b) CommitAndPush's internal rejection-retry at push.go:93 calls
	//       MergeAll again after Fetch. The reset here ratchets HEAD
	//       forward to whichever tip the retry's Fetch just pulled,
	//       which is exactly what the retry needs to converge.
	//
	// Silent skip on any error: localOnly mode, missing origin remote,
	// and first-sync-before-remote-branch-exists all naturally produce
	// non-fatal failures here. Matches the pattern at merge.go:70-72 for
	// Fetch errors. If the reset doesn't happen, the worst case is the
	// existing pre-fix behavior (push rejected, caller handles).
	//
	// Race-window note: the rev-parse + reset is not atomic. A concurrent
	// git fetch in another goroutine could advance origin/a-sync between
	// the two calls. That is benign — `git reset --mixed <ref>` is atomic
	// from git's perspective (HEAD file + index update succeed together or
	// neither happens), so a newer tip just means the reset lands on a
	// fresher origin pointer. The next commit is still FF-able.
	if !m.localOnly {
		if _, err := safecmd.Git(ctx, m.syncDir, "rev-parse", "--verify", "origin/"+SyncBranchName); err == nil {
			_, _ = safecmd.Git(ctx, m.syncDir, "reset", "--mixed", "origin/"+SyncBranchName)
		}
	}

	return totalStats, nil
}

// extractRemoteFiles batch-extracts remote files from the sync branch
// using git archive + tar. Returns the temp directory path containing the
// extracted files. The caller must clean up the temp directory.
//
// Extracts both legacy paths (messages/, events.jsonl) and v0.10.6 paths
// (state/, messages-v2/, receipts/) so MergeAll can handle mixed-version peers.
func (m *Merger) extractRemoteFiles(ctx context.Context) (string, error) {
	tmpDir, err := os.MkdirTemp("", "thrum-merge-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}

	// Use git archive to extract all remote thrum files in a single command.
	// This is significantly faster than multiple git show calls.
	// NOTE: Files are at root level on a-sync branch (no .thrum/ prefix)
	//
	// Raw exec.Command is intentional here: safecmd.Git returns CombinedOutput(),
	// but we need a streaming Stdout to pipe directly into `tar -xf -` without
	// buffering the entire archive in memory. safecmd does not (yet) provide
	// a streaming wrapper, and adding one solely for this one pipe-pattern
	// call site isn't justified.
	//
	// Include both legacy paths AND v0.10.6 paths so old + new peers both work.
	// git archive silently skips paths that don't exist in the remote tree, so
	// listing new paths here is safe against old peers.
	//nolint:gosec // arguments are not user-controlled
	gitCmd := exec.CommandContext(ctx, "git", "archive", "origin/"+SyncBranchName, "--",
		"messages/", "events.jsonl",
		"state/", "messages-v2/", "receipts/",
	)
	gitCmd.Dir = m.syncDir

	tarCmd := exec.CommandContext(ctx, "tar", "-xf", "-", "-C", tmpDir) // #nosec G204 -- tmpDir from os.MkdirTemp, not user input
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

// listRemoteDirFiles lists all .jsonl files in the named subdirectory on the
// remote a-sync branch. Used for messages-v2/ and receipts/ merge.
func (m *Merger) listRemoteDirFiles(ctx context.Context, subDir string) (map[string]bool, error) {
	files := make(map[string]bool)

	output, err := safecmd.Git(ctx, m.syncDir, "ls-tree", "--name-only", "origin/"+SyncBranchName, subDir+"/")
	if err != nil {
		return files, fmt.Errorf("list remote %s files: %w", subDir, err)
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		filename := filepath.Base(line)
		if strings.HasSuffix(filename, ".jsonl") {
			files[filename] = true
		}
	}

	return files, nil
}

// mergeJSONDir handles per-file last-writer-wins merge for a directory of
// small JSON files (e.g. state/agents/ and state/bridge-groups/). Old peers
// that don't write these directories are naturally handled: if the directory
// is absent on the remote the function is a no-op.
func (m *Merger) mergeJSONDir(ctx context.Context, localDir, remoteSubDir, remoteTmpDir string, archiveErr error) error {
	// List local .json files in the directory.
	var localJSONFiles map[string]bool
	if _, err := os.Stat(localDir); os.IsNotExist(err) {
		localJSONFiles = make(map[string]bool)
	} else {
		entries, err := os.ReadDir(localDir)
		if err != nil {
			return fmt.Errorf("read dir %s: %w", localDir, err)
		}
		localJSONFiles = make(map[string]bool, len(entries))
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				localJSONFiles[e.Name()] = true
			}
		}
	}

	// List remote .json files.
	var remoteJSONFiles map[string]bool
	if archiveErr == nil {
		remoteSubDirPath := filepath.Join(remoteTmpDir, remoteSubDir)
		if _, err := os.Stat(remoteSubDirPath); os.IsNotExist(err) {
			remoteJSONFiles = make(map[string]bool)
		} else {
			entries, err := os.ReadDir(remoteSubDirPath)
			if err != nil {
				remoteJSONFiles = make(map[string]bool)
			} else {
				remoteJSONFiles = make(map[string]bool, len(entries))
				for _, e := range entries {
					if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
						remoteJSONFiles[e.Name()] = true
					}
				}
			}
		}
	} else {
		output, err := safecmd.Git(ctx, m.syncDir, "ls-tree", "--name-only", "origin/"+SyncBranchName, remoteSubDir+"/")
		if err != nil {
			// Remote directory does not exist yet — normal for old peers.
			remoteJSONFiles = make(map[string]bool)
		} else {
			lines := strings.Split(string(output), "\n")
			remoteJSONFiles = make(map[string]bool, len(lines))
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				filename := filepath.Base(line)
				if strings.HasSuffix(filename, ".json") {
					remoteJSONFiles[filename] = true
				}
			}
		}
	}

	// For each remote JSON file: if absent locally or remote is newer, copy it.
	// Per spec §6.5: each daemon owns its own state files, so last-writer-wins
	// per path is correct (no two daemons write the same agent file).
	for remoteFile := range remoteJSONFiles {
		localPath := filepath.Join(localDir, remoteFile)
		if err := os.MkdirAll(localDir, 0750); err != nil {
			return fmt.Errorf("create dir %s: %w", localDir, err)
		}
		if archiveErr == nil {
			remotePath := filepath.Join(remoteTmpDir, remoteSubDir, remoteFile)
			data, readErr := os.ReadFile(remotePath) // #nosec G304 -- path from internal temp dir
			if readErr != nil {
				// Remote file may not exist in the archive for this specific path.
				continue
			}
			if writeErr := os.WriteFile(localPath, data, 0600); writeErr != nil { // #nosec G306
				return fmt.Errorf("write state file %s: %w", remoteFile, writeErr)
			}
		} else {
			remotePath := remoteSubDir + "/" + remoteFile
			if cpErr := m.copyRemoteFile(ctx, localPath, remotePath); cpErr != nil {
				return fmt.Errorf("copy state file %s: %w", remoteFile, cpErr)
			}
		}
	}

	// Local-only files are kept as-is (will be pushed on next commit).
	_ = localJSONFiles

	return nil
}

// mergeJSONLDir handles union-merge for a directory of JSONL files
// (e.g. messages-v2/ and receipts/). Mirrors the legacy messages/ merge
// logic using the same union-by-event-id strategy.
func (m *Merger) mergeJSONLDir(
	ctx context.Context,
	localDir, remoteSubDir string,
	localFiles, remoteFiles map[string]bool,
	remoteTmpDir string,
	archiveErr error,
	totalStats *MergeResult,
) error {
	// Merge files that exist in both local and remote.
	for localFile := range localFiles {
		if remoteFiles[localFile] {
			localPath := filepath.Join(localDir, localFile)
			var stats *MergeResult
			var err error
			if archiveErr == nil {
				remotePath := filepath.Join(remoteTmpDir, remoteSubDir, localFile)
				stats, err = m.mergeFileFromDir(localPath, remotePath)
			} else {
				remotePath := remoteSubDir + "/" + localFile
				stats, err = m.mergeFile(ctx, localPath, remotePath)
			}
			if err != nil {
				return fmt.Errorf("merge %s/%s: %w", remoteSubDir, localFile, err)
			}
			m.accumulateStats(totalStats, stats)
		}
	}

	// Copy remote-only files to local.
	for remoteFile := range remoteFiles {
		if !localFiles[remoteFile] {
			localPath := filepath.Join(localDir, remoteFile)
			if err := os.MkdirAll(localDir, 0750); err != nil {
				return fmt.Errorf("create dir %s: %w", localDir, err)
			}
			if archiveErr == nil {
				remotePath := filepath.Join(remoteTmpDir, remoteSubDir, remoteFile)
				data, readErr := os.ReadFile(remotePath) // #nosec G304 -- path from internal temp dir
				if readErr != nil {
					continue
				}
				if writeErr := os.WriteFile(localPath, data, 0600); writeErr != nil { // #nosec G306
					return fmt.Errorf("write %s/%s: %w", remoteSubDir, remoteFile, writeErr)
				}
			} else {
				remotePath := remoteSubDir + "/" + remoteFile
				if cpErr := m.copyRemoteFile(ctx, localPath, remotePath); cpErr != nil {
					return fmt.Errorf("copy %s/%s: %w", remoteSubDir, remoteFile, cpErr)
				}
			}
			// Count events in copied file.
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

	return nil
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
	f, err := os.Create(tmpPath) // #nosec G304 -- tmpPath is derived from internal JSONL file path during merge
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

// ReadLegacyMessageFallback reads legacy messages/<agentID>.jsonl from
// the sync worktree when messages-v2/<agentID>.jsonl is absent for that
// agent. Returns the parsed event rows + the source path used (for
// telemetry). Returns (nil, "", nil) when neither file exists.
//
// Per spec §4.6, the legacy read horizon is indefinite — peers running
// pre-v0.10.6 may still write to messages/ and this fallback supports
// reading from them. The new wire stream (messages-v2/) is preferred
// when present.
func ReadLegacyMessageFallback(syncDir, agentID string) (events []json.RawMessage, sourcePath string, err error) {
	v2Path := filepath.Join(syncDir, "messages-v2", agentID+".jsonl")
	if _, statErr := os.Stat(v2Path); statErr == nil {
		// v2 file is present; no fallback needed.
		// TODO E8 telemetry: slog sync.legacy_read here when this path is taken
		return nil, "", nil
	}

	legacyPath := filepath.Join(syncDir, "messages", agentID+".jsonl")
	if _, statErr := os.Stat(legacyPath); os.IsNotExist(statErr) {
		// Neither file exists.
		return nil, "", nil
	}

	reader, err := jsonl.NewReader(legacyPath)
	if err != nil {
		return nil, legacyPath, fmt.Errorf("open legacy message file: %w", err)
	}

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, legacyPath, fmt.Errorf("read legacy message file: %w", err)
	}

	// TODO E8 telemetry: slog sync.legacy_read here
	return rows, legacyPath, nil
}

// BootstrapIngestLegacyEvents copies events from the synced
// events.jsonl (pre-v0.10.6 location) into the LOCAL .thrum/events.jsonl
// + SQLite events table on first daemon run after upgrade. Idempotent
// via a sentinel file (.thrum/legacy_ingested). Subsequent boots are
// a no-op.
//
// Per spec §4.6, the legacy events.jsonl in the sync worktree is NOT
// deleted after ingest — future tooling (thrum doctor sync, etc.) may
// need it as a historical artifact.
//
// Returns the number of rows ingested. Zero on the no-op path.
func BootstrapIngestLegacyEvents(ctx context.Context, thrumDir, syncDir string, db *safedb.DB) (int, error) {
	sentinelPath := filepath.Join(thrumDir, "legacy_ingested")

	// Fast path: already ingested.
	if _, err := os.Stat(sentinelPath); err == nil {
		return 0, nil
	}

	legacyEventsPath := filepath.Join(syncDir, "events.jsonl")

	// If no legacy events file exists, write sentinel so we don't keep
	// checking every boot and return cleanly.
	if _, err := os.Stat(legacyEventsPath); os.IsNotExist(err) {
		if writeErr := writeSentinel(sentinelPath); writeErr != nil {
			return 0, fmt.Errorf("write legacy_ingested sentinel: %w", writeErr)
		}
		return 0, nil
	}

	// Read legacy events line-by-line.
	f, err := os.Open(legacyEventsPath) // #nosec G304 -- legacyEventsPath is an internal sync file
	if err != nil {
		return 0, fmt.Errorf("open legacy events.jsonl: %w", err)
	}
	defer func() { _ = f.Close() }()

	localJournalPath := filepath.Join(thrumDir, "events.jsonl")
	writer, err := jsonl.NewWriter(localJournalPath)
	if err != nil {
		return 0, fmt.Errorf("open local events.jsonl writer: %w", err)
	}

	ingested := 0
	scanner := bufio.NewScanner(f)
	const maxLineBytes = 4 * 1024 * 1024
	buf := make([]byte, maxLineBytes)
	scanner.Buffer(buf, maxLineBytes)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Parse to extract fields needed for the SQLite insert.
		var row map[string]json.RawMessage
		if err := json.Unmarshal(line, &row); err != nil {
			// Skip malformed rows — legacy file may have corruption.
			continue
		}

		// Append raw line to local journal.
		var rowAny map[string]any
		if err := json.Unmarshal(line, &rowAny); err != nil {
			continue
		}
		if err := writer.Append(rowAny); err != nil {
			// Non-fatal: log the error but keep going.
			continue
		}

		// Insert into SQLite events table.
		// Use OR IGNORE so duplicate event_ids from a partial previous ingest
		// (e.g. interrupted run before sentinel write) don't cause errors.
		evtID := jsonRawString(row["event_id"])
		evtType := jsonRawString(row["type"])
		evtTimestamp := jsonRawString(row["timestamp"])
		evtOrigin := jsonRawString(row["origin_daemon"])
		evtSeq := jsonRawInt64(row["sequence"])

		_, sqlErr := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO events (event_id, sequence, type, timestamp, origin_daemon, event_json) VALUES (?, ?, ?, ?, ?, ?)`,
			evtID, evtSeq, evtType, evtTimestamp, evtOrigin, string(line),
		)
		if sqlErr != nil {
			// Non-fatal: continue ingesting remaining rows.
			continue
		}
		ingested++
	}

	if err := scanner.Err(); err != nil {
		return ingested, fmt.Errorf("scan legacy events.jsonl: %w", err)
	}

	// Write sentinel after successful ingest. Includes timestamp for diagnostics.
	if writeErr := writeSentinel(sentinelPath); writeErr != nil {
		return ingested, fmt.Errorf("write legacy_ingested sentinel: %w", writeErr)
	}

	// DO NOT delete legacy events.jsonl — spec §4.6 keeps it as a historical
	// artifact; future tooling (thrum doctor sync) may need it.

	return ingested, nil
}

// writeSentinel writes the legacy_ingested sentinel file with a timestamp.
func writeSentinel(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("create sentinel dir: %w", err)
	}
	content := time.Now().UTC().Format(time.RFC3339) + "\n"
	return os.WriteFile(path, []byte(content), 0600) // #nosec G306
}

// jsonRawString extracts a string value from a json.RawMessage.
// Returns "" if the raw message is nil or not a valid JSON string.
func jsonRawString(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// jsonRawInt64 extracts an int64 value from a json.RawMessage.
// Returns 0 if the raw message is nil or not a valid JSON number.
func jsonRawInt64(raw json.RawMessage) int64 {
	if raw == nil {
		return 0
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0
	}
	return n
}
