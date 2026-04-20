package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/config"
	agentcontext "github.com/leonletto/thrum/internal/context"
	"github.com/leonletto/thrum/internal/daemon/safecmd"
	"github.com/leonletto/thrum/internal/paths"
	"github.com/leonletto/thrum/internal/runtime"
	ttmux "github.com/leonletto/thrum/internal/tmux"
)

// PrimeContext contains all context sections gathered by `thrum prime`.
type PrimeContext struct {
	Identity            *WhoamiResult    `json:"identity,omitempty"`
	Session             *SessionInfo     `json:"session,omitempty"`
	Agents              *AgentsInfo      `json:"agents,omitempty"`
	Messages            *MessagesInfo    `json:"messages,omitempty"`
	WorkContext         *WorkContextInfo `json:"work_context,omitempty"`
	SyncState           *PrimeSyncInfo   `json:"sync_state,omitempty"`
	RepoPath            string           `json:"repo_path,omitempty"`
	Runtime             string           `json:"runtime,omitempty"`
	SingleAgentMode     bool             `json:"single_agent_mode,omitempty"`
	TmuxMode            bool             `json:"tmux_mode,omitempty"`
	RestartSnapshot     string           `json:"restart_snapshot,omitempty"`
	SavedSessionContext string           `json:"saved_session_context,omitempty"`
}

// PrimeSyncInfo contains sync health for prime output.
type PrimeSyncInfo struct {
	DaemonStatus string `json:"daemon_status"`
	UptimeMs     int64  `json:"uptime_ms,omitempty"`
	SyncState    string `json:"sync_state,omitempty"`
	Version      string `json:"version,omitempty"`
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
	Branch           string   `json:"branch,omitempty"`
	UncommittedFiles []string `json:"uncommitted_files,omitempty"`
	UnmergedCommits  int      `json:"unmerged_commits"`
	Error            string   `json:"error,omitempty"`
}

// ContextPrime gathers comprehensive session context from the daemon and git.
// It gracefully handles missing sections (e.g., no session, no daemon, not a git repo).
// CallerAgentID is optional — when provided, it ensures identity resolution uses the
// local worktree's agent instead of the daemon's default (important for multi-worktree setups).
func ContextPrime(client *Client, callerAgentID ...string) *PrimeContext {
	ctx := &PrimeContext{}

	// Resolve repo path and detect runtime.
	//
	// Prefer process-tree detection over repo-based detection. The process
	// ancestor walk gives us the actual runtime the agent is running under;
	// runtime.DetectRuntime only tells us what the repo is *configured* for.
	if cwd, err := os.Getwd(); err == nil {
		ctx.RepoPath = paths.EffectiveRepoPath(cwd)
		if _, rt := detectAncestor(context.Background()); rt != "" {
			ctx.Runtime = rt
		} else {
			ctx.Runtime = runtime.DetectRuntime(ctx.RepoPath)
		}
	}

	// 1. Agent identity (pass caller ID for correct worktree resolution).
	// All daemon-backed sections below require a non-nil client.
	var whoami *WhoamiResult
	if client != nil && len(callerAgentID) > 0 && callerAgentID[0] != "" {
		w, err := AgentWhoami(client, callerAgentID...)
		if err == nil {
			whoami = w
			ctx.Identity = whoami
		}
	}

	// 2. Session info (derived from whoami)
	if whoami != nil && whoami.SessionID != "" {
		ctx.Session = &SessionInfo{
			SessionID: whoami.SessionID,
			StartedAt: whoami.SessionStart,
		}
	}

	// 3. Agent list
	var agents *ListAgentsResponse
	var err error
	if client != nil {
		agents, err = AgentList(client, AgentListOptions{})
	}
	if err == nil && agents != nil {
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

	// 4. Unread messages (pass caller ID for correct inbox filtering)
	if client != nil && len(callerAgentID) > 0 && callerAgentID[0] != "" {
		inboxOpts := InboxOptions{
			PageSize:      10,
			CallerAgentID: callerAgentID[0],
		}
		// Auto-filter to this agent's messages (matching inboxCmd behavior)
		if whoami != nil {
			inboxOpts.ForAgent = whoami.AgentID
			inboxOpts.ForAgentRole = whoami.Role
		}
		inbox, err := Inbox(client, inboxOpts)
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
	}

	// 5. Git work context
	ctx.WorkContext = getGitWorkContext()

	// 6. Sync/daemon health
	if client != nil {
		var health HealthResult
		if err := client.Call("health", map[string]any{}, &health); err == nil {
			ctx.SyncState = &PrimeSyncInfo{
				DaemonStatus: health.Status,
				UptimeMs:     health.UptimeMs,
				SyncState:    health.SyncState,
				Version:      health.Version,
			}
		}
	}

	// 7. TmuxMode detection. We're in a tmux-managed session if either the
	// current process is running inside tmux (TMUX env var set) or the
	// agent's identity file points at a still-live tmux session we can
	// reach. The identity-file lookup is best-effort — errors are silent.
	if ttmux.InTmux() {
		ctx.TmuxMode = true
	} else if whoami != nil && ctx.RepoPath != "" {
		if idFile, _, err := config.LoadIdentityWithPath(ctx.RepoPath); err == nil && idFile != nil && idFile.TmuxSession != "" {
			sessionName, _, _ := ttmux.ParseTarget(idFile.TmuxSession)
			if ttmux.HasSession(sessionName) {
				ctx.TmuxMode = true
			}
		}
	}

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

// gitOutput runs a git command in the current working directory and returns
// trimmed stdout (routed through safecmd.Git for unified timeout/error handling).
func gitOutput(args ...string) (string, error) {
	out, err := safecmd.Git(context.Background(), ".", args...)
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
		fmt.Fprintf(&out, "Agent: @%s (%s)\n", ctx.Identity.Role, ctx.Identity.AgentID)
		if ctx.Identity.Module != "" {
			fmt.Fprintf(&out, "  Module: %s\n", ctx.Identity.Module)
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
		fmt.Fprintf(&out, "Session: %s%s\n", ctx.Session.SessionID, sessionAge)
		if ctx.Session.Intent != "" {
			fmt.Fprintf(&out, "  Intent: %s\n", ctx.Session.Intent)
		}
	} else {
		out.WriteString("Session: none\n")
	}

	// Agents
	if ctx.Agents != nil {
		fmt.Fprintf(&out, "\nTeam: %d agents (%d active)\n", ctx.Agents.Total, ctx.Agents.Active)
		for _, agent := range ctx.Agents.List {
			fmt.Fprintf(&out, "  @%s (%s)\n", agent.Role, agent.Module)
		}
	}

	// Messages — only show recent messages when there are unread ones
	if ctx.Messages != nil {
		if ctx.Messages.Unread > 0 {
			fmt.Fprintf(&out, "\nInbox: %d unread (%d total) — process these before starting new work\n", ctx.Messages.Unread, ctx.Messages.Total)
			for _, msg := range ctx.Messages.Recent {
				from := extractRole(msg.AgentID)
				content := msg.Body.Content
				if len(content) > 60 {
					content = content[:57] + "..."
				}
				fmt.Fprintf(&out, "  @%s: %s\n", from, content)
			}
		} else {
			fmt.Fprintf(&out, "\nInbox: %d messages (all read)\n", ctx.Messages.Total)
		}
	}

	// Work context
	if ctx.WorkContext != nil {
		if ctx.WorkContext.Error != "" {
			fmt.Fprintf(&out, "\nGit: %s\n", ctx.WorkContext.Error)
		} else {
			fmt.Fprintf(&out, "\nBranch: %s\n", ctx.WorkContext.Branch)
			if ctx.WorkContext.UnmergedCommits > 0 {
				fmt.Fprintf(&out, "  Unmerged commits: %d\n", ctx.WorkContext.UnmergedCommits)
			}
			if len(ctx.WorkContext.UncommittedFiles) > 0 {
				fmt.Fprintf(&out, "  Uncommitted files: %d\n", len(ctx.WorkContext.UncommittedFiles))
				for _, f := range ctx.WorkContext.UncommittedFiles {
					fmt.Fprintf(&out, "    %s\n", f)
				}
			}
		}
	}

	// Sync state
	if ctx.SyncState != nil {
		fmt.Fprintf(&out, "\nDaemon: %s", ctx.SyncState.DaemonStatus)
		if ctx.SyncState.Version != "" {
			fmt.Fprintf(&out, " (v%s)", ctx.SyncState.Version)
		}
		if ctx.SyncState.UptimeMs > 0 {
			fmt.Fprintf(&out, ", up %s", formatDuration(time.Duration(ctx.SyncState.UptimeMs)*time.Millisecond))
		}
		out.WriteString("\n")
		if ctx.SyncState.SyncState != "" {
			fmt.Fprintf(&out, "  Sync: %s\n", ctx.SyncState.SyncState)
		}
	}

	// Section 2: Preamble (role instructions)
	if ctx.RepoPath != "" && ctx.Identity != nil {
		thrumDir := filepath.Join(ctx.RepoPath, ".thrum")
		agentName := ctx.Identity.AgentID
		preamble, err := agentcontext.LoadPreamble(thrumDir, agentName)
		if err == nil && len(preamble) > 0 {
			out.WriteString("\n# Agent Instructions\n\n")
			out.Write(preamble)
			if preamble[len(preamble)-1] != '\n' {
				out.WriteString("\n")
			}
		} else {
			// Fallback: generate in-memory from role
			out.WriteString("\n# Agent Instructions\n\n")
			out.Write(agentcontext.DefaultPreamble())
			out.WriteString("\n")
		}
	}

	// Section 3: Project State
	//
	// project_state.md lives in the main repo's .thrum/context/ and is
	// shared across all worktrees backed by it. Before thrum-92mj this
	// joined ctx.RepoPath (the calling worktree) directly, so feature-
	// worktree agents hit a missing file and the whole section was
	// silently skipped — starting sessions blind to repo structure,
	// decisions, and scope. paths.ResolveThrumDir follows .thrum/redirect
	// when present and returns the worktree-local .thrum/ otherwise, so
	// the same call works for both main-repo and feature-worktree agents.
	if ctx.RepoPath != "" {
		thrumDir, err := paths.ResolveThrumDir(ctx.RepoPath)
		if err != nil {
			// Resolution failed (malformed redirect, missing target).
			// Surface via stderr so the problem is visible — previously
			// all path failures were silent. Continue without blocking;
			// the agent still gets every other section.
			fmt.Fprintf(os.Stderr, "thrum prime: could not resolve .thrum directory for project_state.md: %v\n", err)
		} else {
			projectStatePath := filepath.Join(thrumDir, "context", "project_state.md")
			if data, readErr := os.ReadFile(projectStatePath); readErr == nil && len(data) > 0 { // #nosec G304 -- internal context file resolved via paths.ResolveThrumDir
				// Role-aware filter (thrum-ir2a): coordinator sees the full
				// narrative; implementers/testers/researchers get the
				// architectural subset to avoid flooding context with
				// Recent Sessions and What's Queued blocks they don't act on.
				role := ""
				if ctx.Identity != nil {
					role = ctx.Identity.Role
				}
				data = filterProjectStateSections(data, role)
				// If the filter returned empty bytes (e.g. a file with
				// only non-allowlisted sections for a non-coordinator
				// role), silently skip the Project State block — an
				// empty header with no body would be worse than nothing.
				if len(data) > 0 {
					out.WriteString("\n# Project State\n\n")
					out.WriteString("The following is the current project state that is being maintained ")
					out.WriteString("to give you a full understanding of where you are and what's next.\n\n")
					out.Write(data)
					if data[len(data)-1] != '\n' {
						out.WriteString("\n")
					}
				}
			}
		}
	}

	// Section 4: Session Context (if saved)
	if ctx.SavedSessionContext != "" {
		out.WriteString("\n# Session Context\n\n")
		out.WriteString(ctx.SavedSessionContext)
		if ctx.SavedSessionContext[len(ctx.SavedSessionContext)-1] != '\n' {
			out.WriteString("\n")
		}
	}

	// Section 4.5: Restart Snapshot (if present — consumed from .thrum/restart/)
	if ctx.RestartSnapshot != "" {
		out.WriteString("\n# Previous Session Context\n\n")
		out.WriteString("The following is a conversation log from your previous ")
		out.WriteString("session. Use it to understand what was accomplished and ")
		out.WriteString("continue from where the previous session left off.\n\n")
		out.WriteString(ctx.RestartSnapshot)
		out.WriteString("\n")
	}

	// Sections 5-6: Multi-agent only
	if !ctx.SingleAgentMode && ctx.Identity != nil && ctx.Runtime == "claude" {
		repoPath := ctx.RepoPath
		if repoPath == "" {
			repoPath = "."
		}
		identDir := filepath.Join(repoPath, ".thrum", "identities")
		if entries, err := os.ReadDir(identDir); err == nil && len(entries) > 0 {
			// Section 5: Messaging protocol
			out.WriteString("\n# Multi-Agent Messaging Protocol\n\n")
			if ctx.TmuxMode {
				// Tmux-mode: no listener rules, direct notification delivery
				out.WriteString("## Tmux-Managed Session\n\n")
				out.WriteString("You are running in a tmux-managed session. Message notifications\n")
				out.WriteString("are delivered directly to your input — do NOT spawn a background listener.\n\n")
				out.WriteString("When you see a message notification, check your inbox:\n")
				out.WriteString("  thrum inbox --unread\n\n")
			} else {
				out.WriteString("## Listener Rules\n\n")
				out.WriteString("**Tip:** If tmux is available on your system, consider using ")
				out.WriteString("`thrum tmux start` for zero-cost message delivery instead of ")
				out.WriteString("the listener pattern. See TMUX_SESSIONS.md resource.\n\n")
				out.WriteString("**Keep the listener running at all times.** Spawn it on session start ")
				out.WriteString("and re-arm whenever it completes. The PID file prevents duplicates — ")
				out.WriteString("spawning when one is already running is a no-op.\n\n")
			}
			out.WriteString("1. **Check inbox before starting work and at every breakpoint.** ")
			out.WriteString("`thrum inbox --unread`\n")
			out.WriteString("2. **Send to agent NAMES, never role names.** ")
			out.WriteString("Run `thrum team` first.\n")
			out.WriteString("3. **Save context before compaction.** ")
			out.WriteString("Use `/thrum:update-project` skill.\n\n")
			// Command reference
			out.WriteString("## Commands\n\n")
			out.WriteString("**Check messages:** `thrum inbox --unread`\n")
			out.WriteString("**Send message:** `thrum send \"message\" --to @<agent_name>`\n")
			out.WriteString("**Reply:** `thrum reply <MSG_ID> \"response\"`\n")
			out.WriteString("**Who's online:** `thrum team`\n")
			out.WriteString("**Agent/daemon status:** `thrum status`\n")
			out.WriteString("**Mark all read:** `thrum message read --all`\n\n")
			// Priority handling
			out.WriteString("## Priority Handling\n\n")
			out.WriteString("| Priority | Action |\n")
			out.WriteString("|----------|--------|\n")
			out.WriteString("| `critical` | Stop current work immediately |\n")
			out.WriteString("| `high` | Process at next breakpoint |\n")
			out.WriteString("| `normal` | Process when current sub-task completes |\n")
			out.WriteString("| `low` | Queue, process when convenient |\n\n")
			// Anti-patterns
			out.WriteString("## Anti-Patterns\n\n")
			if !ctx.TmuxMode {
				out.WriteString("- **Deaf Agent** — No listener running. You miss messages.\n")
			}
			out.WriteString("- **Silent Agent** — Never sends status updates.\n\n")

			// Section 6: Listener spawn instructions (legacy mode only)
			if !ctx.TmuxMode {
				agentID := ctx.Identity.AgentID
				out.WriteString("\n## Start Background Message Listener\n\n")
				fmt.Fprintf(&out, "  Task(subagent_type=\"message-listener\", model=\"haiku\",\n")
				fmt.Fprintf(&out, "    prompt=\"Listen for Thrum messages.\\nSTEP_1: %s/scripts/thrum-startup.sh --listener-heartbeat\\nSTEP_2: thrum wait --timeout 8m --after -15s --agent-name %s\")\n", repoPath, agentID)
			}
		}
	}

	return out.String()
}
