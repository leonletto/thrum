# CLI Reference

Complete command syntax for `thrum`. Run `thrum <command> --help` for detailed
flag descriptions.

## Global Flags

These flags apply to every command:

```bash
--json            JSON output for scripting
--quiet           Suppress non-essential output
--verbose         Debug output
--role <role>     Agent role (or THRUM_ROLE env var)
--module <mod>    Agent module (or THRUM_MODULE env var)
```

---

## Messaging

### send

```bash
thrum send <message> --to @name                # Direct message (canonical form)
thrum send <message> --broadcast               # Explicit team-wide fanout (preferred)
thrum send <message> --to @everyone            # Broadcast (legacy keyword form)
thrum send <message> --to @name --scope type:value --scope type:value2
thrum send <message> --to @name --ref type:value
thrum send <message> --to @name --mention @role
thrum send <message> --to @name --format plain
thrum send <message> --to @name --structured '{"key":"value"}'
```

A recipient flag is REQUIRED — `thrum send 'msg'` with no `--to` or
`--broadcast` is a hard error (thrum-t698). `--to` and `--broadcast` are
mutually exclusive.

Flags:

```text
--to string           Recipient — @agent_name or @everyone for broadcast (mutex with --broadcast)
--broadcast           Fan out to the entire team (mutex with --to)
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

```text
--format string   Message format: markdown, plain, json (default "markdown")
```

### inbox

```bash
thrum inbox                                    # Show inbox (auto-filtered to you)
thrum inbox --unread                           # Only unread messages
thrum inbox --all                              # All messages (no auto-filter)
thrum inbox --mentions                         # Only messages mentioning you
thrum inbox --from @agent                      # Filter to messages from a specific sender
thrum inbox --scope type:value                 # Filter by scope
thrum inbox --page 2 --page-size 20            # Pagination
thrum inbox --limit N                          # Alias for --page-size
thrum sent                                     # Show sent items with receipts
thrum sent --unread                            # Only messages with unread recipients
thrum sent --to @agent                         # Filter by recipient or audience
thrum message get <message-id>                 # Full recipient detail for one message
```

Flags:

```text
-a, --all           Show all messages (disable auto-filtering)
--unread            Only unread messages (does not mark as read — safe to peek)
--mentions          Only messages mentioning me
--from string       Filter to messages from a specific sender (use @agent or agent)
--scope string      Filter by scope (format: type:value)
--page int          Page number (default 1)
--page-size int     Results per page (default 10)
--limit int         Alias for --page-size
```

### wait

Blocks until a matching message arrives or timeout. Exit codes: 0=message,
1=timeout, 2=error.

```bash
thrum wait                                     # Wait for any message (30s default)
thrum wait --timeout 5m                        # Wait up to 5 minutes
thrum wait --timeout 8m --after -15s          # Include messages sent up to 15s ago (covers re-arm gap)
thrum wait --mention @reviewer                 # Wait for mention of role
thrum wait --scope module:auth                 # Wait for scoped message
thrum wait --after -30s                        # Include messages sent up to 30s ago
thrum wait --after -5m --json                  # Include messages sent up to 5m ago; JSON output
```

Flags:

```text
--timeout string   Max wait time with unit (e.g., 30s, 5m) (default "30s")
--after string     Relative time offset for filtering messages:
                     Negative (e.g., -30s, -5m): include messages sent up to N ago
                     Positive (e.g., +60s): only messages arriving at least N seconds in the future
                     Omitted: default is "now" (only messages that arrive after wait starts)
--mention string   Wait for mentions of role (format: @role)
--scope string     Filter by scope (format: type:value)
```

Note: `--after` sign convention: negative = "N ago" (look back), positive = "N
from now" (skip ahead). Note: `--timeout` requires a Go duration unit — use
`120s` not `120`.

### message

```bash
thrum message get <msg-id>                     # Get full message details
thrum message edit <msg-id> <new-text>         # Full replacement (author only)
thrum message delete <msg-id> --force          # Delete (--force required)
thrum message read <msg-id> [<msg-id>...]      # Mark as read
thrum message read --all                       # Mark all as read
```

Subcommand flags:

```text
# message delete
--force   Confirm deletion (required)

# message read
--all     Mark all unread messages as read
```

---

## Agent Management

### quickstart

Bootstrap a full session in one command: detect runtime, generate config,
register, start, set intent.

```bash
thrum quickstart --name implementer_auth --role implementer --module auth
thrum quickstart --name reviewer_auth --role reviewer --module auth --intent "Reviewing PR #42"
thrum quickstart --name alice --role impl --module auth --runtime codex
thrum quickstart --name planner_core --role planner_core --module core --no-init
thrum quickstart --name tester_api --role tester --module api --dry-run
```

Flags:

```text
--role string            Agent role (or THRUM_ROLE env var)
--module string          Agent module (or THRUM_MODULE env var)
--name string            Human-readable agent name (defaults to role_hash)
--display string         Display name for the agent
--intent string          Initial work intent description
--runtime string         Runtime preset: claude, codex, cursor, gemini, opencode, auggie, cli-only
--preamble-file string   Custom preamble file to compose with default preamble
--no-init                Skip runtime config generation, just register agent
--force                  Overwrite existing runtime config files
--dry-run                Preview changes without writing files or registering
--no-agent-pid           Persist agent_pid=0 instead of detecting the runtime ancestor; defers PID claim to first /thrum:prime (used for inline tmux quickstart)
```

### agent register

```bash
thrum agent register --role implementer --module auth
thrum agent register --name alice --role impl --module auth
thrum agent register --force                   # Override existing
thrum agent register --re-register             # Re-register same agent
```

Flags:

```text
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

```text
--context         Show work context (branch, commits, intent)
--role string     Filter by role
--module string   Filter by module
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

```text
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

```text
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

```text
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

```text
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

```text
--active         Show only active sessions
--agent string   Filter by agent ID
```

### team

```bash
thrum team                                     # Rich multi-line status for all active agents
thrum team --all                               # Include offline agents
thrum team --json
```

Flags:

```text
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

## Context

```bash
thrum prime                                    # Gather full session context (AI-optimized)
thrum prime --json
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
```

`context show` flags:

```text
--agent string    Override agent name
--raw             Raw output with file boundary markers, no header
--no-preamble     Exclude preamble from output
```

`context save` flags:

```text
--agent string   Override agent name
--file string    Read context from file (default: stdin)
```

`context preamble` flags:

```text
--agent string   Override agent name
--file string    Set preamble from file
--init           Create or reset to default preamble
```

`context clear` / `context sync` flags:

```text
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

The `--local` and `--force` flags are available on all daemon subcommands:

```text
--local   Local-only mode: skip git push/fetch in sync loop
--force   Proceed even when the repo directory is not git-anchored (G2 override)
```

Note: `--foreground` flag does NOT exist. Daemon always starts in background.

---

## Init

`thrum init` also automatically starts the daemon if it is not already running.

```bash
thrum init                                     # On a TTY: launches the interactive wizard. Non-TTY: legacy silent path.
thrum init --runtime claude                    # Pin runtime (skips runtime prompt)
thrum init --runtime codex --force             # Re-init: pre-seeds wizard prompts from existing values
thrum init --runtime all --dry-run             # Preview all runtime configs (bypasses wizard)
thrum init --non-interactive --runtime claude  # Force the legacy silent path even on a TTY
thrum init --stealth                           # Zero tracked-file footprint
thrum init --name alice --role implementer --module auth   # Pre-fill wizard identity prompts
thrum init --no-daemon                         # Run wizard but skip starting the daemon
thrum init --skills                            # Install thrum skill only (no MCP, no startup script)
thrum init --skills --runtime cursor           # Install skill to Cursor's skill directory
```

Flags:

```text
--runtime string        Generate runtime-specific configs: claude|codex|cursor|gemini|opencode|cli-only|all
--skills                Install thrum skill only (no MCP config, no startup script)
--stealth               Use .git/info/exclude instead of .gitignore (zero footprint)
--force                 Force reinitialization. On a TTY this re-runs the wizard with existing values pre-seeded.
--dry-run               Preview changes without writing files (bypasses the wizard)
--non-interactive       Force the legacy silent path even on a TTY
--no-daemon             Skip auto-starting the daemon at the end of the wizard
--name string           Pre-fill the wizard's identity-name prompt
--role string           Pre-fill the wizard's role prompt
--module string         Pre-fill the wizard's module prompt
--worktrees-root string Pre-fill the wizard's worktrees-root prompt (absolute path outside the repo)
--roles string          Pre-fill the wizard's role-template choice: enhanced|default|skip
--yes                   Auto-confirm any safety prompts (e.g. the v0.10.x → v0.11 .gitignore upgrade)
```

The wizard's suggested default agent name is derived from the repo directory,
lowercased and sanitized to satisfy the agent-name validator (a-z, 0-9,
underscore only).

**tmux gate:** if `tmux` is not on `PATH` when the wizard reaches the
daemon-start step, init exits early with an OS-appropriate install hint
(`brew install tmux` / `apt install tmux`).

---

## Setup

```bash
thrum setup claude-md                          # Print Thrum CLAUDE.md block to stdout
thrum setup claude-md --apply                  # Create or append CLAUDE.md block
thrum setup claude-md --apply --force          # Replace existing Thrum block in place
thrum worktree setup                           # Set up redirect in feature worktree (default)
thrum worktree setup --main-repo /path/to/main
```

`setup claude-md` flags:

```text
--apply   Write to ./CLAUDE.md (create or append); errors if block exists (use --force to replace)
--force   Replace an existing Thrum block idempotently; no effect without --apply
```

Block is wrapped in `<!-- BEGIN THRUM -->` / `<!-- END THRUM -->` markers for
detection. Use only if you are NOT running the Thrum Claude Code plugin — the
plugin already injects equivalent content via its SessionStart hook.

`worktree setup` flags:

```text
--main-repo string   Path to the main repository (where daemon runs) (default ".")
```

---

## Sync

```bash
thrum sync force                               # Force immediate sync (non-blocking)
thrum sync status                              # Show sync status, last sync time, errors
thrum sync status --json
```

Note: `thrum sync` alone prints help — use `thrum sync force` or
`thrum sync status`.

---

## Backup

```bash
thrum backup                                   # Snapshot all thrum data
thrum backup --dir /path/to/backup             # Override backup directory
thrum backup status                            # Show last backup info
thrum backup config                            # Show effective backup config
thrum backup schedule                          # Show current schedule
thrum backup schedule 24h                      # Back up every 24 hours
thrum backup schedule 8h --dir dev-docs/backup # Set interval + directory
thrum backup schedule off                      # Disable scheduled backups
```

`backup schedule` flags:

```text
--dir string   Set backup directory
```

Note: Daemon must be restarted for schedule changes to take effect.

---

## Purge

```bash
thrum purge --before 2d                        # Preview: what's older than 2 days
thrum purge --before 2d --confirm              # Execute the purge
thrum purge --before 2026-03-15 --confirm      # Purge before a specific date
thrum purge --all --confirm                    # Delete all messages/sessions/events
```

`purge` flags:

```text
--before string   Cutoff: duration (2d, 24h), date (2026-03-15), or RFC 3339
--all             Purge all messages, sessions, and events
--confirm         Execute the purge (without this, only shows a preview)
```

Removes messages, sessions, and events from both SQLite and sync JSONL files.
Agents, groups, and subscriptions are not touched. `--before` and `--all` are
mutually exclusive.

```bash
thrum backup restore                           # Restore from latest backup
thrum backup restore archive.zip               # Restore from specific archive
thrum backup restore --yes                     # Skip confirmation
thrum backup plugin list                       # List configured plugins
thrum backup plugin add --preset beads         # Add built-in preset
thrum backup plugin add --name myplugin --command "cmd" --include "*.json"
thrum backup plugin remove --name myplugin
```

`backup` flags (inherited by all subcommands):

```text
--dir string   Override backup directory
```

`backup plugin add` flags:

```text
--preset string     Use built-in preset: beads, beads-rust
--name string       Plugin name
--command string    Command to run before collecting files
--include strings   File patterns to collect (glob, repeatable)
```

`backup restore` flags:

```text
--yes   Skip confirmation prompt
```

---

## Peer

```bash
thrum peer add                                 # Start pairing (displays 4-digit code, blocks 5min)
thrum peer join [peercode]                     # Join remote peer
thrum peer list                                # List paired peers
thrum peer remove <name>                       # Remove a paired peer
thrum peer status                              # Detailed sync status for all peers
```

`peer join` accepts the peercode (format: `name:ip:port:code`) via four input
methods (tried in order): positional argument, `--peercode` flag, stdin pipe,
interactive prompt.

Flags:

```text
--peercode string   Peer connection code (format: name:ip:port:code)
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

Supported runtimes: `claude`, `codex`, `cursor`, `gemini`, `opencode`, `auggie`,
`cli-only`

---

## Role Templates

```bash
thrum roles list                               # List templates and matching agents
thrum roles deploy                             # Re-render preambles from templates (all agents)
thrum roles deploy --agent alice               # Deploy for a specific agent
thrum roles deploy --dry-run                   # Preview changes without writing files
thrum roles refresh                            # Re-render templates from saved role_config answers
thrum roles save-config                        # Write role_config to config.json from JSON on stdin
thrum roles templates print <role>-<autonomy>  # Print an embedded shipped template to stdout
```

`roles deploy` flags:

```text
--agent string   Deploy for a specific agent only
--dry-run        Preview changes without writing files
```

`roles templates print` takes a single positional argument of the form
`<role>-<autonomy>` (e.g. `implementer-autonomous`, `coordinator-strict`). Exit
code is non-zero if the template name is not found.

`roles save-config` reads a `RoleConfig` JSON object from stdin and atomically
writes `role_config` to `.thrum/config.json`, preserving all other top-level
keys. Used internally by `/thrum:configure-roles`.

---

## Telegram

```bash
thrum telegram configure                       # Interactive setup (prompts for token, target, user)
thrum telegram configure --token <token> --target <chat-id> --user <username>
thrum telegram configure --token <token> --target <chat-id> --user <username> --yes
thrum telegram status                          # Show bridge connection status and config
```

`telegram configure` flags:

```text
--token string    Telegram bot token
--target string   Target chat ID or username
--user string     Telegram username to associate
--yes             Skip confirmation prompt (non-interactive)
```

When flags are omitted, `configure` runs in interactive mode and prompts for
each value.

`telegram status` has no additional flags beyond globals.

---

## MCP Server

```bash
thrum mcp serve                                # Start MCP stdio server
thrum mcp serve --agent-id alice               # Override agent identity
```

`mcp serve` flags:

```text
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

## Tmux Sessions

```bash
thrum tmux start                               # One-command in current worktree: launch + prime + attach
thrum tmux start --name <session>              # Override session name
thrum tmux start --runtime opencode            # Override runtime
thrum tmux create <name> --cwd <path> \
  --name <agent> --role <role> --module <mod>  # Create session + register agent (flags required)
thrum tmux create <name> --cwd <path> \
  --no-agent                                   # Create session without agent registration
thrum tmux quickstart <name> --cwd <path> \
  --name <agent> --role <role> --module <mod>  # Alias for tmux create
thrum tmux launch <name>                       # Start AI runtime in session
thrum tmux launch <name> --runtime claude      # Override runtime for this launch
thrum tmux status                              # Show managed sessions with state
thrum tmux connect                             # Attach (interactive picker)
thrum tmux connect <name>                      # Attach directly by name
thrum tmux restart <name>                      # Restart session with context snapshot
thrum tmux restart <name> --force              # Restart without confirmation
thrum tmux kill <name>                         # Tear down a session
thrum tmux queue <session> <command>           # Submit command to session queue
thrum tmux queue <session> <command> --wait    # Queue + block until complete
thrum tmux queue-status <session>              # Show active + queued commands
thrum tmux cancel <command-id>                 # Cancel a queued or active command
```

`tmux start` flags (operates on current working directory):

```text
--name string      Override session name (default: directory name)
--runtime string   Override runtime (default: from config or claude)
```

`tmux create` / `tmux quickstart` flags:

```text
--cwd string       Working directory for the session
--name string      Agent name (required unless --no-agent)
--role string      Agent role (required unless --no-agent)
--module string    Agent module (required unless --no-agent)
--intent string    Initial work intent description
--runtime string   Runtime preset: claude, codex, cursor, gemini, opencode, auggie
--no-agent         Skip agent registration (create session only)
--force            Overwrite existing runtime config files
```

Without `--no-agent`, the command errors if `--name`, `--role`, and `--module`
are all missing. Old identity files in the session worktree are cleaned up after
quickstart runs (one identity per worktree enforced).

`tmux launch` flags:

```text
--runtime    AI tool to launch (default: from config or claude)
```

`tmux queue` flags:

```text
--timeout int   Command timeout in seconds (default 120)
--wait          Block until command reaches terminal state
--silence float Silence threshold in seconds (server default: 5.0)
```

---

## Config

```bash
thrum config show                              # Show effective configuration
thrum config show --json
```

---

## Worktree Management

```bash
thrum worktree create <name>                   # Create worktree with thrum/beads setup
thrum worktree create <name> -b <branch>       # Specify branch name
thrum worktree create <name> --detach          # Detached HEAD worktree
thrum worktree create <name> \
  --name <agent> --role <role> --module <mod>  # Create worktree + register agent
thrum worktree setup <name>                    # Alias for worktree create
thrum worktree teardown <name>                       # Remove worktree, keep branch
thrum worktree teardown <name> --delete-branch       # Also delete the worktree's branch
thrum worktree list                                  # List worktrees with agent info
```

`worktree create` / `worktree setup` flags:

```text
--branch, -b string   Branch name (default: feature/<name>)
--detach              Create detached HEAD worktree
--name string         Agent name (triggers quickstart when combined with --role + --module)
--role string         Agent role
--module string       Agent module
--intent string       Initial work intent description
--runtime string      Runtime preset: claude, codex, cursor, gemini, opencode, auggie
```

`worktree teardown` flags:

```text
--delete-branch       Delete the worktree's branch after removing the worktree
                      (branch stays by default)
```

When `--name`, `--role`, and `--module` are all provided, a tmux session is
created with the worktree as cwd and quickstart runs inside the pane (PID
isolation). The agent identity is registered, but **the runtime is NOT started
yet** — follow up with `thrum tmux launch <name>` to start the agent. The output
explicitly shows the next-step launch command.

If the agent flags are omitted, the worktree is created with redirects only and
the output shows the full `tmux create` command needed to register an agent
later.

---

## Monitor Jobs

Run a long-lived command, filter output through a regex, and deliver matching
lines as Thrum messages. Jobs persist across daemon restarts. Max 100
concurrent. The command must follow `--`.

```bash
thrum monitor add --name <n> --match <re> --to @agent -- <cmd> [args...]
thrum monitor add --name <n> --match <re> --to @agent \
  --debounce 120s --env KEY=VALUE -- <cmd>
thrum monitor list                             # Running jobs only
thrum monitor list --all                       # Include stopped/dead (<1 week)
thrum monitor show <id>                        # Full detail (env values redacted)
thrum monitor stop <id>                        # SIGTERM → 5s → SIGKILL
thrum monitor logs <id>                        # Last 20 matched lines
thrum monitor logs <id> -n 50                  # Last 50 matched lines
thrum monitor restart <id>                     # Restart dead/stopped monitor
```

`monitor add` flags:

```text
--name string         Unique monitor name (required)
--match string        Regex pattern — matching lines trigger a message (required)
--to string           Target agent or group, e.g. @coordinator (required)
--debounce duration   Leading-edge debounce window, minimum 30s (default 60s)
--env strings         Environment variable in KEY=VALUE form (repeatable)
--cwd string          Working directory for the command (default: current dir)
```

`monitor list` flags:

```text
--all   Include stopped/dead monitors younger than one week
```

`monitor logs` flags:

```text
-n, --limit int   Max number of matches to return (default 20)
```

Key constraints: local Unix socket only, max line length 2KB (lines truncated),
leading-edge debounce (min 30s), auto-persists across daemon restart, sends a
notify-only message on child exit.

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
