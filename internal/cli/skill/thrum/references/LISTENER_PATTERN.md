# Listener Pattern: Background Message Monitoring

The message listener is a background process that blocks on `thrum wait` and
returns when messages arrive. This enables agents to react to incoming messages
without polling.

## How It Works

1. **Spawn** — Launch a lightweight background process
2. **Block** — Process runs `thrum wait --timeout 8m` which blocks until a
   message arrives or timeout
3. **Return** — When a message arrives, output it and stop
4. **Re-arm** — After processing the message, spawn a new listener

The listener loops internally (up to 10 cycles of 8 min each = ~80 min max
coverage per spawn).

## Wait Command Flags

| Flag            | Purpose                                                                         |
| --------------- | ------------------------------------------------------------------------------- |
| `--timeout 8m`  | Block up to 8 min per cycle (keeps within process time limits)                  |
| `--after -15s`  | Include messages sent up to 15s ago (covers re-arm gap between listener cycles) |
| `--json`        | Machine-readable output                                                         |

## Key Rules

- **Return immediately** when messages arrive (don't wait for more)
- **Read-only** — the listener never sends messages
- **Cost-efficient** — blocks instead of polling, uses minimal resources
- Listener uses CLI only, not MCP tools
