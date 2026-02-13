# Boundaries: When to Use Thrum

## Thrum vs TaskList/SendMessage

| Need | Use Thrum | Use TaskList/SendMessage |
|------|-----------|------------------------|
| Cross-worktree messages | `thrum send --to @name` | Not possible |
| Persist across compaction | Messages survive in git | Lost on compaction |
| Background notifications | `thrum wait` + listener pattern | Polling only |
| Multi-machine sync | Git push/pull via daemon | Local only |
| Group messaging | `thrum send --to @group` | Broadcast to all |
| Track who's editing files | `thrum who-has <file>` | Not available |
| Simple session task list | Overkill | TaskList is better |
| Same-session DM | Works but heavier | SendMessage is simpler |

## Thrum vs Beads

| Thrum (messaging) | Beads (task tracking) |
|---|---|
| Real-time coordination | Persistent work items |
| Status updates, Q&A | Dependencies and blockers |
| Review requests | Progress tracking |
| Notifications and alerts | Work discovery |

**They coexist.** Use Beads to track tasks, Thrum to coordinate about them:

```bash
bd update bd-123 --status in_progress       # Track in Beads
thrum send "Starting bd-123" --to @lead     # Announce via Thrum
```

## MCP Server vs CLI

Thrum provides both an MCP server (`thrum mcp serve`) and CLI commands.

| MCP Server | CLI (`thrum` commands) |
|---|---|
| Native tool integration in Claude Code | Shell-out via Bash |
| Real-time subscriptions | `thrum wait` for blocking |
| Structured JSON responses | Human-readable + `--json` flag |
| Requires MCP support in runtime | Works everywhere |

**Rule of thumb:** Use CLI via `Bash(thrum:*)`. The SKILL.md `allowed-tools` is set to `Bash(thrum:*)` — all data is accessed via CLI output, not file reads.

## Decision Flowchart

1. **Need to communicate with agents in other worktrees?** → Thrum
2. **Need messages to survive compaction?** → Thrum
3. **Need background message monitoring?** → Thrum (listener pattern)
4. **Simple same-session task tracking?** → TaskList
5. **Quick one-off DM to teammate in same session?** → SendMessage
6. **Tracking work items with dependencies?** → Beads
7. **No coordination needed at all?** → Neither
