package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// TeamListRequest represents the request for team.list RPC.
type TeamListRequest struct {
	IncludeOffline bool `json:"include_offline,omitempty"`
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
	WorktreePath    string       `json:"worktree_path,omitempty"`
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
}

// FileChange represents a changed file for team display.
type FileChange struct {
	Path         string `json:"path"`
	LastModified string `json:"last_modified"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Status       string `json:"status"`
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

		// Header: === @name (role @ module) ===
		name := m.AgentID
		out.WriteString(fmt.Sprintf("=== @%s (%s @ %s) ===\n", name, m.Role, m.Module))

		// Location: hostname / worktree
		hostname := m.Hostname
		worktree := ""
		if m.WorktreePath != "" {
			worktree = filepath.Base(m.WorktreePath)
		}
		if hostname != "" || worktree != "" {
			loc := hostname
			if worktree != "" {
				if loc != "" {
					loc += " / " + worktree
				} else {
					loc = worktree
				}
			}
			out.WriteString(fmt.Sprintf("Location: %s\n", loc))
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
			out.WriteString(fmt.Sprintf("Session:  %s%s\n", sessionDisplay, duration))
		} else if m.Status == "offline" {
			lastSeen := ""
			if m.LastSeen != "" {
				if t, err := time.Parse(time.RFC3339Nano, m.LastSeen); err == nil {
					lastSeen = fmt.Sprintf(" (last seen %s)", formatTimeAgo(t))
				} else if t, err := time.Parse(time.RFC3339, m.LastSeen); err == nil {
					lastSeen = fmt.Sprintf(" (last seen %s)", formatTimeAgo(t))
				}
			}
			out.WriteString(fmt.Sprintf("Session:  offline%s\n", lastSeen))
		}

		// Intent
		if m.Intent != "" {
			out.WriteString(fmt.Sprintf("Intent:   %s\n", m.Intent))
		}

		// Current task
		if m.CurrentTask != "" {
			out.WriteString(fmt.Sprintf("Task:     %s\n", m.CurrentTask))
		}

		// Inbox
		out.WriteString(fmt.Sprintf("Inbox:    %d unread / %d total\n", m.InboxUnread, m.InboxTotal))

		// Branch
		if m.Branch != "" {
			commitInfo := ""
			if m.UnmergedCommits > 0 {
				commitInfo = fmt.Sprintf(" (%d commits ahead)", m.UnmergedCommits)
			}
			out.WriteString(fmt.Sprintf("Branch:   %s%s\n", m.Branch, commitInfo))
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
				out.WriteString(fmt.Sprintf("  %-30s %-8s +%-4d -%d\n", f.Path, timeAgo, f.Additions, f.Deletions))
			}
		} else if m.Status == "active" {
			out.WriteString("Files:    (no changes)\n")
		}
	}

	// Footer: shared messages (broadcasts + groups)
	if sm := resp.SharedMessages; sm != nil {
		out.WriteString("\n--- Shared ---\n")
		if sm.BroadcastTotal > 0 {
			out.WriteString(fmt.Sprintf("Broadcasts: %d messages\n", sm.BroadcastTotal))
		}
		for _, g := range sm.Groups {
			out.WriteString(fmt.Sprintf("@%s: %d messages\n", g.Name, g.Total))
		}
	}

	return out.String()
}
