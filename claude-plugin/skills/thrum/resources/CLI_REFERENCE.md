# CLI Reference

Complete command syntax for `thrum`. Run `thrum <command> --help` for detailed
flag descriptions.

## Global Flags

These flags apply to every command:

```bash
--json            JSON output for scripting
--quiet           Suppress non-essential output
--verbose         Debug output
--repo <path>     Repository path (default: ".")
--role <role>     Agent role (or THRUM_ROLE env var)
--module <mod>    Agent module (or THRUM_MODULE env var)
```

---

## Messaging

### send

```bash
thrum send <message> --to @name
thrum send <message> --to @everyone            # Broadcast to all agents
thrum send <message> --everyone                # Alias for --to @everyone
thrum send <message> --broadcast               # Alias for --to @everyone
thrum send <message> --to @name --scope type:value --scope type:value2
thrum send <message> --to @name --ref type:value
thrum send <message> --to @name --mention @role
thrum send <message> --to @name --format plain
thrum send <message> --to @name --structured '{"key":"value"}'
```

Flags:
```
--to string           Direct recipient (format: @role or @name)
--everyone            Send to all agents (alias for --to @everyone)
-b, --broadcast       Send to all agents (alias for --to @everyone)
--mention strings     Mention a role (repeatable, format: @role)
--ref strings         Add reference (repeatable, format: type:value)
--scope strings       Add scope (repeatable, format: type:value)
--format string       Message format: markdown, plain, json (default "markdown")
--structured string   Structured payload (JSON)
```

### reply

```bash
thrum reply <msg-id> <response>
thrum reply <msg-id> <response> --format plain
```

Flags:
```
--format string   Message format: markdown, plain, json (default "markdown")
```

### inbox

```bash
thrum inbox                                    # Show inbox (auto-filtered to you)
thrum inbox --unread                           # Only unread messages
thrum inbox --all                              # All messages (no auto-filter)
thrum inbox --mentions                         # Only messages mentioning you
thrum inbox --scope type:value                 # Filter by scope
thrum inbox --page 2 --page-size 20            # Pagination
thrum inbox --limit N                          # Alias for --page-size
```

Flags:
```
-a, --all           Show all messages (disable auto-filtering)
--unread            Only unread messages
--mentions          Only messages mentioning me
--scope string      Filter by scope (format: type:value)
--page int          Page number (default 1)
--page-size int     Results per page (default 10)
--limit int         Alias for --page-size
```

### wait

Blocks until a matching message arrives or timeout. Exit codes: 0=message, 1=timeout, 2=error.

```bash
thrum wait                                     # Wait for any message (30s default)
thrum wait --timeout 5m                        # Wait up to 5 minutes
thrum wait --timeout 15m --after -1s           # Only new messages (last 1 second)
thrum wait --mention @reviewer                 # Wait for mention of role
thrum wait --scope module:auth                 # Wait for scoped message
thrum wait --after -30s                        # Accept messages from last 30 seconds
thrum wait --after -5m --json                  # JSON output
```

Flags:
```
--timeout string   Max wait time with unit (e.g., 30s, 5m) (default "30s")
--after string     Only return messages after this relative time (e.g., -30s, -5m, +60s)
--mention string   Wait for mentions of role (format: @role)
--scope string     Filter by scope (format: type:value)
```

Note: `--after` defaults to "now" when not specified (only new messages).
Note: `--timeout` requires a Go duration unit — use `120s` not `120`.

### message

```bash
thrum message get <msg-id>                     # Get full message details
thrum message edit <msg-id> <new-text>         # Full replacement (author only)
thrum message delete <msg-id> --force          # Delete (--force required)
thrum message read <msg-id> [<msg-id>...]      # Mark as read
thrum message read --all                       # Mark all as read
```

Subcommand flags:
```
# message delete
--force   Confirm deletion (required)

# message read
--all     Mark all unread messages as read
```

---

## Agent Management

### quickstart

Bootstrap a full session in one command: detect runtime, generate config, register, start, set intent.

```bash
thrum quickstart --role implementer --module auth
thrum quickstart --role reviewer --module auth --intent "Reviewing PR #42"
thrum quickstart --name alice --role impl --module auth --runtime codex
thrum quickstart --role planner --module core --no-init
thrum quickstart --role tester --module api --dry-run
```

Flags:
```
--role string            Agent role (or THRUM_ROLE env var)
--module string          Agent module (or THRUM_MODULE env var)
--name string            Human-readable agent name (defaults to role_hash)
--display string         Display name for the agent
--intent string          Initial work intent description
--runtime string         Runtime preset: claude, codex, cursor, gemini, auggie, cli-only
--preamble-file string   Custom preamble file to compose with default preamble
--no-init                Skip runtime config generation, just register agent
--force                  Overwrite existing runtime config files
--dry-run                Preview changes without writing files or registering
```

### agent register

```bash
thrum agent register --role implementer --module auth
thrum agent register --name alice --role impl --module auth
thrum agent register --force                   # Override existing
thrum agent register --re-register             # Re-register same agent
```

Flags:
```
--role string      Agent role (or THRUM_ROLE env var)
--module string    Agent module (or THRUM_MODULE env var)
--name string      Human-readable agent name (defaults to role_hash)
--display string   Display name for the agent
--force            Force registration (override existing)
--re-register      Re-register same agent
```

### agent list

```bash
thrum agent list                               # List all registered agents
thrum agent list --role reviewer               # Filter by role
thrum agent list --module auth                 # Filter by module
thrum agent list --context                     # Show work context (branch, commits, intent)
thrum agent list --json
```

Flags:
```
--context         Show work context (branch, commits, intent)
--role string     Filter by role
--module string   Filter by module
```

### agent context

```bash
thrum agent context                            # List all active work contexts
thrum agent context @planner                   # Detail for specific agent
thrum agent context --branch feature/auth      # Filter by branch
thrum agent context --file auth.go             # Filter by changed file
```

Flags:
```
--agent string    Filter by agent role
--branch string   Filter by branch
--file string     Filter by changed file
```

### agent set-intent / session set-intent

```bash
thrum agent set-intent "Fixing memory leak in connection pool"
thrum session set-intent "Refactoring login flow"
thrum agent set-intent ""                      # Clear intent
```

### agent set-task / session set-task

```bash
thrum agent set-task beads:thrum-xyz
thrum session set-task "JIRA-1234"
thrum agent set-task ""                        # Clear task
```

### agent heartbeat / session heartbeat

```bash
thrum agent heartbeat
thrum session heartbeat
thrum session heartbeat --add-scope module:auth
thrum session heartbeat --remove-ref pr:42
thrum session heartbeat --add-ref pr:123 --remove-scope module:old
```

Flags:
```
--add-scope strings      Add scope (repeatable, format: type:value)
--remove-scope strings   Remove scope (repeatable, format: type:value)
--add-ref strings        Add ref (repeatable, format: type:value)
--remove-ref strings     Remove ref (repeatable, format: type:value)
```

### agent start / session start

```bash
thrum agent start
thrum session start
```

### agent end / session end

```bash
thrum agent end
thrum session end
thrum session end --reason crash
thrum session end --session-id <id>
```

Flags:
```
--reason string       End reason: normal|crash (default "normal")
--session-id string   Session ID to end (defaults to current session)
```

### agent cleanup

```bash
thrum agent cleanup                            # Interactive cleanup of orphaned agents
thrum agent cleanup --dry-run                  # List orphans without deleting
thrum agent cleanup --force                    # Delete all orphans without prompting
thrum agent cleanup --threshold 60             # Custom staleness threshold (days)
```

Flags:
```
--dry-run         List orphans without deleting
--force           Delete all orphans without prompting
--threshold int   Days since last seen to consider agent stale (default 30)
```

### agent delete

```bash
thrum agent delete <name>
thrum agent delete coordinator_1B9K --force
```

Flags:
```
--force   Skip confirmation prompt
```

### agent whoami / whoami

```bash
thrum agent whoami
thrum whoami
thrum whoami --json
```

### session list

```bash
thrum session list                             # List all sessions
thrum session list --active                    # Only active sessions
thrum session list --agent <agent-id>          # Filter by agent
thrum session list --json
```

Flags:
```
--active         Show only active sessions
--agent string   Filter by agent ID
```

### status

```bash
thrum status                                   # Current agent, session, inbox counts, sync state
thrum status --json
```

### team

```bash
thrum team                                     # Rich multi-line status for all active agents
thrum team --all                               # Include offline agents
thrum team --json
```

Flags:
```
--all   Include offline agents
```

---

## Coordination

```bash
thrum who-has <file>                           # Check which agents are editing a file
thrum ping @name                               # Check agent presence
thrum ping planner                             # @ prefix optional
thrum overview                                 # Combined: identity + team + inbox + sync
thrum overview --json
```

---

## Groups

```bash
thrum group create <name>                      # Create a group
thrum group create <name> --description "text"
thrum group delete <name>                      # Delete a group
thrum group list                               # List all groups
thrum group list --json
thrum group info <name>                        # Detailed group info
thrum group info <name> --json
thrum group members <name>                     # List group members
thrum group members <name> --expand            # Resolve nested groups/roles to agents
thrum group members <name> --json
thrum group add <group> @agent                 # Add agent by name
thrum group add <group> --role reviewer        # Add all agents with role
thrum group remove <group> @agent              # Remove agent
thrum group remove <group> --role reviewer     # Remove role-based member
```

Subcommand flags:
```
# group create
--description string   Group description

# group add / group remove
--role string   Add/remove a role-based member

# group members
--expand   Resolve nested groups and roles to agent IDs
```

---

## Context

```bash
thrum prime                                    # Gather full session context (AI-optimized)
thrum prime --json
thrum context prime                            # Alias for thrum prime
thrum context show                             # Show saved agent context
thrum context show --agent coordinator         # Show another agent's context
thrum context show --raw                       # Raw output with file boundary markers
thrum context show --no-preamble               # Exclude preamble from output
thrum context load                             # Alias for context show
thrum context save                             # Save context from stdin
thrum context save --file path/to/context.md   # Save context from file
thrum context save --agent other_agent
thrum context clear                            # Clear saved context
thrum context clear --agent coordinator
thrum context sync                             # Sync context to a-sync branch
thrum context sync --agent coordinator
thrum context preamble                         # Show current preamble
thrum context preamble --init                  # Create/reset to default preamble
thrum context preamble --file path.md          # Set preamble from file
thrum context preamble --agent coordinator
thrum context update                           # Delegates to /update-context skill
```

`context show` flags:
```
--agent string    Override agent name
--raw             Raw output with file boundary markers, no header
--no-preamble     Exclude preamble from output
```

`context save` flags:
```
--agent string   Override agent name
--file string    Read context from file (default: stdin)
```

`context preamble` flags:
```
--agent string   Override agent name
--file string    Set preamble from file
--init           Create or reset to default preamble
```

`context clear` / `context sync` flags:
```
--agent string   Override agent name
```

---

## Daemon

```bash
thrum daemon start                             # Start daemon in background
thrum daemon stop                              # Stop daemon gracefully
thrum daemon restart                           # Restart (preserves WebSocket port)
thrum daemon status                            # Show daemon status
thrum daemon status --json
thrum daemon start --local                     # Local-only mode (no git push/fetch)
```

The `--local` flag is available on all daemon subcommands:
```
--local   Local-only mode: skip git push/fetch in sync loop
```

Note: `--foreground` flag does NOT exist. Daemon always starts in background.

---

## Init

```bash
thrum init                                     # Init + interactive runtime selection
thrum init --runtime claude                    # Init + generate Claude configs
thrum init --runtime codex --force             # Init + overwrite Codex configs
thrum init --runtime all --dry-run             # Preview all runtime configs
thrum init --stealth                           # Zero tracked-file footprint
thrum init --agent-role implementer --agent-module auth --agent-name alice
```

Flags:
```
--runtime string        Generate runtime-specific configs: claude|codex|cursor|gemini|cli-only|all
--stealth               Use .git/info/exclude instead of .gitignore (zero footprint)
--force                 Force reinitialization / overwrite existing files
--dry-run               Preview changes without writing files
--agent-role string     Agent role for templates (default: implementer)
--agent-module string   Agent module for templates (default: main)
--agent-name string     Agent name for templates (default: default_agent)
```

---

## Setup

```bash
thrum setup worktree                           # Set up redirect in feature worktree (default)
thrum setup worktree --main-repo /path/to/main
thrum setup claude-md                          # Generate CLAUDE.md content (stdout)
thrum setup claude-md --apply                  # Append to CLAUDE.md
thrum setup claude-md --apply --force          # Overwrite existing Thrum section
```

`setup worktree` flags:
```
--main-repo string   Path to the main repository (where daemon runs) (default ".")
```

`setup claude-md` flags:
```
--apply   Append to CLAUDE.md (create if missing)
--force   Overwrite existing Thrum section
```

---

## Sync

```bash
thrum sync force                               # Force immediate sync (non-blocking)
thrum sync status                              # Show sync status, last sync time, errors
thrum sync status --json
```

Note: `thrum sync` alone prints help — use `thrum sync force` or `thrum sync status`.

---

## Backup

```bash
thrum backup                                   # Snapshot all thrum data
thrum backup --dir /path/to/backup             # Override backup directory
thrum backup status                            # Show last backup info
thrum backup config                            # Show effective backup config
thrum backup restore                           # Restore from latest backup
thrum backup restore archive.zip               # Restore from specific archive
thrum backup restore --yes                     # Skip confirmation
thrum backup plugin list                       # List configured plugins
thrum backup plugin add --preset beads         # Add built-in preset
thrum backup plugin add --name myplugin --command "cmd" --include "*.json"
thrum backup plugin remove --name myplugin
```

`backup` flags (inherited by all subcommands):
```
--dir string   Override backup directory
```

`backup plugin add` flags:
```
--preset string     Use built-in preset: beads, beads-rust
--name string       Plugin name
--command string    Command to run before collecting files
--include strings   File patterns to collect (glob, repeatable)
```

`backup restore` flags:
```
--yes   Skip confirmation prompt
```

---

## Peer

```bash
thrum peer add                                 # Start pairing (displays 4-digit code, blocks 5min)
thrum peer join <address>                      # Join remote peer (prompts for code)
thrum peer list                                # List paired peers
thrum peer remove <name>                       # Remove a paired peer
thrum peer status                              # Detailed sync status for all peers
```

---

## Subscribe / Unsubscribe

```bash
thrum subscribe --scope module:auth            # Subscribe to scoped messages
thrum subscribe --mention @reviewer            # Subscribe to role mentions
thrum subscribe --all                          # Subscribe to all messages (firehose)
thrum subscriptions                            # List active subscriptions
thrum unsubscribe <subscription-id>            # Remove a subscription
```

`subscribe` flags:
```
--scope string     Subscribe to scope (format: type:value)
--mention string   Subscribe to mentions of role
--all              Subscribe to all messages
```

---

## Runtime

```bash
thrum runtime list                             # List all runtime presets
thrum runtime list --json
thrum runtime show <name>                      # Show details for a preset
thrum runtime show claude --json
thrum runtime set-default <name>               # Set the default runtime preset
```

Supported runtimes: `claude`, `codex`, `cursor`, `gemini`, `auggie`, `cli-only`

---

## Role Templates

```bash
thrum roles list                               # List templates and matching agents
thrum roles deploy                             # Re-render preambles from templates (all agents)
thrum roles deploy --agent alice               # Deploy for a specific agent
thrum roles deploy --dry-run                   # Preview changes without writing files
```

`roles deploy` flags:
```
--agent string   Deploy for a specific agent only
--dry-run        Preview changes without writing files
```

---

## MCP Server

```bash
thrum mcp serve                                # Start MCP stdio server
thrum mcp serve --agent-id alice               # Override agent identity
```

`mcp serve` flags:
```
--agent-id string   Override agent identity (selects .thrum/identities/{name}.json)
```

Configure in `.claude/settings.json`:
```json
{
  "mcpServers": {
    "thrum": {
      "type": "stdio",
      "command": "thrum",
      "args": ["mcp", "serve"]
    }
  }
}
```

---

## Config

```bash
thrum config show                              # Show effective configuration
thrum config show --json
```

---

## Migrate

```bash
thrum migrate                                  # Migrate from old layout to worktree architecture
```

Safe to run multiple times — detects what needs migration and skips completed steps.

---

## Utility

```bash
thrum version                                  # Show version, build hash, repo URL, docs URL
thrum version --json
thrum prime                                    # Gather full session context for initialization
thrum prime --json
thrum overview                                 # Combined status + team + inbox view
thrum overview --json
```
