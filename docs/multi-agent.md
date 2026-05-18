## Multi-Agent Support

> See also: [Why Thrum Exists](philosophy.md) for the philosophy behind
> human-directed coordination, [Agent Coordination](agent-coordination.md) for
> workflow patterns and Beads integration,
> [Workflow Templates](workflow-templates.md) for structured planning and
> implementation workflows, [Identity System](identity.md) for agent naming and
> registration.

## Overview

You can run agents in different terminals, worktrees, on different machines, or
across repos. Within a repo they share a single daemon and message store. Each
agent gets a unique identity. You get tools to organize them into teams, detect
and configure any AI coding platform, and recover full session context after
compaction.

**Use [tmux-managed sessions](tmux-sessions.md)** to run your agent team. The
coordinator creates tmux sessions, launches agents, and the daemon delivers
messages instantly — no background listeners, no token burn, no operational
boilerplate. See [Tmux-Managed Sessions](tmux-sessions.md) for the full story.

**Note:** New repos default to single-agent mode (`single_agent_mode: true`).
Run `thrum single-agent-mode false` to enable the features on this page. See
[Single-Agent Mode](single-agent-mode.md).

For cross-repo and cross-machine multi-agent setups, see [Peers](peers.md). The
coordinator/implementer/tester patterns below work the same way whether the
agents are in one repo or many.

**Key multi-agent capabilities:**

- **Tmux-Managed Sessions** (v0.7.1) -- Daemon-driven agent lifecycle with
  instant message delivery and zero background listeners
- **Session Restart** (v0.7.1) -- Agents restart mid-task without losing
  conversation history
- **Runtime Presets** -- Auto-detect and configure Claude Code, Codex, Cursor,
  Gemini, and other AI platforms
- **Context Prime** -- Single command to gather full agent state for session
  initialization or recovery
- **Coordination Tools** -- File ownership tracking, agent presence, and
  efficient message waiting

## Broadcast

Use `--to @everyone` to reach all agents at once:

```bash
thrum send "Deploy complete, all clear" --to @everyone
```

Role-name targets like `@reviewer` fan out to every agent with that role — that
works but is discouraged for day-to-day routing because it causes cross-talk.
Prefer specific agent names (`@reviewer_main`, `@reviewer_backend`) for normal
coordination.

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

Thrum uses 3-tier detection to identify which AI platform is running:

**Tier 1 — File markers** (checked in the repo):

| Marker File             | Detected Runtime |
| ----------------------- | ---------------- |
| `.claude/settings.json` | `claude`         |
| `.codex/`               | `codex`          |
| `.cursorrules`          | `cursor`         |
| `.cursor/rules/`        | `cursor`         |
| `.augment/`             | `auggie`         |
| `.gemini/`              | `gemini`         |

**Tier 2 — Environment variables:**

| Variable            | Detected Runtime |
| ------------------- | ---------------- |
| `CLAUDE_SESSION_ID` | `claude`         |
| `CURSOR_SESSION`    | `cursor`         |
| `GEMINI_CLI`        | `gemini`         |
| `AUGMENT_AGENT`     | `auggie`         |

**Tier 3 — Binary verification** (falls back to PATH scan):

| Binary   | Verification                      | Detected Runtime |
| -------- | --------------------------------- | ---------------- |
| `claude` | `claude --version` matches output | `claude`         |
| `codex`  | `codex --version` matches output  | `codex`          |
| `cursor` | Binary exists on PATH             | `cursor`         |
| `gemini` | Binary exists on PATH             | `gemini`         |

Tiers are checked in order. If no runtime is detected at any tier, Thrum falls
back to CLI-only mode (no MCP configuration generated).

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

Add custom runtime presets via `~/.thrum/runtimes.json`:

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

> **Migration note (v0.9.0):** If you have an existing
> `~/.config/thrum/runtimes.json` (Linux) or
> `~/Library/Application Support/thrum/runtimes.json` (macOS), move it to
> `~/.thrum/runtimes.json`. The old paths are no longer read; custom runtimes
> silently disappear if not migrated.

Custom runtimes appear alongside built-in presets in `thrum runtime list`.

## Context Prime

`thrum prime` gathers identity, session info, team status, unread messages, git
context, and saved context into a single output. Run it at session startup or
after compaction to quickly orient a new or recovering agent.

```bash
thrum prime        # Human-readable summary
thrum prime --json # Structured JSON for LLM consumption
```

See [Context Management](context.md) for full documentation including output
format, graceful degradation behavior, and use cases.

## Multi-Worktree Coordination

Multiple agents can work across git worktrees while sharing a single daemon and
message store.

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

The preferred approach is `thrum worktree create` (alias:
`thrum worktree setup`). It handles git worktree creation, Thrum redirect
wiring, and optional agent registration in one command:

```bash
# Create worktree with redirect wiring only
thrum worktree create auth -b feature/auth
# or equivalently:
thrum worktree setup auth -b feature/auth

# Create worktree, register agent, and create the tmux session in one step
thrum worktree create auth -b feature/auth \
  --name furiosa --role implementer --module auth

# Start the runtime — the agent is NOT running until this step
thrum tmux launch auth
```

The worktree path is `worktrees.base_path/<name>` (default
`~/.workspaces/<repo>/<name>`). When you pass `--name`, `--role`, and
`--module`, the command creates a real tmux session and registers the agent
identity inside it, then prints the next-step `thrum tmux launch` command. The
agent runtime is not started until you run `tmux launch`.

If you need to wire up an existing worktree manually:

```bash
# Create the worktree
git worktree add ~/.workspaces/repo/auth -b feature/auth

# Set up thrum redirect (shares daemon with main repo)
thrum setup --main-repo /path/to/repo

# Or use the setup script
scripts/setup-worktree-thrum.sh ~/.workspaces/repo/auth
```

### Running Multiple Agents

Each agent in a worktree gets its own identity. The integrated path handles
worktree creation and registration together:

```bash
# Create worktrees and register agents in one step, then launch each
thrum worktree create auth -b feature/auth \
  --name furiosa --role implementer --module auth
thrum tmux launch auth

thrum worktree create sync -b feature/sync \
  --name nux --role implementer --module sync
thrum tmux launch sync
```

Or register manually after creating worktrees:

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

All three share the same daemon and can message each other:

```bash
# furiosa sends directly to the coordinator by name (run `thrum team` to find names)
# Using --to @coord_main (agent name) targets one agent.
# Using --to @coordinator (role) fans out to ALL agents with the coordinator role.
thrum send "Auth module complete, tests passing" --to @coord_main

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

### CWD Discipline

Agents that `cd` into another worktree mid-session will trigger the
`cross_worktree` identity guard. Use `THRUM_HOME=/path/to/worktree` to pin the
repo path when crossing worktrees, or run `thrum prime` from the new worktree to
refresh identity. See [Troubleshooting Identity](troubleshooting-identity.md)
for the full guard catalog.

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
prevent two agents from editing the same file at the same time.

### ping: Agent Presence

Check if an agent is online and what they're working on:

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
messages directed to this agent (by name, role, or `@everyone` broadcast).
There's no `--all` flag.

```bash
# Wait for any message addressed to this agent
thrum wait --timeout 5m

# Include messages sent up to 1s ago (--after -15s = "1 second ago"; negative = "N ago")
thrum wait --after -15s --timeout 5m --json

# Wait for mentions of your role
thrum wait --mention @reviewer --timeout 5m
```

| Flag        | Format                             | Description                                                                                            |
| ----------- | ---------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `--timeout` | Duration (e.g., `5m`)              | Max wait time (default: `30s`)                                                                         |
| `--scope`   | Scope string (e.g., `module:auth`) | Only messages matching this scope                                                                      |
| `--mention` | `@role`                            | Wait for mentions of a role                                                                            |
| `--after`   | Relative time (e.g., `-1s`)        | Negative = include messages sent up to N ago; positive = only messages N in the future; omit for "now" |
| `--json`    | Boolean                            | Output messages as JSON                                                                                |

**Exit codes:**

- `0` -- message received
- `1` -- timeout (no messages)
- `2` -- error

## Complete Workflows

### Workflow 1: Notifying Reviewers

```bash
# 1. Implementer finishes work and notifies the specific reviewer
#    (prefer named targets over role fan-out)
thrum send "PR #42 ready for review: JWT auth implementation" \
  --to @reviewer_main --ref pr:42

# 2. The reviewer checks their inbox
thrum inbox --mentions

# 2b. Implementer verifies the review request was sent
thrum sent --to @reviewer_main

# 3. Reviewer responds
thrum reply msg_01HXE... "Reviewed. LGTM with minor comments on error handling."
```

### Workflow 2: Cross-Platform Agent Setup

```bash
# Claude Code agent in its own worktree (integrated path — preferred)
thrum worktree create auth -b feature/auth \
  --name claude_agent --role implementer --module auth --runtime claude
thrum tmux launch auth
# Step 1 creates the worktree, wires the redirect, registers the agent, and
# creates the tmux session. Step 2 starts Claude Code inside the session.

# Or register manually in an existing worktree
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
thrum prime
# Shows: identity, session, team, inbox, git state, saved context

# Check for urgent messages
thrum inbox --unread
thrum sent --unread
thrum message read --all       # Mark all messages as read

# Resume work based on recovered context
thrum agent set-intent "Resuming JWT implementation after compaction"
```

### Workflow 4: Multi-Worktree Team Coordination

```bash
# === Coordinator (main worktree) ===
thrum quickstart --name coord_main --role coordinator --module main

# Preferred: create worktrees, register agents, then launch the runtimes
thrum worktree create auth -b feature/auth \
  --name furiosa --role implementer --module auth
thrum tmux launch auth

thrum worktree create sync -b feature/sync \
  --name nux --role implementer --module sync
thrum tmux launch sync

# Assign work
thrum send "furiosa: implement auth (feature/auth worktree)" --to @furiosa
thrum send "nux: implement sync (feature/sync worktree)" --to @nux

# Check team status
thrum agent list --context
# AGENT        ROLE          MODULE  BRANCH         FILES
# coord_main   coordinator   main    main           -
# furiosa      implementer   auth    feature/auth   src/auth.go
# nux          implementer   sync    feature/sync   internal/sync/loop.go

# Broadcast update to all agents
thrum send "Both features shipping in v0.4" --to @everyone

# === Implementer (auth worktree) ===
cd ~/.workspaces/repo/auth
export THRUM_NAME=furiosa
thrum inbox --unread
thrum sent --to @coord_main
thrum send "Auth complete, 15 tests passing" --to @coord_main
```

## Best Practices

### Runtime Configuration

- **Let Thrum auto-detect** when possible (`--runtime auto` or omit the flag)
- **Set a default runtime** on your machine if you always use the same platform:
  `thrum runtime set-default claude`
- **Use `--dry-run`** to preview generated configs before committing to them

### Context Recovery

- **Run `thrum prime`** at the start of every session
- **Save context** at the end of every session:
  `echo "# Next steps\n- ..." | thrum context save`
- **After compaction**, `thrum prime` has everything you need to resume work

### Coordination

- **Check `who-has`** before editing files another agent might be working on
- **Use `thrum wait`** instead of polling loops for efficient message delivery
- **Set clear intents** so other agents can see what you're working on via
  `thrum agent list --context`

## Running this automatically

If you want the coordinator role to be automated — handing off a plan and having
the orchestrator run the implementers through it — see
[Orchestrator Role](orchestrator-role.md). You still write the plan and you
still merge. The orchestrator handles the middle.

## Next Steps

- [Agent Coordination](agent-coordination.md) — workflow patterns and Beads
  integration for multi-agent teams
- [Identity System](identity.md) — agent naming, registration, and per-worktree
  identity files
- [Messaging](messaging.md) — full send/receive/reply reference including scopes
  and mentions
- [Coordinate Two Agents](guides/coordinate-two-agents.md) — step-by-step
  walkthrough of the most common setup
