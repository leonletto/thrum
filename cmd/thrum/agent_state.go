package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/leonletto/thrum/internal/agentstate"
	"github.com/leonletto/thrum/internal/paths"
)

// agentStateCmd builds the `thrum agent state ...` subtree. Returns
// the parent command with `update` (and future `show` / `recover`
// once Tasks 26+ land). Wired into agentCmd() in main.go.
//
// The CLI surface exists to keep state.md mutations consistent with
// the spec §7.5 sliding-window invariants. Skills (per spec §7.5
// "Three update skills") instruct the agent to invoke these
// commands rather than hand-editing the markdown — the strict
// agentstate parser is the source of truth for format correctness.
func agentStateCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "state",
		Short: "Manage scheduled-agent state.md",
		Long: `Manage scheduled-agent state.md (per spec §7.5).

Each scheduled agent has a state.md at .thrum/agents/<agent-id>/state.md
that carries session history as 4 verbatim entries + up to 3
summary blocks of 5 entries each, yielding a 15-19 session
sliding window.

The 'update' subcommand records a just-completed session into
the verbatim queue with proper sliding-window promotion. The
'/thrum:update-agent-state' skill invokes this command at
end-of-session; operators rarely call it directly.`,
	}

	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Record a completed session into state.md (skill-driven)",
		Long: `Record a completed session into the agent's state.md file.

Reads existing state.md (creating an empty one if missing),
applies PromoteAndDrop with the supplied session entry, writes
the updated state.md back. The 19-session sliding window is
enforced by agentstate.PromoteAndDrop per spec §7.5.

Required flags: --session-id, --summary. Optional: --agent-id
(defaults to whoami), --date (defaults to today's UTC ISO date),
--last-worked-on, --planning-next, --run-id, --run-state.`,
		RunE: runAgentStateUpdate,
	}
	updateCmd.Flags().String("agent-id", "", "Agent ID (default: current identity)")
	updateCmd.Flags().String("session-id", "", "Session ID of the completed session (required)")
	updateCmd.Flags().String("summary", "", "One-line summary of the completed session (required)")
	updateCmd.Flags().String("date", "", "Session date (default: today's UTC ISO date)")
	updateCmd.Flags().String("last-worked-on", "", "Replace the 'Last worked on' paragraph")
	updateCmd.Flags().String("planning-next", "", "Replace the 'Planning next' paragraph")
	updateCmd.Flags().String("run-id", "", "Run ID for the metadata header")
	updateCmd.Flags().String("run-state", "", "Run completion state (success/partial/failed)")

	parent.AddCommand(updateCmd)
	return parent
}

func runAgentStateUpdate(cmd *cobra.Command, _ []string) error {
	sessionID, _ := cmd.Flags().GetString("session-id")
	summary, _ := cmd.Flags().GetString("summary")
	if sessionID == "" {
		return fmt.Errorf("--session-id is required")
	}
	if summary == "" {
		return fmt.Errorf("--summary is required")
	}

	agentID, _ := cmd.Flags().GetString("agent-id")
	if agentID == "" {
		resolved, err := currentAgentID()
		if err != nil {
			return fmt.Errorf("resolve current agent: %w", err)
		}
		if resolved == "" {
			return fmt.Errorf("agent-id required (no current identity to default to; pass --agent-id)")
		}
		agentID = resolved
	}

	date, _ := cmd.Flags().GetString("date")
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}

	// Resolve agent's state.md path. Walks cwd-anchored thrum-root
	// (same single-thrum-root simplification as agent_sessions.go's
	// loadSessionsForAgent; multi-worktree edge case is tracked
	// under follow-up).
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	repoRoot, err := paths.FindThrumRoot(cwd)
	if err != nil {
		return fmt.Errorf("find thrum-root: %w", err)
	}
	thrumRoot := filepath.Join(repoRoot, ".thrum")
	stateMDPath := filepath.Join(thrumRoot, "agents", agentID, "state.md")

	state, err := readOrInitStateMD(stateMDPath, agentID)
	if err != nil {
		return err
	}

	// Apply optional metadata overrides.
	if runID, _ := cmd.Flags().GetString("run-id"); runID != "" {
		state.LastRunID = runID
	}
	if runState, _ := cmd.Flags().GetString("run-state"); runState != "" {
		state.LastRunState = runState
	}
	if lwo, _ := cmd.Flags().GetString("last-worked-on"); lwo != "" {
		state.LastWorkedOn = lwo
	}
	if pn, _ := cmd.Flags().GetString("planning-next"); pn != "" {
		state.PlanningNext = pn
	}
	state.LastUpdated = time.Now().UTC()

	// PromoteAndDrop is the load-bearing operation — the strict
	// 4-verbatim / 3-block / 5-per-block invariants live entirely
	// inside the agentstate package. This wrapper is a thin shim.
	agentstate.PromoteAndDrop(state, agentstate.VerbatimEntry{
		SessionID: sessionID,
		Date:      date,
		Summary:   summary,
	})

	if err := writeStateMD(stateMDPath, state); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"Updated %s (verbatim: %d, summary blocks: %d)\n",
		stateMDPath, len(state.Verbatim), len(state.SummaryBlocks))
	return nil
}

// readOrInitStateMD reads an existing state.md or returns a fresh
// empty StateMD. Returns the parse error (NOT a fresh state) when
// the file exists but is malformed — the caller surfaces it so the
// operator knows recovery is needed before subsequent writes.
//
// This guards against a subtle stacking pathology: if Parse fails
// and we silently fall through to a fresh state, the next
// PromoteAndDrop overwrites the existing (malformed) file and
// destroys the partial data that could have been recovered. Spec
// §6.5 mandates: do NOT overwrite a malformed state.md without
// going through the recovery flow.
func readOrInitStateMD(path, agentID string) (*agentstate.StateMD, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path under cwd-anchored thrum-root
	if errors.Is(err, os.ErrNotExist) {
		return &agentstate.StateMD{AgentName: agentID}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state.md: %w", err)
	}
	state, err := agentstate.Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse state.md (recovery required per spec §6.5): %w", err)
	}
	return state, nil
}

// writeStateMD writes a StateMD to disk atomically via temp-file
// + rename. The destination directory is created with 0700 perms
// (matching the session-archive folder layout); the file itself
// is 0600 — same operator-only convention as session.archive.
func writeStateMD(path string, s *agentstate.StateMD) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir state.md parent: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, "state.md.tmp.*")
	if err != nil {
		return fmt.Errorf("create temp state.md: %w", err)
	}
	tmpPath := tmpFile.Name()
	// Clean up the temp file if anything fails before the rename.
	defer func() {
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := s.Write(tmpFile); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write state.md: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp state.md: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("chmod state.md: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename state.md: %w", err)
	}
	return nil
}

// trimEmpty is unused in the current revision but kept for the
// recover-agent-state CLI surface Task 26 will add (the recovery
// command needs to trim leading/trailing whitespace from
// reconstructed paragraphs).
//
// nolint:unused // reserved for Task 26
func trimEmpty(s string) string {
	return strings.TrimSpace(s)
}
