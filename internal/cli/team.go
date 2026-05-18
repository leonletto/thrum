package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/process"
)

// TeamListRequest represents the request for team.list RPC.
type TeamListRequest struct {
	IncludeOffline bool `json:"include_offline,omitempty"`

	// IncludeSystem, when true, surfaces identities marked
	// Reserved=true (e.g. @supervisor_<project>) that are hidden
	// from the default listing. Set via `thrum team --system`.
	IncludeSystem bool `json:"include_system,omitempty"`

	// AgentFilter, when set, restricts the daemon's response to a
	// single agent whose AgentID matches the value and asks the
	// daemon to populate that member's Body via the spec §7.6
	// fallback chain. Backs the `thrum team @<name>` expanded view.
	AgentFilter string `json:"agent_filter,omitempty"`
}

// TeamListResponse represents the response from team.list RPC.
type TeamListResponse struct {
	Members        []TeamMember    `json:"members"`
	SharedMessages *SharedMessages `json:"shared_messages,omitempty"`
}

// SharedMessages contains team-wide message counts (broadcasts + groups).
type SharedMessages struct {
	BroadcastTotal int                 `json:"broadcast_total"`
	Groups         []GroupMessageCount `json:"groups,omitempty"`
}

// GroupMessageCount contains message counts for an agent group.
type GroupMessageCount struct {
	Name  string `json:"name"`
	Total int    `json:"total"`
}

// TeamMember represents a team member's full status for display.
type TeamMember struct {
	AgentID         string       `json:"agent_id"`
	Role            string       `json:"role"`
	Module          string       `json:"module"`
	Display         string       `json:"display,omitempty"`
	Hostname        string       `json:"hostname,omitempty"`
	OriginDaemon    string       `json:"origin_daemon,omitempty"`
	AgentPID        int          `json:"agent_pid,omitempty"`
	Runtime         string       `json:"runtime,omitempty"`
	WorktreePath    string       `json:"worktree,omitempty"`
	SessionID       string       `json:"session_id,omitempty"`
	SessionStart    string       `json:"session_start,omitempty"`
	LastSeen        string       `json:"last_seen,omitempty"`
	Intent          string       `json:"intent,omitempty"`
	CurrentTask     string       `json:"current_task,omitempty"`
	Branch          string       `json:"branch,omitempty"`
	UnmergedCommits int          `json:"unmerged_commits"`
	FileChanges     []FileChange `json:"file_changes,omitempty"`
	InboxTotal      int          `json:"inbox_total"`
	InboxUnread     int          `json:"inbox_unread"`
	Status          string       `json:"status"`
	TmuxSession     string       `json:"tmux_session,omitempty"`
	TmuxState       string       `json:"tmux_state,omitempty"`

	// Reserved marks a daemon-internal pseudo-agent (e.g.
	// @supervisor_<project>) that is hidden from the default
	// `thrum team` output. Only surfaced when IncludeSystem is
	// set on the request.
	Reserved bool `json:"reserved,omitempty"`

	// IsLocal is true when the agent's OriginDaemon matches the local daemon
	// ID or is empty (legacy/fixture entries treated as local). Set by the
	// team.list handler; consumers should gate heartbeat-staleness checks on
	// this field because heartbeats don't propagate across peer daemons.
	IsLocal bool `json:"is_local,omitempty"`

	// Reminders are the open reminder IDs targeted at this agent
	// (state == 'open'). Populated by the daemon's A-B4 substrate.
	Reminders []string `json:"reminders,omitempty"`

	// === B-B1 E6.8 lifecycle fields (Task 56 + batch 2) ===
	//
	// Populated by the daemon's TeamHandler.decorateWithLifecycle
	// from the agents table + scheduler_job_state + B-B1 Migration
	// 27 (agent_lifecycle_events). Surfaced via the expanded view
	// (thrum team @<name>) per spec §7.6.

	// Mode is the registration mode ("persistent" or "ephemeral")
	// per canonical §3.3.
	Mode string `json:"mode,omitempty"`

	// Identity is the identity-lifecycle mode ("long_lived" or
	// "ephemeral") per canonical §3.3.
	Identity string `json:"identity,omitempty"`

	// State is the canonical agent-state vocabulary per spec §6.2
	// (over_budget | dispatched | working | idle | crashed | alive),
	// derived server-side from scheduler_job_state + banner flags.
	// Mirrored here so --json consumers (monitoring dashboards, agent
	// orchestration scripts) see the same per-agent state the daemon
	// rendered, rather than reconstructing the precedence locally.
	State string `json:"state,omitempty"`

	// AutoRespawnDisabledAt is the unix-millisecond timestamp when
	// the auto-respawn loop guard tripped for this agent (zero when
	// not tripped). Surfaces via Banner for human consumption; raw
	// timestamp mirrored here so --json consumers can recompute the
	// banner phrasing or build "how long ago" displays without
	// scraping the rendered string.
	AutoRespawnDisabledAt int64 `json:"auto_respawn_disabled_at,omitempty"`

	// StateMdParseFailedAt is the unix-millisecond timestamp when
	// state.md last failed to parse (zero when clean). Same wire-
	// shape rationale as AutoRespawnDisabledAt above.
	StateMdParseFailedAt int64 `json:"state_md_parse_failed_at,omitempty"`

	// NextRun, LastRun, LastRunState come from scheduler_job_state
	// for scheduled agents (canonical §6). NOT YET POPULATED in
	// v0.11 batch 2 — the scheduler-state join lands with MB-1.S6;
	// fields remain "" until that ships.
	NextRun      string `json:"next_run,omitempty"`
	LastRun      string `json:"last_run,omitempty"`
	LastRunState string `json:"last_run_state,omitempty"`

	// RecentTransitions are the last 5 agent_lifecycle_events
	// formatted as "<RFC3339> · <event_kind> · <reason>".
	RecentTransitions []string `json:"recent_transitions,omitempty"`

	// Usage is today's per-agent telemetry summary. NOT YET
	// POPULATED in v0.11 batch 2 — depends on the MB-1.S6
	// daily_usage_summary table that's deferred to a later
	// epic (.89 follow-up filed: thrum-6qmf.4.90).
	Usage *TeamUsageSummary `json:"usage,omitempty"`

	// Banner is the operator-facing crashed-state explanation
	// when a guard flag is tripped. Empty for non-crashed agents.
	// Per spec §6.2 mapping + §7.6 verbatim phrasing.
	Banner string `json:"banner,omitempty"`

	// Body is the spec §7.6 "what's happening" line for the
	// expanded single-agent view. Populated server-side only when
	// the request's AgentFilter matches this member; empty for the
	// compact view.
	Body string `json:"body,omitempty"`
}

// TeamUsageSummary is today's per-agent telemetry summary
// mirroring rpc.TeamUsageSummary on the wire.
type TeamUsageSummary struct {
	TokensTotal  int64 `json:"tokens_total"`
	MinutesTotal int64 `json:"minutes_total,omitempty"`
}

// FileChange represents a changed file for team display.
type FileChange struct {
	Path         string `json:"path"`
	LastModified string `json:"last_modified"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Status       string `json:"status"`
}

// formatTeamReminders renders the per-agent reminders block for FormatTeam.
// Returns "" when the slice is empty so the caller can splat it
// unconditionally — empty reminders hide the row entirely (most agents
// have none and rendering "(none)" everywhere would dilute the signal).
//
// Layout deviation from A-B4 plan E4.4 acceptance criterion #1: plan
// calls for inline `reminders: [id, id]` "compact view" + separate
// multi-line "expanded view" gated on a `thrum team --agent <name>`
// flag that doesn't exist in current cobra tree. Current `thrum team`
// is uniformly multi-line for every field (Inbox, Branch, Files), so a
// single-line `reminders: [...]` row would be stylistically jarring
// and inconsistent with the rest of the output. Implemented as a
// single multi-line block matching the Files: convention; the Q8
// invariant "IDs only, no body/count/truncation" is honored.
//
// IDs come from the daemon already ordered (by next_reminder_at ASC
// NULLS LAST per Store.OpenForAgent), so this renderer just preserves
// the order.
//
// The synthetic "... +N more" marker from the daemon's
// teamReminderCompactCap passes through unchanged; consumers reading
// past the cap should run 'thrum agent reminder list --target @<name>'
// for the full set.
func formatTeamReminders(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Reminders:\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "  %s\n", id)
	}
	return b.String()
}

// FormatTeamExpanded renders the single-agent expanded view per spec
// §7.6: compact-style header, banner (if any), lifecycle fields
// (Mode/Identity/NextRun/LastRun/Usage), runtime state
// (PID/Tmux/Worktree/Session/Task/Inbox/Branch), recent transitions,
// reminders, then the Body fallback line.
//
// This is the renderer for `thrum team @<name>` (no --journal /
// --files flags). The daemon-populated Body field carries the spec
// §7.6 fallback-chain output (live pane → summary.md → outbound
// message → "no summary").
func FormatTeamExpanded(m *TeamMember) string {
	var out strings.Builder

	// Reuse the compact header (status glyph + agent ID + module).
	summary := &AgentSummary{
		AgentID: m.AgentID,
		Module:  m.Module,
		Intent:  m.Intent,
		Branch:  m.Branch,
		Status:  m.Status,
	}
	out.WriteString(FormatAgentSummaryCompact(summary) + "\n")

	// Banner first if present — it's the highest-signal piece
	// (operator needs to see "auto-respawn disabled" before
	// reading anything else).
	if m.Banner != "" {
		fmt.Fprintf(&out, "Banner:   %s\n", m.Banner)
	}

	// Lifecycle fields per spec §7.6.
	if m.Mode != "" {
		fmt.Fprintf(&out, "Mode:     %s\n", m.Mode)
	}
	if m.Identity != "" {
		fmt.Fprintf(&out, "Identity: %s\n", m.Identity)
	}
	if m.NextRun != "" {
		fmt.Fprintf(&out, "NextRun:  %s\n", m.NextRun)
	}
	if m.LastRun != "" {
		state := ""
		if m.LastRunState != "" {
			state = fmt.Sprintf(" (%s)", m.LastRunState)
		}
		fmt.Fprintf(&out, "LastRun:  %s%s\n", m.LastRun, state)
	}

	// Usage telemetry (MB-1.S6 dependency; usually nil until
	// that substrate ships).
	if m.Usage != nil {
		fmt.Fprintf(&out, "Usage:    %d tokens", m.Usage.TokensTotal)
		if m.Usage.MinutesTotal > 0 {
			fmt.Fprintf(&out, " · %d min", m.Usage.MinutesTotal)
		}
		out.WriteString(" (today UTC)\n")
	}

	// PID liveness, Tmux state, Runtime — same as compact view.
	if m.AgentPID > 0 {
		if process.IsRunning(m.AgentPID) {
			fmt.Fprintf(&out, "PID:      %d [live]\n", m.AgentPID)
		} else {
			fmt.Fprintf(&out, "PID:      %d [stale]\n", m.AgentPID)
		}
	}
	if m.TmuxSession != "" {
		fmt.Fprintf(&out, "Tmux:     %s [%s]\n", m.TmuxSession, m.TmuxState)
	}
	if m.Runtime != "" {
		fmt.Fprintf(&out, "Runtime:  %s\n", m.Runtime)
	}
	if m.WorktreePath != "" {
		fmt.Fprintf(&out, "Worktree: %s\n", filepath.Base(m.WorktreePath))
	}

	// Session, Intent, Task, Inbox, Branch, Files — same renderer
	// helpers as the compact view (slimmed; expanded view doesn't
	// need to duplicate every condition).
	if m.SessionID != "" {
		fmt.Fprintf(&out, "Session:  %s\n", m.SessionID)
	}
	if m.CurrentTask != "" {
		fmt.Fprintf(&out, "Task:     %s\n", m.CurrentTask)
	}
	fmt.Fprintf(&out, "Inbox:    %d unread / %d total\n", m.InboxUnread, m.InboxTotal)
	if m.Branch != "" {
		fmt.Fprintf(&out, "Branch:   %s\n", m.Branch)
	}

	// Recent transitions: the last 5 lifecycle events that drove
	// this agent's state to here. Surfaced inline as a compact
	// list (the full journal is gated behind --journal).
	if len(m.RecentTransitions) > 0 {
		out.WriteString("Recent:\n")
		for _, t := range m.RecentTransitions {
			fmt.Fprintf(&out, "  %s\n", t)
		}
	}

	// Reminders (A-B4 substrate; unchanged from compact view).
	out.WriteString(formatTeamReminders(m.Reminders))

	// Body fallback chain — populated by the daemon when AgentFilter
	// matches. Rendered last so the lifecycle metadata above provides
	// the framing the body's "what's happening" line sits inside.
	if m.Body != "" {
		out.WriteString("\nWhat's happening:\n")
		for line := range strings.SplitSeq(strings.TrimRight(m.Body, "\n"), "\n") {
			out.WriteString("  " + line + "\n")
		}
	}

	return out.String()
}

// JournalRequest mirrors rpc.JournalRequest for `thrum team @<name>
// --journal`.
type JournalRequest struct {
	AgentName string `json:"agent_name"`
}

// JournalResponse mirrors rpc.JournalResponse — the journal payload
// is a pre-formatted multi-line string the CLI prints verbatim.
type JournalResponse struct {
	AgentName string `json:"agent_name"`
	Journal   string `json:"journal"`
}

// FormatJournalSection wraps the daemon-rendered journal output in a
// header so the operator can distinguish the journal block from the
// expanded view above it. Trailing newline-normalised so the section
// renders flush against the next block (or end-of-output) without
// extra blank lines.
func FormatJournalSection(resp *JournalResponse) string {
	if resp == nil {
		return ""
	}
	body := strings.TrimRight(resp.Journal, "\n")
	if body == "" {
		return ""
	}
	return "\nJournal (last events):\n" + body + "\n"
}

// FilesRPCUnavailable is the operator-facing string the --files flag
// emits when the cross-epic `agent.listFiles` RPC isn't registered on
// the daemon. Mirrors the daemon-side rpc.FilesRPCUnavailableMessage
// constant — kept here as a CLI-side echo so the operator-facing
// runbook string stays consistent even when the daemon doesn't get a
// chance to render its own copy (e.g., the probe round-trip itself
// determines absence via JSON-RPC method-not-found rather than a
// response payload).
const FilesRPCUnavailable = "files RPC unavailable in this daemon"

// FormatFilesSection wraps a daemon files response in a section
// header. When the response is nil (RPC unavailable) the rendered
// section surfaces FilesRPCUnavailable so the operator sees an
// explicit reason rather than silence.
func FormatFilesSection(paths []string, available bool) string {
	if !available {
		return "\nFiles:\n  " + FilesRPCUnavailable + "\n"
	}
	if len(paths) == 0 {
		return "\nFiles:\n  (none)\n"
	}
	var out strings.Builder
	out.WriteString("\nFiles:\n")
	for _, p := range paths {
		out.WriteString("  " + p + "\n")
	}
	return out.String()
}

// FormatTeam formats the team list for display.
func FormatTeam(resp *TeamListResponse) string {
	if len(resp.Members) == 0 {
		return "No active agents. Use --all to include offline agents.\n"
	}

	var out strings.Builder

	for i, m := range resp.Members {
		if i > 0 {
			out.WriteString("\n")
		}

		// Header via compact summary format
		summary := &AgentSummary{
			AgentID: m.AgentID,
			Module:  m.Module,
			Intent:  m.Intent,
			Branch:  m.Branch,
			Status:  m.Status,
		}
		out.WriteString(FormatAgentSummaryCompact(summary) + "\n")

		// PID liveness indicator
		if m.AgentPID > 0 {
			if process.IsRunning(m.AgentPID) {
				fmt.Fprintf(&out, "PID:      %d [live]\n", m.AgentPID)
			} else {
				fmt.Fprintf(&out, "PID:      %d [stale]\n", m.AgentPID)
			}
		}

		// Tmux state
		if m.TmuxSession != "" {
			fmt.Fprintf(&out, "Tmux:     %s [%s]\n", m.TmuxSession, m.TmuxState)
		}

		// Runtime
		if m.Runtime != "" {
			fmt.Fprintf(&out, "Runtime:  %s\n", m.Runtime)
		}

		// Worktree and hostname as separate fields
		if m.WorktreePath != "" {
			fmt.Fprintf(&out, "Worktree: %s\n", filepath.Base(m.WorktreePath))
		}
		if m.Hostname != "" {
			fmt.Fprintf(&out, "Host:     %s\n", m.Hostname)
		}

		// Session
		if m.SessionID != "" {
			sessionDisplay := m.SessionID
			if len(sessionDisplay) > 16 {
				sessionDisplay = sessionDisplay[:16] + "..."
			}
			duration := ""
			if m.SessionStart != "" {
				if t, err := time.Parse(time.RFC3339Nano, m.SessionStart); err == nil {
					duration = fmt.Sprintf(" (active %s)", formatDuration(time.Since(t)))
				} else if t, err := time.Parse(time.RFC3339, m.SessionStart); err == nil {
					duration = fmt.Sprintf(" (active %s)", formatDuration(time.Since(t)))
				}
			}
			fmt.Fprintf(&out, "Session:  %s%s\n", sessionDisplay, duration)
		} else if m.Status == "offline" {
			lastSeen := ""
			if m.LastSeen != "" {
				if t, err := time.Parse(time.RFC3339Nano, m.LastSeen); err == nil {
					lastSeen = fmt.Sprintf(" (last seen %s)", formatTimeAgo(t))
				} else if t, err := time.Parse(time.RFC3339, m.LastSeen); err == nil {
					lastSeen = fmt.Sprintf(" (last seen %s)", formatTimeAgo(t))
				}
			}
			fmt.Fprintf(&out, "Session:  offline%s\n", lastSeen)
		}

		// Intent
		if m.Intent != "" {
			fmt.Fprintf(&out, "Intent:   %s\n", m.Intent)
		}

		// Current task
		if m.CurrentTask != "" {
			fmt.Fprintf(&out, "Task:     %s\n", m.CurrentTask)
		}

		// Inbox
		fmt.Fprintf(&out, "Inbox:    %d unread / %d total\n", m.InboxUnread, m.InboxTotal)

		// Reminders (A-B4). Hidden entirely when the agent has none —
		// most agents have no open reminders, so rendering "Reminders:
		// (none)" everywhere would dilute the signal.
		out.WriteString(formatTeamReminders(m.Reminders))

		// Branch
		if m.Branch != "" {
			commitInfo := ""
			if m.UnmergedCommits > 0 {
				commitInfo = fmt.Sprintf(" (%d commits ahead)", m.UnmergedCommits)
			}
			fmt.Fprintf(&out, "Branch:   %s%s\n", m.Branch, commitInfo)
		}

		// Files
		if len(m.FileChanges) > 0 {
			out.WriteString("Files:\n")
			for _, f := range m.FileChanges {
				timeAgo := ""
				if f.LastModified != "" {
					if t, err := time.Parse(time.RFC3339Nano, f.LastModified); err == nil {
						timeAgo = formatTimeAgo(t)
					} else if t, err := time.Parse(time.RFC3339, f.LastModified); err == nil {
						timeAgo = formatTimeAgo(t)
					}
				}
				fmt.Fprintf(&out, "  %-30s %-8s +%-4d -%d\n", f.Path, timeAgo, f.Additions, f.Deletions)
			}
		} else if m.Status == "active" {
			out.WriteString("Files:    (no changes)\n")
		}
	}

	// Footer: shared messages (broadcasts + groups)
	if sm := resp.SharedMessages; sm != nil {
		out.WriteString("\n--- Shared ---\n")
		if sm.BroadcastTotal > 0 {
			fmt.Fprintf(&out, "Broadcasts: %d messages\n", sm.BroadcastTotal)
		}
		for _, g := range sm.Groups {
			fmt.Fprintf(&out, "@%s: %d messages\n", g.Name, g.Total)
		}
	}

	return out.String()
}
