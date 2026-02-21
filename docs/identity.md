
# Agent Identity & Registration

## Overview

Thrum uses a flexible identity system that supports both human-readable named
agents and deterministic hash-based IDs. Each agent is identified by a
combination of **name** (optional), **role**, and **module** within a
repository. Identity files are stored per-agent in
`.thrum/identities/{name}.json`, supporting multiple agents per worktree.

## Identity Components

### Agent ID

Agents can have one of two ID formats:

**Named agents** (recommended):

```
furiosa
```

When a `--name` flag or `THRUM_NAME` env var is provided, the name **is** the
agent ID. The name becomes the canonical identifier for display, messaging
(`@furiosa`), and file paths.

**Unnamed agents** (hash-based fallback):

```
coordinator_1B9K33T6RK
```

When no name is provided, the agent ID is generated deterministically:

```
hash = crockford_base32(sha256(repo_id + "|" + role + "|" + module))[:10]
agent_id = role + "_" + hash
```

This means:

- **Same role+module** in the same repository with no name set produces the
  **same agent ID**
- Different repositories produce different agent IDs (even with same
  role+module)
- Named agents always use their name as the ID regardless of role/module

**Legacy format** (backward compatible):

```
agent:implementer:9F2K3M1Q8Z
```

The `agent:{role}:{hash}` format is still recognized for backward compatibility
but is no longer generated. Legacy IDs are converted internally via
`AgentIDToName()`.

### Agent Name

Human-readable name for the agent. Names are validated with strict rules:

- **Allowed characters**: lowercase letters (`a-z`), digits (`0-9`), underscores
  (`_`)
- **Rejected**: hyphens, dots, spaces, path separators, uppercase, special
  characters
- **Reserved names**: `daemon`, `system`, `thrum`, `all`, `broadcast`
- **Cannot be empty** (when explicitly provided)
- **Cannot equal the role** (v0.4.5+): names that match the role are rejected to
  prevent routing ambiguity. Use a distinct name, e.g. `--name coord_main --role coordinator`
  instead of `--name coordinator --role coordinator`.

Names must be safe for: file paths, `@mention` targets, JSONL field values, and
git tracking.

**Validation regex**: `^[a-z0-9_]+$`

### Role

The agent's **role** describes what type of work they do.

Common roles:

- `implementer` - Writes code, implements features
- `planner` - Designs architecture, plans work
- `reviewer` - Reviews code, provides feedback
- `tester` - Writes and runs tests
- `coordinator` - Coordinates work across agents

Roles are **user-defined** and can be anything that makes sense for your
workflow.

### Module

The **module** describes which part of the codebase the agent is responsible
for.

Common modules:

- Component names: `auth`, `sync-daemon`, `cli`, `ui`
- Feature areas: `authentication`, `messaging`, `notifications`
- System boundaries: `backend`, `frontend`, `api`

Modules help:

- Organize agent responsibilities
- Prevent conflicts (different agents working on same area)
- Track which agent worked on which code

### Display Name

Optional human-readable display name for UI presentation.

Examples:

- "Auth Implementer"
- "Claude (Planner)"
- "Test Runner Bot"

Set via `--display` flag, `THRUM_DISPLAY` env var, or the `display` field in the
identity file.

## Identity Resolution

Thrum resolves agent identity using a priority chain:

```
1. THRUM_NAME env var → selects identity file     [Highest priority]
   ↓
2. --agent-id flag (CLI)
   ↓
3. CLI flags (--name, --role, --module)
   ↓
4. Environment variables (THRUM_ROLE, THRUM_MODULE, THRUM_DISPLAY)
   ↓
5. Identity file auto-selection (.thrum/identities/ directory)
   ↓
6. Error if required fields missing                [Lowest priority]
```

### Environment Variables

```bash
export THRUM_NAME=furiosa          # Select identity file (highest priority)
export THRUM_ROLE=implementer      # Override role
export THRUM_MODULE=auth           # Override module
export THRUM_DISPLAY="Auth Agent"  # Override display name
```

`THRUM_NAME` environment variable takes highest priority in identity resolution,
overriding the `--agent-id` flag and identity file auto-selection. It is the
primary way to select which identity file to load, especially in multi-agent
worktrees. It is also used by external orchestrators (e.g., Gastown) to inject
identity into agent processes.

When `THRUM_NAME` is set and the corresponding identity file does not exist,
loading **fails** rather than falling back to env vars. This ensures validation
errors are not silently ignored.

`THRUM_ROLE` and `THRUM_MODULE` override the role and module from any identity
file but do not affect which file is loaded.

Use environment variables when:

- Running in CI/CD pipelines
- Automating agent workflows
- External orchestrators inject identity
- Temporary identity changes

### CLI Flags

```bash
thrum agent register --name=furiosa --role=implementer --module=auth
thrum quickstart --name furiosa --role implementer --module auth --intent "Working on auth"
```

CLI flags (`--role`, `--module`) override environment variables and identity
file settings. The `--name` flag is available on `quickstart` and
`agent register`.

**Priority exception**: `THRUM_NAME` env var overrides the `--name` CLI flag.
This allows orchestrators to control identity even when scripts pass a `--name`
argument.

### Identity Files

Identity files are stored in `.thrum/identities/` as individual JSON files named
after the agent:

```
.thrum/identities/
├── furiosa.json           # Named agent
├── nux.json               # Named agent
└── coordinator_1B9K33T6RK.json  # Unnamed agent (hash-based)
```

**Identity file format** (version 2):

```json
{
  "version": 2,
  "repo_id": "r_7K2Q1X9M3P0B",
  "agent": {
    "kind": "agent",
    "name": "furiosa",
    "role": "implementer",
    "module": "auth",
    "display": "Auth Implementer"
  },
  "worktree": "auth",
  "confirmed_by": "human:leon",
  "updated_at": "2026-02-03T18:02:10.000Z"
}
```

**Auto-selection rules** for identity files:

1. If `THRUM_NAME` is set, load `{THRUM_NAME}.json` directly (error if not
   found)
2. If only **one** `.json` file exists in `identities/`, load it automatically
   (solo-agent worktree backward compatibility)
3. If **multiple** files exist and no `THRUM_NAME` is set, return an error
   asking the user to set `THRUM_NAME`

Use identity files when:

- You want persistent identity across sessions
- Working in a dedicated worktree for a specific module
- Running multiple agents in the same worktree
- Want to avoid setting env vars every time

### Name-to-File Mapping

The agent name directly maps to both identity and message files:

| Agent Name               | Identity File                                   | Message File                            |
| ------------------------ | ----------------------------------------------- | --------------------------------------- |
| `furiosa`                | `.thrum/identities/furiosa.json`                | `messages/furiosa.jsonl`                |
| `nux`                    | `.thrum/identities/nux.json`                    | `messages/nux.jsonl`                    |
| `coordinator_1B9K33T6RK` | `.thrum/identities/coordinator_1B9K33T6RK.json` | `messages/coordinator_1B9K33T6RK.jsonl` |

Message files are stored in the sync worktree at
`.git/thrum-sync/a-sync/messages/`.

## Registration

### First-Time Registration

When an agent registers for the first time:

1. Agent calls `agent.register` with role, module, and optional name
2. Daemon generates agent ID (name if provided, otherwise `{role}_{hash}`)
3. Daemon checks for conflicts (same name or same role+module)
4. If no conflict:
   - Writes `agent.register` event to JSONL (in
     `.git/thrum-sync/a-sync/events.jsonl`)
   - Updates SQLite agents table
   - Returns `{agent_id, status: "registered"}`

### Same Agent Returning

When an agent with the **same agent ID** registers again:

- **Without `re_register` flag:** Succeeds silently (no event written), returns
  `{status: "registered"}`
- **With `re_register` flag:** Updates registration (writes event with status
  "updated")

This allows agents to:

- Resume work after daemon restart
- Update their display name
- Refresh registration without conflict

### Re-registration

Use the `re_register` flag when:

- You lost the identity file but know your role+module
- You want to update your display name
- You want to explicitly refresh your registration

```json
{
  "name": "furiosa",
  "role": "implementer",
  "module": "auth",
  "re_register": true
}
```

Response:

```json
{
  "agent_id": "furiosa",
  "status": "updated"
}
```

### Quickstart (Recommended)

The `quickstart` command combines registration, session start, and intent
setting into one step:

```bash
thrum quickstart --name furiosa --role implementer --module auth --intent "Working on auth"
```

This chains: `agent.register` -> `session.start` -> `session.setIntent` (if
intent provided). If a conflict occurs, it automatically retries with
re-register.

## Conflict Detection

A conflict occurs when:

- A **different agent** (different agent ID) tries to register with the **same
  role+module** as an existing registered agent
- A different agent tries to use the **same name** as an existing agent

### Conflict Response

When conflict is detected:

```json
{
  "agent_id": "",
  "status": "conflict",
  "conflict": {
    "existing_agent_id": "furiosa",
    "registered_at": "2026-02-03T10:00:00Z",
    "last_seen_at": "2026-02-03T12:00:00Z"
  }
}
```

### Conflict Resolution Options

#### 1. Change Your Role or Module

The simplest solution -- pick a different area:

```bash
thrum quickstart --name nux --role implementer --module auth_testing
```

#### 2. Use `--force` Flag

Override the existing agent (you take ownership):

```json
{
  "name": "furiosa",
  "role": "implementer",
  "module": "auth",
  "force": true
}
```

**Warning:** This is destructive. The previous agent's database entry is deleted
and replaced. Only use when:

- The previous agent is no longer active
- You are taking over responsibility for this module
- You have coordinated with the team

#### 3. Coordinate with Team

Before using `--force`:

1. Check who the existing agent is
2. Verify they are not actively working
3. Coordinate the handoff
4. Document the change

### Why Conflicts Matter

Conflicts prevent:

- **Accidental overwrites** - Two agents editing same code
- **Unclear ownership** - Who is responsible for this module?
- **Lost work** - Messages/sessions attributed to wrong agent

The conflict system ensures **intentional coordination** when multiple agents
work on the same module.

## Agent Deletion and Cleanup

### Deleting a Single Agent

```bash
thrum agent delete furiosa
```

This removes:

- Identity file (`.thrum/identities/furiosa.json`)
- Message file (`.git/thrum-sync/a-sync/messages/furiosa.jsonl`)
- SQLite database record
- Emits an `agent.cleanup` event in `events.jsonl`

### Orphan Detection and Cleanup

```bash
# Preview orphaned agents (dry-run)
thrum agent cleanup --dry-run

# Delete all orphans without prompting
thrum agent cleanup --force
```

The cleanup command detects orphaned agents by checking:

- Whether the agent's identity file still exists
- Whether the agent's worktree still exists (if applicable)
- How long since the agent was last seen

Orphaned agents are those with:

- Missing identity files
- Missing worktrees
- Stale last-seen timestamps exceeding the threshold

## Identity File Best Practices

### Worktree-Specific Identity

When using git worktrees for different features/modules, each worktree can have
its own identity files:

```bash
# Main worktree - coordinator (name must differ from role)
thrum quickstart --name coord_main --role coordinator --module all

# Auth worktree - auth implementer
cd ~/.workspaces/myapp/auth
thrum quickstart --name furiosa --role implementer --module auth

# Sync worktree - sync implementer
cd ~/.workspaces/myapp/sync
thrum quickstart --name nux --role implementer --module sync
```

Each worktree gets its own `.thrum/identities/` directory (or uses
`.thrum/redirect` to share a common `.thrum/` directory).

### Multi-Agent Worktrees

Multiple agents can operate in the same worktree. Each gets a separate identity
file:

```
.thrum/identities/
├── furiosa.json     # agent working on implementation
├── reviewer.json    # agent reviewing code
```

Use `THRUM_NAME` to select which identity to use:

```bash
THRUM_NAME=furiosa thrum send "Implementation complete"
THRUM_NAME=reviewer thrum send "LGTM, approved"
```

### Gitignore Identity Files

**Important:** The `.thrum/` directory is gitignored on the main branch.
Identity files are **local configuration**, not project code. They should NOT be
committed because:

- Different developers have different roles/modules
- Identity files contain agent-specific information
- Would cause conflicts in version control

## Common Workflows

### New Agent Setup

```bash
# One-step setup (recommended)
thrum quickstart --name furiosa --role implementer --module auth --intent "Starting auth work"

# Or step-by-step
thrum agent register --name=furiosa --role=implementer --module=auth
thrum session start
thrum send "Starting work on auth module"
```

### Check Current Identity

```bash
# See who you are and if you have an active session
thrum agent whoami
```

Response:

```json
{
  "agent_id": "furiosa",
  "role": "implementer",
  "module": "auth",
  "display": "Auth Agent",
  "source": "identity_file",
  "session_id": "ses_01HXE8Z7R9K3Q6M2W8F4VY",
  "session_start": "2026-02-03T10:00:00Z"
}
```

### List All Agents

```bash
# See all registered agents
thrum agent list

# Filter by role
thrum agent list --role=implementer

# Filter by module
thrum agent list --module=auth
```

### MCP Server Identity

The MCP server (`thrum mcp serve`) resolves identity at startup from
`.thrum/identities/{name}.json`. In multi-agent worktrees, use the `--agent-id`
flag or `THRUM_NAME` env var:

```bash
# Use THRUM_NAME
THRUM_NAME=furiosa thrum mcp serve

# Or use --agent-id flag
thrum mcp serve --agent-id furiosa
```

The MCP server requires a named agent (the `name` field must be set in the
identity file).

## ID Generation Functions

All ID generation is in `internal/identity/identity.go`. Thrum uses
[ULID](https://github.com/ulid/spec) (Universally Unique Lexicographically
Sortable Identifier) for all unique IDs, and deterministic hashing for agent and
repository IDs.

### ULID-Based IDs

ULIDs provide globally unique, lexicographically sortable identifiers with
embedded timestamps. They are generated using monotonic entropy with mutex
protection for thread safety.

| ID Type       | Format        | Example                      | Purpose                      |
| ------------- | ------------- | ---------------------------- | ---------------------------- |
| Session ID    | `ses_` + ULID | `ses_01HXE8Z7R9K3Q6M2W8F4VY` | Track agent work periods     |
| Session Token | `tok_` + ULID | `tok_01HXE8Z7R9K3Q6M2W8F4VY` | WebSocket reconnection       |
| Message ID    | `msg_` + ULID | `msg_01HXE8Z7R9K3Q6M2W8F4VY` | Identify messages            |
| Event ID      | `evt_` + ULID | `evt_01HXE8Z7R9K3Q6M2W8F4VY` | Deduplication in JSONL merge |

ULID timestamps can be extracted with `ParseULID()` or `ULIDTimestamp()` for
time-based queries.

### Deterministic IDs

| ID Type            | Format                          | Example                  | Purpose                        |
| ------------------ | ------------------------------- | ------------------------ | ------------------------------ |
| Repository ID      | `r_` + base32(sha256(url))[:12] | `r_7K2Q1X9M3P0B`         | Identify repos across machines |
| Agent ID (named)   | `{name}`                        | `furiosa`                | Named agent identifier         |
| Agent ID (unnamed) | `{role}_{hash10}`               | `coordinator_1B9K33T6RK` | Hash-based agent identifier    |
| User ID            | `user:{username}`               | `user:leon`              | Identify browser/UI users      |

Repository IDs are generated from the normalized Git origin URL (SSH and HTTPS
URLs for the same repo produce the same ID). The normalization strips `.git`
suffixes and converts to lowercase HTTPS format.

### Utility Functions

| Function                      | Description                                            |
| ----------------------------- | ------------------------------------------------------ |
| `ValidateAgentName(name)`     | Validates name against naming rules and reserved words |
| `ParseAgentID(agentID)`       | Extracts role and hash from any agent ID format        |
| `AgentIDToName(agentID)`      | Converts any agent ID format to filename-safe name     |
| `ExtractDisplayName(agentID)` | Extracts `@mention`-style display name from agent ID   |

## Troubleshooting

### "Role not specified" Error

**Cause:** No identity source available.

**Solution:** Set environment variables, create identity file, or use CLI flags:

```bash
thrum quickstart --name myagent --role your_role --module your_module
```

### "Multiple identity files found" Error

**Cause:** Multiple identity files in `.thrum/identities/` and no `THRUM_NAME`
set.

**Solution:** Set the `THRUM_NAME` environment variable to select which agent
you are:

```bash
export THRUM_NAME=furiosa
```

### Registration Conflict

**Cause:** Another agent already registered with same role+module or same name.

**Solution:**

1. Check existing agent: `thrum agent list --role=X --module=Y`
2. Either:
   - Pick a different name or module
   - Use `--force` if you are taking over
   - Coordinate with the team

### Wrong Agent ID

**Cause:** Changed name, role, or module, creating new agent ID.

**Solution:** Agent IDs are deterministic for unnamed agents. To get the same
agent ID:

- Use the **exact same** role and module in the **same repository** (same
  repo_id)
- Or use a **named agent** where the name is always the ID

### Identity Source Confusion

**Cause:** Multiple identity sources (env vars + file + flags).

**Check priority:** `THRUM_NAME` > CLI flags > env vars > identity file

**Debug:**

```bash
# Check what identity will be used
thrum agent whoami

# The "source" field shows which source was used:
# - "environment" = env vars
# - "flags" = CLI flags
# - "identity_file" = .thrum/identities/{name}.json
```

## References

- RPC API: `docs/rpc-api.md` - agent.register, agent.list, agent.whoami,
  agent.delete, agent.cleanup methods
- Design Document: `dev-docs/2026-02-06-jsonl-sharding-and-agent-naming.md` -
  Agent naming system design
- Foundation Code: `internal/identity/identity.go` - ID generation and
  validation functions
- Config Loading: `internal/config/config.go` - Identity resolution chain
- Agent RPC: `internal/daemon/rpc/agent.go` - Registration, deletion, cleanup
  handlers
- MCP Server: `internal/mcp/server.go` - MCP identity loading
