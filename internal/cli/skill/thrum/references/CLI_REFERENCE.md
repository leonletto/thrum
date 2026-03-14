# CLI Reference

Complete command syntax for `thrum`. Run `thrum <command> --help` for detailed
flag descriptions.

## Global Flags

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
thrum send <message> --to @name --scope type:value
thrum send <message> --to @name --mention @role
thrum send <message> --to @name --structured '{"key":"value"}'
```

Flags:

```text
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

### inbox

```bash
thrum inbox                                    # Show inbox (auto-filtered to you)
thrum inbox --unread                           # Only unread messages
thrum inbox --all                              # All messages (no auto-filter)
thrum inbox --mentions                         # Only messages mentioning you
thrum inbox --scope type:value                 # Filter by scope
thrum inbox --page 2 --page-size 20            # Pagination
thrum sent                                     # Show sent items with receipts
thrum sent --unread                            # Only messages with unread recipients
```

### wait

Blocks until a matching message arrives or timeout. Exit codes: 0=message,
1=timeout, 2=error.

```bash
thrum wait                                     # Wait for any message (30s default)
thrum wait --timeout 5m                        # Wait up to 5 minutes
thrum wait --timeout 8m --after -15s          # Include messages sent up to 15s ago
thrum wait --mention @reviewer                 # Wait for mention of role
thrum wait --scope module:auth                 # Wait for scoped message
```

Flags:

```text
--timeout string   Max wait time with unit (e.g., 30s, 5m) (default "30s")
--after string     Relative time offset:
                     Negative (e.g., -30s): include messages sent up to N ago
                     Positive (e.g., +60s): only messages arriving N in the future
                     Omitted: "now" (only new arrivals)
--mention string   Wait for mentions of role (format: @role)
--scope string     Filter by scope (format: type:value)
```

Note: `--timeout` requires a Go duration unit — use `120s` not `120`.

### message

```bash
thrum message get <msg-id>                     # Get full message details
thrum message edit <msg-id> <new-text>         # Full replacement (author only)
thrum message delete <msg-id> --force          # Delete (--force required)
thrum message read <msg-id> [<msg-id>...]      # Mark as read
thrum message read --all                       # Mark all as read
```

---

## Agent Management

### quickstart

Bootstrap a full session in one command: detect runtime, generate config,
register, start, set intent.

```bash
thrum quickstart --name implementer_auth --role implementer --module auth
thrum quickstart --name reviewer --role reviewer --module auth --intent "Reviewing PR #42"
```

Flags:

```text
--role string            Agent role (or THRUM_ROLE env var)
--module string          Agent module (or THRUM_MODULE env var)
--name string            Human-readable agent name
--display string         Display name for the agent
--intent string          Initial work intent description
--runtime string         Runtime preset: claude, codex, cursor, gemini, auggie, cli-only
--no-init                Skip runtime config generation
--force                  Overwrite existing runtime config files
--dry-run                Preview changes without writing files
```

### agent register

```bash
thrum agent register --role implementer --module auth
thrum agent register --name alice --role impl --module auth
thrum agent register --force                   # Override existing
```

### agent list

```bash
thrum agent list                               # List all registered agents
thrum agent list --role reviewer               # Filter by role
thrum agent list --context                     # Show work context
```

### status / team / whoami

```bash
thrum status                                   # Current agent, session, inbox, sync state
thrum team                                     # Active agents with intents
thrum team --all                               # Include offline agents
thrum whoami                                   # Show your identity
```

---

## Coordination

```bash
thrum who-has <file>                           # Check which agents are editing a file
thrum ping @name                               # Check agent presence
thrum overview                                 # Combined: identity + team + inbox + sync
```

---

## Groups

```bash
thrum group create <name>                      # Create a group
thrum group delete <name>                      # Delete a group
thrum group list                               # List all groups
thrum group add <group> @agent                 # Add agent by name
thrum group add <group> --role reviewer        # Add all agents with role
thrum group remove <group> @agent              # Remove agent
thrum group members <name>                     # List group members
thrum send "msg" --to @group-name              # Message a group
```

---

## Context

```bash
thrum prime                                    # Gather full session context
thrum context show                             # Show saved agent context
thrum context save --file path/to/context.md   # Save context from file
thrum context clear                            # Clear saved context
thrum context preamble                         # Show current preamble
```

---

## Sessions

```bash
thrum session start                            # Start session
thrum session end                              # End session
thrum session set-intent "..."                 # Update work description
thrum session set-task "JIRA-1234"             # Set current task
thrum session list                             # List all sessions
thrum session list --active                    # Only active sessions
```

---

## Daemon

```bash
thrum daemon start                             # Start daemon in background
thrum daemon stop                              # Stop daemon gracefully
thrum daemon restart                           # Restart
thrum daemon status                            # Show daemon status
thrum daemon start --local                     # Local-only mode (no git push/fetch)
```

Note: `--foreground` flag does NOT exist. Daemon always starts in background.

---

## Init

```bash
thrum init                                     # Init + interactive runtime selection
thrum init --runtime claude                    # Init + generate configs for agent
thrum init --skills                            # Install thrum skill only
thrum init --stealth                           # Zero tracked-file footprint
```

---

## Sync

```bash
thrum sync force                               # Force immediate sync
thrum sync status                              # Show sync status
```

Note: `thrum sync` alone prints help — use `thrum sync force` or
`thrum sync status`.

---

## Backup

```bash
thrum backup                                   # Snapshot all thrum data
thrum backup status                            # Show last backup info
thrum backup restore                           # Restore from latest backup
thrum backup schedule 24h                      # Set backup interval
thrum backup schedule off                      # Disable scheduled backups
```
