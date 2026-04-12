---
title: "Session Restart & Context Recovery"
description:
  "Save conversation history and resume where you left off — from daily shutdown
  to automated mid-task restarts"
category: "orchestration"
order: 5
tags: ["restart", "context", "snapshot", "recovery", "session"]
last_updated: "2026-04-07"
---

## Two Ways to Use This

**If you're a developer using Thrum with one or two agents,** the only thing you
need from this page is the first section. Run `/thrum:update-project` before you
close your laptop. Come back tomorrow. Your agent remembers where it was.

**If you're running agent teams** and agents need to restart mid-task without
losing their place — context exhaustion, rate limits, stuck state — the rest of
this page covers how the daemon extracts conversation history, saves a snapshot,
and relaunches the agent automatically.

---

## Everyday Use: Save and Resume

The `/thrum:update-project` skill is the thing most people need. It saves your
project state and session context so your agent can pick up where it left off
next time.

Run it:

- Before you shut down for the day
- Before compaction hits
- Whenever you want your agent to remember what you were working on

Next session, `thrum prime` loads everything back — project state, session
history, role instructions. The agent is oriented and ready to work immediately.

If you're using [Beads](beads-and-thrum.md) for task tracking, that state
survives too — it's in git. Your agent knows which tasks are open, which are
blocked, and what it was working on.

That's the "close your laptop" workflow. Simple, works everywhere, no tmux
required.

### How This Differs from Restart Snapshots

`/thrum:update-project` saves to `.thrum/context/<agent>.md` and persists
indefinitely. It's your agent's long-term memory of the project — architecture
decisions, open epics, session history. It accumulates over time.

Restart snapshots (the rest of this page) save to `.thrum/restart/<agent>.md`
and are consumed once — `thrum prime` loads the snapshot and deletes it. They're
a one-time context transfer between sessions, carrying the raw conversation so
the new session can pick up mid-task.

Different tools, different purposes. Use `/thrum:update-project` for daily
saves. Restart snapshots happen automatically when an agent needs a fresh
session.

---

## How Restart Snapshots Work

Claude Code stores full session transcripts as append-only JSONL files. The
restart system reads these transcripts and extracts just the conversation — what
the user said and what the assistant said back. No tool calls, no thinking
blocks, no subagent sidechains. Just the conversation you'd see in the terminal.

The pipeline:

1. Find the agent's Claude PID from the identity file
2. Locate the JSONL transcript via `~/.claude/sessions/<pid>.json`
3. Parse the JSONL and extract `user` + `assistant` text entries
4. Skip `isSidechain: true` entries (subagent transcripts)
5. Skip `tool_use`, `tool_result`, and `thinking` content blocks
6. Truncate to the configured line limit (default 1000 lines, oldest removed
   first)
7. Save to `.thrum/restart/<agent>.md`

On the next session start, `thrum prime` detects the snapshot file and includes
it in the session briefing. The agent gets its conversation history and picks up
where it left off. The file is deleted after loading — it's a one-time transfer.

### Snapshot Format

```markdown
# Restart Snapshot — impl_api

**Session:** ses_abc123 **Saved:** 2026-04-07T14:30:00Z **Reason:** external

[Conversation continued from earlier — truncated to last 847 lines]

=== USER === Implement the rate limiter for the /api/submit endpoint...

=== ASSISTANT === I'll add the rate limiter using the token bucket pattern...

=== USER === Looks good. Now add the Redis backend for distributed rate
limiting.

=== ASSISTANT === I'll modify the rate limiter to use Redis...
```

The truncation is boundary-aligned — it always starts on a `=== USER ===` marker
and ends with assistant text. No partial exchanges.

---

## Three Restart Triggers

### Self-Initiated

The agent recognizes it needs a fresh session — context is getting full, it hit
rate limits, or it's stuck. It runs the `/thrum:restart` skill, which:

1. Saves the conversation snapshot via `thrum tmux snapshot save`
2. If in a tmux session, notifies the coordinator to handle the relaunch
3. If not in tmux, prints instructions for the operator

The agent doesn't kill itself. It lets the coordinator (or operator) orchestrate
the restart.

### External

The coordinator decides an agent needs a fresh session:

```bash
thrum tmux restart implementer-api
```

There are two flows — graceful (default) and force. The daemon picks based on
the `--force` flag:

**Graceful flow (default)** — the daemon asks the agent to save its own snapshot
before killing the session. The flow:

1. Daemon sends an `@system` message asking the agent to save a snapshot
2. Daemon nudges the pane to wake the agent
3. Daemon polls for the snapshot file up to `restart.graceful_timeout` seconds
   (default 30)
4. If the snapshot appears, the daemon uses it
5. If the timeout expires, the daemon falls back to JSONL extraction

Use the graceful flow when possible — the agent's own snapshot tends to be more
useful than the raw JSONL extraction (the agent can synthesize what matters
rather than dumping the whole conversation).

**Force flow (`--force`)** — the daemon skips the graceful prompt and extracts
directly from the JSONL conversation file. This is faster but only works for
Claude Code (other runtimes don't use the same JSONL format). Use `--force` when
the agent is unresponsive or you know it can't save.

```bash
thrum tmux restart implementer-api --force
```

Either way, the daemon kills the session, creates a new one, and relaunches. The
new session loads the snapshot via `thrum prime`.

Use `--runtime` to switch runtimes on relaunch:

```bash
thrum tmux restart implementer-api --runtime opencode
```

### Automatic

A Claude Code plugin hook monitors context usage. When `used_percentage` exceeds
the configured threshold, it triggers automatically:

1. Saves the conversation snapshot
2. If in a tmux session, calls `thrum tmux restart` to handle the full cycle
3. If not in tmux, only saves the snapshot — manual restart required

Auto-restart is **disabled by default**. Enable it by setting a threshold:

```bash
thrum config set restart.auto_threshold 80
```

---

## CLI Commands

### `thrum tmux snapshot save`

Save a conversation snapshot for the current agent.

```bash
thrum tmux snapshot save
thrum tmux snapshot save --reason context-threshold
```

The `--reason` flag sets the reason in the snapshot header. Values:
`self-initiated` (default), `external`, `context-threshold`.

### `thrum tmux snapshot restore`

Manual escape hatch for non-tmux agents. Outputs the snapshot to stdout and
deletes the file.

```bash
thrum tmux snapshot restore
```

If no snapshot exists, exits with code 1.

### `thrum tmux snapshot check`

Check if a restart snapshot exists. Exits 0 if yes, 1 if no. No output — for
scripting.

```bash
if thrum tmux snapshot check; then
  echo "Snapshot ready"
fi
```

### `thrum tmux restart`

Full restart cycle for tmux-managed agents. See
[Tmux-Managed Sessions](tmux-sessions.md) for context.

```bash
thrum tmux restart implementer-api
thrum tmux restart implementer-api --force
thrum tmux restart implementer-api --runtime opencode
```

---

## Configuration

```yaml
restart:
  max_lines: 1000 # Max lines in snapshot (default: 1000)
  auto_threshold: 0 # Context % trigger, 0 = disabled (default: 0)
  graceful_timeout: 30 # Seconds to wait for graceful save (default: 30)
```

`max_lines` controls how much conversation history to keep. 1000 lines is
usually enough to capture the last 20-30 exchanges. Increase it if your agents
have long conversations with lots of context.

`auto_threshold` is the percentage of context window usage that triggers an
automatic restart. Set to 0 to disable (the default). A value like 80 means
"restart when 80% of the context window is used."

`graceful_timeout` is how long `thrum tmux restart` waits for the agent to save
its own snapshot before falling back to force extraction.

---

## How It Fits Together

The context preservation story has layers:

- **`/thrum:update-project`** — your everyday tool. Saves project state and
  session context. Persists indefinitely. Run it before shutdown, before
  compaction, whenever you want your agent to remember.
- **Restart snapshots** — automated mid-task recovery. Extracts raw conversation
  history, consumed once on next session start. Happens when agents restart.
- **`thrum prime`** — loads everything on session start. Project state, session
  context, restart snapshot (if present), role instructions.
- **Beads** — task state is in git. Survives everything. Your agent knows which
  tasks are open regardless of how the session started.

For the full technical details on context files, see
[Context Management](context.md).

---

## Next Steps

- [Tmux-Managed Sessions](tmux-sessions.md) — the daemon-managed session system
  that makes automated restarts possible
- [Context Management](context.md) — the three-tier context model and how
  `/thrum:update-project` works under the hood
- [Beads and Thrum](beads-and-thrum.md) — how task tracking and messaging work
  together for persistent state across sessions
