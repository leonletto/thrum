
## Multi-Agent Support

> See also: [Why Thrum Exists](philosophy.md) for the philosophy behind
> human-directed coordination, [Agent Coordination](agent-coordination.md) for
> workflow patterns and Beads integration,
> [Workflow Templates](workflow-templates.md) for structured planning and
> implementation workflows, [Identity System](identity.md) for agent naming and
> registration.

## Overview

Thrum helps you coordinate multiple AI agents from the ground up. You can run
agents in different terminals, worktrees, or on different machines — they share
a single daemon and message store. Each agent gets a unique identity, and you
get tools to organize agents into teams, detect and configure any AI coding
platform, and recover full session context after compaction.

**Key multi-agent capabilities:**

- **Agent Groups** -- Named collections of agents and roles for targeted
  messaging
- **Runtime Presets** -- Auto-detect and configure Claude Code, Codex, Cursor,
  Gemini, and other AI platforms
- **Context Prime** -- Single command to gather full agent state for session
  initialization or recovery
- **Coordination Tools** -- File ownership tracking, agent presence, and
  efficient message waiting

## Agent Groups

Groups let you send messages to collections of agents without addressing each
one individually. Groups can contain specific agents or all agents with a role.

### Quick Reference

| Command                           | Description                                     |
| --------------------------------- | ----------------------------------------------- |
| `thrum group create NAME`         | Create a new group                              |
| `thrum group delete NAME`         | Delete a group (cannot delete `@everyone`)      |
| `thrum group add GROUP MEMBER`    | Add agent or role                               |
| `thrum group remove GROUP MEMBER` | Remove a member                                 |
| `thrum group list`                | List all groups                                 |
| `thrum group info NAME`           | Show group details                              |
| `thrum group members NAME`        | List members (`--expand` resolves to agent IDs) |

### Auto-Created Groups

Thrum automatically creates two types of system groups:

- **`@everyone`** — Created on daemon startup. Includes all registered agents via
  a `role:*` wildcard. New agents are automatically reachable through it. Cannot
  be deleted or modified.
- **Role groups** — Created automatically when an agent registers with a new role
  (e.g., registering with `--role reviewer` creates a `@reviewer` group containing
  all agents with that role). Role groups have IDs like `grp_role_reviewer` and
  are managed by the system.

This means `@reviewer` always routes to all agents with the `reviewer` role,
even without manually creating or managing a group.

```bash
# Send to all agents
thrum send "Deploy complete, all clear" --to @everyone
```

All three forms are equivalent:

```bash
thrum send "Deploy complete" --broadcast
thrum send "Deploy complete" --to @everyone
thrum send "Deploy complete" --everyone
```

### Creating and Managing Groups

```bash
# Create a group with a description
thrum group create reviewers --description "Code review team"

# Add individual agents
thrum group add reviewers @alice
thrum group add reviewers @bob

# Add all agents with a specific role
thrum group add reviewers --role reviewer

# Remove a member
thrum group remove reviewers @bob

# List all groups
thrum group list

# Show group details
thrum group info reviewers
```

### Member Types

Groups support two member types:

| Type  | Syntax            | Resolves To                          |
| ----- | ----------------- | ------------------------------------ |
| Agent | `@alice`          | A specific agent by name             |
| Role  | `--role reviewer` | All agents registered with that role |

### Sending to Groups

Use `--to @groupname` just like sending to an individual agent:

```bash
# Send to the review team
thrum send "PR #42 ready for review" --to @reviewers

# Send to all developers
thrum send "Code freeze starts now" --to @all-devs
```

The daemon resolves group membership at **read time** using a SQL query. This
means agents added to a group after a message was sent still receive it.

### Expanding Group Membership

Use `--expand` to see which individual agents a group resolves to:

```bash
thrum group members reviewers --expand --json
```

Output:

```json
{
  "members": [
    { "type": "agent", "value": "alice" },
    { "type": "role", "value": "reviewer" }
  ],
  "expanded": ["alice", "bob", "carol"]
}
```

### MCP Group Tools

When using the MCP server (`thrum mcp serve`), groups are managed via native MCP
tools:

| MCP Tool              | Parameters                             | Description                         |
| --------------------- | -------------------------------------- | ----------------------------------- |
| `create_group`        | `name`, `description?`                 | Create a named group                |
| `delete_group`        | `name`                                 | Delete a group                      |
| `add_group_member`    | `group`, `member_type`, `member_value` | Add member to group                 |
| `remove_group_member` | `group`, `member_type`, `member_value` | Remove member from group            |
| `list_groups`         | --                                     | List all groups                     |
| `get_group`           | `name`, `expand?`                      | Get details with optional expansion |

Example MCP usage:

```text
mcp__thrum__create_group(name="backend", description="Backend team")
mcp__thrum__add_group_member(group="backend", member_type="role", member_value="implementer")
mcp__thrum__send_message(to="@backend", content="API changes merged")
```


## Runtime Presets

Thrum supports multiple AI coding platforms through a runtime preset system.
Each preset knows how to configure MCP servers, instruction files, and hooks for
a specific platform.

### Built-in Presets

| Preset   | Display Name    | MCP | Hooks | Instructions File           |
| -------- | --------------- | --- | ----- | --------------------------- |
| `claude` | Claude Code     | Yes | Yes   | `CLAUDE.md`                 |
| `codex`  | OpenAI Codex    | Yes | No    | `AGENTS.md`                 |
| `cursor` | Cursor          | Yes | No    | `.cursorrules`              |
| `gemini` | Google Gemini   | Yes | No    | `~/.gemini/instructions.md` |
| `auggie` | Augment         | No  | No    | `CLAUDE.md`                 |
| `amp`    | Sourcegraph Amp | No  | No    | `CLAUDE.md`                 |

### CLI Commands

```bash
# List all available presets (built-in + user-defined)
thrum runtime list

# Show details for a specific preset
thrum runtime show claude

# Set a default runtime for this machine
thrum runtime set-default claude
```

### Auto-Detection

Thrum can auto-detect which AI platform is running by checking for file markers
and environment variables:

**File markers:**

| Marker File             | Detected Runtime |
| ----------------------- | ---------------- |
| `.claude/settings.json` | `claude`         |
| `.codex`                | `codex`          |
| `.cursorrules`          | `cursor`         |
| `.augment`              | `auggie`         |

**Environment variables:**

| Variable            | Detected Runtime |
| ------------------- | ---------------- |
| `CLAUDE_SESSION_ID` | `claude`         |
| `CURSOR_SESSION`    | `cursor`         |
| `GEMINI_CLI`        | `gemini`         |
| `AUGMENT_AGENT`     | `auggie`         |

If no runtime is detected, Thrum falls back to CLI-only mode (no MCP
configuration generated).

### Integration with init and quickstart

Use `--runtime` to specify or auto-detect the platform:

```bash
# Auto-detect and generate config
thrum init --runtime auto

# Explicit runtime selection
thrum init --runtime claude
# Generates .claude/settings.json with thrum MCP server config

thrum init --runtime cursor
# Generates .cursorrules with thrum instructions

# Quickstart with runtime detection
thrum quickstart --name furiosa --role implementer --module auth \
  --runtime claude --intent "JWT auth"
```

### Dry-Run Mode

Preview what configuration files would be generated without connecting to the
daemon or writing files:

```bash
thrum quickstart --runtime cursor --dry-run
# Shows .cursorrules content and MCP config that would be created
```

### Custom Runtimes

Add custom runtime presets via `~/.config/thrum/runtimes.json` (XDG-aware):

```json
{
  "default_runtime": "claude",
  "custom_runtimes": {
    "my-agent": {
      "name": "my-agent",
      "display_name": "My Custom Agent",
      "command": "my-agent",
      "mcp_supported": true,
      "hooks_supported": false,
      "instructions_file": "AGENTS.md",
      "mcp_config_path": "~/.my-agent/settings.json",
      "setup_notes": "Custom setup instructions"
    }
  }
}
```

Custom runtimes appear alongside built-in presets in `thrum runtime list`.


## Context Prime

After compaction or starting a new session, you want to quickly answer: what was
I working on? Context prime gathers all agent state into a single output so an
agent can pick up where it left off.

### Usage

```bash
# Human-readable summary
thrum context prime

# Structured JSON for LLM consumption
thrum context prime --json
```

### What It Gathers

Context prime collects information from multiple sources in one call:

| Section  | Source          | Content                                         |
| -------- | --------------- | ----------------------------------------------- |
| Identity | Identity file   | Agent ID, role, module, display name            |
| Session  | Daemon RPC      | Session ID, intent, task, uptime                |
| Agents   | Daemon RPC      | List of registered agents with status and roles |
| Inbox    | Daemon RPC      | Unread message count, total messages            |
| Git      | Local git state | Current branch, recent commits, changed files   |
| Context  | Context file    | Saved context from previous session (if exists) |

### Example Output

```text
=== Context Prime for furiosa ===

Identity: furiosa (implementer @ auth)
Session:  ses_01HXF2A9... (active 2h15m)
Intent:   JWT authentication implementation

Agents Online:
  planner    coordinator @ main    (active 5m ago)
  reviewer   reviewer @ all        (active 12m ago)

Inbox: 3 unread / 12 total

Git:
  Branch: feature/auth
  Ahead:  3 commits
  Files:  src/auth.go, src/auth_test.go (modified)

Saved Context:
  # Next Steps
  - Finish JWT implementation
  - Add rate limiting tests
```

### Graceful Degradation

Context prime handles missing components gracefully:

- **No daemon running** -- skips session, agents, and inbox sections
- **No active session** -- shows identity only
- **No git repo** -- skips git section
- **No saved context** -- skips context section
- **No remote** -- skips upstream comparison

This makes it safe to run at any point, even before the daemon is started.

### Use Cases

- **Session startup** -- run at the beginning of every session to understand
  current state
- **After compaction** -- recover full context when the LLM's context window
  resets
- **Handoff between agents** -- incoming agent runs context prime to understand
  the project state
- **Debugging** -- quickly see what the agent knows and what it's working on


## Multi-Worktree Coordination

Multiple agents can operate across git worktrees while sharing a single daemon
and message store.

### How It Works

```text
┌──────────────────────────────────────────────────────────────┐
│                     Main Worktree                             │
│  /path/to/repo                                                │
│  ├── .thrum/                    ← Daemon socket, SQLite, IDs  │
│  ├── .git/thrum-sync/a-sync/   ← JSONL event log             │
│  └── Daemon process (shared)                                  │
├──────────────────────────────────────────────────────────────┤
│                     Feature Worktrees                         │
│  ~/.workspaces/repo/auth/                                     │
│  ├── .thrum/redirect → /path/to/repo/.thrum/                 │
│  └── Uses same daemon, same messages                          │
│                                                               │
│  ~/.workspaces/repo/sync/                                     │
│  ├── .thrum/redirect → /path/to/repo/.thrum/                 │
│  └── Uses same daemon, same messages                          │
└──────────────────────────────────────────────────────────────┘
```

- **One daemon per repository** -- not per worktree
- Feature worktrees use `.thrum/redirect` to point to the main `.thrum/`
  directory
- All agents connect via the same Unix socket
- Each agent has its own identity file and message shard

### Setting Up Feature Worktrees

```bash
# Create the worktree
git worktree add ~/.workspaces/repo/auth -b feature/auth

# Set up thrum redirect (shares daemon with main repo)
thrum setup --main-repo /path/to/repo

# Or use the setup script
scripts/setup-worktree-thrum.sh ~/.workspaces/repo/auth
```

### Running Multiple Agents

Each agent in a worktree gets its own identity:

```bash
# Main worktree: coordinator (name must differ from role)
cd /path/to/repo
thrum quickstart --name coord_main --role coordinator --module main

# Auth worktree: implementer
cd ~/.workspaces/repo/auth
export THRUM_NAME=furiosa
thrum quickstart --name furiosa --role implementer --module auth

# Sync worktree: another implementer
cd ~/.workspaces/repo/sync
export THRUM_NAME=nux
thrum quickstart --name nux --role implementer --module sync
```

All three agents share the same daemon and can message each other:

```bash
# furiosa sends to coordinator
thrum send "Auth module complete, tests passing" --to @coordinator

# coordinator sends to the whole team
thrum send "Both features ready, starting integration" --to @everyone
```

### Multiple Agents in the Same Worktree

Multiple agents can coexist in a single worktree. Each gets a separate identity
file in `.thrum/identities/`:

```text
.thrum/identities/
├── furiosa.json     # implementation agent
├── reviewer.json    # review agent
```

Use `THRUM_NAME` to select which identity to use:

```bash
THRUM_NAME=furiosa thrum send "Implementation complete"
THRUM_NAME=reviewer thrum send "LGTM, approved"
```


## Coordination Tools

### who-has: File Ownership

Check which agents are currently working on a file:

```bash
thrum who-has auth.go
# Output: @furiosa is editing auth.go (feature/auth, 3 unmerged commits)

thrum who-has internal/cli/agent.go
# Output: No agent is currently editing internal/cli/agent.go
```

This queries agent work contexts (tracked via `thrum agent list --context`) to
prevent two agents from editing the same file simultaneously.

### ping: Agent Presence

Check if an agent is online and what they are working on:

```bash
thrum ping @reviewer
# Output:
#   reviewer: active (last seen 2m ago)
#   Intent: Reviewing PR #42

thrum ping @furiosa
# Output:
#   furiosa: offline (last seen 3h ago)
```

Agents are considered `active` if seen within the last 2 minutes, `offline`
otherwise.

### wait: Efficient Message Blocking

Block until a matching message arrives or a timeout expires. This is the
foundation of the message-listener pattern for async agent coordination.

`thrum wait` always filters by the calling agent's identity — it only returns
messages directed to this agent (by name or role group). There is no `--all`
flag; use subscriptions if you need a firehose.

```bash
# Wait for any message addressed to this agent
thrum wait --timeout 5m

# Wait for messages since a time offset (skip old messages)
thrum wait --after -30s --timeout 5m --json

# Wait for mentions of your role
thrum wait --mention @reviewer --timeout 5m
```

| Flag        | Format                       | Description                     |
| ----------- | ---------------------------- | ------------------------------- |
| `--timeout` | Duration (e.g., `5m`)        | Max wait time (default: `30s`)  |
| `--mention` | `@role`                      | Wait for mentions of a role     |
| `--after`   | Relative time (e.g., `-30s`) | Only messages after this offset |
| `--json`    | Boolean                      | Output messages as JSON         |

**Exit codes:**

- `0` -- message received
- `1` -- timeout (no messages)
- `2` -- error


## Complete Workflows

### Workflow 1: Setting Up a Review Team

```bash
# 1. Create a review team group
thrum group create reviewers --description "Code review team"
thrum group add reviewers --role reviewer
thrum group add reviewers @alice

# 2. Implementer finishes work and notifies the team
thrum send "PR #42 ready for review: JWT auth implementation" \
  --to @reviewers --ref pr:42

# 3. Reviewers check their inbox
thrum inbox --mentions

# 4. Reviewer responds
thrum reply msg_01HXE... "Reviewed. LGTM with minor comments on error handling."
```

### Workflow 2: Cross-Platform Agent Setup

```bash
# Claude Code agent
thrum init --runtime claude
thrum quickstart --name claude_agent --role implementer --module auth \
  --runtime claude --intent "Implementing auth"
# Generates .claude/settings.json with MCP config

# Cursor agent (same repo, different terminal)
thrum init --runtime cursor
thrum quickstart --name cursor_agent --role reviewer --module auth \
  --runtime cursor --intent "Reviewing auth"
# Generates .cursorrules with thrum instructions

# Both agents share the same daemon and can message each other
```

### Workflow 3: Session Recovery After Compaction

```bash
# Agent's context window was compacted -- recover state
thrum context prime
# Shows: identity, session, team, inbox, git state, saved context

# Check for urgent messages
thrum inbox --unread

# Resume work based on recovered context
thrum agent set-intent "Resuming JWT implementation after compaction"
```

### Workflow 4: Multi-Worktree Team Coordination

```bash
# === Coordinator (main worktree) ===
thrum quickstart --name coord_main --role coordinator --module main
thrum group create backend --description "Backend team"
thrum group add backend @furiosa
thrum group add backend @nux

# Assign work
thrum send "furiosa: implement auth (feature/auth worktree)" --to @furiosa
thrum send "nux: implement sync (feature/sync worktree)" --to @nux

# Check team status
thrum agent list --context
# AGENT        ROLE          MODULE  BRANCH         FILES
# coord_main   coordinator   main    main           -
# furiosa      implementer   auth    feature/auth   src/auth.go
# nux          implementer   sync    feature/sync   internal/sync/loop.go

# Send update to the whole backend team
thrum send "Both features shipping in v0.4" --to @backend

# === Implementer (auth worktree) ===
cd ~/.workspaces/repo/auth
export THRUM_NAME=furiosa
thrum inbox --unread
thrum send "Auth complete, 15 tests passing" --to @coordinator
```


## Best Practices

### Group Organization

- **Start simple** -- create groups as coordination needs emerge, not upfront
- **Use role-based membership** when possible (`--role reviewer`) so new agents
  with that role are automatically included
- **Use `@everyone` sparingly** -- prefer targeted groups to reduce noise

### Runtime Configuration

- **Let Thrum auto-detect** when possible (`--runtime auto` or omit the flag)
- **Set a default runtime** on your machine if you always use the same platform:
  `thrum runtime set-default claude`
- **Use `--dry-run`** to preview generated configs before committing to them

### Context Recovery

- **Run `thrum context prime`** at the start of every session
- **Save context** at the end of every session:
  `echo "# Next steps\n- ..." | thrum context save`
- **After compaction**, context prime provides everything needed to resume work

### Coordination

- **Check `who-has`** before editing files another agent might be working on
- **Use `thrum wait`** instead of polling loops for efficient message delivery
- **Set clear intents** so other agents can see what you are working on via
  `thrum agent list --context`

## See Also

- [Tailscale Sync](tailscale-sync.md) -- Cross-machine sync via Tailscale with
  Ed25519 signing and peer discovery
- [Agent Coordination](agent-coordination.md) -- Workflow patterns and Beads
  integration
- [Identity System](identity.md) -- Agent naming, registration, and conflict
  resolution
- [Messaging System](messaging.md) -- Message structure, threads, scopes, and
  refs
- [MCP Server](mcp-server.md) -- MCP tools for AI agent integration
- [Context Management](context.md) -- Per-agent context storage and preambles
- [CLI Reference](cli.md) -- Complete command documentation
```
