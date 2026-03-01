---
name: thrum-agent
description: >
  Thrum agent coordination reference. Lean complement to the claude-plugin
  skill files. Covers routing rules, key gotchas, and quick command reference.
---

# Thrum - Agent Coordination Reference

> **Primary reference:** Claude Code users should use the Thrum plugin skills
> (`/thrum:*` commands, `thrum prime`). This file covers routing rules and
> gotchas not duplicated elsewhere.
>
> Full messaging docs: https://leonletto.github.io/thrum/docs/messaging.html

## Agent Registration

```bash
# Register at session start (idempotent — safe to re-run)
thrum quickstart --role <role> --module <module> --intent "<description>"

# Common roles: coordinator, implementer, reviewer, planner, tester
# Name must differ from role (registration rejects name==role)
```

Check who's active before sending:

```bash
thrum team                   # List active agents with names and intents
thrum whoami                 # Show your identity
```

## Message Routing Rules

| Address form       | Routing                                          | Use when                        |
| ------------------ | ------------------------------------------------ | ------------------------------- |
| `--to @lead_agent` | Direct to named agent                            | Default for all task messages   |
| `--to @coordinator`| Role fanout — ALL agents with that role (warns)  | Only for intentional group send |
| `--to @backend-team`| Group — all members                             | Team-wide announcements         |
| `--to @everyone`   | Broadcast — all registered agents                | Critical alerts                 |

**Critical:** `@coordinator` is a role, not a name. Sending `--to @coordinator`
fans out to every agent registered with that role and emits a warning. Use
`thrum team` to find the actual agent name, then send `--to @<name>`.

**Unknown recipients are a hard error.** Always verify names with `thrum team`
before sending.

## Quick Command Reference

### Messaging

```bash
thrum send "msg" --to @name              # Direct to agent by name
thrum send "msg" --to @everyone          # Broadcast to all
thrum reply <msg-id> "response"          # Reply (implicit thread)
thrum inbox                              # View inbox (excludes own messages)
thrum inbox --unread                     # Unread only
thrum wait                               # Block until message arrives (30s)
thrum wait --timeout 120s                # Custom timeout (must include unit)
thrum message read --all                 # Mark all as read
```

### Agents & Coordination

```bash
thrum team                               # List active agents
thrum status                             # My status + daemon
thrum who-has <file>                     # Who's editing a file
thrum ping @name                         # Check if agent online
thrum overview                           # Combined status + team + inbox
```

### Groups

```bash
thrum group create <name>                # Create group
thrum group add <name> @agent            # Add agent
thrum group add <name> --role <role>     # Add all agents with role
thrum group list                         # List groups
```

### Sessions & Context

```bash
thrum session set-intent "..."           # Update work description
thrum session end                        # End session when done
thrum context show                       # Show saved work context
thrum prime                              # Full session context
```

### Sync & Daemon

```bash
thrum sync force                         # Force sync (bare `thrum sync` prints help)
thrum sync status                        # Sync state
thrum daemon status                      # Daemon health
```

## Key Gotchas

| Gotcha | Correct usage |
|--------|---------------|
| `thrum sync` (bare) just prints help | Use `thrum sync force` or `thrum sync status` |
| `--timeout` requires a duration unit | `--timeout 120s` not `--timeout 120` |
| `thrum wait --all` does not exist | Use `thrum wait --timeout <duration>` |
| `thrum daemon health` does not exist | Use `thrum daemon status` |
| `thrum context update` does not exist | Use `/thrum:update-context` skill |
| `--foreground` flag not on `daemon start` | Just use `thrum daemon start` |
| `--group` nesting flag not on `group add` | Use `--group <name>` as member arg: `thrum group add leads --group backend-team` |
| Name==role rejected at registration | `--name lead-agent --role coordinator`, not `--name coordinator --role coordinator` |
| Threads are implicit — no explicit commands | Use `thrum reply <msg-id>` to continue a thread |
| `broadcast_message` MCP tool filter fields are dead code | Use `send_message` with `to="@everyone"` |

## Message Listener (CLI fallback)

Sub-agents cannot access MCP tools and fall back to Bash. Use `thrum wait`
directly:

```bash
# Block until a message arrives (use this in listener sub-agent prompts)
cd /path/to/repo && thrum wait --timeout 15m --after -1s --json
```

Use `--after -1s` (negative = "N ago"; avoids catching stale messages). See
[LISTENER_PATTERN.md](../../claude-plugin/skills/thrum/resources/LISTENER_PATTERN.md)
for the full background listener template.

## Thrum + Beads: Division of Responsibility

| Use Thrum for               | Use Beads for                |
|-----------------------------|------------------------------|
| Real-time coordination      | Task tracking and management |
| Status updates between agents | Dependencies and blockers  |
| Code review requests        | Work discovery and filing    |
| Notifications and alerts    | Persistent task state        |

## Plugin Resources

The claude-plugin covers these topics in detail — do not duplicate here:

- Full MCP tools reference → `thrum prime` or SKILL.md
- Listener pattern → `claude-plugin/skills/thrum/resources/LISTENER_PATTERN.md`
- Common anti-patterns → `claude-plugin/skills/thrum/resources/ANTI_PATTERNS.md`
- Complete CLI syntax → `claude-plugin/skills/thrum/resources/CLI_REFERENCE.md`
- Multi-worktree patterns → `claude-plugin/skills/thrum/resources/WORKTREES.md`
- Group management → `claude-plugin/skills/thrum/resources/GROUPS.md`
- Context compaction recovery → `claude-plugin/skills/thrum/resources/MESSAGING.md`

---

**Version:** 2.0 | Condensed from v1.4 — plugin is now primary reference.
