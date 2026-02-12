package cli

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// PrimeContext contains all context sections gathered by `thrum context prime`.
type PrimeContext struct {
	Identity    *WhoamiResult        `json:"identity,omitempty"`
	Session     *SessionInfo         `json:"session,omitempty"`
	Agents      *AgentsInfo          `json:"agents,omitempty"`
	Messages    *MessagesInfo        `json:"messages,omitempty"`
	WorkContext *WorkContextInfo     `json:"work_context,omitempty"`
}

// SessionInfo is a simplified session summary for context prime output.
type SessionInfo struct {
	SessionID string `json:"session_id"`
	StartedAt string `json:"started_at,omitempty"`
	Intent    string `json:"intent,omitempty"`
}

// AgentsInfo summarizes the team for context prime output.
type AgentsInfo struct {
	Total  int         `json:"total"`
	Active int         `json:"active"`
	List   []AgentInfo `json:"list"`
}

// MessagesInfo summarizes inbox state for context prime output.
type MessagesInfo struct {
	Unread int       `json:"unread"`
	Total  int       `json:"total"`
	Recent []Message `json:"recent,omitempty"`
}

// WorkContextInfo contains git work context for context prime output.
type WorkContextInfo struct {
	Branch          string   `json:"branch,omitempty"`
	UncommittedFiles []string `json:"uncommitted_files,omitempty"`
	UnmergedCommits  int      `json:"unmerged_commits"`
	Error           string   `json:"error,omitempty"`
}

// ContextPrime gathers comprehensive session context from the daemon and git.
// It gracefully handles missing sections (e.g., no session, no daemon, not a git repo).
func ContextPrime(client *Client) *PrimeContext {
	ctx := &PrimeContext{}

	// 1. Agent identity
	whoami, err := AgentWhoami(client)
	if err == nil {
		ctx.Identity = whoami
	}

	// 2. Session info (derived from whoami)
	if whoami != nil && whoami.SessionID != "" {
		ctx.Session = &SessionInfo{
			SessionID: whoami.SessionID,
			StartedAt: whoami.SessionStart,
		}
	}

	// 3. Agent list
	agents, err := AgentList(client, AgentListOptions{})
	if err == nil {
		info := &AgentsInfo{
			Total: len(agents.Agents),
			List:  agents.Agents,
		}
		// Count active by checking for sessions via listContext
		contexts, ctxErr := AgentListContext(client, "", "", "")
		if ctxErr == nil {
			activeSet := make(map[string]bool)
			for _, c := range contexts.Contexts {
				if c.SessionID != "" {
					activeSet[c.AgentID] = true
				}
			}
			info.Active = len(activeSet)
		}
		ctx.Agents = info
	}

	// 4. Unread messages
	inbox, err := Inbox(client, InboxOptions{PageSize: 10})
	if err == nil {
		info := &MessagesInfo{
			Total:  inbox.Total,
			Unread: inbox.Unread,
		}
		// Include up to 5 most recent messages
		limit := 5
		if len(inbox.Messages) < limit {
			limit = len(inbox.Messages)
		}
		info.Recent = inbox.Messages[:limit]
		ctx.Messages = info
	}

	// 5. Git work context
	ctx.WorkContext = getGitWorkContext()

	return ctx
}

// getGitWorkContext gathers git context from the current directory.
func getGitWorkContext() *WorkContextInfo {
	wc := &WorkContextInfo{}

	// Current branch
	branch, err := gitOutput("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		wc.Error = "not a git repository"
		return wc
	}
	wc.Branch = branch

	// Uncommitted files
	status, err := gitOutput("status", "--porcelain")
	if err == nil && status != "" {
		for _, line := range strings.Split(status, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				// Extract filename (skip the 2-char status prefix + space)
				if len(line) > 3 {
					wc.UncommittedFiles = append(wc.UncommittedFiles, strings.TrimSpace(line[2:]))
				}
			}
		}
	}

	// Unmerged commits count — try upstream tracking branch, then origin/main, then origin/master.
	countStr, err := gitOutput("rev-list", "--count", "@{upstream}..HEAD")
	if err != nil {
		countStr, err = gitOutput("rev-list", "--count", "origin/main..HEAD")
	}
	if err != nil {
		countStr, err = gitOutput("rev-list", "--count", "origin/master..HEAD")
	}
	if err == nil {
		var count int
		if _, err := fmt.Sscanf(countStr, "%d", &count); err == nil {
			wc.UnmergedCommits = count
		}
	}

	return wc
}

// gitOutput runs a git command and returns trimmed stdout.
func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// FormatPrimeContext formats the prime context for human-readable display.
func FormatPrimeContext(ctx *PrimeContext) string {
	var out strings.Builder

	// Identity
	if ctx.Identity != nil {
		out.WriteString(fmt.Sprintf("Agent: @%s (%s)\n", ctx.Identity.Role, ctx.Identity.AgentID))
		if ctx.Identity.Module != "" {
			out.WriteString(fmt.Sprintf("  Module: %s\n", ctx.Identity.Module))
		}
	} else {
		out.WriteString("Agent: not registered\n")
	}

	// Session
	if ctx.Session != nil {
		sessionAge := ""
		if ctx.Session.StartedAt != "" {
			if t, err := time.Parse(time.RFC3339, ctx.Session.StartedAt); err == nil {
				sessionAge = fmt.Sprintf(" (%s)", formatDuration(time.Since(t)))
			}
		}
		out.WriteString(fmt.Sprintf("Session: %s%s\n", ctx.Session.SessionID, sessionAge))
		if ctx.Session.Intent != "" {
			out.WriteString(fmt.Sprintf("  Intent: %s\n", ctx.Session.Intent))
		}
	} else {
		out.WriteString("Session: none\n")
	}

	// Agents
	if ctx.Agents != nil {
		out.WriteString(fmt.Sprintf("\nTeam: %d agents (%d active)\n", ctx.Agents.Total, ctx.Agents.Active))
		for _, agent := range ctx.Agents.List {
			out.WriteString(fmt.Sprintf("  @%s (%s)\n", agent.Role, agent.Module))
		}
	}

	// Messages
	if ctx.Messages != nil {
		if ctx.Messages.Unread > 0 {
			out.WriteString(fmt.Sprintf("\nInbox: %d unread (%d total)\n", ctx.Messages.Unread, ctx.Messages.Total))
		} else {
			out.WriteString(fmt.Sprintf("\nInbox: %d messages (all read)\n", ctx.Messages.Total))
		}
		for _, msg := range ctx.Messages.Recent {
			from := extractRole(msg.AgentID)
			content := msg.Body.Content
			if len(content) > 60 {
				content = content[:57] + "..."
			}
			out.WriteString(fmt.Sprintf("  @%s: %s\n", from, content))
		}
	}

	// Work context
	if ctx.WorkContext != nil {
		if ctx.WorkContext.Error != "" {
			out.WriteString(fmt.Sprintf("\nGit: %s\n", ctx.WorkContext.Error))
		} else {
			out.WriteString(fmt.Sprintf("\nBranch: %s\n", ctx.WorkContext.Branch))
			if ctx.WorkContext.UnmergedCommits > 0 {
				out.WriteString(fmt.Sprintf("  Unmerged commits: %d\n", ctx.WorkContext.UnmergedCommits))
			}
			if len(ctx.WorkContext.UncommittedFiles) > 0 {
				out.WriteString(fmt.Sprintf("  Uncommitted files: %d\n", len(ctx.WorkContext.UncommittedFiles)))
				for _, f := range ctx.WorkContext.UncommittedFiles {
					out.WriteString(fmt.Sprintf("    %s\n", f))
				}
			}
		}
	}

	return out.String()
}
