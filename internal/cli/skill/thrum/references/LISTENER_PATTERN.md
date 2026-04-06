# Listener Pattern: Background Message Monitoring

The message-listener is a background sub-agent that blocks on `thrum wait` and
returns when messages arrive. A PID file (`.thrum/var/<agent_id>-listener.pid`)
prevents duplicate listeners — spawning when one is already running is safe.

## Quick Start

```text
Task(
  subagent_type="message-listener",
  model="haiku",
  prompt="Listen for Thrum messages.\nSTEP_1: /path/to/repo/scripts/thrum-startup.sh --listener-heartbeat\nSTEP_2: thrum wait --timeout 8m --after -15s --agent-name <agent_id>"
)
```

Replace `/path/to/repo` with the actual repo path and `<agent_id>` with your
agent name. `thrum prime` outputs a ready-to-use spawn instruction with both
pre-filled.

## How It Works

1. **Spawn** — Launch as background Task (`background: true` in frontmatter)
2. **PID file** — `thrum wait --agent-name` writes a PID file on start, updates
   it each cycle, and removes it on exit (or signal). All spawn paths check this
   file with `kill -0` before spawning.
3. **Block** — `thrum wait --timeout 8m` blocks until message or timeout
4. **Return or loop** — Message received → stop. Timeout → back to step 2.

The listener loops internally (up to ~30 cycles of 8 min = ~4 hours max).

## Wait Command Flags

| Flag              | Purpose                                                 |
| ----------------- | ------------------------------------------------------- |
| `--timeout 8m`    | Block up to 8 min per cycle (under Bash 600s limit)     |
| `--after -15s`    | Include messages sent up to 15s ago (covers re-arm gap) |
| `--agent-name ID` | Write PID file for spawn coordination                   |

## Cron Watchdog (Recommended)

Automatically respawn the listener if it dies or is lost after compaction.

```text
CronCreate(
  cron="*/30 * * * *",
  prompt="Check the listener PID file at .thrum/var/<agent_id>-listener.pid.\nIf the file does not exist, or if kill -0 <pid> fails (process not running),\nspawn a new listener:\n\nAgent(subagent_type=\"message-listener\", model=\"haiku\", prompt=\"Listen for Thrum messages.\\nSTEP_1: /path/to/repo/scripts/thrum-startup.sh --listener-heartbeat\\nSTEP_2: thrum wait --timeout 8m --after -15s --agent-name <agent_id>\")"
)
```

Spawn the initial listener on session start, then create the cron watchdog.

## Key Rules

- **Spawn freely** — PID file prevents duplicates
- **Return immediately** when messages arrive (don't wait for more)
- **Read-only** — the listener never sends messages
- **Cost-efficient** — runs on Haiku, blocks instead of polling
- Listener uses CLI only (`Bash` tool), not MCP tools
