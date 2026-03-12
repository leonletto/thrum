# Listener Pattern: Background Message Monitoring

The message-listener is a background sub-agent that blocks on `thrum wait` and
returns when messages arrive. It updates a heartbeat in the agent's identity file
so the Stop hook can detect if the listener dies and prompt a restart.

## Quick Start

```text
Task(
  subagent_type="message-listener",
  model="haiku",
  run_in_background=true,
  prompt="Listen for Thrum messages.\nSTEP_1: /path/to/repo/scripts/thrum-startup.sh --listener-heartbeat\nSTEP_2: thrum wait --timeout 8m --after -1s"
)
```

Replace `/path/to/repo` with the actual repo path. When using the Thrum plugin,
`thrum prime` outputs a ready-to-use spawn instruction with the correct path.

## How It Works

1. **Spawn** — Launch as background Task with `run_in_background: true`
2. **Heartbeat** — Listener calls `scripts/thrum-startup.sh --listener-heartbeat`
   to update its heartbeat in the identity file
3. **Block** — Listener runs `thrum wait --timeout 8m` which blocks until
   message or timeout (stays under Bash 600s limit)
4. **Return or loop** — If message received, output JSON and stop. If timeout,
   go back to step 2.

The listener loops internally (up to 10 cycles of 8 min each = ~80 min max).

## Heartbeat Mechanism

The listener updates `.thrum/identities/<agent>.json` with a `listener` key:

```json
{
  "listener": {
    "agent_id": "coordinator_main",
    "session_id": "ses_...",
    "heartbeat": "2026-03-12T20:19:00Z"
  }
}
```

The Stop hook reads this heartbeat. If it's missing, stale (>10 min), or from a
different session, the hook tells Claude to restart the listener.

## Wait Command Flags

| Flag            | Purpose                                                                        |
| --------------- | ------------------------------------------------------------------------------ |
| `--timeout 8m`  | Block up to 8 min per cycle (under Bash 600s limit)                            |
| `--after -1s`   | Include messages sent up to 1s ago (prevents stale replay; negative = "N ago") |
| `--json`        | Machine-readable output (not used by listener)                                 |

## Key Rules

- **Return immediately** when messages arrive (don't wait for more)
- **Read-only** — the listener never sends messages
- **Heartbeat before each wait** — keeps the Stop hook informed
- **Cost-efficient** — runs on Haiku, blocks instead of polling
- Listener uses CLI only (`Bash` tool), not MCP tools
