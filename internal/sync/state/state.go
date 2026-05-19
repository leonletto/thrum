// Package state provides state-file writers and readers for
// state/agents/<agent_id>.json and state/bridge-groups/<group_id>.json.
//
// Author-owned rule: each daemon writes only the files it owns. The Writer
// enforces this by comparing the caller's daemonID against the resolved owner
// of the agent or bridge-group. Writes to files owned by a different daemon
// return ErrNotOwner.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	gosync "sync"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safecmd"
)

// ErrNotOwner is returned when a Writer attempts to write or delete a state
// file that belongs to a different daemon.
var ErrNotOwner = errors.New("state: caller does not own this state file (author-owned rule)")

// AgentStateSnapshot mirrors the §4.1 file-on-disk schema for
// state/agents/<agent_id>.json.
type AgentStateSnapshot struct {
	AgentID    string    `json:"agent_id"`
	Name       string    `json:"name"`
	Role       string    `json:"role"`
	Module     string    `json:"module"`
	Display    string    `json:"display"`
	Hostname   string    `json:"hostname"`
	Worktree   string    `json:"worktree"`
	Branch     string    `json:"branch"`
	Kind       string    `json:"kind"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Version    int       `json:"v"`
}

// BridgeGroupStateSnapshot mirrors the §4.2 file-on-disk schema for
// state/bridge-groups/<group_id>.json.
type BridgeGroupStateSnapshot struct {
	GroupID     string    `json:"group_id"`
	Kind        string    `json:"kind"`        // always "bridge_group"
	BridgeKind  string    `json:"bridge_kind"` // "telegram" | "peer"
	OwnerDaemon string    `json:"owner_daemon"`
	Members     []string  `json:"members"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	Version     int       `json:"v"`
}

// Writer owns the per-agent and per-bridge-group state files this daemon
// authors. Enforces the author-owned rule: rejects writes to a state file
// the daemon does not own.
//
// All writes are serialised via an internal mutex — no data races, no
// corruption under concurrent goroutines writing the same file.
type Writer struct {
	syncDir        string
	daemonID       string
	ownerResolver  func(agentID string) (string, error)
	branchResolver func(ctx context.Context, worktree string) string
	mu             gosync.Mutex
}

// NewWriter creates a Writer.
//
//   - syncDir: absolute path to the sync worktree root (a-sync checkout).
//   - daemonID: identity of this daemon; used as the author-owned check.
//   - ownerResolver: func that maps an agentID to the daemonID that owns it.
//     Returns ("", nil) when the agent is unknown (treated as not-owned by caller).
//   - branchResolver: func that returns the current branch for a given
//     worktree path. Production impl calls gitctx.ExtractWorkContext; tests
//     inject a stub.
func NewWriter(
	syncDir, daemonID string,
	ownerResolver func(agentID string) (string, error),
	branchResolver func(ctx context.Context, worktree string) string,
) *Writer {
	return &Writer{
		syncDir:        syncDir,
		daemonID:       daemonID,
		ownerResolver:  ownerResolver,
		branchResolver: branchResolver,
	}
}

// WriteAgent writes (or overwrites) state/agents/<agent_id>.json with the
// given snapshot. The Branch field is resolved via the injected branchResolver
// at write time (overrides whatever was passed in snap.Branch).
//
// Returns ErrNotOwner if the agent's ownerResolver result != this writer's daemonID.
func (w *Writer) WriteAgent(ctx context.Context, snap AgentStateSnapshot) error {
	// Resolve ownership before acquiring the mutex (resolver may call DB/git).
	ownerDaemon, err := w.ownerResolver(snap.AgentID)
	if err != nil {
		return fmt.Errorf("state: owner resolution for %s: %w", snap.AgentID, err)
	}
	if ownerDaemon != w.daemonID {
		return ErrNotOwner
	}

	// Resolve branch field via injected resolver.
	snap.Branch = w.branchResolver(ctx, snap.Worktree)

	w.mu.Lock()
	defer w.mu.Unlock()

	dir := filepath.Join(w.syncDir, "state", "agents")
	return atomicWriteJSON(dir, snap.AgentID+".json", snap)
}

// DeleteAgent removes state/agents/<agent_id>.json via git rm + on-disk
// delete. Returns ErrNotOwner if the caller does not own the agent.
//
// Idempotent: if the file does not exist, returns nil without invoking git rm.
func (w *Writer) DeleteAgent(ctx context.Context, agentID string) error {
	ownerDaemon, err := w.ownerResolver(agentID)
	if err != nil {
		return fmt.Errorf("state: owner resolution for %s: %w", agentID, err)
	}
	if ownerDaemon != w.daemonID {
		return ErrNotOwner
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	path := filepath.Join(w.syncDir, "state", "agents", agentID+".json")
	return deleteStateFile(ctx, w.syncDir, path)
}

// WriteBridgeGroup writes (or overwrites) state/bridge-groups/<group_id>.json.
//
// Ownership is determined by snap.OwnerDaemon: if it does not match this
// writer's daemonID, ErrNotOwner is returned and the file is not touched.
func (w *Writer) WriteBridgeGroup(ctx context.Context, snap BridgeGroupStateSnapshot) error {
	if snap.OwnerDaemon != w.daemonID {
		return ErrNotOwner
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	dir := filepath.Join(w.syncDir, "state", "bridge-groups")
	return atomicWriteJSON(dir, snap.GroupID+".json", snap)
}

// DeleteBridgeGroup removes state/bridge-groups/<group_id>.json via git rm +
// on-disk delete.
//
// Idempotent: if the file does not exist, returns nil without invoking git rm.
// Ownership is verified by reading the existing file's owner_daemon field; if
// the file is absent, the delete is a no-op (first-run idempotency).
func (w *Writer) DeleteBridgeGroup(ctx context.Context, groupID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	path := filepath.Join(w.syncDir, "state", "bridge-groups", groupID+".json")

	// If file exists, check ownership before deleting.
	if _, err := os.Stat(path); err == nil {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("state: read bridge-group for ownership check: %w", readErr)
		}
		var existing BridgeGroupStateSnapshot
		if jsonErr := json.Unmarshal(data, &existing); jsonErr != nil {
			return fmt.Errorf("state: unmarshal bridge-group for ownership check: %w", jsonErr)
		}
		if existing.OwnerDaemon != w.daemonID {
			return ErrNotOwner
		}
	}

	return deleteStateFile(ctx, w.syncDir, path)
}

// Reader walks state files (from any daemon, not just the current writer's)
// and returns parsed snapshots. Used by the projection for cross-peer ingest
// and bootstrap from the sync branch.
type Reader struct {
	syncDir string
}

// NewReader creates a Reader rooted at syncDir.
func NewReader(syncDir string) *Reader {
	return &Reader{syncDir: syncDir}
}

// ReadAllAgents reads all .json files under state/agents/ and returns them as
// AgentStateSnapshot values. Non-JSON files (no .json extension) are silently
// ignored. If the directory does not exist, returns an empty slice (not an error).
func (r *Reader) ReadAllAgents(ctx context.Context) ([]AgentStateSnapshot, error) {
	dir := filepath.Join(r.syncDir, "state", "agents")
	var out []AgentStateSnapshot
	if err := readJSONDir(dir, func(data []byte) error {
		var s AgentStateSnapshot
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		out = append(out, s)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// ReadAllBridgeGroups reads all .json files under state/bridge-groups/ and
// returns them as BridgeGroupStateSnapshot values. Non-JSON files are ignored.
// If the directory does not exist, returns an empty slice (not an error).
func (r *Reader) ReadAllBridgeGroups(ctx context.Context) ([]BridgeGroupStateSnapshot, error) {
	dir := filepath.Join(r.syncDir, "state", "bridge-groups")
	var out []BridgeGroupStateSnapshot
	if err := readJSONDir(dir, func(data []byte) error {
		var s BridgeGroupStateSnapshot
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		out = append(out, s)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// ReadAgent reads state/agents/<agentID>.json. Returns (nil, nil) when the
// file does not exist.
func (r *Reader) ReadAgent(ctx context.Context, agentID string) (*AgentStateSnapshot, error) {
	path := filepath.Join(r.syncDir, "state", "agents", agentID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("state: read agent %s: %w", agentID, err)
	}
	var s AgentStateSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("state: unmarshal agent %s: %w", agentID, err)
	}
	return &s, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// atomicWriteJSON marshals v to JSON and writes it to dir/<filename> using a
// temp-file + rename pattern to avoid partial writes on crash.
//
// The directory is created with MkdirAll if it does not exist.
func atomicWriteJSON(dir, filename string, v any) error {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("state: mkdir %s: %w", dir, err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("state: marshal: %w", err)
	}

	final := filepath.Join(dir, filename)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("state: write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup
		return fmt.Errorf("state: rename %s → %s: %w", tmp, final, err)
	}
	return nil
}

// deleteStateFile removes the file at path from disk and from git (via
// safecmd.Git("rm", ...)). It is idempotent: if the file does not exist,
// it returns nil without invoking git rm.
//
// syncDir is used as the working directory for the git command.
func deleteStateFile(ctx context.Context, syncDir, path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// File does not exist — idempotent no-op.
		return nil
	}

	// Compute path relative to syncDir for git rm.
	rel, err := filepath.Rel(syncDir, path)
	if err != nil {
		return fmt.Errorf("state: rel path for git rm: %w", err)
	}

	// git rm first (so git's index stays in sync), then remove from disk if
	// git rm didn't already do it. Errors from git rm are returned — a failed
	// git rm is a sync-correctness problem.
	if _, gitErr := safecmd.Git(ctx, syncDir, "rm", "--force", "--ignore-unmatch", rel); gitErr != nil {
		// git rm failed but we still need the file gone from disk for consistency.
		// Return the error after attempting the disk remove so callers know.
		_ = os.Remove(path)
		return gitErr
	}

	// git rm --force removes the file from disk too, but remove it explicitly
	// in case the file wasn't tracked (--ignore-unmatch suppresses the error
	// but leaves the file untouched in that case).
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("state: remove %s: %w", path, err)
	}
	return nil
}

// readJSONDir reads all .json files in dir, calling fn for each file's raw
// bytes. Non-.json files are silently skipped. If dir does not exist, returns
// nil (empty result, not an error).
func readJSONDir(dir string, fn func([]byte) error) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("state: readdir %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("state: read %s: %w", e.Name(), err)
		}
		if err := fn(data); err != nil {
			return fmt.Errorf("state: parse %s: %w", e.Name(), err)
		}
	}
	return nil
}
