# Listener Pattern: Background Message Monitoring

The message-listener is a background sub-agent that blocks on `thrum wait` and
returns when messages arrive. It updates a heartbeat in the agent's identity
file so a local watchdog or session-end reminder can detect if the listener dies and prompt a restart.

## Quick Start

```text
Launch a background agent (or delegated subagent) with a prompt like:

Listen for Thrum messages.
STEP_1: /path/to/repo/scripts/thrum-startup.sh --listener-heartbeat
STEP_2: thrum wait --timeout 8m --after -15s
```

Replace `/path/to/repo` with the actual repo path. When using the Thrum plugin,
`thrum prime` outputs a ready-to-use spawn instruction with the correct path.

## How It Works

1. **Spawn** — Launch as a background agent in your runtime
2. **Heartbeat** — Listener calls
   `scripts/thrum-startup.sh --listener-heartbeat` to update its heartbeat in
   the identity file
3. **Block** — Listener runs `thrum wait --timeout 8m` which blocks until
   message or timeout (keeps each wait cycle bounded)
4. **Return or loop** — If message received, output JSON and stop. If timeout,
   go back to step 2.

The listener loops internally (up to 30 cycles of 8 min each = ~4 hours max).

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

A local watchdog or session-end reminder can read this heartbeat. If it is
missing, stale (>10 min), or from a different session, restart the listener.

## Wait Command Flags

| Flag           | Purpose                                                                         |
| -------------- | ------------------------------------------------------------------------------- |
| `--timeout 8m` | Block up to 8 min per cycle while keeping each wait cycle bounded               |
| `--after -15s` | Include messages sent up to 15s ago (covers re-arm gap between listener cycles) |
| `--json`       | Machine-readable output (not used by listener)                                  |

## Optional Watchdog Automation

If your environment supports scheduled automation, add a 30-minute watchdog
that checks for a healthy listener heartbeat and starts a new background
listener when needed.

Recommended behavior:

1. Check whether the listener heartbeat is present and recent.
2. If it is missing or stale, launch a new background listener using the prompt
   from the quick-start section above.
3. If a healthy listener is already running, do nothing.

**Setup:** Start one listener at session start, then let the watchdog handle
future re-arming if your workflow supports it.

## Key Rules

- **Return immediately** when messages arrive (don't wait for more)
- **Read-only** — the listener never sends messages
- **Heartbeat before each wait** — keeps the watchdog/reminder informed
- **Cost-efficient** — use a low-cost model and block instead of polling
- Listener uses CLI only through the shell/terminal tool, not MCP tools
