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

## Messaging

```bash
thrum send <message> --to @name
thrum send <message> --to @everyone            # Broadcast to all agents
thrum send <message> --everyone                # Alias for --to @everyone
thrum reply <msg-id> <response>
thrum inbox [--unread] [--limit N] [--json]
thrum message get <msg-id>
thrum message edit <msg-id> <new-text>
thrum message delete <msg-id>
thrum message read <msg-id> [<msg-id>...]     # Mark as read
thrum message read --all                       # Mark all as read
thrum wait [--timeout <duration>] [--after <duration>] [--mention <role>] [--scope <scope>] [--json]
```

## Agent Management

```bash
thrum quickstart --role <role> --module <module> --intent "<text>"
thrum agent register [--role <role>] [--module <module>]
thrum agent list [--json]
thrum agent delete <name>
thrum whoami [--json]
thrum status [--json]
thrum team [--json]
```

## Sessions

```bash
thrum session start
thrum session end
thrum session heartbeat
thrum session set-intent "<text>"
```

## Groups

```bash
thrum group create <name> [--description "<text>"]
thrum group delete <name>
thrum group add <group> @agent                 # Add agent
thrum group add <group> --role <role>          # Add by role
thrum group add <group> --group <other-group>  # Nest group
thrum group remove <group> @agent
thrum group list [--json]
thrum group info <name> [--json]
thrum group members <name> [--expand] [--json]
```

## Coordination

```bash
thrum who-has <file>
thrum ping @name
thrum overview [--json]
```

## Context

```bash
thrum prime [--json]
thrum context prime [--json]                   # Alias for thrum prime
thrum context save [--file <path>] [--agent <name>]
thrum context show [--agent <name>]
thrum context clear [--agent <name>]
```

## Daemon

```bash
thrum daemon start [--foreground]
thrum daemon stop
thrum daemon status [--json]
thrum init [--stealth] [--force] [--repo <path>]
```

## Sync

```bash
thrum sync force
thrum sync status [--json]
```

## MCP Server

```bash
thrum mcp serve                                # Start MCP stdio server
```

## Runtime

```bash
thrum runtime list [--json]
thrum runtime show <name> [--json]
```

## Role Templates

```bash
thrum roles list                         # List templates + matching agents
thrum roles deploy [--agent NAME] [--dry-run]  # Re-render preambles from templates
```
