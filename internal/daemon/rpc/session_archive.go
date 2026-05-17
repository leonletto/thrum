package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/leonletto/thrum/internal/agent"
	"github.com/leonletto/thrum/internal/daemon/sessionarchive"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// SessionArchiveRequest is the wire shape for the session.archive RPC.
// Only agent_id is required; the daemon resolves all paths internally
// from the registry + identity-file scan.
type SessionArchiveRequest struct {
	AgentID string `json:"agent_id"`
}

// SessionArchiveResponse is the spec §3.1 return shape extended with
// `content` for the Task 7 CLI-side prime adaptation (spec §3.6 was
// written assuming a daemon-orchestrated prime builder; the actual
// code is CLI-orchestrated). The CLI calls session.archive and uses
// `content` as the snapshot body it inserts into prime output,
// replacing the prior restart.ConsumeInPrime+CleanupConsumed two-step.
//
// All fields are pointer-strings so they serialize as JSON null when
// absent. content is null iff archived_path is null (missing or
// empty source).
type SessionArchiveResponse struct {
	ArchivedPath *string `json:"archived_path"`
	BigPicture   *string `json:"big_picture"`
	Content      *string `json:"content"`
}

// SessionArchiveHandler wraps sessionarchive.Archive for the daemon's
// JSON-RPC surface. It owns the agent-registry lookup, the worktree
// thrum-dir resolution via the identity-file scan, and the request-
// shape validation. The actual move logic lives in
// internal/daemon/sessionarchive.
type SessionArchiveHandler struct {
	state    *state.State
	thrumDir string // main-repo .thrum/ (h.thrumDir convention)
	registry agent.AgentRegistry
}

// NewSessionArchiveHandler constructs the handler. thrumDir is the
// daemon's main-repo .thrum/ directory (mirror of TmuxHandler.thrumDir).
// The agent registry is built once over state.DB() since safedb.DB
// is the canonical handle.
func NewSessionArchiveHandler(s *state.State, thrumDir string) *SessionArchiveHandler {
	return &SessionArchiveHandler{
		state:    s,
		thrumDir: thrumDir,
		registry: agent.NewSQLiteRegistry(s.DB()),
	}
}

// HandleArchive is the session.archive RPC entry point.
//
// Returns:
//
//	{archived_path: "<path>", big_picture: "<§1 body>" | null} on success
//	{archived_path: null,    big_picture: null}                if no snapshot
//
// Errors (wire-mapped via server.go to -32603 Internal error):
//
//	"agent_id is required"            — empty/missing AgentID
//	"agent <id> not registered"       — Registry returns ErrAgentNotFound
//	wrapped registry error            — other Lookup failures
//	wrapped sessionarchive error      — collision-cap / fs failures
//
// Worktree thrum-dir resolution: scans the daemon's identity directories
// (primary + every git worktree under repo root) for the agent's
// identity file. The .thrum/ ancestor of that file IS the worktree
// thrum-dir. Missing identity files yield a clear error rather than a
// silent move into the wrong tree.
func (h *SessionArchiveHandler) HandleArchive(ctx context.Context, params json.RawMessage) (any, error) {
	var req SessionArchiveRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if req.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}

	a, err := h.registry.Lookup(ctx, req.AgentID)
	if err != nil {
		if errors.Is(err, agent.ErrAgentNotFound) {
			return nil, fmt.Errorf("agent %s not registered", req.AgentID)
		}
		return nil, fmt.Errorf("registry lookup: %w", err)
	}

	worktreeThrumDir, err := h.resolveWorktreeThrumDir(ctx, req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("resolve worktree thrum-dir: %w", err)
	}

	srcPath := filepath.Join(worktreeThrumDir, "restart", req.AgentID+".md")
	result, err := sessionarchive.Archive(
		ctx, a, srcPath,
		h.thrumDir, worktreeThrumDir,
		sessionarchive.Opts{Logger: slog.Default()},
	)
	if err != nil {
		return nil, fmt.Errorf("session archive: %w", err)
	}
	if result == nil {
		return &SessionArchiveResponse{}, nil
	}
	return &SessionArchiveResponse{
		ArchivedPath: result.ArchivedPath,
		BigPicture:   result.BigPicture,
		Content:      result.Content,
	}, nil
}

// resolveWorktreeThrumDir locates the .thrum/ directory of the worktree
// the named agent lives in by scanning identity files across all
// worktrees. The identity file path is `<wt>/.thrum/identities/<id>.json`,
// so the worktree thrum-dir is two `filepath.Dir` levels up.
//
// Returns a clear error if the agent has no identity file anywhere —
// session-archive without a known worktree home would otherwise pick a
// silently-wrong destination tree.
func (h *SessionArchiveHandler) resolveWorktreeThrumDir(ctx context.Context, agentID string) (string, error) {
	paths := IdentityPathsAcrossWorktrees(ctx, h.thrumDir)
	idPath, ok := paths[agentID]
	if !ok {
		return "", fmt.Errorf("no identity file found for agent %s", agentID)
	}
	// idPath = <wt>/.thrum/identities/<id>.json → grandparent = <wt>/.thrum/
	return filepath.Dir(filepath.Dir(idPath)), nil
}
