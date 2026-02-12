# Design: `thrum team` — Rich Per-Agent Status

**Date:** 2026-02-12
**Epic:** thrum-clze
**Status:** Approved

## Overview

New CLI command `thrum team` that displays a rich, multi-line status report for
every active agent. Powered by a single server-side SQL JOIN query for instant
response (<10ms). Also adds a `hostname` field to agent registration so the
display can show where each agent is running.

## Context

Current options for viewing team status are incomplete:

| Command                      | Shows                                    | Missing                                         |
| ---------------------------- | ---------------------------------------- | ----------------------------------------------- |
| `thrum status`               | Full detail for current agent only       | Other agents                                    |
| `thrum agent list --context` | Condensed table, all agents              | Session duration, inbox counts, file details     |
| `thrum overview`             | Single-line per teammate                 | Inbox counts, file changes, machine info         |

`thrum team` fills the gap: full `status`-like detail for every agent.

## Architecture

### Single JOIN Query (Not N RPC Calls)

The daemon's SQLite projection is already a materialized view. Data is updated
incrementally as agents work:

- `agents` table: updated on registration
- `sessions` table: updated on session start/heartbeat/end
- `agent_work_contexts`: updated on every heartbeat (git state, intent, task)
- `messages` + `message_reads`: updated on send/read

`thrum team` adds one new RPC method (`team.list`) that runs a single SQL query
joining these tables. No git commands, no per-agent RPC round-trips.

Remote agents are included naturally — Tailscale sync brings their events into
the same SQLite tables.

### Hostname Field

Agents currently have no machine identification. Adding `hostname` to the
`agents` table enables the display to show `machine / worktree` location.

- Set via `os.Hostname()` during registration, `.local` suffix stripped
- `THRUM_HOSTNAME` env var override for friendly names
- Stored in `agents` table and carried in `agent.register` events for sync

## Implementation Details

### Schema Migration v11

```sql
ALTER TABLE agents ADD COLUMN hostname TEXT
```

In `internal/schema/schema.go`, add migration block after v10:

```go
if startVersion < 11 && endVersion >= 11 {
    _, err = tx.Exec(`ALTER TABLE agents ADD COLUMN hostname TEXT`)
    if err != nil {
        return fmt.Errorf("add hostname column: %w", err)
    }
}
```

### Agent Register Changes

**`internal/types/events.go`** — Add `Hostname` to `AgentRegisterEvent`:

```go
type AgentRegisterEvent struct {
    // ... existing fields ...
    Hostname string `json:"hostname,omitempty"`
}
```

**`internal/daemon/rpc/agent.go`** — In `HandleRegister`, capture hostname:

```go
func resolveHostname() string {
    if h := os.Getenv("THRUM_HOSTNAME"); h != "" {
        return h
    }
    h, err := os.Hostname()
    if err != nil {
        return ""
    }
    return strings.TrimSuffix(h, ".local")
}
```

Set `event.Hostname = resolveHostname()` in the register handler.

**`internal/projection/projector.go`** — Update `applyAgentRegister` to store
hostname:

```go
INSERT OR REPLACE INTO agents (agent_id, kind, role, module, display, hostname, registered_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
```

### New RPC Method: `team.list`

**File:** `internal/daemon/rpc/team.go` (new)

```go
type TeamListRequest struct {
    IncludeOffline bool `json:"include_offline,omitempty"`
}

type TeamListResponse struct {
    Members []TeamMember `json:"members"`
}

type TeamMember struct {
    AgentID         string             `json:"agent_id"`
    Role            string             `json:"role"`
    Module          string             `json:"module"`
    Display         string             `json:"display,omitempty"`
    Hostname        string             `json:"hostname,omitempty"`
    WorktreePath    string             `json:"worktree_path,omitempty"`
    SessionID       string             `json:"session_id,omitempty"`
    SessionStart    string             `json:"session_start,omitempty"`
    LastSeen        string             `json:"last_seen,omitempty"`
    Intent          string             `json:"intent,omitempty"`
    CurrentTask     string             `json:"current_task,omitempty"`
    Branch          string             `json:"branch,omitempty"`
    UnmergedCommits int                `json:"unmerged_commits"`
    FileChanges     []types.FileChange `json:"file_changes,omitempty"`
    InboxTotal      int                `json:"inbox_total"`
    InboxUnread     int                `json:"inbox_unread"`
    Status          string             `json:"status"` // "active", "offline"
}
```

**Core SQL query:**

```sql
SELECT
    a.agent_id, a.role, a.module, a.display, a.hostname,
    s.session_id, s.started_at, s.last_seen_at,
    wc.branch, wc.worktree_path, wc.intent, wc.current_task,
    wc.unmerged_commits, wc.file_changes, wc.git_updated_at
FROM agents a
LEFT JOIN sessions s ON s.agent_id = a.agent_id AND s.ended_at IS NULL
LEFT JOIN agent_work_contexts wc ON wc.session_id = s.session_id
WHERE 1=1
ORDER BY s.started_at DESC NULLS LAST
```

Without `include_offline`, add `AND s.session_id IS NOT NULL` to filter to
active agents only.

**Inbox counts** — computed as a second query (simpler than a massive JOIN):

```sql
SELECT
    a.agent_id,
    COUNT(DISTINCT m.message_id) as total,
    COUNT(DISTINCT CASE WHEN mr.message_id IS NULL THEN m.message_id END) as unread
FROM agents a
LEFT JOIN messages m ON m.deleted = 0 AND m.agent_id != a.agent_id
LEFT JOIN message_reads mr ON m.message_id = mr.message_id AND mr.agent_id = a.agent_id
GROUP BY a.agent_id
```

The results are merged in Go by `agent_id`. Two simple queries are more
maintainable than one massive JOIN with inbox subquery.

**Handler registration** in `cmd/thrum/main.go`:

```go
teamHandler := rpc.NewTeamHandler(st)
server.RegisterHandler("team.list", teamHandler.HandleList)
```

### CLI Command: `thrum team`

**`cmd/thrum/main.go`** — Register command:

```go
teamCmd := &cobra.Command{
    Use:   "team",
    Short: "Show status of all active agents",
    RunE:  runTeam,
}
teamCmd.Flags().Bool("json", false, "Output as JSON")
teamCmd.Flags().Bool("all", false, "Include offline agents")
```

**`internal/cli/team.go`** (new) — Format function:

```go
func FormatTeam(members []TeamMember) string
```

Output format per agent:

```
=== @furiosa (implementer @ auth) ===
Location: macbook-pro / feature-auth
Session:  ses_01HXF... (active 2h15m)
Intent:   JWT authentication implementation
Inbox:    3 unread / 12 total
Branch:   feature/auth (3 commits ahead)
Files:
  src/auth.go              5m ago   +413 -187
  src/auth_test.go         12m ago  +89  -12
  internal/config/jwt.go   1h ago   +45  -3

=== @reviewer (reviewer @ all) ===
Location: macbook-pro / main
Session:  ses_01HXG... (active 12m)
Intent:   Reviewing PR #42
Inbox:    0 unread / 5 total
Branch:   main
Files:    (no changes)
```

Uses helper functions from status.go: `formatDuration()`, `formatTimeAgo()`.

## Dependencies

- **thrum-5vvo** (per-file tracking) — Already completed (5b22b46). FileChanges
  field available in `agent_work_contexts`.

## Testing Strategy

**Unit tests:**
- `FormatTeam()` with mock data: 0 agents, 1 agent, multiple, offline agents
- `resolveHostname()` with/without THRUM_HOSTNAME, with/without `.local` suffix
- Schema migration v11 applies cleanly

**Integration tests:**
- Register 2+ agents, verify `team.list` RPC returns correct data
- Send messages, verify inbox counts in team output
- Verify `--json` output is valid JSON
- Verify `--all` includes agents without active sessions
- Verify hostname appears in register event and agents table

**Performance:**
- `team.list` response <50ms for 20 agents (all local SQLite)

## Out of Scope

- Diff bar visualization (sparklines) — nice-to-have, can add later
- Real-time updates via WebSocket — `thrum team` is a snapshot command
- Filtering by role/module — can add flags later if needed
