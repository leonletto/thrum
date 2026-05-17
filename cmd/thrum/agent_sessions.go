package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/leonletto/thrum/internal/cli"
	"github.com/leonletto/thrum/internal/daemon/sessionarchive"
	"github.com/leonletto/thrum/internal/paths"
)

// SessionEntry is the operator-visible projection of one archived
// snapshot file in `<thrumRoot>/agents/<id>/sessions/`. Built by
// loadSessionsForAgent from filesystem + frontmatter; consumed by
// the three render functions (default/verbose/json).
//
// Field set matches spec §6.4 JSON output schema so renderJSON can
// serialize verbatim and the other renderers pick the subset they
// need. Per-task expansion:
//
//   - Task 10 (this commit) populates Timestamp / Size / Reason /
//     Path / BigPictureNormalized for the default render.
//   - Task 11 adds BigPictureRaw + SessionID + MachineID for
//     --verbose and --json.
//   - Task 12 keeps the shape stable and only changes the
//     listAllAgents iteration.
type SessionEntry struct {
	Timestamp            time.Time
	Size                 int64
	Reason               string
	Path                 string
	AgentID              string
	SessionID            string
	MachineID            string
	BigPictureNormalized string
	BigPictureRaw        string
}

// errAgentNotRegistered is returned by loadSessionsForAgent when no
// identity file exists for the named agent under the resolved
// thrum-root. CLI command surfaces this as a clear "agent X not
// registered" stderr error + non-zero exit code.
var errAgentNotRegistered = errors.New("agent not registered")

// agentSessionsCmd builds the `thrum agent sessions ...` subtree.
// Returns the parent command with `list` and `archive` children.
// Wired into agentCmd() via cmd.AddCommand(agentSessionsCmd()) in
// main.go.
func agentSessionsCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "sessions",
		Short: "Manage archived session snapshots",
		Long: `Manage archived session snapshots for thrum agents.

Each /thrum:restart snapshot is preserved in
<thrum-root>/agents/<agent-id>/sessions/ instead of being deleted.
'thrum agent sessions list' browses the archive; 'thrum agent
sessions archive <agent-id>' is a debug-only invocation of the
session.archive RPC (Q-Spec-6) for manual testing.`,
	}

	listCmd := &cobra.Command{
		Use:   "list [agent-id]",
		Short: "List archived sessions for an agent",
		Long: `List archived sessions for an agent.

Default output is a table with TIMESTAMP / SIZE / REASON / SUMMARY
columns; SUMMARY is the first-line collapse of the snapshot's
"## 1. Big picture" section. With no agent-id, resolves the
caller's identity from cwd. Use --verbose for full §1 bodies,
--json for newline-delimited JSON records, or --all to walk
every agent's archive in one pass.`,
		RunE: runAgentSessionsList,
	}
	listCmd.Flags().Bool("all", false, "List sessions across every agent with an identity file")
	listCmd.Flags().Bool("verbose", false, "Show full §1 Big picture body per session (mutually exclusive with --json)")
	listCmd.Flags().Bool("json", false, "Emit newline-delimited JSON records (mutually exclusive with --verbose)")

	archiveCmd := &cobra.Command{
		Use:   "archive <agent-id>",
		Short: "Invoke session.archive RPC for an agent (debug)",
		Long: `Debug-only invocation of the session.archive RPC for the named
agent. Manually triggers the same archive flow the daemon runs
during prime-context build. Useful for dogfooding and manual
testing; not part of any production workflow.

Per Q-Spec-6 (spec §3.1).`,
		Args: cobra.ExactArgs(1),
		RunE: runAgentSessionsArchive,
	}

	parent.AddCommand(listCmd)
	parent.AddCommand(archiveCmd)
	return parent
}

func runAgentSessionsList(cmd *cobra.Command, args []string) error {
	all, _ := cmd.Flags().GetBool("all")
	verbose, _ := cmd.Flags().GetBool("verbose")
	asJSON, _ := cmd.Flags().GetBool("json")

	// Flag-combination validation (Task 10 acceptance criteria).
	if verbose && asJSON {
		return fmt.Errorf("--verbose and --json are mutually exclusive (verbose is human-display; json is structured)")
	}

	agentID := ""
	if len(args) > 0 {
		agentID = args[0]
	}
	if all && agentID != "" {
		return fmt.Errorf("--all cannot be combined with an explicit agent-id")
	}

	if all {
		return listAllAgents(cmd, verbose, asJSON)
	}

	if agentID == "" {
		resolved, err := currentAgentID()
		if err != nil {
			return fmt.Errorf("resolve current agent: %w", err)
		}
		if resolved == "" {
			return fmt.Errorf("agent-id required (no current identity to default to; pass an explicit <agent-id> or use --all)")
		}
		agentID = resolved
	}

	sessions, err := loadSessionsForAgent(agentID)
	if errors.Is(err, errAgentNotRegistered) {
		return fmt.Errorf("agent %q not registered", agentID)
	}
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Sessions for %s: none yet.\n", agentID)
		return nil
	}

	// Render modes — Task 11 fills in verbose + json; Task 10
	// ships default rendering only.
	switch {
	case verbose:
		return renderVerbose(cmd, agentID, sessions)
	case asJSON:
		return renderJSON(cmd, sessions)
	default:
		return renderDefault(cmd, agentID, sessions)
	}
}

func runAgentSessionsArchive(cmd *cobra.Command, args []string) error {
	agentID := args[0]
	client, err := getClient()
	if err != nil {
		return fmt.Errorf("connect daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	var resp struct {
		ArchivedPath  *string `json:"archived_path"`
		BigPicture    *string `json:"big_picture"`
		Content       *string `json:"content"`
		DiscoveryHint *string `json:"discovery_hint"`
	}
	req := map[string]string{"agent_id": agentID}
	if err := client.Call("session.archive", req, &resp); err != nil {
		return fmt.Errorf("session.archive: %w", err)
	}

	out := cmd.OutOrStdout()
	if resp.ArchivedPath == nil {
		fmt.Fprintf(out, "No snapshot to archive for %s.\n", agentID)
		return nil
	}
	fmt.Fprintf(out, "Archived: %s\n", *resp.ArchivedPath)
	if resp.BigPicture != nil {
		fmt.Fprintf(out, "Big picture: %s\n", *resp.BigPicture)
	}
	if resp.DiscoveryHint != nil {
		fmt.Fprintln(out, *resp.DiscoveryHint)
	}
	return nil
}

// renderDefault prints the spec §6.2 table: TIMESTAMP / SIZE /
// REASON / SUMMARY columns, descending by timestamp. SUMMARY is
// the first line of the normalized §1 body, or a placeholder when
// the snapshot has no §1 section (auto-extracted snapshots from
// the no-skill-flow path).
func renderDefault(cmd *cobra.Command, agentID string, sessions []SessionEntry) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Sessions for %s (%d total, most recent %s):\n\n",
		agentID, len(sessions), sessions[0].Timestamp.Format("2006-01-02"))
	fmt.Fprintf(out, "%-28s %-7s %-9s %s\n", "TIMESTAMP", "SIZE", "REASON", "SUMMARY")
	for _, s := range sessions {
		summary := firstLine(s.BigPictureNormalized)
		if summary == "" {
			summary = "(no big-picture summary)"
		}
		fmt.Fprintf(out, "%-28s %-7s %-9s %s\n",
			s.Timestamp.Format(time.RFC3339),
			humanSize(s.Size),
			s.Reason,
			summary)
	}
	return nil
}

// renderVerbose emits one session per stanza with the full raw §1
// body indented under each. Spec §6.3: header followed by numbered
// entries; each entry's "Big picture" body preserves the original
// agent-authored line breaks (raw=true on ParseBigPicture). Sessions
// missing §1 (auto-extracted snapshots, agents that pre-date the
// Task 14 skill template) render a "(no §1 section)" placeholder.
func renderVerbose(cmd *cobra.Command, agentID string, sessions []SessionEntry) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Sessions for %s (%d total, most recent %s):\n\n",
		agentID, len(sessions), sessions[0].Timestamp.Format("2006-01-02"))
	for i, s := range sessions {
		fmt.Fprintf(out, "[%d] %s · %s · %s\n",
			i+1, s.Timestamp.Format(time.RFC3339), s.Reason, humanSize(s.Size))
		if s.BigPictureRaw == "" {
			fmt.Fprintln(out, "    (no §1 section in this session)")
		} else {
			fmt.Fprintln(out, "    Big picture:")
			for line := range strings.SplitSeq(s.BigPictureRaw, "\n") {
				fmt.Fprintf(out, "      %s\n", line)
			}
		}
		fmt.Fprintln(out)
	}
	return nil
}

// renderJSON emits newline-delimited JSON records per spec §6.4. One
// record per session, descending by timestamp. The 8-field schema is
// frozen by the spec; downstream consumers (other CLI tools, scripts)
// parse it field-by-field. Uses a typed struct (not map[string]any)
// to lock the field order in JSON output for diff-friendly review.
func renderJSON(cmd *cobra.Command, sessions []SessionEntry) error {
	type jsonRecord struct {
		Timestamp  string `json:"timestamp"`
		Size       int64  `json:"size"`
		Reason     string `json:"reason"`
		Path       string `json:"path"`
		AgentID    string `json:"agent_id"`
		SessionID  string `json:"session_id"`
		MachineID  string `json:"machine_id"`
		BigPicture string `json:"big_picture"`
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	for _, s := range sessions {
		rec := jsonRecord{
			Timestamp:  s.Timestamp.Format(time.RFC3339),
			Size:       s.Size,
			Reason:     s.Reason,
			Path:       s.Path,
			AgentID:    s.AgentID,
			SessionID:  s.SessionID,
			MachineID:  s.MachineID,
			BigPicture: s.BigPictureNormalized,
		}
		if err := enc.Encode(rec); err != nil {
			return fmt.Errorf("encode record: %w", err)
		}
	}
	return nil
}

// listAllAgents walks every identity file in the cwd-anchored
// thrum-root and aggregates each agent's archived sessions. JSON
// mode emits a single global newline-delimited stream sorted
// descending by timestamp (so cross-agent timeline analysis is
// trivial). Default + --verbose modes group by agent with a
// blank-line separator between blocks.
//
// --verbose --json is rejected at the runAgentSessionsList layer
// before this is invoked. --all + explicit agent-id is also
// rejected at runAgentSessionsList.
func listAllAgents(cmd *cobra.Command, verbose, asJSON bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	repoRoot, err := paths.FindThrumRoot(cwd)
	if err != nil {
		return fmt.Errorf("find thrum-root: %w", err)
	}
	// Append .thrum/ — see loadSessionsForAgent doc for the
	// FindThrumRoot return-value convention.
	return listAllAgentsFromThrumRoot(cmd, filepath.Join(repoRoot, ".thrum"), verbose, asJSON)
}

// listAllAgentsFromThrumRoot is the testable core of listAllAgents
// — takes the thrum-root explicitly instead of resolving from cwd.
// Test fixtures call this directly.
func listAllAgentsFromThrumRoot(cmd *cobra.Command, thrumRoot string, verbose, asJSON bool) error {
	idDir := filepath.Join(thrumRoot, "identities")
	entries, err := os.ReadDir(idDir)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(cmd.OutOrStdout(), "No agents registered.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("read identities: %w", err)
	}

	type agentBlock struct {
		AgentID  string
		Sessions []SessionEntry
	}
	var blocks []agentBlock
	var allSessions []SessionEntry // for global JSON sort

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		agentID := strings.TrimSuffix(e.Name(), ".json")
		sessions, err := loadSessionsFromThrumRoot(thrumRoot, agentID)
		if err != nil {
			// Skip agents we can't read — corrupt identity files,
			// transient FS errors. Filing under best-effort: a
			// single bad agent shouldn't blank out the whole
			// `--all` list.
			continue
		}
		if len(sessions) == 0 {
			continue
		}
		blocks = append(blocks, agentBlock{AgentID: agentID, Sessions: sessions})
		allSessions = append(allSessions, sessions...)
	}

	if asJSON {
		sort.Slice(allSessions, func(i, j int) bool {
			return allSessions[i].Timestamp.After(allSessions[j].Timestamp)
		})
		return renderJSON(cmd, allSessions)
	}

	if len(blocks) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No agents with archived sessions.")
		return nil
	}

	// Stable ordering of blocks by agent ID — keeps test snapshots
	// reproducible and the diff-noise across runs minimal.
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].AgentID < blocks[j].AgentID
	})

	out := cmd.OutOrStdout()
	for i, block := range blocks {
		if i > 0 {
			fmt.Fprintln(out)
		}
		if verbose {
			if err := renderVerbose(cmd, block.AgentID, block.Sessions); err != nil {
				return err
			}
		} else {
			if err := renderDefault(cmd, block.AgentID, block.Sessions); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadSessionsForAgent walks the cwd-anchored thrum-root, locates
// the agent's identity file, and reads its sessions/ folder. Returns
// errAgentNotRegistered if no identity file is found for the agent.
//
// Path resolution simplification (Task 10 scope): always uses the
// cwd-anchored thrum-root for the sessions/ folder, regardless of
// agent mode. This works correctly for the single-thrum-root common
// case (CLI run from main repo on persistent agent, or from worktree
// on ephemeral agent). Multi-worktree edge case where CLI is in
// worktree A but querying a persistent agent registered to the
// main repo: would need agent.Mode resolution via the daemon and
// is tracked as a follow-up.
func loadSessionsForAgent(agentID string) ([]SessionEntry, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	// paths.FindThrumRoot returns the REPO root (parent of .thrum/),
	// not .thrum/ itself. loadSessionsFromThrumRoot's parameter is
	// the .thrum/ directory — append the suffix here so the testable
	// core stays parameterized on .thrum/ paths.
	repoRoot, err := paths.FindThrumRoot(cwd)
	if err != nil {
		return nil, fmt.Errorf("find thrum-root: %w", err)
	}
	return loadSessionsFromThrumRoot(filepath.Join(repoRoot, ".thrum"), agentID)
}

// loadSessionsFromThrumRoot is the testable core of
// loadSessionsForAgent — takes the thrum-root explicitly instead
// of resolving from cwd. Test fixtures use this directly.
func loadSessionsFromThrumRoot(thrumRoot, agentID string) ([]SessionEntry, error) {
	// Step 1: verify the agent has an identity file under this thrum-root.
	idPath := filepath.Join(thrumRoot, "identities", agentID+".json")
	if _, err := os.Stat(idPath); errors.Is(err, os.ErrNotExist) {
		return nil, errAgentNotRegistered
	} else if err != nil {
		return nil, fmt.Errorf("stat identity file: %w", err)
	}

	// Step 2: walk the sessions/ folder.
	sessionsDir := filepath.Join(thrumRoot, "agents", agentID, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // folder hasn't been created yet → "none yet"
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read sessions folder: %w", err)
	}

	var sessions []SessionEntry
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), "-restart.md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(sessionsDir, e.Name())
		content, err := os.ReadFile(path) // #nosec G304 -- path under thrumRoot we resolved
		if err != nil {
			continue
		}
		// Parse frontmatter for saved_at (mtime fallback) + §1 body
		// (both normalized + raw for the renderers that need each).
		ts := sessionarchive.ParseSavedAtFrontmatter(string(content), info.ModTime())
		bpNormalized := sessionarchive.ParseBigPicture(content, false)
		bpRaw := sessionarchive.ParseBigPicture(content, true)
		reason := extractFrontmatterField(content, "reason")
		sessionID := extractFrontmatterField(content, "session_id")
		machineID := extractFrontmatterField(content, "machine_id")

		sessions = append(sessions, SessionEntry{
			Timestamp:            ts,
			Size:                 info.Size(),
			Reason:               reason,
			Path:                 path,
			AgentID:              agentID,
			SessionID:            sessionID,
			MachineID:            machineID,
			BigPictureNormalized: bpNormalized,
			BigPictureRaw:        bpRaw,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Timestamp.After(sessions[j].Timestamp)
	})
	return sessions, nil
}

// extractFrontmatterField is a tiny shared helper for pulling
// arbitrary frontmatter keys from snapshot content. Mirrors the
// parsing grammar used by ParseSavedAtFrontmatter (spec §4.4) but
// returns the raw value for non-saved_at keys. Empty string on any
// failure mode.
func extractFrontmatterField(content []byte, key string) string {
	s := string(content)
	rest, ok := strings.CutPrefix(s, "---\n")
	if !ok {
		return ""
	}
	block, _, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return ""
	}
	prefix := key + ":"
	for line := range strings.SplitSeq(block, "\n") {
		value, ok := strings.CutPrefix(line, prefix)
		if !ok {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}

// currentAgentID resolves the cwd-anchored agent's AgentID via the
// daemon's agent.whoami RPC. Returns empty string + nil error when
// no identity is locally configured (caller treats as "explicit
// agent-id required"). Other errors propagate.
func currentAgentID() (string, error) {
	client, err := getClient()
	if err != nil {
		// No daemon → no identity resolution. Treat as "no current
		// identity" rather than propagating a connect error;
		// callers can still pass an explicit agent-id.
		return "", nil
	}
	defer func() { _ = client.Close() }()
	whoami, err := cli.AgentWhoami(client)
	if err != nil {
		return "", nil
	}
	if whoami == nil {
		return "", nil
	}
	return whoami.AgentID, nil
}

// firstLine returns the substring of s up to the first newline,
// or s if there is no newline. Used for the SUMMARY column collapse.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// humanSize renders a byte count as a short human-readable string
// suitable for a fixed-width column ("12K", "1.2M"). Threshold-based;
// no fractional precision below the kilobyte threshold to keep
// alignment predictable.
func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%dK", n/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1fM", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1fG", float64(n)/(1024*1024*1024))
	}
}
