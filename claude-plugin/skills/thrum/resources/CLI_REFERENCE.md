# CLI Reference

Complete command syntax for `thrum`. Run `thrum <command> --help` for detailed flag descriptions.

## Global Flags

```
--json            JSON output for scripting
--quiet           Suppress non-essential output
--verbose         Debug output
--repo <path>     Repository path (default: ".")
--role <role>     Agent role (or THRUM_ROLE env var)
--module <mod>    Agent module (or THRUM_MODULE env var)
```

## Messaging

```
thrum send <message> --to @name [--priority critical|high|normal|low]
thrum send <message> --to @everyone            # Broadcast to all agents
thrum reply <msg-id> <response>
thrum inbox [--unread] [--json]
thrum message get <msg-id>
thrum message edit <msg-id> <new-text>
thrum message delete <msg-id>
thrum message read <msg-id> [<msg-id>...]     # Mark as read
thrum message read --all                       # Mark all as read
thrum wait [--all] [--timeout <duration>] [--after <duration>] [--mention <role>] [--scope <scope>] [--json]
```

## Agent Management

```
thrum quickstart --role <role> --module <module> --intent "<text>"
thrum agent register [--role <role>] [--module <module>]
thrum agent list [--json]
thrum agent delete <name>
thrum whoami [--json]
thrum status [--json]
thrum team [--json]
```

## Sessions

```
thrum session start
thrum session end
thrum session heartbeat
thrum session set-intent "<text>"
```

## Groups

```
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

```
thrum who-has <file>
thrum ping @name
thrum overview [--json]
```

## Context

```
thrum prime [--json]
thrum context prime [--json]                   # Alias for thrum prime
thrum context save [--file <path>] [--agent <name>]
thrum context show [--agent <name>]
thrum context clear [--agent <name>]
```

## Daemon

```
thrum daemon start [--foreground]
thrum daemon stop
thrum daemon status [--json]
thrum init [--repo <path>]
```

## Sync

```
thrum sync force
thrum sync status [--json]
```

## MCP Server

```
thrum mcp serve                                # Start MCP stdio server
```

## Runtime

```
thrum runtime list [--json]
thrum runtime show <name> [--json]
```
