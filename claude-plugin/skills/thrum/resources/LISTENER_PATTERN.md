# Listener Pattern: Background Message Monitoring

The message-listener is a background sub-agent that blocks on `thrum wait` and
returns when messages arrive. Runs on Haiku for cost efficiency
(~$0.00003/cycle).

## Quick Start

```text
Task(
  subagent_type="message-listener",
  model="haiku",
  run_in_background=true,
  prompt="Listen for Thrum messages. WAIT_CMD=cd /path/to/repo && thrum wait --timeout 15m --after -1s --json"
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

```text
# When listener returns, check result
listenerResult = TaskOutput(listener_id)

if "MESSAGES_RECEIVED" in result:
    # Read full messages
    thrum inbox --unread

    # Re-arm listener
    Task(subagent_type="message-listener", ...)
else:
    # Timeout, no messages — re-arm if still working
    Task(subagent_type="message-listener", ...)
```

## Wait Command Flags

| Flag             | Purpose                                              |
| ---------------- | ---------------------------------------------------- |
| `--timeout 15m`  | Block up to 15 minutes per cycle                     |
| `--after -1s`    | Include messages sent up to 1s ago (prevents stale replay; negative = "N ago") |
| `--json`         | Machine-readable output                              |
| `--mention @<role>` | Only messages that mention the specified role      |

## Return Format

Messages received:

```text
MESSAGES_RECEIVED
---
FROM: @coordinator
CONTENT: Please review PR #42
TIMESTAMP: 2026-02-13T10:30:00Z
---
RE-ARM: This listener has stopped. Spawn a new message-listener agent to continue listening.
```

Timeout:

```text
NO_MESSAGES_TIMEOUT
RE-ARM: This listener has stopped. Spawn a new message-listener agent to continue listening.
```

## Key Rules

- **Always re-arm** after processing results
- **Return immediately** when messages arrive (don't wait for more)
- **Read-only** — the listener never sends messages
- **Cost-efficient** — runs on Haiku, blocks instead of polling
- Listener uses CLI only (`Bash` tool), not MCP tools
