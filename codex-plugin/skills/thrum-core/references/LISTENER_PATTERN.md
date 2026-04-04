# Listener Pattern: Background Message Monitoring

The message-listener is a background sub-agent that blocks on `thrum wait` and
returns when messages arrive. It updates a heartbeat in the agent's identity
file so the Stop hook can detect if the listener dies and prompt a restart.

## Quick Start

```text
Task(
  subagent_type="message-listener",
  model="haiku",
  run_in_background=true,
  prompt="Listen for Thrum messages.\nSTEP_1: /path/to/repo/scripts/thrum-startup.sh --listener-heartbeat\nSTEP_2: thrum wait --timeout 8m --after -15s"
)
```

Replace `/path/to/repo` with the actual repo path. When using the Thrum plugin,
`thrum prime` outputs a ready-to-use spawn instruction with the correct path.

## How It Works

1. **Spawn** — Launch as background Task with `run_in_background: true`
2. **Heartbeat** — Listener calls
   `scripts/thrum-startup.sh --listener-heartbeat` to update its heartbeat in
   the identity file
3. **Block** — Listener runs `thrum wait --timeout 8m` which blocks until
   message or timeout (stays under Bash 600s limit)
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

The Stop hook reads this heartbeat. If it's missing, stale (>10 min), or from a
different session, the hook tells Claude to restart the listener.

## Wait Command Flags

| Flag           | Purpose                                                                         |
| -------------- | ------------------------------------------------------------------------------- |
| `--timeout 8m` | Block up to 8 min per cycle (under Bash 600s limit)                             |
| `--after -15s` | Include messages sent up to 15s ago (covers re-arm gap between listener cycles) |
| `--json`       | Machine-readable output (not used by listener)                                  |

## Cron Watchdog (Recommended)

Use a cron job to automatically respawn the listener if it dies, times out, or
is lost after compaction. The watchdog checks the heartbeat file before spawning
to avoid duplicates.

```text
CronCreate(
  cron="*/30 * * * *",
  prompt="Check the listener heartbeat file at .thrum/identities/<agent_id>.json.\nExtract the .listener.heartbeat timestamp. If it is missing or older than\n10 minutes, spawn a new listener:\n\nAgent(subagent_type=\"message-listener\", model=\"haiku\", run_in_background=true, prompt=\"Listen for Thrum messages.\\nSTEP_1: /path/to/repo/scripts/thrum-startup.sh --listener-heartbeat\\nSTEP_2: thrum wait --timeout 8m --after -15s\")"
)
```

The cron fires every 30 minutes. It reads the heartbeat from the identity file.
If the heartbeat is fresh (< 10 min old), a listener is alive — skip. If stale
or missing, spawn a new one. This is the same heartbeat-gated logic used by the
Stop hook and PostCompact hook.

**Setup:** Spawn the initial listener on session start, then create the cron
watchdog. The cron handles all subsequent re-arming automatically.

## Key Rules

- **Return immediately** when messages arrive (don't wait for more)
- **Read-only** — the listener never sends messages
- **Heartbeat before each wait** — keeps the Stop hook informed
- **Cost-efficient** — runs on Haiku, blocks instead of polling
- Listener uses CLI only (`Bash` tool), not MCP tools
