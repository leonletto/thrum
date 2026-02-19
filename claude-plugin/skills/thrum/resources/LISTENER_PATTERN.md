# Listener Pattern: Background Message Monitoring

The message-listener is a background sub-agent that blocks on `thrum wait` and
returns when messages arrive. Runs on Haiku for cost efficiency
(~$0.00003/cycle).

## Quick Start

```
Task(
  subagent_type="message-listener",
  model="haiku",
  run_in_background=true,
  prompt="Listen for Thrum messages. WAIT_CMD=cd /path/to/repo && thrum wait --all --timeout 15m --after -30s --json"
)
```

Replace `/path/to/repo` with the actual repo path. When using the Thrum plugin,
`thrum prime` outputs a ready-to-use spawn instruction with the correct path.

## How It Works

1. **Spawn** — Launch as background Task with `run_in_background: true`
2. **Block** — Listener runs `thrum wait` which blocks until message or timeout
3. **Return** — Returns `MESSAGES_RECEIVED` (with message data) or
   `NO_MESSAGES_TIMEOUT`
4. **Re-arm** — After processing, spawn a new listener to continue monitoring

The listener loops internally (up to 6 cycles of 15 min each = ~90 min max).

## Processing Messages

```
# When listener returns, check result
listenerResult = TaskOutput(listener_id)

if "MESSAGES_RECEIVED" in result:
    # Read full messages
    thrum inbox --unread

    # Handle by priority:
    # - critical: stop current work, handle immediately
    # - high: handle at next breakpoint
    # - normal/low: queue for later

    # Re-arm listener
    Task(subagent_type="message-listener", ...)
else:
    # Timeout, no messages — re-arm if still working
    Task(subagent_type="message-listener", ...)
```

## Wait Command Flags

| Flag             | Purpose                                              |
| ---------------- | ---------------------------------------------------- |
| `--all`          | Subscribe to all messages (broadcasts + directed)    |
| `--timeout 15m`  | Block up to 15 minutes per cycle                     |
| `--after -30s`   | Only return messages from last 30 seconds (skip old) |
| `--json`         | Machine-readable output                              |
| `--mention-only` | Only messages that @mention you                      |

## Return Format

Messages received:

```
MESSAGES_RECEIVED
---
FROM: @coordinator
PRIORITY: high
CONTENT: Please review PR #42
TIMESTAMP: 2026-02-13T10:30:00Z
---
RE-ARM: This listener has stopped. Spawn a new message-listener agent to continue listening.
```

Timeout:

```
NO_MESSAGES_TIMEOUT
RE-ARM: This listener has stopped. Spawn a new message-listener agent to continue listening.
```

## Key Rules

- **Always re-arm** after processing results
- **Return immediately** when messages arrive (don't wait for more)
- **Read-only** — the listener never sends messages
- **Cost-efficient** — runs on Haiku, blocks instead of polling
- Listener uses CLI only (`Bash` tool), not MCP tools
